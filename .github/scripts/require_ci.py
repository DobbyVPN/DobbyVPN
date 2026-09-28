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
            "run CI on this commit before Release",
            file=sys.stderr,
        )
        return 1
    selected = max(matches, key=lambda run: int(run["id"]))
    print(f"CI run {selected['id']} passed for {args.source_sha}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError) as error:
        print(f"CI verification failed: {error}", file=sys.stderr)
        raise SystemExit(1) from error
