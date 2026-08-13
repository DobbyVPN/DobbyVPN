#!/usr/bin/env python3
"""Create and verify the deterministic public release provenance manifest.

The manifest deliberately describes only public release metadata and files.  It
is not a place for qualification evidence, credentials, configuration, logs,
or any other private operational data.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
from typing import Any, Iterable


MANIFEST_NAME = "release-provenance.json"
SCHEMA = 1
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")
SOURCE_SHA_RE = re.compile(r"[0-9a-f]{40}\Z")
TAG_RE = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\Z")
MANIFEST_KEYS = frozenset(
    {
        "android_version_code",
        "assets",
        "release_run_id",
        "release_run_number",
        "schema",
        "source_sha",
        "tag",
        "version",
    }
)
ASSET_KEYS = frozenset({"name", "sha256", "size"})


class ProvenanceError(ValueError):
    """Raised when public release provenance is malformed or inconsistent."""


def _positive_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise ProvenanceError(f"{label} must be a positive integer")
    return value


def _validate_metadata(
    *,
    tag: Any,
    version: Any,
    source_sha: Any,
    release_run_id: Any,
    release_run_number: Any,
    android_version_code: Any,
) -> dict[str, Any]:
    if not isinstance(tag, str) or not TAG_RE.fullmatch(tag):
        raise ProvenanceError("tag must be canonical vMAJOR.MINOR.PATCH without leading zeroes")
    if not isinstance(version, str) or version != tag[1:]:
        raise ProvenanceError("version must exactly match the canonical tag without its v prefix")
    if not isinstance(source_sha, str) or not SOURCE_SHA_RE.fullmatch(source_sha):
        raise ProvenanceError("source_sha must be exactly 40 lowercase hexadecimal characters")
    return {
        "tag": tag,
        "version": version,
        "source_sha": source_sha,
        "release_run_id": _positive_int(release_run_id, "release_run_id"),
        "release_run_number": _positive_int(release_run_number, "release_run_number"),
        "android_version_code": _positive_int(android_version_code, "android_version_code"),
    }


def _validate_asset_names(asset_names: Iterable[Any]) -> list[str]:
    names = list(asset_names)
    if not names:
        raise ProvenanceError("at least one public release asset is required")
    for name in names:
        if not isinstance(name, str) or not name:
            raise ProvenanceError("asset names must be non-empty strings")
        if name in (".", "..", MANIFEST_NAME) or "/" in name or "\\" in name or "\x00" in name:
            raise ProvenanceError(f"asset name is not a flat safe filename: {name!r}")
        if Path(name).name != name:
            raise ProvenanceError(f"asset name is not a flat safe filename: {name!r}")
    if names != sorted(names):
        raise ProvenanceError("asset allowlist must be supplied in strictly sorted order")
    if len(names) != len(set(names)):
        raise ProvenanceError("asset allowlist contains duplicate names")
    return names


def _regular_file(path: Path, label: str) -> os.stat_result:
    try:
        result = path.lstat()
    except FileNotFoundError as error:
        raise ProvenanceError(f"missing {label}: {path.name}") from error
    if stat.S_ISLNK(result.st_mode):
        raise ProvenanceError(f"{label} must not be a symlink: {path.name}")
    if not stat.S_ISREG(result.st_mode):
        raise ProvenanceError(f"{label} must be a regular file: {path.name}")
    return result


def _assert_directory_shape(directory: Path, allowed_assets: list[str], *, allow_manifest: bool) -> None:
    try:
        directory_stat = directory.lstat()
    except FileNotFoundError as error:
        raise ProvenanceError(f"release directory does not exist: {directory}") from error
    if stat.S_ISLNK(directory_stat.st_mode) or not stat.S_ISDIR(directory_stat.st_mode):
        raise ProvenanceError(f"release directory must be a real directory: {directory}")

    expected = set(allowed_assets)
    if allow_manifest:
        expected.add(MANIFEST_NAME)
    found: set[str] = set()
    for entry in directory.iterdir():
        found.add(entry.name)
        _regular_file(entry, "release directory entry")
    missing = sorted(expected - found)
    extra = sorted(found - expected)
    if missing or extra:
        details = []
        if missing:
            details.append("missing: " + ", ".join(missing))
        if extra:
            details.append("unexpected: " + ", ".join(extra))
        raise ProvenanceError("release directory must contain exactly the allowlisted assets" + "; " + "; ".join(details))


def _file_record(path: Path, name: str) -> dict[str, Any]:
    before = _regular_file(path, "asset")
    if before.st_size <= 0:
        raise ProvenanceError(f"asset must not be empty: {name}")
    digest = hashlib.sha256()
    try:
        with _open_checked_read(path, before, "asset") as source:
            while block := source.read(1024 * 1024):
                digest.update(block)
    except OSError as error:
        raise ProvenanceError(f"cannot read asset: {name}") from error
    after = _regular_file(path, "asset")
    if (
        before.st_dev,
        before.st_ino,
        before.st_size,
        before.st_mtime_ns,
    ) != (
        after.st_dev,
        after.st_ino,
        after.st_size,
        after.st_mtime_ns,
    ):
        raise ProvenanceError(f"asset changed while it was hashed: {name}")
    return {"name": name, "size": before.st_size, "sha256": digest.hexdigest()}


def _canonical_bytes(payload: dict[str, Any]) -> bytes:
    return (json.dumps(payload, ensure_ascii=True, separators=(",", ":"), sort_keys=True) + "\n").encode("utf-8")


def _reject_duplicate_json_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ProvenanceError(f"manifest has duplicate key: {key}")
        result[key] = value
    return result


def _reject_nonstandard_json_constant(value: str) -> None:
    raise ProvenanceError(f"manifest contains a non-standard JSON constant: {value}")


def _load_canonical_manifest(path: Path) -> dict[str, Any]:
    raw = _read_regular_file(path, "manifest")
    try:
        payload = json.loads(
            raw.decode("utf-8"),
            object_pairs_hook=_reject_duplicate_json_keys,
            parse_constant=_reject_nonstandard_json_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ProvenanceError("manifest is not valid UTF-8 JSON") from error
    if not isinstance(payload, dict):
        raise ProvenanceError("manifest root must be a JSON object")
    if raw != _canonical_bytes(payload):
        raise ProvenanceError("manifest is not in canonical deterministic form")
    return payload


def _read_regular_file(path: Path, label: str) -> bytes:
    before = _regular_file(path, label)
    try:
        with _open_checked_read(path, before, label) as source:
            data = source.read()
    except OSError as error:
        raise ProvenanceError(f"cannot read {label}: {path.name}") from error
    after = _regular_file(path, label)
    if (
        before.st_dev,
        before.st_ino,
        before.st_size,
        before.st_mtime_ns,
    ) != (
        after.st_dev,
        after.st_ino,
        after.st_size,
        after.st_mtime_ns,
    ):
        raise ProvenanceError(f"{label} changed while it was read: {path.name}")
    return data


def _open_checked_read(path: Path, expected: os.stat_result, label: str):
    """Open one already-lstat'ed file without following a swapped symlink."""
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise ProvenanceError(f"cannot open {label}: {path.name}") from error
    try:
        opened = os.fstat(descriptor)
        if not stat.S_ISREG(opened.st_mode) or (opened.st_dev, opened.st_ino) != (
            expected.st_dev,
            expected.st_ino,
        ):
            raise ProvenanceError(f"{label} changed while it was opened: {path.name}")
        return os.fdopen(descriptor, "rb")
    except BaseException:
        os.close(descriptor)
        raise


