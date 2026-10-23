#!/usr/bin/env python3
"""Resolve and validate the Test run paired with one immutable Build."""
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

from build_manifest import BuildManifestError, _artifact_inventory, _json_command
import release_guard

TEST_WORKFLOW_PATH = ".github/workflows/test.yml"
RESULT_ARTIFACT = "test-result"
TORTURER_REPOSITORY = "DobbyVPN/Torturer"
SHA_RE = re.compile(r"[0-9a-f]{40}\Z")
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")
REPOSITORY_RE = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
ANDROID_PAIRING_FIELDS = frozenset({
    "build_run_id", "build_manifest_sha256", "product_sha",
    "unsigned_product_apk_sha256", "test_companion_sha256",
    "torturer_repository", "torturer_sha", "torturer_tree_sha256",
})
REQUIRED_CHECKS = frozenset({
    "resolve_build", "ios_analysis", "android_reproducibility", "fdroid_preflight",
    "migrate_windows", "migrate_macos_arm", "migrate_macos_intel", "fixture_hosts",
    "qualify_linux", "qualify_windows", "qualify_macos", "qualify_android",
    "source_linux", "source_linux_arm", "source_windows", "source_macos",
    "source_android", "ios_simulator",
})


class TestManifestError(ValueError):
    """The selected Test run is invalid or does not qualify the selected Build."""


def _workflow_path_matches(value: Any) -> bool:
    return isinstance(value, str) and (
        value == TEST_WORKFLOW_PATH or value.startswith(TEST_WORKFLOW_PATH + "@")
    )


def _selected_test_run(repository: str, run_id: int) -> dict[str, Any]:
    run = _json_command(["gh", "api", f"repos/{repository}/actions/runs/{run_id}"])
    if not isinstance(run, dict):
        raise TestManifestError("GitHub returned an invalid Test run")
    repo = run.get("repository")
    if not isinstance(repo, dict) or repo.get("full_name") != repository:
        raise TestManifestError("selected Test belongs to a different repository")
    if type(run.get("id")) is not int or run["id"] != run_id:
        raise TestManifestError("selected Test response does not match its run ID")
    if not _workflow_path_matches(run.get("path")) or run.get("event") != "workflow_dispatch":
        raise TestManifestError("selected run is not a manually dispatched Test workflow")
    if run.get("head_branch") != "main":
        raise TestManifestError("Publish can only select a Test run from main")
    if (
        type(run.get("run_attempt")) is not int or run["run_attempt"] < 1
        or run.get("status") != "completed" or run.get("conclusion") != "success"
    ):
        raise TestManifestError("selected Test must be a successful completed attempt")
    if type(run.get("run_number")) is not int or run["run_number"] <= 0:
        raise TestManifestError("selected Test has an invalid run number")
    if not isinstance(run.get("head_sha"), str) or not SHA_RE.fullmatch(run["head_sha"]):
        raise TestManifestError("selected Test has an invalid source revision")
    return run


