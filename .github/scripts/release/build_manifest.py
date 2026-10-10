#!/usr/bin/env python3
"""Create or resolve the immutable artifact inventory for one Build run."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any

BUILD_WORKFLOW_PATH = ".github/workflows/build.yml"
MANIFEST_ARTIFACT = "build-manifest"
SHA_RE = re.compile(r"[0-9a-f]{40}\Z")
DIGEST_RE = re.compile(r"sha256:[0-9a-f]{64}\Z")
VERSION_RE = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")


class BuildManifestError(ValueError):
    """The selected Build identity or artifact inventory is invalid."""


def required_artifacts(version: str) -> tuple[str, ...]:
    return tuple(sorted((
        "DobbyVPNRuntime.xcframework",
        "DobbyVPNRuntime-debug.xcframework",
        "DobbyVPN.xcarchive.tar.gz",
        "DobbyVPN.xcarchive-debug.tar.gz",
        "dobbyvpn-android-unsign.apk",
        "dobbyvpn-android-test-companion-unsigned.apk",
        "dobbyvpn-android-debug.apk",
        "dobbyvpn-android-build-metadata",
        "dobbyVPN-linux.deb",
        "dobbyVPN-linux-debug.deb",
        "dobbyVPN-windows-amd64.msi",
        "dobbyVPN-windows-amd64-debug.msi",
        "dobbyVPN-macos-aarch64.pkg",
        "dobbyVPN-macos-aarch64-debug.pkg",
        "dobbyVPN-macos-amd64.pkg",
        "dobbyVPN-macos-amd64-debug.pkg",
    )))


def _workflow_path_matches(value: Any, expected: str) -> bool:
    return isinstance(value, str) and (value == expected or value.startswith(expected + "@"))


def _json_command(command: list[str]) -> Any:
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode:
        sys.stdout.buffer.write(result.stdout)
        sys.stderr.buffer.write(result.stderr)
        raise BuildManifestError(f"command failed with exit code {result.returncode}: {command[0]}")
    try:
        return json.loads(result.stdout)
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BuildManifestError(f"command returned invalid JSON: {command[0]}") from error


def _artifact_inventory(repository: str, run_id: int) -> dict[str, dict[str, Any]]:
    payload = _json_command([
        "gh", "api", "--paginate", "--slurp",
        f"repos/{repository}/actions/runs/{run_id}/artifacts?per_page=100",
    ])
    pages = payload if isinstance(payload, list) else [payload]
    inventory: dict[str, dict[str, Any]] = {}
    for page in pages:
        artifacts = page.get("artifacts") if isinstance(page, dict) else None
        if not isinstance(artifacts, list):
            raise BuildManifestError("GitHub returned an invalid Build artifact list")
        for artifact in artifacts:
            if not isinstance(artifact, dict) or not isinstance(artifact.get("name"), str):
                raise BuildManifestError("GitHub returned an invalid Build artifact record")
            name = artifact["name"]
            if name in inventory:
                raise BuildManifestError(f"Build artifact name is ambiguous: {name}")
            inventory[name] = artifact
    return inventory


def _run(repository: str, run_id: int, *, require_main: bool = False) -> dict[str, Any]:
    run = _json_command([
        "gh", "api", f"repos/{repository}/actions/runs/{run_id}",
    ])
    if not isinstance(run, dict):
        raise BuildManifestError("GitHub returned an invalid Build run")
    repo = run.get("repository")
    if not isinstance(repo, dict) or repo.get("full_name") != repository:
        raise BuildManifestError("selected Build belongs to a different repository")
    if type(run.get("id")) is not int or run["id"] != run_id:
        raise BuildManifestError("selected Build response does not match its run ID")
    if not _workflow_path_matches(run.get("path"), BUILD_WORKFLOW_PATH) or run.get("event") != "workflow_dispatch":
        raise BuildManifestError("selected run is not a manually dispatched Build workflow")
    if (
        type(run.get("run_attempt")) is not int or run["run_attempt"] != 1
        or run.get("status") != "completed" or run.get("conclusion") != "success"
    ):
        raise BuildManifestError("selected Build must be a successful first attempt")
    if require_main and run.get("head_branch") != "main":
        raise BuildManifestError("production Release can only select a Build from main")
    if not isinstance(run.get("head_sha"), str) or not SHA_RE.fullmatch(run["head_sha"]):
        raise BuildManifestError("selected Build has an invalid source revision")
    return run


def _current_build_run(repository: str, run_id: int, run_number: int, source_sha: str) -> None:
    run = _json_command(["gh", "api", f"repos/{repository}/actions/runs/{run_id}"])
    repo = run.get("repository") if isinstance(run, dict) else None
    if not isinstance(repo, dict) or repo.get("full_name") != repository:
        raise BuildManifestError("current Build belongs to a different repository")
    if not _workflow_path_matches(run.get("path"), BUILD_WORKFLOW_PATH) or run.get("event") != "workflow_dispatch":
        raise BuildManifestError("current run is not a manually dispatched Build workflow")
    if type(run.get("run_attempt")) is not int or run["run_attempt"] != 1 or run.get("head_sha") != source_sha:
        raise BuildManifestError("current Build identity does not match its source revision or first attempt")
    if run.get("run_number") != run_number or run.get("id") != run_id:
        raise BuildManifestError("current Build ID or run number does not match its manifest")
    if run.get("status") not in {"queued", "in_progress"}:
        raise BuildManifestError("Build manifest must be created by the active Build run")


def create_manifest(
    *, repository: str, run_id: int, run_number: int, source_sha: str,
    source_tree: str, version: str, android_version_code: int,
    apple_build_number: int,
) -> dict[str, Any]:
    if not SHA_RE.fullmatch(source_sha) or not SHA_RE.fullmatch(source_tree):
        raise BuildManifestError("source commit and tree must be full lowercase Git identities")
    if any(type(value) is not int or value <= 0 for value in (run_id, run_number, android_version_code, apple_build_number)):
        raise BuildManifestError("Build IDs, run numbers and version numbers must be positive integers")
    if not isinstance(version, str) or not VERSION_RE.fullmatch(version):
        raise BuildManifestError("Build version must be canonical X.Y.Z")
    _current_build_run(repository, run_id, run_number, source_sha)
    inventory = _artifact_inventory(repository, run_id)
    artifacts: list[dict[str, Any]] = []
    for name in required_artifacts(version):
        item = inventory.get(name)
        if item is None or item.get("expired") is not False:
            raise BuildManifestError(f"Build artifact is missing or expired: {name}")
        digest = item.get("digest")
        size = item.get("size_in_bytes")
        if not isinstance(digest, str) or not DIGEST_RE.fullmatch(digest):
            raise BuildManifestError(f"Build artifact has no valid SHA-256 digest: {name}")
        if type(size) is not int or size <= 0:
            raise BuildManifestError(f"Build artifact has an invalid size: {name}")
        artifacts.append({"name": name, "sha256": digest[7:], "size": size})
    return {
        "schema": 1,
        "repository": repository,
        "workflow_path": BUILD_WORKFLOW_PATH,
        "run_id": run_id,
        "run_number": run_number,
        "source_sha": source_sha,
        "source_tree": source_tree,
        "version": version,
        "android_version_code": android_version_code,
        "apple_build_number": apple_build_number,
        "artifacts": artifacts,
    }


def resolve_build(repository: str, run_id: int, destination: Path, *, require_main: bool = False) -> dict[str, Any]:
    run = _run(repository, run_id, require_main=require_main)
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    subprocess.run([
        sys.executable,
        str(Path(__file__).with_name("download_artifact.py")),
        str(run_id), MANIFEST_ARTIFACT, str(destination),
    ], check=True, env={**os.environ, "GITHUB_REPOSITORY": repository})
    files = [path for path in destination.iterdir() if path.is_file() and not path.is_symlink()]
    if len(files) != 1 or files[0].name != "build.json":
        raise BuildManifestError("Build manifest artifact must contain exactly build.json")
    manifest = json.loads(files[0].read_text(encoding="utf-8"))
    if not isinstance(manifest, dict) or manifest.get("schema") != 1:
        raise BuildManifestError("selected Build manifest has an unsupported schema")
    if (
        manifest.get("repository") != repository
        or manifest.get("workflow_path") != BUILD_WORKFLOW_PATH
        or manifest.get("run_id") != run_id
        or manifest.get("run_number") != run.get("run_number")
        or manifest.get("source_sha") != run.get("head_sha")
    ):
        raise BuildManifestError("Build manifest does not match the selected GitHub run")
    expected_fields = {
        "schema", "repository", "workflow_path", "run_id", "run_number", "source_sha",
        "source_tree", "version", "android_version_code", "apple_build_number", "artifacts",
    }
    if set(manifest) != expected_fields:
        raise BuildManifestError("Build manifest has unexpected or missing fields")
    if (
        type(manifest.get("run_id")) is not int
        or type(manifest.get("run_number")) is not int
        or type(manifest.get("android_version_code")) is not int
        or type(manifest.get("apple_build_number")) is not int
        or manifest["run_id"] <= 0 or manifest["run_number"] <= 0
        or manifest["android_version_code"] <= 0 or manifest["apple_build_number"] <= 0
        or not isinstance(manifest.get("source_tree"), str)
        or not SHA_RE.fullmatch(manifest["source_tree"])
        or not isinstance(manifest.get("version"), str)
        or not VERSION_RE.fullmatch(manifest["version"])
    ):
        raise BuildManifestError("Build manifest contains invalid version or run metadata")
    expected_names = set(required_artifacts(manifest["version"]))
    records = manifest.get("artifacts")
    if not isinstance(records, list) or len(records) != len(expected_names):
        raise BuildManifestError("Build manifest does not list the complete package set")
    names: set[str] = set()
    current = _artifact_inventory(repository, run_id)
    for record in records:
        if not isinstance(record, dict) or set(record) != {"name", "sha256", "size"}:
            raise BuildManifestError("Build manifest contains an invalid artifact record")
        name, sha256, size = record["name"], record["sha256"], record["size"]
        if (
            not isinstance(name, str) or name not in expected_names or name in names
            or not isinstance(sha256, str) or not re.fullmatch(r"[0-9a-f]{64}", sha256)
            or type(size) is not int or size <= 0
        ):
            raise BuildManifestError("Build manifest contains an invalid or duplicate artifact identity")
        names.add(name)
        item = current.get(record["name"])
        if (
            item is None or item.get("expired") is not False
            or item.get("digest") != f"sha256:{record.get('sha256')}"
            or item.get("size_in_bytes") != record.get("size")
        ):
            raise BuildManifestError(f"Build artifact changed since its manifest was written: {record['name']}")
    if names != expected_names:
        raise BuildManifestError("Build manifest does not list the complete package set")
    manifest["manifest_sha256"] = hashlib.sha256(files[0].read_bytes()).hexdigest()
    return manifest


def _write_output(path: Path, manifest: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    create = sub.add_parser("create")
    create.add_argument("--repository", required=True)
    create.add_argument("--run-id", type=int, required=True)
    create.add_argument("--run-number", type=int, required=True)
    create.add_argument("--source-sha", required=True)
    create.add_argument("--source-tree", required=True)
    create.add_argument("--version", required=True)
    create.add_argument("--android-version-code", type=int, required=True)
    create.add_argument("--apple-build-number", type=int, required=True)
    create.add_argument("--output", type=Path, required=True)
    resolve = sub.add_parser("resolve")
    resolve.add_argument("--repository", required=True)
    resolve.add_argument("--run-id", type=int, required=True)
    resolve.add_argument("--download-dir", type=Path, required=True)
    resolve.add_argument("--output", type=Path, required=True)
    resolve.add_argument("--require-main", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.command == "create":
            manifest = create_manifest(
                repository=args.repository, run_id=args.run_id, run_number=args.run_number,
                source_sha=args.source_sha, source_tree=args.source_tree, version=args.version,
                android_version_code=args.android_version_code, apple_build_number=args.apple_build_number,
            )
            _write_output(args.output, manifest)
        else:
            manifest = resolve_build(args.repository, args.run_id, args.download_dir, require_main=args.require_main)
            _write_output(args.output, manifest)
    except (BuildManifestError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"Build manifest error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
