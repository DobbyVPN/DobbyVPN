#!/usr/bin/env python3
"""Fail-stop guard for manually dispatched Test runs used by Publish/Release."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any, Mapping
from urllib.parse import urlencode, urlparse


TASK = "release-verification"
ENVIRONMENT = "release-verification"
TEST_WORKFLOW_PATH = ".github/workflows/test.yml"
TEST_WORKFLOW_FILE = "test.yml"
TESTS_REPOSITORY = "DobbyVPN/Torturer"
SCHEMA = 1
PAGE_SIZE = 100
SHA_RE = re.compile(r"[0-9a-f]{40}\Z")
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")
REPOSITORY_RE = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
CANDIDATE_KEYS = frozenset({
    "schema", "build_run_id", "build_manifest_sha256", "product_sha",
    "tests_repository", "tests_sha", "tests_tree_sha256",
})
STATES = frozenset({"queued", "pending", "in_progress", "success", "failure", "error", "inactive"})
NON_SUCCESS_CONCLUSIONS = frozenset({
    "failure", "cancelled", "timed_out", "action_required", "stale", "neutral", "skipped",
    "startup_failure",
})
WORKFLOW_RUN_STATUSES = frozenset({"queued", "in_progress", "completed", "waiting", "requested", "pending"})


class ReleaseGuardError(ValueError):
    """A guard record or GitHub response is invalid, ambiguous, or unresolved."""


class GitHubAPI:
    """Small gh-api adapter. All state and policy remain in this module."""

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> Any:
        command = ["gh", "api", path, "--method", method.upper()]
        input_bytes = None
        if payload is not None:
            command.extend(("--input", "-"))
            input_bytes = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
        try:
            result = subprocess.run(command, input=input_bytes, capture_output=True, check=True)
        except subprocess.CalledProcessError as error:
            _forward_bytes(sys.stdout, error.stdout)
            _forward_bytes(sys.stderr, error.stderr)
            raise
        except OSError as error:
            raise ReleaseGuardError("cannot execute gh api") from error
        try:
            return json.loads(result.stdout.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            _forward_bytes(sys.stdout, result.stdout)
            _forward_bytes(sys.stderr, result.stderr)
            raise


def _forward_bytes(stream: Any, value: bytes | None) -> None:
    if not value:
        return
    binary_stream = getattr(stream, "buffer", None)
    if binary_stream is None:
        raise TypeError("diagnostic stream does not expose a byte-preserving buffer")
    binary_stream.write(value)
    binary_stream.flush()


def tests_tree_sha256(root: Path) -> str:
    """Fingerprint the exact nonignored test files used locally or by hosted Test."""
    root = root.resolve()
    command = ["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"]
    result = subprocess.run(command, capture_output=True, check=False)
    if result.returncode:
        _forward_bytes(sys.stdout, result.stdout)
        _forward_bytes(sys.stderr, result.stderr)
        result.check_returncode()

    index = subprocess.run(
        ["git", "-C", str(root), "ls-files", "--stage", "-z"],
        capture_output=True,
        check=False,
    )
    if index.returncode:
        _forward_bytes(sys.stdout, index.stdout)
        _forward_bytes(sys.stderr, index.stderr)
        index.check_returncode()
    for entry in index.stdout.split(b"\0"):
        if not entry:
            continue
        metadata, separator, encoded_path = entry.partition(b"\t")
        mode = metadata.split(b" ", 1)[0]
        if separator and mode == b"160000":
            path = Path(os.fsdecode(encoded_path)).as_posix()
            raise ReleaseGuardError(f"selected test tree contains a Git submodule entry: {path}")

    files = sorted({Path(os.fsdecode(name)) for name in result.stdout.split(b"\0") if name},
                   key=lambda path: path.as_posix())
    digest = hashlib.sha256()
    count = 0
    for relative in files:
        source = root / relative
        if source.is_symlink():
            raise ReleaseGuardError(f"selected test tree contains a symlink: {relative.as_posix()}")
        if not source.is_file():
            continue
        encoded = relative.as_posix().encode("utf-8")
        digest.update(len(encoded).to_bytes(8, "big"))
        digest.update(encoded)
        with source.open("rb") as stream:
            digest.update(hashlib.file_digest(stream, "sha256").digest())
        count += 1
    if not count:
        raise ReleaseGuardError("selected test tree contains no files")
    return digest.hexdigest()


def _log_url(record: Mapping[str, Any]) -> str | None:
    value = record.get("log_url")
    return None if value is None or value == "" else value


def _api_request(api: Any, method: str, path: str, payload: dict[str, Any] | None = None) -> Any:
    try:
        result = api.request(method, path, payload)
    except ReleaseGuardError:
        raise
    except Exception as error:
        raise ReleaseGuardError(f"GitHub API request failed: {method.upper()} {path.split('?', 1)[0]}") from error
    return result


def _positive_int(value: Any, label: str) -> int:
    if type(value) is not int or value <= 0:
        raise ReleaseGuardError(f"{label} must be a positive integer")
    return value


def validate_candidate(candidate: Any) -> dict[str, Any]:
    if not isinstance(candidate, dict) or set(candidate) != CANDIDATE_KEYS:
        raise ReleaseGuardError("candidate has unexpected or missing identity fields")
    if type(candidate.get("schema")) is not int or candidate["schema"] != SCHEMA:
        raise ReleaseGuardError(f"candidate schema must be {SCHEMA}")
    checked = dict(candidate)
    checked["build_run_id"] = _positive_int(candidate.get("build_run_id"), "build_run_id")
    if not isinstance(candidate.get("build_manifest_sha256"), str) or not SHA256_RE.fullmatch(candidate["build_manifest_sha256"]):
        raise ReleaseGuardError("build_manifest_sha256 must be 64 lowercase hexadecimal characters")
    for key in ("product_sha", "tests_sha"):
        if not isinstance(candidate.get(key), str) or not SHA_RE.fullmatch(candidate[key]):
            raise ReleaseGuardError(f"{key} must be 40 lowercase hexadecimal characters")
    if not isinstance(candidate.get("tests_tree_sha256"), str) or not SHA256_RE.fullmatch(candidate["tests_tree_sha256"]):
        raise ReleaseGuardError("tests_tree_sha256 must be 64 lowercase hexadecimal characters")
    if candidate.get("tests_repository") != TESTS_REPOSITORY:
        raise ReleaseGuardError(f"tests_repository must be {TESTS_REPOSITORY}")
    return checked


def _validate_repository(repository: Any) -> str:
    if not isinstance(repository, str) or not REPOSITORY_RE.fullmatch(repository):
        raise ReleaseGuardError("repository must be an owner/repository name")
    return repository


def _paged(api: Any, endpoint: str) -> list[Any]:
    records: list[Any] = []
    seen_ids: set[int] = set()
    page = 1
    while True:
        path = f"{endpoint}?{urlencode({'per_page': PAGE_SIZE, 'page': page})}"
        response = _api_request(api, "GET", path)
        if not isinstance(response, list):
            raise ReleaseGuardError("GitHub API returned an invalid paginated list")
        for item in response:
            if not isinstance(item, dict):
                raise ReleaseGuardError("GitHub API returned a malformed list item")
            identifier = item.get("id")
            if type(identifier) is not int or identifier <= 0 or identifier in seen_ids:
                raise ReleaseGuardError("GitHub API returned a missing or duplicate record ID")
            seen_ids.add(identifier)
            records.append(item)
        if len(response) < PAGE_SIZE:
            return records
        page += 1


def _deployment_url(repository: str, deployment_id: int) -> str:
    return f"https://api.github.com/repos/{repository}/deployments/{deployment_id}"


def _run_url(repository: str, run_id: int) -> str:
    return f"https://github.com/{repository}/actions/runs/{run_id}"


def _parse_run_url(value: Any, repository: str) -> int:
    if not isinstance(value, str):
        raise ReleaseGuardError("deployment status is missing its Test run URL")
    parsed = urlparse(value)
    match = re.fullmatch(rf"/{re.escape(repository)}/actions/runs/([1-9][0-9]*)", parsed.path)
    if parsed.scheme != "https" or parsed.netloc != "github.com" or parsed.params or parsed.query or parsed.fragment or not match:
        raise ReleaseGuardError("deployment status has a malformed Test run URL")
    return int(match.group(1))


def _parse_payload(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError as error:
            raise ReleaseGuardError("release-verification deployment payload is not valid JSON") from error
    return validate_candidate(value)


def _all_deployments(api: Any, repository: str) -> list[dict[str, Any]]:
    records = _paged(api, f"repos/{repository}/deployments")
    target: list[dict[str, Any]] = []
    for item in records:
        task, environment = item.get("task"), item.get("environment")
        if not isinstance(task, str) or not isinstance(environment, str):
            raise ReleaseGuardError("GitHub returned a deployment without a task or environment")
        if task == TASK or environment == ENVIRONMENT:
            if task != TASK or environment != ENVIRONMENT:
                raise ReleaseGuardError("deployment has a malformed release-verification namespace")
            deployment_id = _positive_int(item.get("id"), "deployment ID")
            candidate = _parse_payload(item.get("payload"))
            if item.get("ref") != candidate["product_sha"]:
                raise ReleaseGuardError("deployment ref does not match its immutable product SHA")
            item = dict(item)
            item["_candidate"] = candidate
            target.append(item)
    return target


def _target_deployment(api: Any, repository: str, deployment_id: int) -> dict[str, Any]:
    deployment_id = _positive_int(deployment_id, "deployment ID")
    matches = [item for item in _all_deployments(api, repository) if item["id"] == deployment_id]
    if len(matches) != 1:
        raise ReleaseGuardError("release-verification deployment is missing or ambiguous")
    return matches[0]


def _statuses(api: Any, repository: str, deployment: dict[str, Any]) -> list[dict[str, Any]]:
    deployment_id = deployment["id"]
    records = _paged(api, f"repos/{repository}/deployments/{deployment_id}/statuses")
    expected_url = _deployment_url(repository, deployment_id)
    for record in records:
        state = record.get("state")
        if state not in STATES:
            raise ReleaseGuardError("deployment status has an unknown state")
        if record.get("deployment_url") != expected_url:
            raise ReleaseGuardError("deployment status points at a different deployment")
        log_url = _log_url(record)
        if log_url is not None:
            _parse_run_url(log_url, repository)
        elif state in {"in_progress", "success", "failure", "error"}:
            raise ReleaseGuardError(f"{state} deployment status is missing its Test run URL")
    return sorted(records, key=lambda item: item["id"])


def _latest_status(statuses: list[dict[str, Any]]) -> dict[str, Any] | None:
    return statuses[-1] if statuses else None


def _validate_run(
    run: Any,
    *,
    repository: str,
    run_id: int,
    candidate: dict[str, Any],
    allow_retried: bool = False,
) -> dict[str, Any]:
    if not isinstance(run, dict):
        raise ReleaseGuardError("GitHub returned an invalid Test workflow run")
    repo = run.get("repository")
    path = run.get("path")
    if not isinstance(repo, dict) or repo.get("full_name") != repository:
        raise ReleaseGuardError("Test workflow run belongs to a different repository")
    if not isinstance(path, str) or not (path == TEST_WORKFLOW_PATH or path.startswith(TEST_WORKFLOW_PATH + "@")):
        raise ReleaseGuardError("linked run is not the public Test workflow")
    if run.get("event") != "workflow_dispatch" or run.get("head_branch") != "main":
        raise ReleaseGuardError("linked Test must be dispatched from main")
    if type(run.get("id")) is not int or run["id"] != run_id:
        raise ReleaseGuardError("GitHub returned a different Test run ID")
    attempt = run.get("run_attempt")
    if type(attempt) is not int or attempt < 1 or (attempt != 1 and not allow_retried):
        raise ReleaseGuardError("linked Test must be its first workflow attempt")
    if run.get("head_sha") != candidate["product_sha"]:
        raise ReleaseGuardError("linked Test product SHA does not match the reservation")
    if run.get("status") not in {"in_progress", "completed"}:
        raise ReleaseGuardError("linked Test is not active or completed")
    if run.get("status") == "completed" and run.get("conclusion") not in ({"success"} | NON_SUCCESS_CONCLUSIONS):
        raise ReleaseGuardError("completed Test has an unknown conclusion")
    return run


def _get_run(api: Any, repository: str, run_id: int, candidate: dict[str, Any], *, allow_retried: bool = False) -> dict[str, Any]:
    run_id = _positive_int(run_id, "Test run ID")
    run = _api_request(api, "GET", f"repos/{repository}/actions/runs/{run_id}")
    return _validate_run(run, repository=repository, run_id=run_id, candidate=candidate, allow_retried=allow_retried)


def _active_test_runs(api: Any, repository: str) -> list[dict[str, Any]]:
    """List every active main-branch Test dispatch before clearing an unclaimed reservation."""
    endpoint = f"repos/{repository}/actions/workflows/{TEST_WORKFLOW_FILE}/runs"
    active: list[dict[str, Any]] = []
    seen_ids: set[int] = set()
    page = 1
    while True:
        query = urlencode({
            "per_page": PAGE_SIZE,
            "page": page,
            "branch": "main",
            "event": "workflow_dispatch",
        })
        response = _api_request(api, "GET", f"{endpoint}?{query}")
        if not isinstance(response, dict) or not isinstance(response.get("workflow_runs"), list):
            raise ReleaseGuardError("GitHub API returned an invalid Test workflow-run list")
        page_runs = response["workflow_runs"]
        for run in page_runs:
            if not isinstance(run, dict):
                raise ReleaseGuardError("GitHub API returned a malformed Test workflow run")
            run_id = _positive_int(run.get("id"), "Test workflow run ID")
            if run_id in seen_ids:
                raise ReleaseGuardError("GitHub API returned a duplicate Test workflow run ID")
            seen_ids.add(run_id)
            repo = run.get("repository")
            path = run.get("path")
            if not isinstance(repo, dict) or repo.get("full_name") != repository:
                raise ReleaseGuardError("Test workflow run belongs to a different repository")
            if not isinstance(path, str) or not (path == TEST_WORKFLOW_PATH or path.startswith(TEST_WORKFLOW_PATH + "@")):
                raise ReleaseGuardError("active run list contains a different workflow")
            if run.get("event") != "workflow_dispatch" or run.get("head_branch") != "main":
                raise ReleaseGuardError("active Test run was not dispatched from main")
            status = run.get("status")
            if status not in WORKFLOW_RUN_STATUSES:
                raise ReleaseGuardError("GitHub returned a Test run with an unknown status")
            if status != "completed":
                active.append(run)
        if len(page_runs) < PAGE_SIZE:
            return active
        page += 1


def _post_status(
    api: Any,
    repository: str,
    deployment: dict[str, Any],
    state: str,
    *,
    run_id: int | None,
    description: str,
) -> dict[str, Any]:
    payload: dict[str, Any] = {"state": state, "description": description, "auto_inactive": False}
    if run_id is not None:
        payload["log_url"] = _run_url(repository, run_id)
    response = _api_request(
        api, "POST", f"repos/{repository}/deployments/{deployment['id']}/statuses", payload,
    )
    if not isinstance(response, dict) or response.get("state") != state:
        raise ReleaseGuardError("GitHub did not confirm the requested deployment status")
    _positive_int(response.get("id"), "deployment status ID")
    if response.get("deployment_url") != _deployment_url(repository, deployment["id"]):
        raise ReleaseGuardError("GitHub returned a status for a different deployment")
    expected_run_url = _run_url(repository, run_id) if run_id is not None else None
    if _log_url(response) != expected_run_url:
        raise ReleaseGuardError("GitHub did not preserve the exact Test run URL")
    return response


def _status_attempt(status: Mapping[str, Any]) -> int | None:
    description = status.get("description", "")
    if not isinstance(description, str):
        return None
    match = re.fullmatch(r"(?:Test run claimed|Test guard finalized: (?:success|failure|error)) \(attempt ([1-9][0-9]*)\)", description)
    if match:
        return int(match.group(1))
    # Existing first-attempt deployments keep their original descriptions.
    if description == "Test run claimed" or description in {
        "Test guard finalized: success", "Test guard finalized: failure", "Test guard finalized: error",
    }:
        return 1
    return None


def _ensure_no_blockers(api: Any, repository: str, *, exclude_id: int | None = None) -> list[dict[str, Any]]:
    deployments = _all_deployments(api, repository)
    blockers: list[dict[str, Any]] = []
    for deployment in deployments:
        if deployment["id"] == exclude_id:
            continue
        latest = _latest_status(_statuses(api, repository, deployment))
        if latest is None:
            blockers.append({"id": deployment["id"], "state": "missing"})
            continue
        state = latest["state"]
        if state == "inactive":
            continue
        if state == "success":
            run_id = _parse_run_url(latest.get("log_url"), repository)
            run = _get_run(api, repository, run_id, deployment["_candidate"], allow_retried=True)
            if (run.get("status") != "completed" or run.get("conclusion") != "success"
                    or _status_attempt(latest) != run.get("run_attempt")):
                blockers.append({"id": deployment["id"], "state": "success-unverified"})
            continue
        blockers.append({"id": deployment["id"], "state": state})
    if blockers:
        details = ", ".join(f"{item['id']}:{item['state']}" for item in blockers)
        raise ReleaseGuardError(f"release-verification is blocked by unresolved deployments ({details})")
    return deployments


def require_unblocked(api: Any, repository: str) -> dict[str, Any]:
    """Require no unresolved reservation, claim, or failed Test across revisions."""
    repository = _validate_repository(repository)
    deployments = _ensure_no_blockers(api, repository)
    return {"repository": repository, "environment": ENVIRONMENT, "unblocked": True,
            "deployment_ids": [item["id"] for item in deployments]}


def _test_reservation(api, repository, test_run_id):
    matches = [deployment for deployment in _all_deployments(api, repository)
               if any(_log_url(status) == _run_url(repository, test_run_id)
                      for status in _statuses(api, repository, deployment))]
    if len(matches) > 1:
        raise ReleaseGuardError("Test is linked to multiple immutable reservations")
    return matches[0] if matches else None


def validate_early_test(api, repository, test_run_id, build_run_id, product_sha,
                        tests_sha, tests_tree_sha256, *, reservation_id=None):
    """Validate existing stops without reserving an unfinished Build manifest."""
    repository = _validate_repository(repository)
    other_runs = [run for run in _active_test_runs(api, repository) if run["id"] != test_run_id]
    if other_runs:
        raise ReleaseGuardError("another Test workflow is active")
    deployment = (_target_deployment(api, repository, reservation_id)
                  if reservation_id else _test_reservation(api, repository, test_run_id))
    if deployment is None:
        require_unblocked(api, repository)
        return
    candidate = deployment["_candidate"]
    expected = {"build_run_id": build_run_id, "product_sha": product_sha,
                "tests_sha": tests_sha, "tests_tree_sha256": tests_tree_sha256,
                "tests_repository": "DobbyVPN/Torturer"}
    if any(candidate[key] != value for key, value in expected.items()):
        raise ReleaseGuardError("early Test differs from supplied reservation")
    _ensure_no_blockers(api, repository, exclude_id=deployment["id"])
    latest = _latest_status(_statuses(api, repository, deployment))
    if latest is None or latest["state"] != "queued":
        raise ReleaseGuardError("early Test reservation is not queued for execution")
    match = re.fullmatch(r"Retry authorized for attempt ([1-9][0-9]*)", latest.get("description", ""))
    if match:
        run = _get_run(api, repository, test_run_id, candidate, allow_retried=True)
        if _log_url(latest) != _run_url(repository, test_run_id) or int(match.group(1)) != run["run_attempt"]:
            raise ReleaseGuardError("early Test retry authorization differs from its run or attempt")
    elif _log_url(latest) is not None:
        raise ReleaseGuardError("unclaimed early Test reservation has an unexpected run link")


def reservation_for_test(api, repository, test_run_id, candidate, *, reservation_id=None):
    """Called only after resolving the successful final Build manifest."""
    candidate = validate_candidate(dict(candidate))
    deployment = (_target_deployment(api, repository, reservation_id)
                  if reservation_id else _test_reservation(api, repository, test_run_id))
    if deployment is not None:
        if deployment["_candidate"] != candidate:
            raise ReleaseGuardError("final Build differs from immutable Test reservation")
        return deployment["id"]
    return reserve(api, repository, candidate)["deployment_id"]


def recover_reservation(api, repository, candidate):
    """Recover only one exact unclaimed reservation after an interrupted POST."""
    candidate = validate_candidate(dict(candidate))
    matches = []
    for deployment in _all_deployments(api, repository):
        if deployment["_candidate"] != candidate:
            continue
        statuses = _statuses(api, repository, deployment)
        latest = _latest_status(statuses)
        if not statuses or (len(statuses) == 1 and latest["state"] == "queued"
                            and _log_url(latest) is None):
            matches.append(deployment)
    if len(matches) > 1:
        raise ReleaseGuardError("multiple unclaimed reservations match this candidate")
    if not matches:
        return None
    deployment = matches[0]
    _ensure_no_blockers(api, repository, exclude_id=deployment["id"])
    if not _statuses(api, repository, deployment):
        _post_status(api, repository, deployment, "queued", run_id=None, description="Test reservation queued")
    return {"deployment_id": deployment["id"], "candidate": candidate, "state": "queued"}


def reserve(api: Any, repository: str, candidate: Mapping[str, Any]) -> dict[str, Any]:
    """Create the immutable queued reservation before dispatching Test."""
    repository = _validate_repository(repository)
    candidate = validate_candidate(dict(candidate))
    recovered = recover_reservation(api, repository, candidate)
    if recovered is not None:
        return recovered
    deployments = _ensure_no_blockers(api, repository)
    for deployment in deployments:
        latest = _latest_status(_statuses(api, repository, deployment))
        if deployment["_candidate"] == candidate and latest is not None and latest.get("state") == "success":
            raise ReleaseGuardError("this exact candidate already has a reusable successful Test; do not rerun it")
    payload = {
        "ref": candidate["product_sha"],
        "task": TASK,
        "environment": ENVIRONMENT,
        "auto_merge": False,
        "required_contexts": [],
        "payload": candidate,
    }
    deployment = _api_request(api, "POST", f"repos/{repository}/deployments", payload)
    if not isinstance(deployment, dict):
        raise ReleaseGuardError("GitHub returned an invalid deployment")
    deployment_id = _positive_int(deployment.get("id"), "deployment ID")
    if (
        deployment.get("task") != TASK or deployment.get("environment") != ENVIRONMENT
        or deployment.get("ref") != candidate["product_sha"]
        or _parse_payload(deployment.get("payload")) != candidate
    ):
        raise ReleaseGuardError("GitHub created a deployment with different immutable identity")
    created = dict(deployment)
    created["_candidate"] = candidate
    _post_status(api, repository, created, "queued", run_id=None, description="Test reservation queued")
    return {"deployment_id": deployment_id, "candidate": candidate, "state": "queued"}


def _known_run_ids(api: Any, repository: str) -> set[int]:
    known: set[int] = set()
    for deployment in _all_deployments(api, repository):
        for status in _statuses(api, repository, deployment):
            log_url = _log_url(status)
            if log_url is not None:
                known.add(_parse_run_url(log_url, repository))
    return known


def claim(
    api: Any,
    repository: str,
    deployment_id: int,
    test_run_id: int,
    candidate: Mapping[str, Any],
) -> dict[str, Any]:
    """Claim the original run or the next explicitly authorized attempt."""
    repository = _validate_repository(repository)
    deployment = _target_deployment(api, repository, deployment_id)
    candidate = validate_candidate(dict(candidate))
    if deployment["_candidate"] != candidate:
        raise ReleaseGuardError("claim candidate does not match the immutable reservation")
    statuses = _statuses(api, repository, deployment)
    latest = _latest_status(statuses)
    retry_attempt = None
    if latest is not None and latest.get("state") == "in_progress" and _log_url(latest) == _run_url(repository, test_run_id):
        run = _get_run(api, repository, test_run_id, candidate, allow_retried=True)
        if run.get("status") == "in_progress" and _status_attempt(latest) == run["run_attempt"]:
            _ensure_no_blockers(api, repository, exclude_id=deployment["id"])
            return {"deployment_id": deployment["id"], "test_run_id": test_run_id,
                    "log_url": _run_url(repository, test_run_id), "state": "in_progress"}
    if latest is None or latest.get("state") != "queued":
        raise ReleaseGuardError("only a queued reservation can be claimed")
    retry_match = re.fullmatch(r"Retry authorized for attempt ([1-9][0-9]*)", latest.get("description", ""))
    if retry_match:
        retry_attempt = int(retry_match.group(1))
        if _log_url(latest) != _run_url(repository, test_run_id):
            raise ReleaseGuardError("retry authorization is linked to a different Test run")
    elif len(statuses) != 1 or _log_url(latest) is not None:
        raise ReleaseGuardError("only an unclaimed queued reservation can be claimed")
    _ensure_no_blockers(api, repository, exclude_id=deployment["id"])
    test_run_id = _positive_int(test_run_id, "Test run ID")
    if retry_attempt is None and test_run_id in _known_run_ids(api, repository):
        raise ReleaseGuardError("this Test run is already linked to a reservation")
    run = _get_run(api, repository, test_run_id, candidate, allow_retried=retry_attempt is not None)
    if run.get("status") != "in_progress":
        raise ReleaseGuardError("claim must run inside the active Test workflow")
    if retry_attempt is not None and run.get("run_attempt") != retry_attempt:
        raise ReleaseGuardError("active Test attempt does not match the authorized retry")
    _post_status(api, repository, deployment, "in_progress", run_id=test_run_id,
                 description=f"Test run claimed (attempt {run['run_attempt']})")
    return {"deployment_id": deployment["id"], "test_run_id": test_run_id,
            "log_url": _run_url(repository, test_run_id), "state": "in_progress"}


def reserve_early_retry(api, repository, candidate, test_run_id):
    """Bind a reviewed early failure once its Build has a successful manifest."""
    candidate = validate_candidate(dict(candidate))
    run = _get_run(api, repository, test_run_id, candidate, allow_retried=True)
    if run.get("status") != "completed" or run.get("conclusion") not in {"failure", "cancelled", "timed_out"}:
        raise ReleaseGuardError("early retry requires a completed unsuccessful Test")
    existing = _test_reservation(api, repository, test_run_id)
    if existing is not None:
        latest = _latest_status(_statuses(api, repository, existing))
        if (existing["_candidate"] == candidate and latest is not None and latest["state"] == "queued"
                and latest.get("description") == f"Retry authorized for attempt {run['run_attempt'] + 1}"
                and _log_url(latest) == _run_url(repository, test_run_id)):
            _ensure_no_blockers(api, repository, exclude_id=existing["id"])
            return {"deployment_id": existing["id"], "candidate": candidate,
                    "state": "queued", "attempt": run["run_attempt"] + 1}
        raise ReleaseGuardError("early Test already has a reservation")
    reservation = reserve(api, repository, candidate)
    deployment = _target_deployment(api, repository, reservation["deployment_id"])
    attempt = run["run_attempt"] + 1
    _post_status(api, repository, deployment, "queued", run_id=test_run_id,
                 description=f"Retry authorized for attempt {attempt}")
    return {**reservation, "attempt": attempt}


def prepare_retry(
    api: Any, repository: str, deployment_id: int, test_run_id: int,
) -> dict[str, Any]:
    """Durably authorize the next native Test attempt without clearing its history.

    Repeating this call after an ambiguous gh dispatch is safe: the same queued
    authorization is returned while GitHub still reports the old failed attempt
    or has started the exact authorized attempt.
    """
    repository = _validate_repository(repository)
    deployment = _target_deployment(api, repository, deployment_id)
    test_run_id = _positive_int(test_run_id, "Test run ID")
    statuses = _statuses(api, repository, deployment)
    latest = _latest_status(statuses)
    run = _get_run(api, repository, test_run_id, deployment["_candidate"], allow_retried=True)
    if run.get("status") == "completed" and run.get("conclusion") == "success":
        raise ReleaseGuardError("successful Test runs cannot be retried")
    if latest is None:
        raise ReleaseGuardError("Test reservation has no status history")
    expected_url = _run_url(repository, test_run_id)
    match = re.fullmatch(r"Retry authorized for attempt ([1-9][0-9]*)", latest.get("description", ""))
    if latest.get("state") == "queued" and match:
        target_attempt = int(match.group(1))
        if _log_url(latest) != expected_url:
            raise ReleaseGuardError("retry authorization is linked to a different Test run")
        if run.get("run_attempt") not in {target_attempt - 1, target_attempt}:
            raise ReleaseGuardError("authorized retry no longer matches the Test run attempt")
        if run.get("run_attempt") == target_attempt and run.get("status") == "completed" and run.get("conclusion") == "success":
            raise ReleaseGuardError("successful Test attempts cannot be retried")
        if run.get("run_attempt") != target_attempt or run.get("status") != "completed":
            return {"deployment_id": deployment_id, "test_run_id": test_run_id,
                    "attempt": target_attempt, "already_authorized": True}
        # The authorized attempt ended before its finalizer could record failure.
        # Preserve that outcome before authorizing a later reviewed attempt.
        latest_is_unfinalized = True
    else:
        latest_is_unfinalized = (
            latest.get("state") == "in_progress" and _log_url(latest) == expected_url
            and _status_attempt(latest) == run.get("run_attempt")
        )
    if run.get("status") != "completed" or run.get("conclusion") not in NON_SUCCESS_CONCLUSIONS:
        raise ReleaseGuardError("retry requires a completed failed Test attempt")
    unclaimed_first_attempt = (
        run["run_attempt"] == 1 and latest.get("state") == "queued"
        and len(statuses) == 1 and _log_url(latest) is None
        and test_run_id not in _known_run_ids(api, repository)
    )
    recorded_failure = (
        latest.get("state") in {"failure", "error"} and _log_url(latest) == expected_url
        and _status_attempt(latest) == run.get("run_attempt")
    )
    if not (recorded_failure or unclaimed_first_attempt or latest_is_unfinalized):
        raise ReleaseGuardError("latest guard status does not record this failed Test attempt")
    _ensure_no_blockers(api, repository, exclude_id=deployment_id)
    if not recorded_failure:
        _post_status(api, repository, deployment, "failure", run_id=test_run_id,
                     description=f"Test guard finalized: failure (attempt {run['run_attempt']})")
    target_attempt = run["run_attempt"] + 1
    _post_status(api, repository, deployment, "queued", run_id=test_run_id,
                 description=f"Retry authorized for attempt {target_attempt}")
    return {"deployment_id": deployment_id, "test_run_id": test_run_id,
            "attempt": target_attempt, "already_authorized": False}


def finalize(
    api: Any,
    repository: str,
    deployment_id: int,
    test_run_id: int,
    candidate: Mapping[str, Any],
    *,
    outcome: str,
    result_identity: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Write one terminal guard result for the previously claimed run.

    A success requires the caller to pass the exact identity extracted from its
    already validated Test result manifest. Failure/error remain blocking until
    an explicit clear after the linked workflow is no longer running.
    """
    repository = _validate_repository(repository)
    candidate = validate_candidate(dict(candidate))
    if outcome not in {"success", "failure", "error"}:
        raise ReleaseGuardError("outcome must be success, failure, or error")
    deployment = _target_deployment(api, repository, deployment_id)
    if deployment["_candidate"] != candidate:
        raise ReleaseGuardError("finalizer candidate does not match the immutable reservation")
    test_run_id = _positive_int(test_run_id, "Test run ID")
    run = _get_run(api, repository, test_run_id, candidate, allow_retried=True)
    if outcome == "success":
        if run.get("status") == "completed" and run.get("conclusion") != "success":
            raise ReleaseGuardError("cannot finalize Test success for a non-successful workflow run")
        identity = dict(result_identity) if isinstance(result_identity, Mapping) else {}
        identity_candidate = {key: identity.get(key) for key in CANDIDATE_KEYS}
        if (
            result_identity is None or validate_candidate(identity_candidate) != candidate
            or identity.get("test_run_id") != test_run_id
            or identity.get("run_attempt") != run.get("run_attempt")
        ):
            raise ReleaseGuardError("successful finalization requires the exact validated Test result identity")
    elif outcome == "failure" and run.get("status") == "completed" and run.get("conclusion") == "success":
        raise ReleaseGuardError("cannot finalize Test failure for a successful workflow run")

    statuses = _statuses(api, repository, deployment)
    latest = _latest_status(statuses)
    expected_url = _run_url(repository, test_run_id)
    if (latest is not None and latest.get("state") == outcome and latest.get("log_url") == expected_url
            and _status_attempt(latest) == run.get("run_attempt")):
        return {"deployment_id": deployment_id, "test_run_id": test_run_id, "state": outcome,
                "log_url": expected_url, "already_finalized": True}
    claims = [item for item in statuses if item.get("state") == "in_progress" and item.get("log_url") == expected_url]
    retry_match = re.fullmatch(r"Retry authorized for attempt ([1-9][0-9]*)", latest.get("description", "")) if latest else None
    retry_armed = bool(retry_match and latest.get("state") == "queued" and _log_url(latest) == expected_url
                       and int(retry_match.group(1)) == run.get("run_attempt"))
    active_claim = bool(latest and latest.get("state") == "in_progress" and _log_url(latest) == expected_url
                        and _status_attempt(latest) == run.get("run_attempt")
                        and len(claims) >= 1)
    if not (active_claim or retry_armed):
        raise ReleaseGuardError("finalizer must match the active or authorized Test attempt")
    _ensure_no_blockers(api, repository, exclude_id=deployment["id"])
    _post_status(api, repository, deployment, outcome, run_id=test_run_id,
                 description=f"Test guard finalized: {outcome} (attempt {run['run_attempt']})")
    return {"deployment_id": deployment_id, "test_run_id": test_run_id,
            "state": outcome, "log_url": expected_url}


