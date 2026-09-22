#!/usr/bin/env python3
"""Converge trusted Actions storage to the current workflow run.

Pull-request and Dependabot workflows may not have ``actions: write``.  The
caller therefore invokes this helper only from trusted push/manual workflows.
The selection logic is deliberately pure and is covered by unit tests; the
CLI never deletes a run that is active, queued, or the current run.
"""

from __future__ import annotations

import argparse
from datetime import date, timedelta
import json
import os
from dataclasses import dataclass
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Any, Iterable


ACTIVE_STATES = frozenset({"queued", "in_progress", "waiting", "requested", "pending"})


class RetentionError(RuntimeError):
    """An Actions listing or deletion failed."""


@dataclass(frozen=True)
class Deletion:
    kind: str
    identifier: str


@dataclass(frozen=True)
class DeletionResult:
    deletion: Deletion
    outcome: str
    stdout: str
    stderr: str
    error: str | None = None


def _emit_streams(label: str, stdout: str, stderr: str) -> None:
    def emit(destination: Any, stream_name: str, payload: str) -> None:
        destination.write(f"[{label} {stream_name} begin]\n")
        destination.write(payload)
        if payload and not payload.endswith("\n"):
            destination.write("\n")
        destination.write(f"[{label} {stream_name} end]\n")
        destination.flush()

    emit(sys.stdout, "stdout", stdout)
    emit(sys.stderr, "stderr", stderr)


def _id(value: Any) -> str:
    return str(value)


def deletion_candidates(runs: Iterable[dict[str, Any]], current_run_id: str) -> list[Deletion]:
    """Return only completed, non-current runs in deterministic API order."""
    candidates: list[Deletion] = []
    for run in runs:
        run_id = _id(run.get("id", ""))
        if not run_id or run_id == current_run_id:
            continue
        if run.get("status") != "completed":
            continue
        candidates.append(Deletion("run", run_id))
    return candidates


def cache_candidates(caches: Iterable[dict[str, Any]]) -> list[Deletion]:
    return [
        Deletion("cache", _id(cache.get("id", "")))
        for cache in caches
        if _id(cache.get("id", ""))
    ]


def artifact_candidates(
    artifacts: Iterable[dict[str, Any]],
    current_run_id: str,
    keep_names: set[str],
) -> list[Deletion]:
    """Select current-run artifacts not needed after final consumers finish."""
    result: list[Deletion] = []
    for artifact in artifacts:
        artifact_id = _id(artifact.get("id", ""))
        owner = artifact.get("workflow_run")
        owner_id = _id(owner.get("id", "")) if isinstance(owner, dict) else ""
        if (
            artifact_id
            and owner_id == current_run_id
            and not artifact.get("expired", False)
            and artifact.get("name") not in keep_names
        ):
            result.append(Deletion("artifact", artifact_id))
    return result