def validate_result(
    result: Any, *, repository: str, run_id: int, build: dict[str, Any], source_sha: str,
) -> None:
    expected_fields = {
        "schema", "repository", "workflow_path", "run_id", "build_run_id",
        "source_sha", "build_manifest_sha256", "checks", "torturer_repository",
        "torturer_sha", "torturer_tree_sha256", "release_attempt_id", "android_pairing",
    }
    if not isinstance(result, dict) or set(result) not in (expected_fields, expected_fields | {"run_attempt"}):
        raise TestManifestError("Test result has unexpected or missing fields")
    if (
        type(result.get("schema")) is not int or result.get("schema") != 2
        or result.get("repository") != repository
        or not isinstance(repository, str) or not REPOSITORY_RE.fullmatch(repository)
    ):
        raise TestManifestError("Test result has an unsupported schema or repository")
    if (
        result.get("workflow_path") != TEST_WORKFLOW_PATH
        or type(result.get("run_id")) is not int or result.get("run_id") <= 0
        or result.get("run_id") != run_id
    ):
        raise TestManifestError("Test result does not identify the selected Test run")
    if type(result.get("run_attempt", 1)) is not int or result.get("run_attempt", 1) < 1:
        raise TestManifestError("Test result has an invalid workflow attempt")
    if (
        type(result.get("build_run_id")) is not int
        or result.get("build_run_id") <= 0
        or result.get("build_run_id") != build.get("run_id")
    ):
        raise TestManifestError("Test result does not qualify the selected Build run")
    if (
        not isinstance(result.get("source_sha"), str) or not SHA_RE.fullmatch(result["source_sha"])
        or result.get("source_sha") != source_sha or source_sha != build.get("source_sha")
    ):
        raise TestManifestError("Build and Test source revisions do not match")
    manifest_sha = result.get("build_manifest_sha256")
    if not isinstance(manifest_sha, str) or not SHA256_RE.fullmatch(manifest_sha):
        raise TestManifestError("Test result has an invalid Build manifest digest")
    if manifest_sha != build.get("manifest_sha256"):
        raise TestManifestError("Test result was produced for a different Build manifest")
    if result.get("torturer_repository") != TORTURER_REPOSITORY:
        raise TestManifestError(f"Test result torturer_repository must be {TORTURER_REPOSITORY}")
    if not isinstance(result.get("torturer_sha"), str) or not SHA_RE.fullmatch(result["torturer_sha"]):
        raise TestManifestError("Test result has an invalid Torturer commit SHA")
    if not isinstance(result.get("torturer_tree_sha256"), str) or not SHA256_RE.fullmatch(result["torturer_tree_sha256"]):
        raise TestManifestError("Test result has an invalid Torturer tree SHA-256")
    if type(result.get("release_attempt_id")) is not int or result["release_attempt_id"] <= 0:
        raise TestManifestError("Test result release_attempt_id must be a positive integer")
    _validate_android_pairing(result, build)
    checks = result.get("checks")
    if not isinstance(checks, dict) or set(checks) != REQUIRED_CHECKS:
        raise TestManifestError("Test result does not contain the complete qualification check set")
    if any(value != "success" for value in checks.values()):
        raise TestManifestError("Test result contains a failed or skipped qualification check")


def _validate_android_pairing(result: dict[str, Any], build: dict[str, Any]) -> None:
    pairing = result.get("android_pairing")
    if not isinstance(pairing, dict) or set(pairing) != ANDROID_PAIRING_FIELDS:
        raise TestManifestError("Test result has an invalid Android Build/Test pairing record")
    if type(pairing.get("build_run_id")) is not int or pairing["build_run_id"] <= 0:
        raise TestManifestError("Android pairing has an invalid Build run ID")
    for key, value in (
        ("build_run_id", result["build_run_id"]),
        ("build_manifest_sha256", result["build_manifest_sha256"]),
        ("product_sha", result["source_sha"]),
        ("torturer_repository", result["torturer_repository"]),
        ("torturer_sha", result["torturer_sha"]),
        ("torturer_tree_sha256", result["torturer_tree_sha256"]),
    ):
        if pairing.get(key) != value:
            raise TestManifestError(f"Android pairing {key} does not match the Test result identity")
    if pairing["build_run_id"] != build.get("run_id"):
        raise TestManifestError("Android pairing does not identify the selected Build run")
    if pairing["build_manifest_sha256"] != build.get("manifest_sha256"):
        raise TestManifestError("Android pairing does not identify the selected Build manifest")
    if not isinstance(pairing.get("unsigned_product_apk_sha256"), str) or not SHA256_RE.fullmatch(pairing["unsigned_product_apk_sha256"]):
        raise TestManifestError("Android pairing has an invalid unsigned product APK digest")
    if not isinstance(pairing.get("test_companion_sha256"), str) or not SHA256_RE.fullmatch(pairing["test_companion_sha256"]):
        raise TestManifestError("Android pairing has an invalid test companion digest")


def validate_android_pairing(result: Any, unsigned_product_apk_sha256: str) -> None:
    """Match the Test result's selected product APK digest to a downloaded APK."""
    if not isinstance(result, dict):
        raise TestManifestError("Test result must be an object before checking Android pairing")
    guard_candidate(result)
    pairing = result.get("android_pairing")
    if not isinstance(pairing, dict) or not isinstance(unsigned_product_apk_sha256, str):
        raise TestManifestError("Android pairing or selected unsigned product digest is missing")
    if not SHA256_RE.fullmatch(unsigned_product_apk_sha256):
        raise TestManifestError("selected unsigned product APK digest is invalid")
    if pairing.get("unsigned_product_apk_sha256") != unsigned_product_apk_sha256:
        raise TestManifestError("Test Android pairing does not match the selected product APK")


