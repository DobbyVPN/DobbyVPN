#!/usr/bin/env python3
"""Require a successful full CI run for the selected product commit."""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path


SCRIPT_ROOT = Path(__file__).resolve().parents[1]
if str(SCRIPT_ROOT) not in sys.path:
    sys.path.insert(0, str(SCRIPT_ROOT))

from ci_reuse import (  # noqa: E402
    REQUIRED_CI_JOB_NAMES,
    find_reusable_ci_run,
    missing_or_failed_required_jobs,
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    token = os.environ.get("GH_TOKEN")
    if not token:
        parser.error("GH_TOKEN is required")
    run_id, reason = find_reusable_ci_run(args.repository, args.source_sha, token)
    if run_id is None:
        print(
            f"no trusted successful full CI run exists for {args.source_sha}: {reason}; "
            "run complete CI on this commit before Release",
            file=sys.stderr,
        )
        return 1
    print(f"Complete CI run {run_id} passed for {args.source_sha}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError) as error:
        print(f"CI verification failed: {error}", file=sys.stderr)
        raise SystemExit(1) from error
