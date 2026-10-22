#!/usr/bin/env python3
"""Wait on Linux for successful package producers and pin their immutable outputs."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import time

from artifact_attempts import parse_artifact_name, select_artifacts
import build_manifest
from version_metadata import parse_version

# GitHub reports reusable job names with their caller prefix.
PACKAGES = {
    "linux": {"dobbyVPN-linux.deb": "build_linux / linux / amd64 package"},
    "windows": {"dobbyVPN-windows-amd64.msi": "build_windows / windows / amd64 package"},
    "macos": {
        "dobbyVPN-macos-aarch64.pkg": "build_macos_arm / macos / arm64 package",
        "dobbyVPN-macos-amd64.pkg": "build_macos_intel / macos / amd64 package",
    },
    "android": {
        "dobbyvpn-android-unsign.apk": "android_build / android_build",
        "dobbyvpn-android-build-metadata": "android_build / android_build",
    },
    "ios": {"DobbyVPNRuntime.xcframework": "ios_libraries_build / build_ios_lib"},
}


def identity(repository, run_id, source_sha):
    run = build_manifest._run(repository, run_id, require_main=True, allow_active=True)
    if run["head_sha"] != source_sha:
        raise ValueError("Build and Test source revisions differ")
    version = parse_version(Path("VERSION").read_text())
    return run, {**version.outputs(), "source_sha": source_sha,
                 "apple_build_number": str(version.android_version_code + run["run_number"])}


def select_ready(run, artifacts, jobs_by_attempt, group):
    """None means ordinary producer progress, never a successful missing check."""
    required = PACKAGES[group]
    selected = select_artifacts(artifacts, run["run_attempt"])
    result = {}
    for logical, producer in required.items():
        artifact = selected.get(logical)
        if artifact is None:
            current_jobs = jobs_by_attempt.get(run["run_attempt"], [])
            failures = [j for j in current_jobs if j.get("name") == producer
                        and j.get("status") == "completed" and j.get("conclusion") != "success"]
            if failures or run.get("status") == "completed":
                raise ValueError(f"package unavailable from completed producer: {producer}")
            return None
        _, attempt = parse_artifact_name(artifact["name"])
        replacing = [j for j in jobs_by_attempt.get(run["run_attempt"], [])
                     if j.get("name") == producer] if attempt < run["run_attempt"] else []
        if replacing:
            if len(replacing) != 1:
                raise ValueError(f"ambiguous replacement producer: {producer}")
            if replacing[0].get("status") != "completed":
                return None
            raise ValueError(f"current producer has no successful current-attempt artifact: {producer}")
        jobs = [j for j in jobs_by_attempt.get(attempt, []) if j.get("name") == producer]
        if len(jobs) != 1:
            raise ValueError(f"missing or ambiguous producer: {producer} attempt {attempt}")
        job = jobs[0]
        if job.get("status") != "completed":
            return None
        if job.get("conclusion") != "success":
            raise ValueError(f"unsuccessful package producer: {producer}")
        if type(job.get("id")) is not int or job["id"] <= 0:
            raise ValueError(f"invalid package producer job ID: {producer}")
        if (type(artifact.get("id")) is not int or artifact["id"] <= 0
                or not build_manifest.DIGEST_RE.fullmatch(artifact.get("digest", ""))
                or artifact.get("workflow_run", {}).get("id") != run["id"]
                or artifact.get("workflow_run", {}).get("head_sha") != run["head_sha"]):
            raise ValueError(f"invalid package identity: {logical}")
        result[logical] = {
            "id": artifact["id"], "name": artifact["name"], "digest": artifact["digest"],
            "producer_attempt": attempt, "producer_job_id": job["id"],
            "build_run_id": run["id"], "source_sha": run["head_sha"],
        }
    return result


def _pages(repository, suffix, key):
    pages = build_manifest._json_command([
        "gh", "api", "--paginate", "--slurp", f"repos/{repository}/{suffix}?per_page=100",
    ])
    if not isinstance(pages, list) or any(not isinstance(p, dict) or not isinstance(p.get(key), list) for p in pages):
        raise ValueError(f"invalid GitHub {key} response")
    return [item for page in pages for item in page[key]]


def wait_packages(repository, run_id, source_sha, group, deadline):
    while True:
        run, _ = identity(repository, run_id, source_sha)
        artifacts = _pages(repository, f"actions/runs/{run_id}/artifacts", "artifacts")
        jobs = {attempt: _pages(repository, f"actions/runs/{run_id}/attempts/{attempt}/jobs", "jobs")
                for attempt in range(1, run["run_attempt"] + 1)}
        ready = select_ready(run, artifacts, jobs, group)
        if ready is not None:
            return ready
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError(f"package readiness deadline expired: {group}")
        print(f"Waiting for Build {run_id} {group} producers", flush=True)
        time.sleep(min(15, remaining))


def reconcile(build, selections, inventory, *, jobs_by_attempt=None):
    """Reject replacements even when their payload digests happen to match."""
    manifest = {a["name"]: a for a in build["artifacts"]}
    replaced = []
    for group, selected in selections.items():
        if set(selected) != set(PACKAGES[group]):
            raise ValueError(f"incomplete consumed package set: {group}")
        final_selection = None
        if jobs_by_attempt is not None:
            final_selection = select_ready(
                {"id": build["run_id"], "head_sha": build["source_sha"],
                 "status": "completed", "run_attempt": build.get("run_attempt", 1)},
                list(inventory.values()), jobs_by_attempt, group)
            if final_selection is None:
                raise ValueError("completed Build has an unfinished producer")
        for logical, consumed in selected.items():
            current = inventory[logical]
            if (consumed["build_run_id"] != build["run_id"]
                    or consumed["source_sha"] != build["source_sha"]
                    or consumed["id"] != current["id"]
                    or consumed["name"] != current["name"]
                    or consumed["producer_attempt"] != parse_artifact_name(current["name"])[1]
                    or consumed["digest"] != current["digest"]
                    or consumed["digest"] != "sha256:" + manifest[logical]["sha256"]
                    or (final_selection is not None and consumed != final_selection[logical])):
                replaced.append({"group": group, "artifact": logical})
    if replaced:
        print("BUILD_PACKAGE_REPLACEMENTS=" + json.dumps(replaced, sort_keys=True), flush=True)
        groups = sorted({item["group"] for item in replaced})
        raise ValueError("consumed artifacts replaced or altered; repeat dependent " + ", ".join(groups) + " results")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("identity", "wait", "final"))
    parser.add_argument("--group", choices=PACKAGES)
    parser.add_argument("--timeout", type=int, default=21600)
    args = parser.parse_args()
    repository = os.environ["GITHUB_REPOSITORY"]
    run_id = int(os.environ["BUILD_RUN_ID"])
    source_sha = os.environ["GITHUB_SHA"]
    deadline = time.monotonic() + args.timeout
    if args.action == "identity":
        _, outputs = identity(repository, run_id, source_sha)
    elif args.action == "wait":
        outputs = {"packages": json.dumps(wait_packages(repository, run_id, source_sha, args.group, deadline), separators=(",", ":"))}
    else:
        while True:
            run, _ = identity(repository, run_id, source_sha)
            if run["status"] == "completed":
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("final Build deadline expired")
            time.sleep(min(15, remaining))
        destination = Path(os.environ["RUNNER_TEMP"]) / "final-build-manifest"
        build = build_manifest.resolve_build(repository, run_id, destination, require_main=True)
        Path(os.environ["RUNNER_TEMP"], "build.json").write_text(json.dumps(build))
        outputs = {"build_manifest": json.dumps(build, separators=(",", ":")), "manifest_sha256": build["manifest_sha256"]}
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        for key, value in outputs.items():
            output.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
