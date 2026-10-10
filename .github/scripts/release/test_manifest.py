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

TEST_WORKFLOW_PATH = ".github/workflows/test.yml"
RESULT_ARTIFACT = "test-result"
SHA_RE = re.compile(r"[0-9a-f]{40}\Z")
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")
REQUIRED_CHECKS = frozenset({
    "resolve_build", "ios_analysis", "android_reproducibility", "fdroid_preflight",
    "migrate_windows", "migrate_macos_arm", "migrate_macos_intel", "render_start",
    "qualify_linux", "qualify_windows", "qualify_macos", "qualify_android", "render_cleanup",
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
        type(run.get("run_attempt")) is not int or run["run_attempt"] != 1
        or run.get("status") != "completed" or run.get("conclusion") != "success"
    ):
        raise TestManifestError("selected Test must be a successful first attempt")
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
        "source_sha", "build_manifest_sha256", "checks",
    }
    if not isinstance(result, dict) or set(result) != expected_fields:
        raise TestManifestError("Test result has unexpected or missing fields")
    if type(result.get("schema")) is not int or result.get("schema") != 1 or result.get("repository") != repository:
        raise TestManifestError("Test result has an unsupported schema or repository")
    if (
        result.get("workflow_path") != TEST_WORKFLOW_PATH
        or type(result.get("run_id")) is not int or result.get("run_id") != run_id
    ):
        raise TestManifestError("Test result does not identify the selected Test run")
    if (
        type(result.get("build_run_id")) is not int
        or result.get("build_run_id") != build.get("run_id")
    ):
        raise TestManifestError("Test result does not qualify the selected Build run")
    if result.get("source_sha") != source_sha or source_sha != build.get("source_sha"):
        raise TestManifestError("Build and Test source revisions do not match")
    manifest_sha = result.get("build_manifest_sha256")
    if not isinstance(manifest_sha, str) or not SHA256_RE.fullmatch(manifest_sha):
        raise TestManifestError("Test result has an invalid Build manifest digest")
    if manifest_sha != build.get("manifest_sha256"):
        raise TestManifestError("Test result was produced for a different Build manifest")
    checks = result.get("checks")
    if not isinstance(checks, dict) or set(checks) != REQUIRED_CHECKS:
        raise TestManifestError("Test result does not contain the complete qualification check set")
    if any(value != "success" for value in checks.values()):
        raise TestManifestError("Test result contains a failed or skipped qualification check")


def resolve_test(
    repository: str, run_id: int, build: dict[str, Any], destination: Path,
) -> dict[str, Any]:
    run = _selected_test_run(repository, run_id)
    if run.get("head_sha") != build.get("source_sha"):
        raise TestManifestError("selected Build and Test source revisions do not match")
    inventory = _artifact_inventory(repository, run_id)
    artifact = inventory.get(RESULT_ARTIFACT)
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
        str(run_id), RESULT_ARTIFACT, str(destination),
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