def require_success(
    api: Any,
    repository: str,
    candidate: Mapping[str, Any],
    test_run_id: int | None = None,
) -> dict[str, Any]:
    """Require the exact reusable reservation and a completed successful run."""
    repository = _validate_repository(repository)
    candidate = validate_candidate(dict(candidate))
    deployments = _ensure_no_blockers(api, repository)
    matches: list[tuple[dict[str, Any], dict[str, Any], int]] = []
    for deployment in deployments:
        if deployment["_candidate"] != candidate:
            continue
        latest = _latest_status(_statuses(api, repository, deployment))
        if latest is None or latest.get("state") != "success":
            continue
        linked_id = _parse_run_url(latest.get("log_url"), repository)
        if test_run_id is not None and linked_id != _positive_int(test_run_id, "Test run ID"):
            continue
        run = _get_run(api, repository, linked_id, candidate, allow_retried=True)
        if (run.get("status") != "completed" or run.get("conclusion") != "success"
                or _status_attempt(latest) != run.get("run_attempt")):
            raise ReleaseGuardError("successful deployment is not backed by the completed successful Test attempt")
        matches.append((deployment, latest, linked_id))
    if len(matches) != 1:
        raise ReleaseGuardError("no unique successful Test guard matches this exact candidate and run")
    deployment, _status, linked_id = matches[0]
    return {"deployment_id": deployment["id"], "test_run_id": linked_id,
            "run_attempt": run["run_attempt"], "candidate": candidate, "state": "success"}


