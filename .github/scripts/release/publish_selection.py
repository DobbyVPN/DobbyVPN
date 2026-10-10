#!/usr/bin/env python3
"""Resolve one successful Publish run and verify its Build/Test/GitHub Release."""
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

import build_manifest
import release_provenance
import test_manifest

PUBLISH_WORKFLOW_PATH = ".github/workflows/publish.yml"
PUBLISH_RESULT_ARTIFACT = "publish-result"
ASSET_NAMES = (
    "DobbyVPN-v{version}-android-provenance.json",
    "DobbyVPN-v{version}-sign.apk",
    "DobbyVPN-v{version}-unsign.apk",
    "version.txt",
    "dobbyVPN-linux.deb",
    "dobbyVPN-macos-aarch64.pkg",
    "dobbyVPN-macos-amd64.pkg",
    "dobbyVPN-windows-amd64.msi",
    "dobbyVPN-linux-debug.deb",
    "dobbyVPN-windows-amd64-debug.msi",
    "dobbyVPN-macos-aarch64-debug.pkg",
    "dobbyVPN-macos-amd64-debug.pkg",
    "DobbyVPN.xcarchive-debug.tar.gz",
    "DobbyVPN-v{version}-debug.apk",
)


class PublishSelectionError(ValueError):
    """The selected Publish run or its release lineage is invalid."""


def workflow_path_matches(value: Any, expected: str) -> bool:
    return isinstance(value, str) and (value == expected or value.startswith(expected + "@"))


def expected_assets(version: str) -> tuple[str, ...]:
    return tuple(sorted(name.format(version=version) for name in ASSET_NAMES))


def validate_publish_run(run: Any, *, repository: str, run_id: int) -> dict[str, Any]:
    if not isinstance(run, dict):
        raise PublishSelectionError("GitHub returned an invalid Publish run")
    repo = run.get("repository")
    if not isinstance(repo, dict) or repo.get("full_name") != repository:
        raise PublishSelectionError("selected Publish belongs to a different repository")
    if not workflow_path_matches(run.get("path"), PUBLISH_WORKFLOW_PATH):
        raise PublishSelectionError("selected run is not the Publish workflow")
    if run.get("event") != "workflow_dispatch" or run.get("head_branch") != "main":
        raise PublishSelectionError("selected Publish must be manually dispatched from main")
    if type(run.get("run_attempt")) is not int or run["run_attempt"] < 1:
        raise PublishSelectionError("selected Publish has an invalid attempt number")
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        raise PublishSelectionError("selected Publish must be successful")
    if type(run.get("id")) is not int or run["id"] != run_id:
        raise PublishSelectionError("GitHub returned a different Publish run ID")
    if type(run.get("run_number")) is not int or run["run_number"] <= 0:
        raise PublishSelectionError("selected Publish has an invalid run number")
    return run


def validate_publish_manifest(
    payload: Any, *, repository: str, publish_run: dict[str, Any],
    build: dict[str, Any], test: dict[str, Any],
) -> None:
    expected = release_provenance.PUBLISHED_MANIFEST_KEYS
    if not isinstance(payload, dict) or set(payload) != expected:
        raise PublishSelectionError("Publish result has unexpected or missing provenance fields")
    if payload.get("schema") != release_provenance.PUBLISHED_SCHEMA:
        raise PublishSelectionError("Publish result has an unsupported provenance schema")
    if payload.get("publish_run_id") != publish_run["id"] or payload.get("publish_run_number") != publish_run["run_number"]:
        raise PublishSelectionError("Publish result does not identify the selected workflow run")
    if (
        payload.get("build_run_id") != build.get("run_id")
        or payload.get("build_run_number") != build.get("run_number")
        or payload.get("source_sha") != build.get("source_sha")
        or payload.get("version") != build.get("version")
        or payload.get("android_version_code") != build.get("android_version_code")
        or payload.get("apple_build_number") != build.get("apple_build_number")
        or payload.get("build_manifest_sha256") != build.get("manifest_sha256")
    ):
        raise PublishSelectionError("Publish result does not match the selected Build identity")
    if (
        payload.get("test_run_id") != test.get("run_id")
        or payload.get("test_run_number") != test.get("run_number")
        or payload.get("test_result_sha256") != test.get("manifest_sha256")
        or test.get("build_run_id") != build.get("run_id")
        or test.get("source_sha") != build.get("source_sha")
    ):
        raise PublishSelectionError("Publish result does not match the successful Test for this Build")
    if payload.get("tag") != f"v{build.get('version')}":
        raise PublishSelectionError("Publish result tag does not match the selected Build version")
    if not isinstance(payload.get("assets"), list):
        raise PublishSelectionError("Publish result contains no release asset inventory")
    if {item.get("name") for item in payload["assets"] if isinstance(item, dict)} != set(expected_assets(build["version"])):
        raise PublishSelectionError("Publish result does not list the complete stable asset set")


