#!/usr/bin/env python3
"""Require a successful first-attempt CI run for the selected Release commit."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen


REQUIRED_CI_JOB_NAMES = (
    "Go unit and race tests",
    "Selected Go runtime tests on ARM64 Linux",
    "Selected Go runtime tests on Windows",
    "Selected Go runtime tests on macOS",
    "Swift lifecycle unit tests",
    "Android lint",
    "Go source analysis",
    "Swift source analysis",
    "Dependency and credential scans",
    "GitHub workflow validation",
    "Android build and native library checks",
    "iOS Simulator UI test",
    "Complete CI",
)


def missing_or_failed_required_jobs(payload: object) -> list[str]:
    """Return required CI job names that did not complete successfully."""
    if not isinstance(payload, dict) or not isinstance(payload.get("jobs"), list):
        raise ValueError("GitHub CI job list is invalid")

    required = set(REQUIRED_CI_JOB_NAMES)
    seen: dict[str, bool] = {}
    for job in payload["jobs"]:
        if not isinstance(job, dict):
            raise ValueError("GitHub CI job record is invalid")
        name = job.get("name")
        if not isinstance(name, str):
            raise ValueError("GitHub CI job name is invalid")
        if name not in required:
            continue
        passed = job.get("status") == "completed" and job.get("conclusion") == "success"
        seen[name] = seen.get(name, True) and passed

    return [name for name in REQUIRED_CI_JOB_NAMES if not seen.get(name, False)]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        parser.error("--source-sha must be a full lowercase commit SHA")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
        parser.error("--repository must be an owner/repository name")
    token = os.environ.get("GH_TOKEN")
    if not token:
        parser.error("GH_TOKEN is required")

    path = (
        f"repos/{args.repository}/actions/workflows/ci.yml/runs?"
        + urlencode({"head_sha": args.source_sha, "branch": "main", "per_page": 100})
    )
    request = Request(
        "https://api.github.com/" + quote(path, safe="/?=&"),
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urlopen(request, timeout=30) as response:
        payload = json.load(response)
    runs = payload.get("workflow_runs")
    if not isinstance(runs, list):
        raise ValueError("GitHub CI run list is invalid")
    matches = [
        run for run in runs
        if isinstance(run, dict)
        and run.get("head_sha") == args.source_sha
        and run.get("head_branch") == "main"
        and run.get("event") in {"push", "workflow_dispatch"}
        and run.get("status") == "completed"
        and run.get("conclusion") == "success"
        and run.get("run_attempt") == 1
    ]
    if not matches:
        print(
            f"no successful first-attempt CI run exists for {args.source_sha}; "
            "run complete CI on this commit before Release",
            file=sys.stderr,
        )
        return 1

    for candidate in sorted(matches, key=lambda run: int(run["id"]), reverse=True):
        run_id = candidate.get("id")
        if not isinstance(run_id, int) or run_id <= 0:
            continue
        jobs_path = f"repos/{args.repository}/actions/runs/{run_id}/jobs?per_page=100"
        jobs_request = Request(
            "https://api.github.com/" + quote(jobs_path, safe="/?=&"),
            headers={
                "Authorization": f"Bearer {token}",
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        with urlopen(jobs_request, timeout=30) as response:
            jobs_payload = json.load(response)
        failures = missing_or_failed_required_jobs(jobs_payload)
        if not failures:
            print(f"Complete CI run {run_id} passed for {args.source_sha}")
            return 0
        print(
            f"CI run {run_id} is not complete CI; missing or unsuccessful jobs: "
            + ", ".join(failures),
            file=sys.stderr,
        )

    print(
        f"no successful complete first-attempt CI run exists for {args.source_sha}; "
        "run complete CI on this commit before Release",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError) as error:
        print(f"CI verification failed: {error}", file=sys.stderr)
        raise SystemExit(1) from error
