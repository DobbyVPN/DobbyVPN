#!/usr/bin/env python3
"""Build one complete, binary-safe diagnostics directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import struct
import sys
import zlib


_TEXT_SUFFIXES = frozenset({".json", ".jsonl", ".log", ".txt", ".out", ".err", ".xml", ".md"})
_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


class CollectionError(RuntimeError):
    pass


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
    """Forward the exact stored bytes with path boundaries on stderr."""
    path = relative.as_posix()
    stream = sys.stderr
    binary = getattr(stream, "buffer", None)
    if binary is not None:
        binary.write(f"[diagnostic path={path} stream=text begin]\n".encode("utf-8"))
        binary.write(payload)
        if payload and not payload.endswith(b"\n"):
            binary.write(b"\n")
        binary.write(f"[diagnostic path={path} stream=text end]\n".encode("utf-8"))
        binary.flush()
        return
    rendered = payload.decode("utf-8", errors="backslashreplace")
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


def collect(sources: list[Path], output: Path) -> list[dict[str, object]]:
    if output.exists():
        raise CollectionError("diagnostics output already exists")
    output_resolved = output.resolve()
    output.mkdir(mode=0o700, parents=True)
    output.chmod(0o700)
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
            width = height = None
            if path.suffix.lower() == ".png":
                kind = "binary"
                mime = "image/png"
                width, height = _png_dimensions(payload)
                stored = payload
            elif _looks_text(path, payload):
                kind = "text"
                mime = mimetypes.guess_type(path.name)[0] or "text/plain"
                stored = payload
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
            }
            if width is not None and height is not None:
                record.update(width=width, height=height)
            records.append(record)
            print(json.dumps(record, sort_keys=True))
            if kind == "text":
                _forward_text_payload(relative, stored)
    if not records:
        raise CollectionError("diagnostics sources contain no readable files")
    manifest = {"schema": 3, "files": records}
    manifest_path = output / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    manifest_path.chmod(0o600)
    return records


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    collect(args.source, args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