def _artifact_file(repository: str, run_id: int, download_dir: Path) -> tuple[Path, str]:
    inventory = build_manifest._artifact_inventory(repository, run_id)
    artifact = inventory.get(PUBLISH_RESULT_ARTIFACT)
    if artifact is None or artifact.get("expired") is not False:
        raise PublishSelectionError("selected Publish is missing its unexpired result artifact")
    digest = artifact.get("digest")
    size = artifact.get("size_in_bytes")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise PublishSelectionError("Publish result artifact has an invalid SHA-256 digest")
    if type(size) is not int or size <= 0:
        raise PublishSelectionError("Publish result artifact has an invalid size")
    download_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    subprocess.run([
        sys.executable, str(Path(__file__).with_name("download_artifact.py")),
        str(run_id), PUBLISH_RESULT_ARTIFACT, str(download_dir),
    ], check=True, env={**os.environ, "GITHUB_REPOSITORY": repository})
    files = [path for path in download_dir.iterdir() if path.is_file() and not path.is_symlink()]
    if len(files) != 1 or files[0].name != release_provenance.MANIFEST_NAME:
        raise PublishSelectionError("Publish result artifact must contain exactly release-provenance.json")
    raw = files[0].read_bytes()
    # download_artifact verifies GitHub's SHA-256 digest over the archived ZIP;
    # the returned digest here identifies the extracted JSON bytes instead.
    return files[0], hashlib.sha256(raw).hexdigest()


def _assert_tag_source(repository: str, tag: str, source_sha: str) -> None:
    refs = build_manifest._json_command([
        "gh", "api", f"repos/{repository}/git/matching-refs/tags/{tag}",
    ])
    if not isinstance(refs, list):
        raise PublishSelectionError("GitHub returned an invalid stable tag reference list")
    matches = [item for item in refs if isinstance(item, dict) and item.get("ref") == f"refs/tags/{tag}"]
    if len(matches) != 1:
        raise PublishSelectionError("published stable tag is missing or ambiguous")
    target = matches[0].get("object")
    seen: set[str] = set()
    while isinstance(target, dict) and target.get("type") == "tag":
        object_sha = target.get("sha")
        if not isinstance(object_sha, str) or object_sha in seen:
            raise PublishSelectionError("published stable tag contains an invalid or cyclic annotated tag")
        seen.add(object_sha)
        record = build_manifest._json_command(["gh", "api", f"repos/{repository}/git/tags/{object_sha}"])
        target = record.get("object") if isinstance(record, dict) else None
    if not isinstance(target, dict) or target.get("type") != "commit" or target.get("sha") != source_sha:
        raise PublishSelectionError("published stable tag does not resolve to the selected Build source")


def _release_assets(directory: Path, version: str) -> None:
    expected = set(expected_assets(version)) | {release_provenance.MANIFEST_NAME}
    optional = {f"DobbyVPN-v{version}-sign.apk.idsig"}
    children = list(directory.iterdir())
    actual = {path.name for path in children if path.is_file() and not path.is_symlink()}
    invalid = [path.name for path in children if path.is_symlink() or path.is_dir()]
    if actual - expected - optional or expected - actual or invalid:
        raise PublishSelectionError(
            "GitHub Release files do not match the stable package allowlist: "
            f"missing={sorted(expected - actual)}, unexpected={sorted(actual - expected - optional)}, "
            f"invalid={sorted(invalid)}"
        )