def guard_candidate(result: Any) -> dict[str, Any]:
    """Return the exact release-guard identity derived from a validated Test result."""
    if not isinstance(result, dict):
        raise TestManifestError("Test result must be an object before creating a guard candidate")
    build = {
        "run_id": result.get("build_run_id"),
        "manifest_sha256": result.get("build_manifest_sha256"),
        "source_sha": result.get("source_sha"),
    }
    validate_result(
        result,
        repository=result.get("repository"),
        run_id=result.get("run_id"),
        build=build,
        source_sha=result.get("source_sha"),
    )
    return {
        "schema": 1,
        "build_run_id": result["build_run_id"],
        "build_manifest_sha256": result["build_manifest_sha256"],
        "product_sha": result["source_sha"],
        "tests_repository": result["torturer_repository"],
        "tests_sha": result["torturer_sha"],
        "tests_tree_sha256": result["torturer_tree_sha256"],
    }


def resolve_test(
    repository: str, run_id: int, build: dict[str, Any], destination: Path,
) -> dict[str, Any]:
    run = _selected_test_run(repository, run_id)
    if run.get("head_sha") != build.get("source_sha"):
        raise TestManifestError("selected Build and Test source revisions do not match")
    inventory = _artifact_inventory(repository, run_id)
    artifact = inventory.get(f"{RESULT_ARTIFACT}-attempt-{run['run_attempt']}") or inventory.get(RESULT_ARTIFACT)
    if artifact is None or artifact.get("expired") is not False:
        raise TestManifestError("selected Test is missing its unexpired result artifact")
    if type(artifact.get("size_in_bytes")) is not int or artifact["size_in_bytes"] <= 0:
        raise TestManifestError("selected Test result artifact has an invalid size")
    digest = artifact.get("digest")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise TestManifestError("selected Test result artifact has an invalid digest")

    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    subprocess.run([
        sys.executable,
        str(Path(__file__).with_name("download_artifact.py")),
        str(run_id), artifact["name"], str(destination),
    ], check=True, env={**os.environ, "GITHUB_REPOSITORY": repository})
    files = [path for path in destination.iterdir() if path.is_file() and not path.is_symlink()]
    if len(files) != 1 or files[0].name != "test-result.json":
        raise TestManifestError("Test result artifact must contain exactly test-result.json")
    raw = files[0].read_bytes()
    result = json.loads(raw.decode("utf-8"))
    validate_result(
        result, repository=repository, run_id=run_id, build=build,
        source_sha=run["head_sha"],
    )
    if result.get("run_attempt", 1) != run["run_attempt"]:
        raise TestManifestError("Test result attempt does not match the selected GitHub attempt")
    result.setdefault("run_attempt", 1)
    candidate = guard_candidate(result)
    try:
        guard = release_guard.require_success(
            release_guard.GitHubAPI(), repository, candidate, run_id,
        )
    except release_guard.ReleaseGuardError as error:
        raise TestManifestError(f"selected Test does not have a successful release guard: {error}") from error
    if (
        not isinstance(guard, dict)
        or guard.get("state") != "success"
        or guard.get("candidate") != candidate
        or guard.get("test_run_id") != run_id
        or guard.get("run_attempt") != run["run_attempt"]
        or guard.get("deployment_id") != result["release_attempt_id"]
    ):
        raise TestManifestError("Test result release_attempt_id does not match its successful release guard")
    result["run_number"] = run["run_number"]
    result["manifest_sha256"] = hashlib.sha256(raw).hexdigest()
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--run-id", type=int, required=True)
    parser.add_argument("--build-manifest", type=Path, required=True)
    parser.add_argument("--download-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        build = json.loads(args.build_manifest.read_text(encoding="utf-8"))
        result = resolve_test(args.repository, args.run_id, build, args.download_dir)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    except (BuildManifestError, TestManifestError, OSError, UnicodeError, json.JSONDecodeError,
            subprocess.SubprocessError, ValueError) as error:
        print(f"Test manifest error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
