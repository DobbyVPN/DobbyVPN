#!/usr/bin/env python3
"""Package and validate the attempt-bound Android CI build payload."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import sys
import zipfile

MANIFEST = "android-ci-build-manifest.json"
CHECKSUMS = "SHA256SUMS"
SHA40 = re.compile(r"[0-9a-f]{40}\Z")
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")


class ArtifactError(ValueError):
    """The Android CI artifact is incomplete or bound to a different build."""


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def filenames(version_name: str) -> tuple[str, str]:
    if not VERSION.fullmatch(version_name):
        raise ArtifactError("version name must use canonical X.Y.Z format")
    return f"DobbyVPN-v{version_name}-unsign.apk", f"DobbyVPN-v{version_name}-debug.apk"


def descriptor(path: Path) -> dict[str, object]:
    if not path.is_file() or path.is_symlink() or path.stat().st_size == 0:
        raise ArtifactError(f"APK is missing, empty, or not a regular file: {path}")
    try:
        with zipfile.ZipFile(path) as archive:
            if "AndroidManifest.xml" not in archive.namelist() or archive.testzip() is not None:
                raise ArtifactError(f"APK is incomplete or has a corrupt ZIP entry: {path.name}")
    except zipfile.BadZipFile as error:
        raise ArtifactError(f"APK is not a valid ZIP archive: {path.name}") from error
    return {"path": path.name, "bytes": path.stat().st_size, "sha256": sha256(path)}


def package(args: argparse.Namespace) -> None:
    if not SHA40.fullmatch(args.source_sha):
        raise ArtifactError("source SHA must be a full lowercase commit identity")
    if not REPOSITORY.fullmatch(args.repository):
        raise ArtifactError("repository must be OWNER/NAME")
    if not args.run_id.isdigit() or int(args.run_id) < 1 or args.run_attempt < 1:
        raise ArtifactError("producer run ID and attempt must be positive integers")
    if args.version_code < 1:
        raise ArtifactError("version code must be positive")
    release_name, debug_name = filenames(args.version_name)
    output = args.output_dir
    if output.exists() and any(output.iterdir()):
        raise ArtifactError(f"refusing to package into a nonempty directory: {output}")
    output.mkdir(parents=True, exist_ok=True)
    release = output / release_name
    debug = output / debug_name
    shutil.copyfile(args.release_apk, release)
    shutil.copyfile(args.debug_apk, debug)
    artifacts = [descriptor(release), descriptor(debug)]
    document = {
        "schema": 1,
        "repository": args.repository,
        "source_sha": args.source_sha,
        "producer_run_id": args.run_id,
        "producer_run_attempt": args.run_attempt,
        "version_name": args.version_name,
        "version_code": args.version_code,
        "artifacts": artifacts,
    }
    (output / MANIFEST).write_text(
        json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8"
    )
    (output / CHECKSUMS).write_text(
        "".join(f"{item['sha256']}  {item['path']}\n" for item in artifacts), encoding="ascii"
    )


def validate(args: argparse.Namespace) -> None:
    if not SHA40.fullmatch(args.source_sha) or not REPOSITORY.fullmatch(args.repository):
        raise ArtifactError("expected source or repository identity is invalid")
    if not args.run_id.isdigit() or int(args.run_id) < 1 or args.run_attempt < 1:
        raise ArtifactError("expected producer run ID and attempt must be positive integers")
    release_name, debug_name = filenames(args.version_name)
    root = args.artifact_dir
    expected_files = {release_name, debug_name, MANIFEST, CHECKSUMS}
    if not root.is_dir() or any(path.is_symlink() for path in root.iterdir()):
        raise ArtifactError("downloaded Android artifact directory is missing or contains symlinks")
    observed_files = {path.name for path in root.iterdir() if path.is_file()}
    if observed_files != expected_files or len(observed_files) != len(list(root.iterdir())):
        raise ArtifactError(
            f"Android artifact payload is incomplete or unexpected: expected {sorted(expected_files)}, "
            f"found {sorted(observed_files)}"
        )
    try:
        document = json.loads((root / MANIFEST).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ArtifactError("Android CI build manifest is unreadable or invalid JSON") from error
    expected_identity = {
        "schema": 1,
        "repository": args.repository,
        "source_sha": args.source_sha,
        "producer_run_id": args.run_id,
        "producer_run_attempt": args.run_attempt,
        "version_name": args.version_name,
        "version_code": args.version_code,
    }
    if not isinstance(document, dict) or any(document.get(key) != value for key, value in expected_identity.items()):
        raise ArtifactError("Android CI build manifest does not match source, producer attempt, or version")
    if set(document) != {*expected_identity, "artifacts"}:
        raise ArtifactError("Android CI build manifest has missing or unexpected fields")
    expected_artifacts = [descriptor(root / release_name), descriptor(root / debug_name)]
    if document.get("artifacts") != expected_artifacts:
        raise ArtifactError("Android CI build manifest APK checksums do not match the payload")
    expected_checksums = "".join(
        f"{item['sha256']}  {item['path']}\n" for item in expected_artifacts
    )
    if (root / CHECKSUMS).read_text(encoding="ascii") != expected_checksums:
        raise ArtifactError("Android CI SHA256SUMS does not match the complete APK payload")
    print(
        f"Validated Android CI payload for source {args.source_sha}, "
        f"run {args.run_id} attempt {args.run_attempt}"
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("package")
    create.add_argument("--release-apk", required=True, type=Path)
    create.add_argument("--debug-apk", required=True, type=Path)
    create.add_argument("--output-dir", required=True, type=Path)
    create.add_argument("--source-sha", required=True)
    create.add_argument("--repository", required=True)
    create.add_argument("--run-id", required=True)
    create.add_argument("--run-attempt", required=True, type=int)
    create.add_argument("--version-name", required=True)
    create.add_argument("--version-code", required=True, type=int)
    check = commands.add_parser("verify")
    check.add_argument("--artifact-dir", required=True, type=Path)
    check.add_argument("--source-sha", required=True)
    check.add_argument("--repository", required=True)
    check.add_argument("--run-id", required=True)
    check.add_argument("--run-attempt", required=True, type=int)
    check.add_argument("--version-name", required=True)
    check.add_argument("--version-code", required=True, type=int)
    args = parser.parse_args(argv)
    try:
        package(args) if args.command == "package" else validate(args)
    except (ArtifactError, OSError, ValueError) as error:
        print(f"Android CI artifact {args.command} failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
