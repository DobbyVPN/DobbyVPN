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

from version_metadata import parse_version


MANIFEST_NAME = "release-provenance.json"
SCHEMA = 1
PUBLISHED_SCHEMA = 2
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
PUBLISHED_MANIFEST_KEYS = frozenset({
    "android_version_code", "apple_build_number", "assets", "build_manifest_sha256",
    "build_run_id", "build_run_number", "release_notes_sha256", "publish_run_id",
    "publish_run_number", "schema", "source_sha", "tag", "test_result_sha256",
    "test_run_id", "test_run_number", "version",
})


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


def _validate_version_document(directory: Path, metadata: dict[str, Any], asset_names: list[str]) -> None:
    """Check the legacy HTTP updater document when it is in this manifest."""
    if "version.txt" not in asset_names:
        return

    try:
        parsed = parse_version(metadata["version"])
    except ValueError as error:
        raise ProvenanceError(f"release version cannot produce a version.txt document: {error}") from error
    if parsed.android_version_code != metadata["android_version_code"]:
        raise ProvenanceError("Android version code does not match the canonical release version")
    expected = parsed.update_document().encode("utf-8")
    try:
        actual = (directory / "version.txt").read_bytes()
    except FileNotFoundError as error:
        raise ProvenanceError("missing asset: version.txt") from error
    except OSError as error:
        raise ProvenanceError("cannot read asset: version.txt") from error
    if actual != expected:
        raise ProvenanceError("version.txt does not match the asserted release version metadata")


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
    _validate_version_document(directory, metadata, asset_names)
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
    _validate_version_document(directory, metadata, asset_names)
    manifest = directory / MANIFEST_NAME
    payload = _load_manifest(manifest)
    records = _validate_manifest_payload(payload, metadata, asset_names)
    for record in records:
        if _file_record(directory / record["name"], record["name"]) != record:
            raise ProvenanceError(f"asset digest or metadata does not match manifest: {record['name']}")
    return manifest


def _validate_published_metadata(**values: Any) -> dict[str, Any]:
    tag, version, source_sha = values["tag"], values["version"], values["source_sha"]
    if not isinstance(tag, str) or not TAG_RE.fullmatch(tag):
        raise ProvenanceError("tag must be canonical vMAJOR.MINOR.PATCH without leading zeroes")
    if not isinstance(version, str) or version != tag[1:]:
        raise ProvenanceError("version must exactly match the canonical tag without its v prefix")
    if not isinstance(source_sha, str) or not SOURCE_SHA_RE.fullmatch(source_sha):
        raise ProvenanceError("source_sha must be exactly 40 lowercase hexadecimal characters")
    checked: dict[str, Any] = {"tag": tag, "version": version, "source_sha": source_sha}
    for key in (
        "build_run_id", "build_run_number", "test_run_id", "test_run_number", "publish_run_id",
        "publish_run_number", "android_version_code", "apple_build_number",
    ):
        checked[key] = _positive_int(values[key], key)
    for key in ("build_manifest_sha256", "test_result_sha256", "release_notes_sha256"):
        value = values[key]
        if not isinstance(value, str) or not SHA256_RE.fullmatch(value):
            raise ProvenanceError(f"{key} must be exactly 64 lowercase hexadecimal characters")
        checked[key] = value
    return checked


def _validate_published_payload(
    payload: Any, metadata: dict[str, Any], asset_names: list[str],
) -> list[dict[str, Any]]:
    if not isinstance(payload, dict) or set(payload) != PUBLISHED_MANIFEST_KEYS:
        raise ProvenanceError("published provenance has unexpected or missing fields")
    if payload["schema"] != PUBLISHED_SCHEMA:
        raise ProvenanceError(f"published provenance schema must be {PUBLISHED_SCHEMA}")
    checked = _validate_published_metadata(**{
        key: payload[key] for key in PUBLISHED_MANIFEST_KEYS if key != "assets" and key != "schema"
    })
    if checked != metadata:
        raise ProvenanceError("published provenance does not match the selected Build, Test and Publish")
    records = payload["assets"]
    if not isinstance(records, list) or len(records) != len(asset_names):
        raise ProvenanceError("published asset records do not match the asserted allowlist")
    names: list[str] = []
    for record in records:
        if not isinstance(record, dict) or set(record) != ASSET_KEYS:
            raise ProvenanceError("published asset record has unexpected fields")
        name, size, sha256 = record["name"], record["size"], record["sha256"]
        if not isinstance(name, str) or not isinstance(sha256, str) or not SHA256_RE.fullmatch(sha256):
            raise ProvenanceError("published asset record has invalid name or sha256")
        _nonnegative_int(size, "asset size")
        names.append(name)
    if len(set(names)) != len(names) or set(names) != set(asset_names):
        raise ProvenanceError("published assets must exactly match the asserted allowlist")
    return records


