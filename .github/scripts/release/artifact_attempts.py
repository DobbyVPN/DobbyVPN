"""Resolve immutable GitHub Actions artifact names across workflow attempts."""

from __future__ import annotations

import re
from typing import Any, Iterable


ATTEMPT_SUFFIX_RE = re.compile(r"(?P<logical>.+)-attempt-(?P<attempt>[0-9]+)\Z")


class ArtifactAttemptError(ValueError):
    """Artifact names or attempts cannot be resolved unambiguously."""


def parse_artifact_name(name: object) -> tuple[str, int]:
    if not isinstance(name, str) or not name:
        raise ArtifactAttemptError("artifact name is invalid")
    if "-attempt-" not in name:
        return name, 1
    match = ATTEMPT_SUFFIX_RE.fullmatch(name)
    if match is None:
        raise ArtifactAttemptError(f"malformed artifact attempt suffix: {name}")
    attempt_text = match.group("attempt")
    attempt = int(attempt_text)
    if attempt < 2 or str(attempt) != attempt_text:
        raise ArtifactAttemptError(f"malformed artifact attempt suffix: {name}")
    return match.group("logical"), attempt


def select_artifacts(
    artifacts: Iterable[dict[str, Any]],
    current_attempt: int,
    *,
    required_names: Iterable[str] | None = None,
) -> dict[str, dict[str, Any]]:
    """Select each logical artifact's newest unexpired record up to this attempt."""
    if type(current_attempt) is not int or current_attempt < 1:
        raise ArtifactAttemptError("current workflow attempt is invalid")
    required = set(required_names) if required_names is not None else None
    candidates: dict[str, dict[int, list[dict[str, Any]]]] = {}
    for artifact in artifacts:
        if not isinstance(artifact, dict):
            raise ArtifactAttemptError("artifact record is invalid")
        name = artifact.get("name")
        if not isinstance(name, str):
            raise ArtifactAttemptError("artifact name is invalid")
        try:
            logical_name, attempt = parse_artifact_name(name)
        except ArtifactAttemptError:
            if required is None or any(name.startswith(base + "-attempt-") for base in required):
                raise
            continue
        if required is not None and logical_name not in required:
            continue
        if type(artifact.get("expired")) is not bool:
            raise ArtifactAttemptError(f"artifact {name!r} has invalid expiration metadata")
        if attempt > current_attempt:
            raise ArtifactAttemptError(
                f"artifact {name!r} is from a future attempt (current: {current_attempt})"
            )
        candidates.setdefault(logical_name, {}).setdefault(attempt, []).append(artifact)

    selected: dict[str, dict[str, Any]] = {}
    for name, attempts in candidates.items():
        latest = max(attempts)
        records = attempts[latest]
        if len(records) != 1:
            raise ArtifactAttemptError(
                f"artifact {name!r} is ambiguous at attempt {latest}"
            )
        if records[0]["expired"]:
            raise ArtifactAttemptError(f"artifact {name!r} is expired at attempt {latest}")
        selected[name] = records[0]
    if required is not None:
        missing = sorted(required - selected.keys())
        if missing:
            name = missing[0]
            raise ArtifactAttemptError(f"artifact {name!r} is missing")
    return selected
