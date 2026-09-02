#!/usr/bin/env python3
"""Fail-closed identity policy for the Release/Torturer publication handoff.

This module deliberately contains no GitHub or App Store client.  The
coordinator workflow obtains API documents and calls these pure validators
before publication. Torturer remains the sole owner of functional meaning;
this code checks only immutable run and artifact identity.
"""

from __future__ import annotations

import argparse
import datetime
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
import re
import sys
from typing import Any


DOBBYVPN_REPOSITORY = "DobbyVPN/DobbyVPN"
TORTURER_REPOSITORY = "DobbyVPN/Torturer"
RELEASE_WORKFLOW_PATH = ".github/workflows/release.yml"
RELEASE_WORKFLOW_NAME = "Release"
TORTURER_WORKFLOW_PATH = ".github/workflows/functional.yml"
TORTURER_WORKFLOW_NAME = "Trusted public Release qualification"
RELEASE_BRANCH = "main"
RELEASE_EVENT = "push"
TORTURER_BRANCH = "main"
TORTURER_EVENT = "workflow_dispatch"

PLATFORMS = ("linux", "windows", "macos", "android")

_SHA40 = re.compile(r"[0-9a-f]{40}\Z")
_POSITIVE = re.compile(r"[1-9][0-9]*\Z")
_RENDER_COMPLETE_ARTIFACT = re.compile(
    r"^render-complete-(?P<lease>[0-9a-f]{32})-"
    r"(?P<platform>linux|windows|macos|android)$"
)
_PUBLIC_EVIDENCE_ARTIFACT = re.compile(
    r"^public-functional-evidence-(?P<platform>linux|windows|macos|android)-"
    r"(?P<run_id>[1-9][0-9]*)-(?P<run_attempt>[1-9][0-9]*)$"
)
_PUBLIC_RAW_LOGS_ARTIFACT = re.compile(
    r"^functional-raw-logs-(?P<run_id>[1-9][0-9]*)-"
    r"(?P<run_attempt>[1-9][0-9]*)-"
    r"(?P<platform>linux|windows|macos|android)$"
)

# These are the only Release artifacts consumed by public qualification or
# publication. Intermediate build artifacts may exist in the Release run but
# are never downloaded by this lane.
REQUIRED_RELEASE_ARTIFACTS = (
    "dobbyVPN-linux.deb",
    "dobbyVPN-windows-amd64.msi",
    "dobbyVPN-macos-amd64.pkg",
    "dobbyVPN-macos-aarch64.pkg",
    "dobbyvpn-android-sign.apk",
    "dobbyvpn-android-unsign.apk",
    "dobbyvpn-android-provenance",
    "dobbyvpn-android-test-companion.apk",
    "DobbyVPN.ipa",
    "DobbyVPN.ipa.provenance",
)


class PublicationPolicyError(ValueError):
    """A handoff document failed the publication policy."""