def create_published_manifest(
    directory: Path, *, tag: str, version: str, source_sha: str,
    build_run_id: int, build_run_number: int, test_run_id: int, test_run_number: int,
    publish_run_id: int, publish_run_number: int, android_version_code: int,
    apple_build_number: int, build_manifest_sha256: str, test_result_sha256: str,
    release_notes_sha256: str, assets: Iterable[str],
) -> Path:
    """Write public provenance linking one published package set to Build and Test."""
    directory = Path(directory)
    asset_names = _validate_asset_names(assets)
    metadata = _validate_published_metadata(
        tag=tag, version=version, source_sha=source_sha, build_run_id=build_run_id,
        build_run_number=build_run_number, test_run_id=test_run_id, test_run_number=test_run_number,
        publish_run_id=publish_run_id, publish_run_number=publish_run_number,
        android_version_code=android_version_code, apple_build_number=apple_build_number,
        build_manifest_sha256=build_manifest_sha256, test_result_sha256=test_result_sha256,
        release_notes_sha256=release_notes_sha256,
    )
    _validate_version_document(directory, metadata, asset_names)
    records = [_file_record(directory / name, name) for name in asset_names]
    manifest = directory / MANIFEST_NAME
    manifest.write_bytes(_json_bytes({"schema": PUBLISHED_SCHEMA, **metadata, "assets": records}))
    return manifest


def verify_published_manifest(
    directory: Path, *, tag: str, version: str, source_sha: str,
    build_run_id: int, build_run_number: int, test_run_id: int, test_run_number: int,
    publish_run_id: int, publish_run_number: int, android_version_code: int,
    apple_build_number: int, build_manifest_sha256: str, test_result_sha256: str,
    release_notes_sha256: str, assets: Iterable[str],
) -> Path:
    """Verify the published assets and their Build/Test/Publish identities."""
    directory = Path(directory)
    asset_names = _validate_asset_names(assets)
    metadata = _validate_published_metadata(
        tag=tag, version=version, source_sha=source_sha, build_run_id=build_run_id,
        build_run_number=build_run_number, test_run_id=test_run_id, test_run_number=test_run_number,
        publish_run_id=publish_run_id, publish_run_number=publish_run_number,
        android_version_code=android_version_code, apple_build_number=apple_build_number,
        build_manifest_sha256=build_manifest_sha256, test_result_sha256=test_result_sha256,
        release_notes_sha256=release_notes_sha256,
    )
    _validate_version_document(directory, metadata, asset_names)
    manifest = directory / MANIFEST_NAME
    records = _validate_published_payload(_load_manifest(manifest), metadata, asset_names)
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


def _add_published_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-sha", required=True)
    for name in ("build-run-id", "build-run-number", "test-run-id", "test-run-number", "publish-run-id", "publish-run-number",
                 "android-version-code", "apple-build-number"):
        parser.add_argument(f"--{name}", type=_positive_argument, required=True)
    for name in ("build-manifest-sha256", "test-result-sha256", "release-notes-sha256"):
        parser.add_argument(f"--{name}", required=True)
    parser.add_argument("--asset", action="append", required=True, help="exact public asset filename; repeat as needed")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="create release-provenance.json")
    verify = commands.add_parser("verify", help="verify release-provenance.json")
    create_published = commands.add_parser("create-published", help="create Build/Test/Publish provenance")
    verify_published = commands.add_parser("verify-published", help="verify Build/Test/Publish provenance")
    _add_common_arguments(create)
    _add_common_arguments(verify)
    _add_published_arguments(create_published)
    _add_published_arguments(verify_published)
    args = parser.parse_args(argv)
    try:
        if args.command in {"create", "verify"}:
            operation = create_manifest if args.command == "create" else verify_manifest
            manifest = operation(
                args.directory, tag=args.tag, version=args.version, source_sha=args.source_sha,
                release_run_id=args.release_run_id, release_run_number=args.release_run_number,
                android_version_code=args.android_version_code, assets=args.asset,
            )
        else:
            operation = create_published_manifest if args.command == "create-published" else verify_published_manifest
            manifest = operation(
                args.directory, tag=args.tag, version=args.version, source_sha=args.source_sha,
                build_run_id=args.build_run_id, build_run_number=args.build_run_number,
                test_run_id=args.test_run_id, test_run_number=args.test_run_number,
                publish_run_id=args.publish_run_id,
                publish_run_number=args.publish_run_number, android_version_code=args.android_version_code,
                apple_build_number=args.apple_build_number, build_manifest_sha256=args.build_manifest_sha256,
                test_result_sha256=args.test_result_sha256, release_notes_sha256=args.release_notes_sha256,
                assets=args.asset,
            )
    except (OSError, ProvenanceError) as error:
        print(f"release provenance error: {error}", file=sys.stderr)
        return 1
    print(manifest)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