def clear(api: Any, repository: str, deployment_id: int) -> dict[str, Any]:
    """Mark one exact nonrunning failed/unresolved attempt inactive; never dispatch."""
    repository = _validate_repository(repository)
    deployment = _target_deployment(api, repository, deployment_id)
    statuses = _statuses(api, repository, deployment)
    latest = _latest_status(statuses)
    if latest is not None and latest.get("state") == "inactive":
        return {"deployment_id": deployment["id"], "state": "inactive", "already_cleared": True}

    linked_id = None
    for status in reversed(statuses):
        log_url = _log_url(status)
        if log_url is not None:
            linked_id = _parse_run_url(log_url, repository)
            break
    if linked_id is not None:
        run = _get_run(api, repository, linked_id, deployment["_candidate"], allow_retried=True)
        if run.get("status") != "completed":
            raise ReleaseGuardError("cannot clear a reservation while its Test workflow is running")
        if (
            latest is not None and latest.get("state") == "success"
            and run.get("conclusion") == "success"
            and _status_attempt(latest) == run.get("run_attempt")
        ):
            raise ReleaseGuardError("a successful Test reservation cannot be cleared")
    else:
        active_runs = _active_test_runs(api, repository)
        if active_runs:
            identifiers = ", ".join(str(run["id"]) for run in active_runs)
            raise ReleaseGuardError(f"cannot clear an unclaimed reservation while Test runs are active ({identifiers})")
    _post_status(api, repository, deployment, "inactive", run_id=linked_id,
                 description="Test reservation explicitly cleared")
    return {"deployment_id": deployment["id"], "state": "inactive", "cleared_run_id": linked_id}