def _gh_json(repository: str, endpoint: str) -> Any:
    result = subprocess.run(
        ["gh", "api", "--paginate", "--slurp", f"repos/{repository}/{endpoint}"],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="backslashreplace",
        check=False,
    )
    _emit_streams("actions-retention list", result.stdout, result.stderr)
    if result.returncode:
        raise RetentionError(
            f"GitHub API listing failed (exit {result.returncode})\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    try:
        pages = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RetentionError(
            f"GitHub API listing was not JSON\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        ) from error
    if not isinstance(pages, list):
        pages = [pages]
    return pages


def _gh_single_json(repository: str, endpoint: str) -> Any:
    """Read one API page, preserving the endpoint's total-count metadata."""
    result = subprocess.run(
        ["gh", "api", f"repos/{repository}/{endpoint}"],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="backslashreplace",
        check=False,
    )
    _emit_streams("actions-retention list", result.stdout, result.stderr)
    if result.returncode:
        raise RetentionError(
            f"GitHub API listing failed (exit {result.returncode})\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RetentionError(
            f"GitHub API listing was not JSON\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        ) from error


def _flatten_runs(pages: Iterable[Any]) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for page in pages:
        values = page.get("workflow_runs", []) if isinstance(page, dict) else page
        if isinstance(values, list):
            result.extend(item for item in values if isinstance(item, dict))
    return result


def _flatten_caches(pages: Iterable[Any]) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for page in pages:
        values = page.get("actions_caches", []) if isinstance(page, dict) else page
        if isinstance(values, list):
            result.extend(item for item in values if isinstance(item, dict))
    return result


def _completed_runs(repository: str) -> list[dict[str, Any]]:
    """List all completed runs, including histories beyond GitHub's 1,000 cap.

    The Actions runs endpoint silently limits one query to its newest 1,000
    records.  Split the created-date range whenever a query fills that cap;
    date ranges are inclusive, so the stack uses half-open intervals to avoid
    duplicate records.  A repository is extremely unlikely to create 1,000
    runs in one UTC day; if it does, the helper fails closed rather than
    deleting an incomplete view of history.
    """
    today = date.today()
    pending: list[tuple[date, date]] = [(date(2000, 1, 1), today + timedelta(days=1))]
    runs: dict[str, dict[str, Any]] = {}
    while pending:
        start, end = pending.pop()
        last_day = end - timedelta(days=1)
        endpoint = (
            f"actions/runs?status=completed&per_page=100&created="
            f"{start.isoformat()}..{last_day.isoformat()}"
        )
        overview = _gh_single_json(
            repository,
            endpoint.replace("per_page=100", "per_page=1"),
        )
        total = overview.get("total_count") if isinstance(overview, dict) else None
        if not isinstance(total, int):
            raise RetentionError("GitHub completed-run response has no valid total_count")
        if total >= 1000:
            if end - start <= timedelta(days=1):
                raise RetentionError(
                    f"GitHub completed-run history has at least 1,000 runs on {start.isoformat()}; "
                    "retention cannot safely enumerate that day"
                )
            midpoint = start + (end - start) // 2
            pending.extend(((start, midpoint), (midpoint, end)))
            continue
        pages = _gh_json(
            repository,
            endpoint,
        )
        values = _flatten_runs(pages)
        for run in values:
            run_id = _id(run.get("id", ""))
            if run_id:
                runs[run_id] = run
    return list(runs.values())


def _delete(repository: str, deletion: Deletion) -> DeletionResult:
    if deletion.kind == "run":
        endpoint = f"repos/{repository}/actions/runs/{deletion.identifier}"
    elif deletion.kind == "cache":
        endpoint = f"repos/{repository}/actions/caches/{deletion.identifier}"
    elif deletion.kind == "artifact":
        endpoint = f"repos/{repository}/actions/artifacts/{deletion.identifier}"
    else:  # pragma: no cover - protected by constructors above
        raise RetentionError(f"unknown deletion kind: {deletion.kind}")
    try:
        result = subprocess.run(
            ["gh", "api", "--method", "DELETE", endpoint],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            errors="backslashreplace",
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as error:
        return DeletionResult(
            deletion,
            "failed",
            str(getattr(error, "stdout", "") or ""),
            str(getattr(error, "stderr", "") or ""),
            f"GitHub deletion could not complete for {deletion.kind} {deletion.identifier}: "
            f"{type(error).__name__}: {error}",
        )
    if result.returncode:
        combined = f"{result.stdout}\n{result.stderr}".lower()
        if "404" in combined or "not found" in combined or "already deleted" in combined:
            return DeletionResult(deletion, "already-deleted", result.stdout, result.stderr)
        return DeletionResult(
            deletion,
            "failed",
            result.stdout,
            result.stderr,
            f"GitHub deletion failed for {deletion.kind} {deletion.identifier} "
            f"(exit {result.returncode})",
        )
    return DeletionResult(deletion, "deleted", result.stdout, result.stderr)


def _print_deletion_result(result: DeletionResult) -> None:
    deletion = result.deletion
    print(f"retention: {result.outcome} {deletion.kind} {deletion.identifier}")
    _emit_streams(
        f"retention {deletion.kind} {deletion.identifier}",
        result.stdout,
        result.stderr,
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY"), required=False)
    parser.add_argument("--current-run-id", default=os.environ.get("GITHUB_RUN_ID"), required=False)
    parser.add_argument("--delete-caches", action="store_true")
    parser.add_argument("--delete-intermediate-artifacts", action="store_true")
    parser.add_argument("--keep-artifact", action="append", default=[])
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument(
        "--workers",
        type=int,
        default=8,
        help="parallel deletion workers (default: 8)",
    )
    args = parser.parse_args(argv)
    if not args.repository or not args.current_run_id:
        parser.error("--repository and --current-run-id are required")
    if args.workers <= 0:
        parser.error("--workers must be positive")

    try:
        runs = _completed_runs(args.repository)
        deletions = deletion_candidates(runs, str(args.current_run_id))
        if args.delete_caches:
            cache_pages = _gh_json(args.repository, "actions/caches?per_page=100")
            deletions.extend(cache_candidates(_flatten_caches(cache_pages)))
        if args.delete_intermediate_artifacts:
            artifact_pages = _gh_json(args.repository, "actions/artifacts?per_page=100")
            artifacts = [
                item
                for page in artifact_pages
                for item in (page.get("artifacts", []) if isinstance(page, dict) else [])
                if isinstance(item, dict)
            ]
            deletions.extend(
                artifact_candidates(artifacts, str(args.current_run_id), set(args.keep_artifact))
            )
        failures: list[str] = []
        attempted = len(deletions)
        if args.dry_run:
            action = "would delete"
            for deletion in deletions:
                _print_deletion_result(DeletionResult(deletion, action, "", ""))
        if not args.dry_run:
            completed: dict[Deletion, DeletionResult] = {}
            with ThreadPoolExecutor(max_workers=args.workers) as executor:
                futures = {
                    executor.submit(_delete, args.repository, deletion): deletion
                    for deletion in deletions
                }
                for future in as_completed(futures):
                    deletion = futures[future]
                    try:
                        completed[deletion] = future.result()
                    except Exception as error:
                        failures.append(
                            f"{deletion.kind} {deletion.identifier}: "
                            f"{type(error).__name__}: {error}"
                        )
            for deletion in deletions:
                result = completed.get(deletion)
                if result is not None:
                    _print_deletion_result(result)
                    if result.error is not None:
                        failures.append(result.error)
        if failures:
            raise RetentionError(";\n".join(failures))
        action = "would delete" if args.dry_run else "processed"
        print(
            f"retention: inspected {len(runs)} completed workflow runs; "
            f"{action} {attempted} records/caches"
        )
        return 0
    except Exception as error:
        print(f"{type(error).__name__}: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