def _mapping(value: object, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise PublicationPolicyError(f"{label} has an unsafe shape")
    return value


def _positive(value: object, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise PublicationPolicyError(f"{label} is invalid")
    return value


def _positive_text(value: object, label: str) -> int:
    if not isinstance(value, str) or _POSITIVE.fullmatch(value) is None:
        raise PublicationPolicyError(f"{label} is invalid")
    return int(value)


def _sha(value: object, label: str) -> str:
    if not isinstance(value, str) or _SHA40.fullmatch(value) is None:
        raise PublicationPolicyError(f"{label} is invalid")
    return value


def normalize_identity(value: Mapping[str, Any]) -> dict[str, object]:
    """Validate and return the immutable identity carried by the handoff."""

    result = {
        "dobbyvpn_commit": _sha(value.get("dobbyvpn_commit"), "DobbyVPN commit"),
        "release_run_id": _positive_text(value.get("release_run_id"), "Release run id"),
        "release_run_attempt": _positive_text(
            value.get("release_run_attempt"), "Release run attempt"
        ),
        "torturer_commit": _sha(value.get("torturer_commit"), "Torturer commit"),
        "torturer_run_id": _positive_text(value.get("torturer_run_id"), "Torturer run id"),
        "torturer_run_attempt": _positive_text(
            value.get("torturer_run_attempt"), "Torturer run attempt"
        ),
    }
    if set(value) - set(result):
        raise PublicationPolicyError("handoff identity contains unexpected fields")
    return result


def identity_message(identity: Mapping[str, Any]) -> str:
    """Return the canonical marker for one exact qualification identity."""

    normalized = normalize_identity(identity)
    return "publication-handoff-v1|" + "|".join(
        f"{key}={normalized[key]}" for key in normalized
    )


def publication_identity_marker(identity: Mapping[str, Any]) -> str:
    """Return the stable marker owned by one coordinator identity.

    The marker is used in both draft and published release bodies. It
    deliberately contains the six handoff fields and never a child workflow
    run id.
    """

    return f"<!-- {identity_message(identity)} -->"


def validate_identity(identity: Mapping[str, Any]) -> dict[str, object]:
    """Validate the six identity-only fields accepted from Torturer."""

    return normalize_identity(identity)

def _workflow_identity(
    workflow: Mapping[str, Any], *, repository: str, path: str, name: str
) -> int:
    workflow_id = _positive(workflow.get("id"), "workflow id")
    if workflow.get("name") != name:
        raise PublicationPolicyError(f"workflow name is not {name}")
    if workflow.get("path") != path:
        raise PublicationPolicyError("workflow path is not the fixed workflow")
    if workflow.get("state") != "active":
        raise PublicationPolicyError("workflow is not active")
    workflow_repository = workflow.get("repository")
    if workflow_repository is not None:
        workflow_repository = _mapping(workflow_repository, "workflow repository")
        if workflow_repository.get("full_name") != repository:
            raise PublicationPolicyError("workflow repository is not fixed")
    return workflow_id


def _validate_run_common(
    run: Mapping[str, Any],
    *,
    expected_id: int,
    expected_attempt: int,
    expected_commit: str,
    workflow_id: int,
    repository: str,
    event: str,
    branch: str,
    workflow_name: str,
    workflow_path: str,
    label: str,
) -> dict[str, object]:
    expected = {
        "id": expected_id,
        "run_attempt": expected_attempt,
        "workflow_id": workflow_id,
        "name": workflow_name,
        "path": workflow_path,
        "event": event,
        "head_branch": branch,
        "head_sha": expected_commit,
        "status": "completed",
        "conclusion": "success",
    }
    for key, expected_value in expected.items():
        if run.get(key) != expected_value:
            raise PublicationPolicyError(f"{label} {key} does not match")
    for key in ("repository", "head_repository"):
        repository_record = _mapping(run.get(key), f"{label} {key}")
        if repository_record.get("full_name") != repository:
            raise PublicationPolicyError(f"{label} {key} is not fixed")
    return {
        "repository": repository,
        "workflow": workflow_path,
        "run_id": expected_id,
        "run_attempt": expected_attempt,
        "commit": expected_commit,
    }


def validate_release_run(
    run: Mapping[str, Any],
    workflow: Mapping[str, Any],
    *,
    expected_run_id: int,
    expected_attempt: int,
    expected_commit: str,
) -> dict[str, object]:
    workflow_id = _workflow_identity(
        workflow,
        repository=DOBBYVPN_REPOSITORY,
        path=RELEASE_WORKFLOW_PATH,
        name=RELEASE_WORKFLOW_NAME,
    )
    return _validate_run_common(
        run,
        expected_id=_positive(expected_run_id, "Release run id"),
        expected_attempt=_positive(expected_attempt, "Release run attempt"),
        expected_commit=_sha(expected_commit, "DobbyVPN commit"),
        workflow_id=workflow_id,
        repository=DOBBYVPN_REPOSITORY,
        event=RELEASE_EVENT,
        branch=RELEASE_BRANCH,
        workflow_name=RELEASE_WORKFLOW_NAME,
        workflow_path=RELEASE_WORKFLOW_PATH,
        label="Release run",
    )


def validate_torturer_run(
    run: Mapping[str, Any],
    workflow: Mapping[str, Any],
    *,
    expected_run_id: int,
    expected_attempt: int,
    expected_commit: str,
) -> dict[str, object]:
    workflow_id = _workflow_identity(
        workflow,
        repository=TORTURER_REPOSITORY,
        path=TORTURER_WORKFLOW_PATH,
        name=TORTURER_WORKFLOW_NAME,
    )
    return _validate_run_common(
        run,
        expected_id=_positive(expected_run_id, "Torturer run id"),
        expected_attempt=_positive(expected_attempt, "Torturer run attempt"),
        expected_commit=_sha(expected_commit, "Torturer commit"),
        workflow_id=workflow_id,
        repository=TORTURER_REPOSITORY,
        event=TORTURER_EVENT,
        branch=TORTURER_BRANCH,
        workflow_name=TORTURER_WORKFLOW_NAME,
        workflow_path=TORTURER_WORKFLOW_PATH,
        label="Torturer run",
    )


def validate_release_jobs(jobs: Mapping[str, Any] | Sequence[Mapping[str, Any]]) -> None:
    """Require the exact successful iOS build and internal TestFlight step."""

    values = _jobs(values=jobs, label="Release jobs")
    ios = [job for job in values if job.get("name") == "ios_build / ios_build"]
    if len(ios) != 1 or ios[0].get("conclusion") != "success":
        raise PublicationPolicyError("Release has no unique successful iOS build job")
    steps = _jobs(values=ios[0].get("steps"), label="iOS build steps")
    uploads = [
        step
        for step in steps
        if step.get("name") == "Fastlane upload_testflight"
        and step.get("conclusion") == "success"
    ]
    if len(uploads) != 1:
        raise PublicationPolicyError("Release iOS job has no successful TestFlight upload")


def validate_release_jobs_for_attempt(
    jobs: Mapping[str, Any] | Sequence[Mapping[str, Any]], *, expected_attempt: int
) -> list[tuple[datetime.datetime, datetime.datetime]]:
    """Bind the Release job evidence to the selected rerun attempt.

    GitHub's artifact ``workflow_run`` record does not expose ``run_attempt``.
    The jobs endpoint does, so attempt binding is performed here and the
    artifact records are bound to the already validated run id and commit.
    """

    attempt = _positive(expected_attempt, "Release run attempt")
    values = _jobs(values=jobs, label="Release jobs")
    if not values:
        raise PublicationPolicyError("Release jobs are empty")
    intervals: list[tuple[datetime.datetime, datetime.datetime]] = []
    for job in values:
        if job.get("run_attempt") != attempt:
            raise PublicationPolicyError("Release job belongs to another run attempt")
        if job.get("status") != "completed" or job.get("conclusion") not in {
            "success", "skipped",
        }:
            raise PublicationPolicyError("Release attempt has an incomplete or failed job")
        if job.get("conclusion") == "skipped":
            continue
        started = _timestamp(job.get("started_at"), "Release job start")
        completed = _timestamp(job.get("completed_at"), "Release job completion")
        if completed < started:
            raise PublicationPolicyError("Release job interval is invalid")
        intervals.append((started, completed))
    if not intervals:
        raise PublicationPolicyError("Release attempt has no successful job intervals")
    return intervals


def validate_torturer_jobs(
    jobs: Mapping[str, Any] | Sequence[Mapping[str, Any]], *, expected_attempt: int
) -> dict[str, list[tuple[datetime.datetime, datetime.datetime]]]:
    """Require one successful exact-attempt job for every public platform."""

    attempt = _positive(expected_attempt, "Torturer run attempt")
    values = _jobs(values=jobs, label="Torturer jobs")
    if not values:
        raise PublicationPolicyError("Torturer jobs are empty")
    for job in values:
        if job.get("run_attempt") != attempt:
            raise PublicationPolicyError("Torturer job belongs to another run attempt")
    intervals: dict[str, list[tuple[datetime.datetime, datetime.datetime]]] = {}
    for platform in PLATFORMS:
        expected_name = f"{platform} / Public Release qualification / {platform}"
        matching = [job for job in values if job.get("name") == expected_name]
        if (
            len(matching) != 1
            or matching[0].get("status") != "completed"
            or matching[0].get("conclusion") != "success"
        ):
            raise PublicationPolicyError(f"Torturer has no unique successful {platform} job")
        started = _timestamp(matching[0].get("started_at"), f"{platform} job start")
        completed = _timestamp(matching[0].get("completed_at"), f"{platform} job completion")
        if completed < started:
            raise PublicationPolicyError(f"{platform} job interval is invalid")
        intervals[platform] = [(started, completed)]
    return intervals


def _jobs(
    values: Mapping[str, Any] | Sequence[Mapping[str, Any]] | object, *, label: str
) -> list[Mapping[str, Any]]:
    if isinstance(values, Mapping):
        values = values.get("jobs")
    if not isinstance(values, Sequence) or isinstance(values, (str, bytes)):
        raise PublicationPolicyError(f"{label} has an unsafe shape")
    result = []
    for value in values:
        result.append(_mapping(value, label[:-1] if label.endswith("s") else label))
    return result


def _timestamp(value: object, label: str) -> datetime.datetime:
    if not isinstance(value, str):
        raise PublicationPolicyError(f"{label} is invalid")
    try:
        parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise PublicationPolicyError(f"{label} is invalid") from error
    if parsed.tzinfo is None:
        raise PublicationPolicyError(f"{label} is invalid")
    return parsed


def _artifact_pages(listing: Mapping[str, Any] | Sequence[Mapping[str, Any]]) -> list[Mapping[str, Any]]:
    if isinstance(listing, Mapping):
        values = listing.get("artifacts")
        if not isinstance(values, Sequence) or isinstance(values, (str, bytes)):
            raise PublicationPolicyError("artifact listing has an unsafe shape")
        total = listing.get("total_count")
        if total is not None and total != len(values):
            raise PublicationPolicyError("artifact listing is incomplete")
        return [_mapping(item, "artifact") for item in values]
    if not isinstance(listing, Sequence) or isinstance(listing, (str, bytes)):
        raise PublicationPolicyError("artifact listing has an unsafe shape")
    result: list[Mapping[str, Any]] = []
    for page in listing:
        page_map = _mapping(page, "artifact listing page")
        page_values = page_map.get("artifacts")
        if not isinstance(page_values, Sequence) or isinstance(page_values, (str, bytes)):
            raise PublicationPolicyError("artifact listing page has an unsafe shape")
        result.extend(_mapping(item, "artifact") for item in page_values)
    return result


def _artifact_record(
    item: Mapping[str, Any], *, run_id: int, commit: str | None
) -> dict[str, object]:
    workflow_run = _mapping(item.get("workflow_run"), "artifact workflow run")
    if workflow_run.get("id") != run_id:
        raise PublicationPolicyError("artifact is from another workflow run")
    # The GitHub REST artifact schema intentionally has no run_attempt. The
    # caller must validate the parent run and its jobs endpoint for the exact
    # attempt; requiring a fabricated artifact field would reject valid API
    # responses and would not improve provenance.
    if commit is not None and workflow_run.get("head_sha") != commit:
        raise PublicationPolicyError("artifact workflow commit does not match")
    if item.get("expired") is not False:
        raise PublicationPolicyError("artifact is expired")
    artifact_id = _positive(item.get("id"), "artifact id")
    return {
        "id": artifact_id,
        "name": item.get("name"),
    }


def _created_in_intervals(
    item: Mapping[str, Any],
    intervals: Sequence[tuple[datetime.datetime, datetime.datetime]],
    label: str,
) -> bool:
    created = _timestamp(item.get("created_at"), f"{label} artifact creation time")
    return any(started <= created <= completed for started, completed in intervals)


def validate_release_artifacts(
    listing: Mapping[str, Any] | Sequence[Mapping[str, Any]],
    *,
    expected_run_id: int,
    expected_attempt: int | None = None,
    expected_commit: str | None = None,
    job_intervals: Sequence[tuple[datetime.datetime, datetime.datetime]] | None = None,
) -> dict[str, dict[str, object]]:
    run_id = _positive(expected_run_id, "Release run id")
    attempt = None if expected_attempt is None else _positive(expected_attempt, "Release run attempt")
    if attempt is not None and job_intervals is None:
        raise PublicationPolicyError("Release artifact attempt binding requires exact job intervals")
    commit = None if expected_commit is None else _sha(expected_commit, "DobbyVPN commit")
    values = _artifact_pages(listing)
    selected: dict[str, dict[str, object]] = {}
    seen_ids: set[int] = set()
    for name in REQUIRED_RELEASE_ARTIFACTS:
        matches = [
            item for item in values
            if item.get("name") == name
            and (
                job_intervals is None
                or _created_in_intervals(item, job_intervals, "Release")
            )
        ]
        if len(matches) != 1:
            raise PublicationPolicyError(f"required Release artifact {name} is missing or ambiguous")
        record = _artifact_record(matches[0], run_id=run_id, commit=commit)
        artifact_id = int(record["id"])
        if artifact_id in seen_ids:
            raise PublicationPolicyError("Release artifact IDs are duplicated")
        seen_ids.add(artifact_id)
        record["name"] = name
        selected[name] = record
    return selected


def validate_qualification_artifacts(
    listing: Mapping[str, Any] | Sequence[Mapping[str, Any]],
    *,
    expected_run_id: int,
    expected_attempt: int | None = None,
    expected_commit: str | None = None,
    job_intervals: Mapping[str, Sequence[tuple[datetime.datetime, datetime.datetime]]] | None = None,
) -> dict[str, dict[str, dict[str, object]]]:
    """Require the exact result, deletion marker, and raw logs for each platform."""

    run_id = _positive(expected_run_id, "Torturer run id")
    attempt = None if expected_attempt is None else _positive(
        expected_attempt, "Torturer run attempt"
    )
    if attempt is not None and job_intervals is None:
        raise PublicationPolicyError(
            "Torturer artifact attempt binding requires exact job intervals"
        )
    commit = None if expected_commit is None else _sha(
        expected_commit, "Torturer commit"
    )
    values = _artifact_pages(listing)
    result: dict[str, dict[str, dict[str, object]]] = {
        platform: {} for platform in PLATFORMS
    }
    seen_ids: set[int] = set()
    for item in values:
        name = item.get("name")
        if not isinstance(name, str):
            continue
        match = _PUBLIC_EVIDENCE_ARTIFACT.fullmatch(name)
        kind = "public-functional-evidence"
        carries_run_identity = True
        if match is None:
            match = _PUBLIC_RAW_LOGS_ARTIFACT.fullmatch(name)
            kind = "functional-raw-logs"
        if match is None:
            match = _RENDER_COMPLETE_ARTIFACT.fullmatch(name)
            kind = "render-complete"
            carries_run_identity = False
        if match is None:
            continue
        platform = match.group("platform")
        if job_intervals is not None:
            platform_intervals = job_intervals.get(platform)
            if platform_intervals is None:
                raise PublicationPolicyError(
                    f"successful {platform} qualification job is missing"
                )
            if not _created_in_intervals(
                item, platform_intervals, f"{platform} qualification"
            ):
                continue
        if carries_run_identity and (
            int(match.group("run_id")) != run_id
            or (attempt is not None and int(match.group("run_attempt")) != attempt)
        ):
            raise PublicationPolicyError(
                f"{kind} artifact name is from another run attempt"
            )
        if kind in result[platform]:
            raise PublicationPolicyError(f"duplicate {kind} artifact for {platform}")
        record = _artifact_record(item, run_id=run_id, commit=commit)
        artifact_id = int(record["id"])
        if artifact_id in seen_ids:
            raise PublicationPolicyError("Torturer artifact IDs are duplicated")
        seen_ids.add(artifact_id)
        result[platform][kind] = record
    required = {
        "public-functional-evidence",
        "render-complete",
        "functional-raw-logs",
    }
    for platform in PLATFORMS:
        if set(result[platform]) != required:
            raise PublicationPolicyError(
                f"Torturer qualification artifacts incomplete for {platform}"
            )
    return result


def validate_publication_state(
    *,
    tag_commit: str | None,
    release: Mapping[str, Any] | None,
    expected_commit: str,
    expected_tag: str | None = None,
    expected_identity: Mapping[str, Any] | None = None,
) -> None:
    """Reject conflicting pre-existing publication state before any mutation."""

    expected = _sha(expected_commit, "DobbyVPN commit")
    if tag_commit is not None and _sha(tag_commit, "existing tag commit") != expected:
        raise PublicationPolicyError("existing release tag points to another commit")
    if release is not None:
        if expected_identity is None:
            raise PublicationPolicyError(
                "existing release requires an exact coordinator identity"
            )
        marker = publication_identity_marker(expected_identity)
        if release.get("body") != marker:
            raise PublicationPolicyError(
                "existing release is not owned by this exact qualification identity"
            )
        if expected_tag is not None and release.get("tag_name") != expected_tag:
            raise PublicationPolicyError("existing GitHub Release has the wrong tag")
        if release.get("prerelease") is not False:
            raise PublicationPolicyError("existing GitHub Release is a prerelease")
        # target_commitish is usually the branch name ("main"), not the
        # commit. The resolved Git ref above is the authoritative commit check.
        if tag_commit is None:
            raise PublicationPolicyError("existing GitHub Release has no matching tag")


def _read_json(path: Path) -> Any:
    def reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key: {key}")
            result[key] = value
        return result

    def reject_nonfinite(value: str) -> None:
        raise ValueError(f"non-finite JSON number: {value}")

    try:
        return json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=reject_duplicate_keys,
            parse_constant=reject_nonfinite,
        )
    except (OSError, ValueError) as error:
        raise PublicationPolicyError(f"cannot read JSON document {path}") from error


def _intervals_document(
    value: object, *, label: str
) -> list[tuple[datetime.datetime, datetime.datetime]]:
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes)):
        raise PublicationPolicyError(f"{label} has an unsafe shape")
    intervals: list[tuple[datetime.datetime, datetime.datetime]] = []
    for item in value:
        if not isinstance(item, Sequence) or isinstance(item, (str, bytes)) or len(item) != 2:
            raise PublicationPolicyError(f"{label} has an unsafe interval")
        started = _timestamp(item[0], f"{label} start")
        completed = _timestamp(item[1], f"{label} completion")
        if completed < started:
            raise PublicationPolicyError(f"{label} interval is invalid")
        intervals.append((started, completed))
    if not intervals:
        raise PublicationPolicyError(f"{label} is empty")
    return intervals