def _candidate_from_args(args: argparse.Namespace) -> dict[str, Any]:
    return {
        "schema": SCHEMA,
        "build_run_id": args.build_run_id,
        "build_manifest_sha256": args.build_manifest_sha256,
        "product_sha": args.product_sha,
        "tests_repository": args.tests_repository,
        "tests_sha": args.tests_sha,
        "tests_tree_sha256": args.tests_tree_sha256,
    }


def _add_candidate_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--build-run-id", type=int, required=True)
    parser.add_argument("--build-manifest-sha256", required=True)
    parser.add_argument("--product-sha", required=True)
    parser.add_argument("--tests-repository", required=True)
    parser.add_argument("--tests-sha", required=True)
    parser.add_argument("--tests-tree-sha256", required=True)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="operation", required=True)
    reserve_parser = subparsers.add_parser("reserve")
    reserve_parser.add_argument("--repository", required=True)
    _add_candidate_args(reserve_parser)
    claim_parser = subparsers.add_parser("claim")
    claim_parser.add_argument("--repository", required=True)
    claim_parser.add_argument("--deployment-id", type=int, required=True)
    claim_parser.add_argument("--test-run-id", type=int, required=True)
    _add_candidate_args(claim_parser)
    finalize_parser = subparsers.add_parser("finalize")
    finalize_parser.add_argument("--repository", required=True)
    finalize_parser.add_argument("--deployment-id", type=int, required=True)
    finalize_parser.add_argument("--test-run-id", type=int, required=True)
    finalize_parser.add_argument("--outcome", choices=("success", "failure", "error"), required=True)
    finalize_parser.add_argument("--result-identity-file", type=Path)
    _add_candidate_args(finalize_parser)
    retry_parser = subparsers.add_parser("prepare-retry")
    retry_parser.add_argument("--repository", required=True)
    retry_parser.add_argument("--deployment-id", type=int, required=True)
    retry_parser.add_argument("--test-run-id", type=int, required=True)
    for operation in ("require-unblocked", "require-success", "clear"):
        current = subparsers.add_parser(operation)
        current.add_argument("--repository", required=True)
        if operation == "require-success":
            current.add_argument("--test-run-id", type=int)
            _add_candidate_args(current)
        elif operation == "clear":
            current.add_argument("--deployment-id", type=int, required=True)
    args = parser.parse_args(argv)
    repository = args.repository
    api = GitHubAPI()
    try:
        if args.operation == "reserve":
            result = reserve(api, repository, _candidate_from_args(args))
        elif args.operation == "claim":
            result = claim(api, repository, args.deployment_id, args.test_run_id, _candidate_from_args(args))
        elif args.operation == "finalize":
            identity = None
            if args.result_identity_file is not None:
                identity = json.loads(args.result_identity_file.read_text(encoding="utf-8"))
            result = finalize(
                api, repository, args.deployment_id, args.test_run_id, _candidate_from_args(args),
                outcome=args.outcome, result_identity=identity,
            )
        elif args.operation == "prepare-retry":
            result = prepare_retry(api, repository, args.deployment_id, args.test_run_id)
        elif args.operation == "require-unblocked":
            result = require_unblocked(api, repository)
        elif args.operation == "require-success":
            result = require_success(api, repository, _candidate_from_args(args), args.test_run_id)
        else:
            result = clear(api, repository, args.deployment_id)
    except (ReleaseGuardError, OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
        print(f"Release guard error: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