def resolve_publish(repository: str, run_id: int, destination: Path) -> dict[str, Any]:
    run = validate_publish_run(
        build_manifest._json_command(["gh", "api", f"repos/{repository}/actions/runs/{run_id}"]),
        repository=repository, run_id=run_id,
    )
    result_path, result_sha = _artifact_file(repository, run_id, destination / "publish-result")
    payload = json.loads(result_path.read_text(encoding="utf-8"))
    if not isinstance(payload, dict) or set(payload) != release_provenance.PUBLISHED_MANIFEST_KEYS:
        raise PublishSelectionError("Publish result has unexpected or missing provenance fields")
    for key in ("build_run_id", "build_run_number", "test_run_id"):
        if type(payload.get(key)) is not int or payload[key] <= 0:
            raise PublishSelectionError(f"Publish result has an invalid {key}")
    build_dir = destination / "build-manifest"
    build = build_manifest.resolve_build(repository, payload.get("build_run_id"), build_dir, require_main=True)
    test = test_manifest.resolve_test(repository, payload.get("test_run_id"), build, destination / "test-result")
    validate_publish_manifest(payload, repository=repository, publish_run=run, build=build, test=test)

    version = build["version"]
    tag = f"v{version}"
    if payload.get("tag") != tag:
        raise PublishSelectionError("Publish result stable tag does not match Build VERSION")
    release_api = build_manifest._json_command(["gh", "api", f"repos/{repository}/releases/tags/{tag}"])
    if (
        not isinstance(release_api, dict) or release_api.get("tag_name") != tag
        or release_api.get("draft") is not False or release_api.get("prerelease") is not False
    ):
        raise PublishSelectionError("selected Publish does not have a stable GitHub Release")
    body = release_api.get("body")
    if not isinstance(body, str) or hashlib.sha256(body.encode("utf-8")).hexdigest() != payload.get("release_notes_sha256"):
        raise PublishSelectionError("GitHub Release notes do not match the reviewed Publish notes")

    release_dir = destination / "release"
    release_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    subprocess.run([
        "gh", "release", "download", tag, "--repo", repository, "--dir", str(release_dir),
    ], check=True)
    _release_assets(release_dir, version)
    release_manifest_sha = hashlib.sha256(
        (release_dir / release_provenance.MANIFEST_NAME).read_bytes()
    ).hexdigest()
    if release_manifest_sha != result_sha:
        raise PublishSelectionError("GitHub Release provenance differs from the selected Publish result artifact")
    release_provenance.verify_published_manifest(
        release_dir,
        tag=tag, version=version, source_sha=build["source_sha"],
        build_run_id=build["run_id"], build_run_number=build["run_number"],
        test_run_id=test["run_id"], test_run_number=test["run_number"],
        publish_run_id=run_id, publish_run_number=run["run_number"],
        android_version_code=build["android_version_code"], apple_build_number=build["apple_build_number"],
        build_manifest_sha256=build["manifest_sha256"], test_result_sha256=test["manifest_sha256"],
        release_notes_sha256=payload["release_notes_sha256"], assets=expected_assets(version),
    )
    _assert_tag_source(repository, tag, build["source_sha"])
    return {
        **payload,
        "repository": repository,
        "publish_result_sha256": result_sha,
        "build_manifest_sha256": build["manifest_sha256"],
        "test_result_sha256": test["manifest_sha256"],
        "test_run_number": test["run_number"],
        "release_notes": body,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--run-id", type=int, required=True)
    parser.add_argument("--download-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        result = resolve_publish(args.repository, args.run_id, args.download_dir)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    except (build_manifest.BuildManifestError, test_manifest.TestManifestError,
            release_provenance.ProvenanceError, PublishSelectionError, OSError,
            UnicodeError, json.JSONDecodeError, subprocess.SubprocessError, ValueError) as error:
        print(f"Publish selection error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