def _write_intervals(path: Path, intervals: object) -> None:
    if isinstance(intervals, Mapping):
        document = {
            platform: [
                [started.isoformat(), completed.isoformat()]
                for started, completed in values
            ]
            for platform, values in intervals.items()
        }
    else:
        document = [
            [started.isoformat(), completed.isoformat()]
            for started, completed in intervals  # type: ignore[union-attr]
        ]
    try:
        path.write_text(json.dumps(document, sort_keys=True) + "\n", encoding="utf-8")
    except (OSError, TypeError, ValueError) as error:
        raise PublicationPolicyError(f"cannot write interval document {path}") from error


def _write_selected(path: Path, selected: object) -> None:
    try:
        path.write_text(
            json.dumps(selected, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
    except (OSError, TypeError, ValueError) as error:
        raise PublicationPolicyError(f"cannot write selected artifact document {path}") from error


def _cli() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for command in ("release-run", "torturer-run"):
        item = sub.add_parser(command)
        item.add_argument("--run-json", type=Path, required=True)
        item.add_argument("--workflow-json", type=Path, required=True)
        item.add_argument("--run-id", type=int, required=True)
        item.add_argument("--run-attempt", type=int, required=True)
        item.add_argument("--commit", required=True)
    jobs = sub.add_parser("release-jobs")
    jobs.add_argument("--jobs-json", type=Path, required=True)
    jobs.add_argument("--run-attempt", type=int, required=True)
    jobs.add_argument("--intervals-json", type=Path)
    torturer_jobs = sub.add_parser("torturer-jobs")
    torturer_jobs.add_argument("--jobs-json", type=Path, required=True)
    torturer_jobs.add_argument("--run-attempt", type=int, required=True)
    torturer_jobs.add_argument("--intervals-json", type=Path)
    for command in ("release-artifacts", "qualification-artifacts"):
        item = sub.add_parser(command)
        item.add_argument("--listing-json", type=Path, required=True)
        item.add_argument("--run-id", type=int, required=True)
        item.add_argument("--run-attempt", type=int)
        item.add_argument("--commit")
        item.add_argument("--job-intervals-json", type=Path)
        item.add_argument("--selected-json", type=Path)
    identity = sub.add_parser("validate-identity")
    identity.add_argument("--identity-json", type=Path, required=True)
    state = sub.add_parser("publication-state")
    state.add_argument("--state-json", type=Path, required=True)
    state.add_argument("--commit", required=True)
    state.add_argument("--tag", required=True)
    state.add_argument("--identity-json", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "release-run":
            validate_release_run(
                _mapping(_read_json(args.run_json), "run"),
                _mapping(_read_json(args.workflow_json), "workflow"),
                expected_run_id=args.run_id,
                expected_attempt=args.run_attempt,
                expected_commit=args.commit,
            )
        elif args.command == "torturer-run":
            validate_torturer_run(
                _mapping(_read_json(args.run_json), "run"),
                _mapping(_read_json(args.workflow_json), "workflow"),
                expected_run_id=args.run_id,
                expected_attempt=args.run_attempt,
                expected_commit=args.commit,
            )
        elif args.command == "release-jobs":
            jobs_document = _read_json(args.jobs_json)
            intervals = validate_release_jobs_for_attempt(
                jobs_document, expected_attempt=args.run_attempt
            )
            validate_release_jobs(jobs_document)
            if args.intervals_json is not None:
                _write_intervals(args.intervals_json, intervals)
        elif args.command == "torturer-jobs":
            intervals = validate_torturer_jobs(
                _read_json(args.jobs_json), expected_attempt=args.run_attempt
            )
            if args.intervals_json is not None:
                _write_intervals(args.intervals_json, intervals)
        elif args.command == "release-artifacts":
            intervals = None
            if args.job_intervals_json is not None:
                intervals = _intervals_document(
                    _read_json(args.job_intervals_json), label="Release job intervals"
                )
            selected = validate_release_artifacts(
                _read_json(args.listing_json), expected_run_id=args.run_id,
                expected_attempt=args.run_attempt, expected_commit=args.commit,
                job_intervals=intervals,
            )
            if args.selected_json is not None:
                _write_selected(args.selected_json, selected)
        elif args.command == "qualification-artifacts":
            intervals = None
            if args.job_intervals_json is not None:
                raw_intervals = _mapping(
                    _read_json(args.job_intervals_json), "Torturer job intervals"
                )
                intervals = {
                    platform: _intervals_document(raw_intervals.get(platform), label=f"{platform} job intervals")
                    for platform in PLATFORMS
                }
            selected = validate_qualification_artifacts(
                _read_json(args.listing_json), expected_run_id=args.run_id,
                expected_attempt=args.run_attempt, expected_commit=args.commit,
                job_intervals=intervals,
            )
            if args.selected_json is not None:
                _write_selected(args.selected_json, selected)
        elif args.command == "validate-identity":
            validate_identity(_mapping(_read_json(args.identity_json), "identity"))
        elif args.command == "publication-state":
            state_document = _mapping(_read_json(args.state_json), "publication state")
            tag_commit = state_document.get("tag_commit")
            if tag_commit == "":
                tag_commit = None
            release = state_document.get("release")
            if release is not None:
                release = _mapping(release, "GitHub Release")
            identity = None
            if args.identity_json is not None:
                identity = _mapping(_read_json(args.identity_json), "identity")
            validate_publication_state(
                tag_commit=tag_commit, release=release,
                expected_commit=args.commit, expected_tag=args.tag,
                expected_identity=identity,
            )
    except PublicationPolicyError as error:
        print(f"publication policy: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(_cli())
