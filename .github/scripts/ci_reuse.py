#!/usr/bin/env python3
"""Find and validate a completed full CI run for an exact product commit."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen


CI_WORKFLOW_PATH = ".github/workflows/ci.yml"
# Keep this set in sync with the jobs required by complete_ci in ci.yml.
REQUIRED_CI_JOB_NAMES = (
    "Select CI scope",
    "Go source build (Linux x86_64)",
    "Go source build (Linux ARM64)",
    "Go source build (Windows)",
    "Go source build (macOS)",
    "Android lint",
    "Go source analysis",
    "Swift source analysis",
    "Dependency and credential scans",
    "GitHub workflow validation",
    "Android product build and native library checks / Compile Android APK artifacts",
    "Android product build and native library checks / Verify Android APK artifacts",
    "iOS production analysis and archive",
    "Complete CI",
)
API_ROOT = "https://api.github.com"
API_TIMEOUT_SECONDS = 30
PAGE_SIZE = 100


def missing_or_failed_required_jobs(
    payload: object,
    required_job_names: tuple[str, ...] = REQUIRED_CI_JOB_NAMES,
) -> list[str]:
    """Return required job names absent or not completed successfully."""
    if not isinstance(payload, dict) or not isinstance(payload.get("jobs"), list):
        raise ValueError("GitHub CI job list is invalid")

    required = set(required_job_names)
    seen: dict[str, bool] = {}
    for job in payload["jobs"]:
        if not isinstance(job, dict):
            raise ValueError("GitHub CI job record is invalid")
        name = job.get("name")
        if not isinstance(name, str):
            raise ValueError("GitHub CI job name is invalid")
        if name in required:
            passed = job.get("status") == "completed" and job.get("conclusion") == "success"
            seen[name] = seen.get(name, True) and passed
    return [name for name in required_job_names if not seen.get(name, False)]


def _full_name(record: object) -> str | None:
    if not isinstance(record, dict):
        return None
    value = record.get("full_name")
    return value if isinstance(value, str) else None


def _workflow_path_matches(value: object, repository: str, workflow_path: str) -> bool:
    if not isinstance(value, str):
        return False
    path = value.split("@", 1)[0].lstrip("/")
    return path in {workflow_path, f"{repository}/{workflow_path}"}


def ci_run_rejection_reason(
    run: object,
    *,
    repository: str,
    source_sha: str,
    current_run_id: int | None = None,
    workflow_path: str = CI_WORKFLOW_PATH,
) -> str | None:
    """Return why a run is untrusted, or None when its identity is eligible."""
    if not isinstance(run, dict):
        return "invalid run record"
    run_id = run.get("id")
    if not isinstance(run_id, int) or isinstance(run_id, bool) or run_id <= 0:
        return "invalid run ID"
    if current_run_id is not None and run_id == current_run_id:
        return "current run cannot reuse itself"
    if run.get("head_sha") != source_sha:
        return "commit SHA differs"
    if _full_name(run.get("repository")) != repository:
        return "run repository differs"
    if _full_name(run.get("head_repository")) != repository:
        return "head repository differs"
    if not _workflow_path_matches(run.get("path"), repository, workflow_path):
        return "workflow path differs"
    event = run.get("event")
    if event not in {"push", "workflow_dispatch"}:
        return "event is not trusted"
    if event == "push" and run.get("head_branch") != "main":
        return "push was not on main"
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        return "run did not complete successfully"
    if type(run.get("run_attempt")) is not int or run["run_attempt"] < 1:
        return "run attempt is invalid"
    return None


def full_ci_run_rejection_reason(
    run: object,
    jobs_payload: object,
    *,
    repository: str,
    source_sha: str,
    current_run_id: int | None = None,
    workflow_path: str = CI_WORKFLOW_PATH,
    required_job_names: tuple[str, ...] = REQUIRED_CI_JOB_NAMES,
) -> str | None:
    """Purely validate run identity and every required full CI job result."""
    reason = ci_run_rejection_reason(
        run,
        repository=repository,
        source_sha=source_sha,
        current_run_id=current_run_id,
        workflow_path=workflow_path,
    )
    if reason is not None:
        return reason
    try:
        failures = missing_or_failed_required_jobs(jobs_payload, required_job_names)
    except ValueError as error:
        return str(error)
    if failures:
        return "missing or unsuccessful jobs: " + ", ".join(failures)
    return None


def _api_json(repository: str, path: str, token: str, *, opener=None) -> object:
    request = Request(
        f"{API_ROOT}/repos/{repository}/{path.lstrip('/')}",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with (opener or urlopen)(request, timeout=API_TIMEOUT_SECONDS) as response:
        return json.load(response)


def _all_pages(repository: str, path: str, key: str, token: str, *, opener=None) -> list[dict]:
    records: list[dict] = []
    page = 1
    total_count: int | None = None
    while total_count is None or len(records) < total_count:
        separator = "&" if "?" in path else "?"
        payload = _api_json(
            repository,
            f"{path}{separator}{urlencode({'per_page': PAGE_SIZE, 'page': page})}",
            token,
            opener=opener,
        )
        if not isinstance(payload, dict) or not isinstance(payload.get(key), list):
            raise ValueError(f"GitHub CI {key} response is invalid")
        values = payload[key]
        if any(not isinstance(value, dict) for value in values):
            raise ValueError(f"GitHub CI {key} record is invalid")
        if total_count is None:
            count = payload.get("total_count")
            if not isinstance(count, int) or isinstance(count, bool) or count < 0:
                raise ValueError(f"GitHub CI {key} count is invalid")
            total_count = count
        records.extend(values)
        if not values or len(values) < PAGE_SIZE:
            if len(records) < total_count:
                raise ValueError(f"GitHub CI {key} pagination is incomplete")
            break
        page += 1
    return records


def find_reusable_ci_run(
    repository: str,
    source_sha: str,
    token: str,
    *,
    current_run_id: int | None = None,
    workflow_path: str = CI_WORKFLOW_PATH,
    opener=None,
) -> tuple[int | None, str]:
    """Return a verified reusable run ID, or a reason to run ordinary full CI."""
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        return None, "repository name is invalid"
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha):
        return None, "source SHA must be a full lowercase commit SHA"
    try:
        runs = _all_pages(
            repository,
            f"actions/workflows/{quote(workflow_path.rsplit('/', 1)[-1], safe='')}/runs?{urlencode({'head_sha': source_sha})}",
            "workflow_runs",
            token,
            opener=opener,
        )
        candidates = sorted(runs, key=lambda item: item.get("id", 0), reverse=True)
        rejected: list[str] = []
        for run in candidates:
            identity_reason = ci_run_rejection_reason(
                run,
                repository=repository,
                source_sha=source_sha,
                current_run_id=current_run_id,
                workflow_path=workflow_path,
            )
            if identity_reason is not None:
                rejected.append(identity_reason)
                continue
            jobs = _all_pages(
                repository,
                f"actions/runs/{run['id']}/jobs",
                "jobs",
                token,
                opener=opener,
            )
            reason = full_ci_run_rejection_reason(
                run,
                {"jobs": jobs},
                repository=repository,
                source_sha=source_sha,
                current_run_id=current_run_id,
                workflow_path=workflow_path,
            )
            if reason is None:
                return run["id"], "verified full CI"
            rejected.append(reason)
        if rejected:
            return None, "no trusted complete full CI run (" + "; ".join(rejected) + ")"
        return None, "no completed full CI run found"
    except (OSError, ValueError, KeyError, TypeError) as error:
        return None, f"reuse verification unavailable: {error}"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--current-run-id", required=True, type=int)
    args = parser.parse_args(argv)
    token = os.environ.get("GITHUB_TOKEN")
    if not token:
        print("GITHUB_TOKEN is unavailable; selecting full CI", file=sys.stderr)
        print("scope=full\nreason=reuse-verification-unavailable\nreused_run_id=")
        return 0

    run_id, reason = find_reusable_ci_run(
        args.repository,
        args.source_sha,
        token,
        current_run_id=args.current_run_id,
    )
    if run_id is None:
        print(f"CI reuse unavailable; selecting full CI: {reason}", file=sys.stderr)
        print("scope=full\nreason=reuse-unavailable\nreused_run_id=")
    else:
        print(f"Verified full CI run {run_id} for {args.source_sha}; reusing it.", file=sys.stderr)
        print(f"scope=reused\nreason=verified-run-{run_id}\nreused_run_id={run_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