def _validate_manifest_payload(payload: Any, metadata: dict[str, Any], asset_names: list[str]) -> list[dict[str, Any]]:
    if not isinstance(payload, dict) or set(payload) != MANIFEST_KEYS:
        raise ProvenanceError("manifest has an unexpected schema or fields")
    if payload["schema"] != SCHEMA:
        raise ProvenanceError(f"manifest schema must be {SCHEMA}")
    checked_metadata = _validate_metadata(
        tag=payload["tag"],
        version=payload["version"],
        source_sha=payload["source_sha"],
        release_run_id=payload["release_run_id"],
        release_run_number=payload["release_run_number"],
        android_version_code=payload["android_version_code"],
    )
    if checked_metadata != metadata:
        raise ProvenanceError("manifest metadata does not match the asserted release metadata")
    records = payload["assets"]
    if not isinstance(records, list) or len(records) != len(asset_names):
        raise ProvenanceError("manifest asset records do not match the asserted allowlist")
    names: list[str] = []
    for record in records:
        if not isinstance(record, dict) or set(record) != ASSET_KEYS:
            raise ProvenanceError("manifest asset record has unexpected fields")
        name, size, sha256 = record["name"], record["size"], record["sha256"]
        if not isinstance(name, str) or not isinstance(sha256, str) or not SHA256_RE.fullmatch(sha256):
            raise ProvenanceError("manifest asset record has invalid name or sha256")
        _positive_int(size, "asset size")
        names.append(name)
    if names != asset_names:
        raise ProvenanceError("manifest assets must exactly match the sorted asserted allowlist")
    return records


