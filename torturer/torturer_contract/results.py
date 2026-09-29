"""Runtime metadata and readable per-scenario results."""

from __future__ import annotations

from dataclasses import dataclass
import re
from typing import Mapping

from .assertions import AssertionOutcome


_IDENTIFIER = re.compile(r"^[a-z][a-z0-9._-]*$")
_VERSION = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")
_PROTOCOL = re.compile(r"^[A-Z][A-Z0-9_]*$")


@dataclass(frozen=True)
class RunProvenance:
    platform: str
    platform_version: str
    architecture: str

    def __post_init__(self) -> None:
        if not _IDENTIFIER.fullmatch(self.platform):
            raise ValueError("platform has an invalid format")
        if not _VERSION.fullmatch(self.platform_version):
            raise ValueError("platform_version has an invalid format")
        if not _VERSION.fullmatch(self.architecture):
            raise ValueError("architecture has an invalid format")

    def to_dict(self) -> dict[str, str]:
        return {
            "platform": self.platform,
            "platform_version": self.platform_version,
            "architecture": self.architecture,
        }


@dataclass(frozen=True, order=True)
class ConnectionIdentity:
    index: int
    protocol: str

    def __post_init__(self) -> None:
        if not isinstance(self.index, int) or isinstance(self.index, bool) or self.index < 0:
            raise ValueError("connection.index must be a non-negative integer")
        if not isinstance(self.protocol, str) or not _PROTOCOL.fullmatch(self.protocol):
            raise ValueError("connection.protocol has an invalid format")

    def to_dict(self) -> dict[str, object]:
        return {"index": self.index, "protocol": self.protocol}


@dataclass(frozen=True)
class ScenarioResult:
    scenario_id: str
    provenance: RunProvenance
    connection: ConnectionIdentity
    outcome: str
    assertions: tuple[AssertionOutcome, ...]
    cleanup: Mapping[str, bool]
    measurements: Mapping[str, float | int]
    elapsed_ms: int
    failure_code: str | None = None

    def to_dict(self) -> dict[str, object]:
        result: dict[str, object] = {
            "environment": self.provenance.to_dict(),
            "connection": self.connection.to_dict(),
            "scenario": {"id": self.scenario_id},
            "outcome": self.outcome,
            "assertions": [assertion.to_dict() for assertion in self.assertions],
            "measurements": dict(self.measurements),
            "timing": {"elapsed_ms": self.elapsed_ms},
            "cleanup": dict(self.cleanup),
        }
        if self.failure_code is not None:
            result["failure"] = {"code": self.failure_code}
        return result
