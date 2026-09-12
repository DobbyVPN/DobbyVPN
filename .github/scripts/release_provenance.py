#!/usr/bin/env python3
"""Create and verify public release provenance manifests.

The manifest deliberately describes only public release metadata and files.  It
is not a place for qualification evidence, credentials, configuration, logs,
or any other private operational data.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
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


def _nonnegative_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ProvenanceError(f"{label} must be a nonnegative integer")
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
        if name == MANIFEST_NAME:
            raise ProvenanceError("the provenance manifest cannot describe itself")
    return sorted(set(names))


def _file_record(path: Path, name: str) -> dict[str, Any]:
    try:
        data = path.read_bytes()
    except FileNotFoundError as error:
        raise ProvenanceError(f"missing asset: {name}") from error
    except OSError as error:
        raise ProvenanceError(f"cannot read asset: {name}") from error
    return {"name": name, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()}


def _json_bytes(payload: dict[str, Any]) -> bytes:
    return (json.dumps(payload, ensure_ascii=True, separators=(",", ":"), sort_keys=True) + "\n").encode("utf-8")


def _load_manifest(path: Path) -> dict[str, Any]:
    try:
        raw = path.read_bytes()
    except FileNotFoundError as error:
        raise ProvenanceError(f"missing manifest: {path.name}") from error
    except OSError as error:
        raise ProvenanceError(f"cannot read manifest: {path.name}") from error
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ProvenanceError("manifest is not valid UTF-8 JSON") from error
    if not isinstance(payload, dict):
        raise ProvenanceError("manifest root must be a JSON object")
    return payload


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
        _nonnegative_int(size, "asset size")
        names.append(name)
    if set(names) != set(asset_names):
        raise ProvenanceError("manifest assets must exactly match the asserted allowlist")
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
    """Write a manifest describing exactly the named public assets."""
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
    records = [_file_record(directory / name, name) for name in asset_names]
    payload = {"schema": SCHEMA, **metadata, "assets": records}
    encoded = _json_bytes(payload)
    manifest.write_bytes(encoded)
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
    """Verify that the named release assets match the manifest."""
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
    payload = _load_manifest(manifest)
    records = _validate_manifest_payload(payload, metadata, asset_names)
    for record in records:
        if _file_record(directory / record["name"], record["name"]) != record:
            raise ProvenanceError(f"asset digest or metadata does not match manifest: {record['name']}")
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
    parser.add_argument("--asset", action="append", required=True, help="exact public asset filename; repeat as needed")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="create release-provenance.json")
    verify = commands.add_parser("verify", help="verify release-provenance.json")
    _add_common_arguments(create)
    _add_common_arguments(verify)
    args = parser.parse_args(argv)
    if args.command == "create":
        operation = create_manifest
    else:
        operation = verify_manifest
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
