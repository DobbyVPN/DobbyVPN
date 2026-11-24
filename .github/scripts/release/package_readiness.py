#!/usr/bin/env python3
"""Pin package identities from a completed Build and check them again after Test."""
from __future__ import annotations

import json
import os
from pathlib import Path

from artifact_attempts import parse_artifact_name
import build_manifest
from version_metadata import parse_version

# GitHub reports reusable job names with their caller prefix.
PACKAGES = {
    "linux": {"dobbyVPN-linux.deb": "build_linux / linux / amd64 package"},
    "windows": {"dobbyVPN-windows-amd64.msi": "build_windows / windows / amd64 package"},
    "macos": {
        "dobbyVPN-macos-universal.pkg": "build_macos / macos / universal package",
    },
    "android": {
        "dobbyvpn-android-unsign.apk": "android_build / android_build",
        "dobbyvpn-android-build-metadata": "android_build / android_build",
    },
    "ios": {"DobbyVPNRuntime.xcframework": "ios_libraries_build / build_ios_lib"},
}


def _carried_forward(original, current):
    # GitHub can copy an earlier execution into a later attempt with a new
    # job ID and creation time. The copied execution keeps its run times.
    started = original.get("started_at")
    completed = original.get("completed_at")
    created = current.get("created_at")
    return (
        current.get("status") == "completed" and current.get("conclusion") == "success"
        and isinstance(started, str) and isinstance(completed, str) and isinstance(created, str)
        and started == current.get("started_at") and completed == current.get("completed_at")
        and started <= completed and started < created
    )


def select_group(build, inventory, jobs_by_attempt, group):
    """Bind each package to its original successful producer, including carried-forward jobs."""
    required = PACKAGES[group]
    current_attempt = build.get("run_attempt", 1)
    if group in {"macos", "ios"} and any(
        job.get("name") == build_manifest.APPLE_PRODUCER
        for jobs in jobs_by_attempt.values() for job in jobs
    ):
        required = dict.fromkeys(required, build_manifest.APPLE_PRODUCER)
    result = {}
    for logical, producer in required.items():
        artifact = inventory.get(logical)
        if artifact is None:
            raise ValueError(f"completed Build is missing {logical}")
        _, attempt = parse_artifact_name(artifact["name"])
        jobs = [j for j in jobs_by_attempt.get(attempt, []) if j.get("name") == producer]
        if len(jobs) != 1:
            raise ValueError(f"missing or ambiguous producer: {producer} attempt {attempt}")
        job = jobs[0]
        if job.get("status") != "completed" or job.get("conclusion") != "success":
            raise ValueError(f"unsuccessful package producer: {producer}")
        for number in range(attempt + 1, current_attempt + 1):
            later = [j for j in jobs_by_attempt.get(number, []) if j.get("name") == producer]
            if later and (len(later) != 1 or not _carried_forward(job, later[0])):
                raise ValueError(f"current producer has no successful current-attempt artifact: {producer}")
        if producer == build_manifest.APPLE_PRODUCER:
            build_manifest.verify_apple_artifact_attempt(inventory, attempt)
        if type(job.get("id")) is not int or job["id"] <= 0:
            raise ValueError(f"invalid package producer job ID: {producer}")
        if (type(artifact.get("id")) is not int or artifact["id"] <= 0
                or not build_manifest.DIGEST_RE.fullmatch(artifact.get("digest", ""))
                or artifact.get("workflow_run", {}).get("id") != build["run_id"]
                or artifact.get("workflow_run", {}).get("head_sha") != build["source_sha"]):
            raise ValueError(f"invalid package identity: {logical}")
        result[logical] = {
            "id": artifact["id"], "name": artifact["name"], "digest": artifact["digest"],
            "producer_attempt": attempt, "producer_job_id": job["id"],
            "build_run_id": build["run_id"], "source_sha": build["source_sha"],
        }
    return result


def package_snapshot(repository, run_id, destination):
    # Keep the whole validated inventory. The combined Apple producer owns six
    # artifacts even though the public checks consume only a subset of them.
    build, inventory = build_manifest.resolve_build_with_inventory(repository, run_id, destination, require_main=True)
    attempt = build.get("run_attempt", 1)
    jobs_by_attempt = {}
    for number in range(1, attempt + 1):
        pages = build_manifest._json_command([
            "gh", "api", "--paginate", "--slurp",
            f"repos/{repository}/actions/runs/{run_id}/attempts/{number}/jobs?per_page=100",
        ])
        if not isinstance(pages, list) or any(not isinstance(p, dict) or not isinstance(p.get("jobs"), list) for p in pages):
            raise ValueError("invalid GitHub jobs response")
        jobs_by_attempt[number] = [job for page in pages for job in page["jobs"]]
    selections = {group: select_group(build, inventory, jobs_by_attempt, group) for group in PACKAGES}
    return build, selections


def reconcile(consumed, current):
    """Equal bytes from a replacement artifact or job cannot qualify a prior check."""
    if consumed == current:
        return
    replaced = [
        {"group": group, "artifact": name}
        for group, packages in current.items()
        for name, identity in packages.items()
        if consumed.get(group, {}).get(name) != identity
    ]
    print("BUILD_PACKAGE_REPLACEMENTS=" + json.dumps(replaced, sort_keys=True), flush=True)
    groups = sorted({item["group"] for item in replaced})
    if groups:
        raise ValueError("consumed package set changed; repeat dependent " + ", ".join(groups) + " results")
    raise ValueError("consumed selections contain unexpected groups or packages")


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    run_id = int(os.environ["BUILD_RUN_ID"])
    scratch = Path(os.environ["RUNNER_TEMP"])
    build, packages = package_snapshot(repository, run_id, scratch / "final-build-manifest")
    version = parse_version(Path("VERSION").read_text())
    if build["source_sha"] != os.environ["GITHUB_SHA"] or build["version"] != version.version_name:
        raise ValueError("Build and Test source revisions or versions differ")
    if (build["android_version_code"] != version.android_version_code
            or build["apple_build_number"] != version.android_version_code + build["run_number"]):
        raise ValueError("Build version codes differ from the selected source")
    build_json = json.dumps(build, separators=(",", ":"))
    (scratch / "build.json").write_text(build_json)
    outputs = {**version.outputs(), "source_sha": build["source_sha"], "build_manifest": build_json,
               "packages": json.dumps(packages, separators=(",", ":"))}
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in outputs.items():
            output.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
