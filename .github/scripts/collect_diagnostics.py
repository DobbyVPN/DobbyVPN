#!/usr/bin/env python3
"""Build one complete, redacted, binary-safe diagnostics directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import re
import struct
import sys
import tomllib
import zlib


_PRIVATE_KEY = re.compile(
    r"(?i)(password|passphrase|secret|token|credential|username|server|host|"
    r"address|url|certificate|private.?key|public.?key|access.?key|short.?id|"
    r"server.?name|uuid|(^|[_.-])id($|[_.-])|path)"
)
_TEXT_SUFFIXES = frozenset({".json", ".jsonl", ".log", ".txt", ".out", ".err", ".xml", ".md"})
_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
_PRIVATE_FIELD = rb'''["']?[\w.-]*(?:password|passphrase|secret|token|credential|username|server|host|address|url|certificate|private.?key|public.?key|access.?key|short.?id|server.?name|uuid|(?:^|[_.-])id(?:$|[_.-])|path)[\w.-]*["']?'''
_PRIVATE_QUOTED_ASSIGNMENT = re.compile(
    rb"(?im)(?P<prefix>" + _PRIVATE_FIELD + rb"\s*[:=]\s*[\"'])(?P<value>[^\"'\r\n]*)(?P<suffix>[\"'])"
)
_PRIVATE_BARE_ASSIGNMENT = re.compile(
    rb"(?im)(?P<prefix>" + _PRIVATE_FIELD + rb"\s*[:=]\s*)(?P<value>[^\s,#}\]\r\n]+)"
)
_PRIVATE_ASSIGNMENTS = (_PRIVATE_QUOTED_ASSIGNMENT, _PRIVATE_BARE_ASSIGNMENT)


class CollectionError(RuntimeError):
    pass


def _private_values(profile: Path) -> tuple[bytes, ...]:
    raw = profile.read_bytes()
    values: set[bytes] = {raw} if raw else set()
    try:
        parsed = tomllib.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, tomllib.TOMLDecodeError):
        # A malformed profile still has named fields that must be redacted.
        # Keep the whole source and extract both quoted and bare values from
        # the surrounding assignment rather than giving up at the TOML parser.
        for pattern in _PRIVATE_ASSIGNMENTS:
            values.update(
                match.group("value")
                for match in pattern.finditer(raw)
                if match.group("value")
            )
        return tuple(sorted(values, key=len, reverse=True))

    def visit(value: object, key: str = "") -> None:
        if isinstance(value, dict):
            for child_key, child in value.items():
                visit(child, str(child_key))
        elif isinstance(value, list):
            for child in value:
                visit(child, key)
        elif isinstance(value, str) and value and _PRIVATE_KEY.search(key):
            values.add(value.encode("utf-8"))

    visit(parsed)
    return tuple(sorted(values, key=len, reverse=True))


def _redact(payload: bytes, values: tuple[bytes, ...]) -> bytes:
    for private in values:
        if len(private) >= 4:
            payload = payload.replace(private, b"[REDACTED]")
            continue
        # A short credential cannot safely be replaced as an isolated token:
        # doing so would corrupt unrelated words and structured scalars. It is
        # still redacted when diagnostic output identifies it as a private
        # field assignment, preserving the complete surrounding context.
        for pattern in _PRIVATE_ASSIGNMENTS:
            matches = list(pattern.finditer(payload))
            for match in reversed(matches):
                if match.group("value") != private:
                    continue
                replacement = match.group(0).replace(private, b"[REDACTED]")
                payload = payload[:match.start()] + replacement + payload[match.end():]
    return payload


def _png_dimensions(payload: bytes) -> tuple[int, int]:
    if not payload.startswith(_PNG_SIGNATURE):
        raise CollectionError("PNG signature is invalid")
    offset = len(_PNG_SIGNATURE)
    width = height = None
    saw_idat = False
    saw_iend = False
    while offset < len(payload):
        if len(payload) - offset < 12:
            raise CollectionError("PNG chunk framing is truncated")
        length = struct.unpack_from(">I", payload, offset)[0]
        offset += 4
        kind = payload[offset:offset + 4]
        offset += 4
        end = offset + length
        if end + 4 > len(payload):
            raise CollectionError("PNG chunk payload is truncated")
        chunk = payload[offset:end]
        offset = end
        expected_crc = struct.unpack_from(">I", payload, offset)[0]
        offset += 4
        if zlib.crc32(kind + chunk) & 0xFFFFFFFF != expected_crc:
            raise CollectionError("PNG chunk checksum is invalid")
        if width is None and kind != b"IHDR":
            raise CollectionError("PNG does not begin with IHDR")
        if kind == b"IHDR":
            if width is not None or len(chunk) != 13:
                raise CollectionError("PNG IHDR is invalid")
            width, height, bit_depth, color_type, compression, filtering, interlace = struct.unpack(
                ">IIBBBBB", chunk
            )
            if (
                width <= 0 or height <= 0 or bit_depth != 8
                or color_type not in {2, 6}
                or compression != 0 or filtering != 0 or interlace != 0
            ):
                raise CollectionError("PNG IHDR encoding is unsupported")
        elif kind == b"IDAT":
            saw_idat = True
        elif kind == b"IEND":
            if chunk:
                raise CollectionError("PNG IEND is invalid")
            saw_iend = True
            if offset != len(payload):
                raise CollectionError("PNG has trailing bytes after IEND")
            break
    if width is None or height is None or not saw_idat or not saw_iend:
        raise CollectionError("PNG is missing a complete IDAT/IEND stream")
    return width, height


def _sha256(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def _looks_text(path: Path, payload: bytes) -> bool:
    if path.suffix.lower() in _TEXT_SUFFIXES:
        return True
    if b"\x00" in payload:
        return False
    try:
        payload.decode("utf-8")
    except UnicodeDecodeError:
        return False
    return True


def _forward_text_payload(relative: Path, payload: bytes) -> None:
    """Forward one stored text payload without mixing it with binary data.

    The collector's stdout remains a metadata stream for callers that parse
    one JSON record per file.  Redacted text is therefore forwarded on stderr
    with explicit path/stream boundaries.  ``backslashreplace`` keeps every
    malformed byte observable without making the diagnostics command itself
    fail while rendering it.
    """

    path = relative.as_posix()
    rendered = payload.decode("utf-8", errors="backslashreplace")
    stream = sys.stderr
    stream.write(f"[diagnostic path={path} stream=text begin]\n")
    stream.write(rendered)
    if rendered and not rendered.endswith("\n"):
        stream.write("\n")
    stream.write(f"[diagnostic path={path} stream=text end]\n")
    stream.flush()


def _source_files(source: Path) -> list[Path]:
    """Enumerate every regular file and fail visibly on unreadable folders."""

    pending = [source]
    files: list[Path] = []
    while pending:
        directory = pending.pop()
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    path = Path(entry.path)
                    if entry.is_symlink():
                        raise CollectionError(
                            f"diagnostics source contains a symlink: {path}"
                        )
                    if entry.is_dir(follow_symlinks=False):
                        pending.append(path)
                    elif entry.is_file(follow_symlinks=False):
                        files.append(path)
        except OSError as error:
            raise CollectionError(
                f"diagnostics source directory could not be read: {directory}"
            ) from error
    return sorted(files)


def collect(profile: Path, sources: list[Path], output: Path) -> list[dict[str, object]]:
    if not profile.is_file() or profile.is_symlink():
        raise CollectionError("profile is unavailable")
    if output.exists():
        raise CollectionError("diagnostics output already exists")
    output_resolved = output.resolve()
    output.mkdir(mode=0o700, parents=True)
    output.chmod(0o700)
    private_values = _private_values(profile)
    records: list[dict[str, object]] = []
    seen_destinations: set[Path] = set()
    for source in sources:
        if not source.is_dir() or source.is_symlink():
            raise CollectionError(f"diagnostics source is unavailable: {source}")
        source_resolved = source.resolve()
        if output_resolved == source_resolved or output_resolved.is_relative_to(source_resolved):
            raise CollectionError("diagnostics output must not be inside a source directory")
        for path in _source_files(source):
            relative = Path(source.name) / path.relative_to(source)
            destination = output / relative
            if destination in seen_destinations:
                raise CollectionError(f"diagnostics sources overlap at {relative}")
            seen_destinations.add(destination)
            destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            try:
                payload = path.read_bytes()
            except OSError as error:
                raise CollectionError(
                    f"diagnostics source file could not be read: {relative}"
                ) from error
            source_bytes = len(payload)
            source_sha256 = _sha256(payload)
            width = height = None
            if path.suffix.lower() == ".png":
                kind = "binary"
                mime = "image/png"
                width, height = _png_dimensions(payload)
                stored = payload
            elif _looks_text(path, payload):
                kind = "text"
                mime = mimetypes.guess_type(path.name)[0] or "text/plain"
                stored = _redact(payload, private_values)
            else:
                kind = "binary"
                mime = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
                stored = payload
            destination.write_bytes(stored)
            destination.chmod(0o600)
            try:
                if destination.read_bytes() != stored:
                    raise CollectionError(f"diagnostics transfer verification failed: {relative}")
            except OSError as error:
                raise CollectionError(f"diagnostics transfer verification failed: {relative}") from error
            record: dict[str, object] = {
                "path": relative.as_posix(),
                "kind": kind,
                "mime_type": mime,
                "mime": mime,
                "bytes": len(stored),
                "sha256": _sha256(stored),
                "source_bytes": source_bytes,
                "source_sha256": source_sha256,
                "redacted": stored != payload,
            }
            if width is not None and height is not None:
                record.update(width=width, height=height)
            records.append(record)
            print(json.dumps(record, sort_keys=True))
            if kind == "text":
                _forward_text_payload(relative, stored)
    if not records:
        raise CollectionError("diagnostics sources contain no readable files")
    manifest = {"schema": 2, "files": records}
    manifest_path = output / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    manifest_path.chmod(0o600)
    return records


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--source", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    collect(args.profile, args.source, args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