def create_manifest(
    directory: Path,
    *,
    tag: str,
    version: str,
    source_sha: str,
    release_run_id: int,
    release_run_number: int,
    android_version_code: int,
    assets: Iterable[str],
) -> Path:
    """Write a canonical manifest after validating exactly the public assets."""
    directory = Path(directory)
    asset_names = _validate_asset_names(assets)
    metadata = _validate_metadata(
        tag=tag,
        version=version,
        source_sha=source_sha,
        release_run_id=release_run_id,
        release_run_number=release_run_number,
        android_version_code=android_version_code,
    )
    manifest = directory / MANIFEST_NAME
    _assert_directory_shape(directory, asset_names, allow_manifest=manifest.exists())
    records = [_file_record(directory / name, name) for name in asset_names]
    payload = {"schema": SCHEMA, **metadata, "assets": records}
    encoded = _canonical_bytes(payload)
    temporary = directory / f".{MANIFEST_NAME}.tmp-{os.getpid()}"
    try:
        with temporary.open("xb") as output:
            output.write(encoded)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, manifest)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass
    _assert_directory_shape(directory, asset_names, allow_manifest=True)
    return manifest


def verify_manifest(
    directory: Path,
    *,
    tag: str,
    version: str,
    source_sha: str,
    release_run_id: int,
    release_run_number: int,
    android_version_code: int,
    assets: Iterable[str],
) -> Path:
    """Fail closed unless the directory and canonical manifest agree exactly."""
    directory = Path(directory)
    asset_names = _validate_asset_names(assets)
    metadata = _validate_metadata(
        tag=tag,
        version=version,
        source_sha=source_sha,
        release_run_id=release_run_id,
        release_run_number=release_run_number,
        android_version_code=android_version_code,
    )
    _assert_directory_shape(directory, asset_names, allow_manifest=True)
    manifest = directory / MANIFEST_NAME
    payload = _load_canonical_manifest(manifest)
    records = _validate_manifest_payload(payload, metadata, asset_names)
    for record in records:
        if _file_record(directory / record["name"], record["name"]) != record:
            raise ProvenanceError(f"asset digest or metadata does not match manifest: {record['name']}")
    _assert_directory_shape(directory, asset_names, allow_manifest=True)
    return manifest


def _positive_argument(value: str) -> int:
    try:
        result = int(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be a positive integer") from error
    if result <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return result


def _add_common_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--release-run-id", type=_positive_argument, required=True)
    parser.add_argument("--release-run-number", type=_positive_argument, required=True)
    parser.add_argument("--android-version-code", type=_positive_argument, required=True)
    parser.add_argument("--asset", action="append", required=True, help="exact public asset filename; repeat in sorted order")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="create canonical release-provenance.json")
    verify = commands.add_parser("verify", help="verify canonical release-provenance.json")
    _add_common_arguments(create)
    _add_common_arguments(verify)
    args = parser.parse_args(argv)
    operation = create_manifest if args.command == "create" else verify_manifest
    try:
        manifest = operation(
            args.directory,
            tag=args.tag,
            version=args.version,
            source_sha=args.source_sha,
            release_run_id=args.release_run_id,
            release_run_number=args.release_run_number,
            android_version_code=args.android_version_code,
            assets=args.asset,
        )
    except (OSError, ProvenanceError) as error:
        print(f"release provenance error: {error}", file=sys.stderr)
        return 1
    print(manifest)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
