"""Small semantic scenario engine shared by hosted and local adapters."""

from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
import math
import time
from typing import Protocol

from .assertions import evaluate_assertions
from .capabilities import Capability
from .results import ConnectionIdentity, RunProvenance, ScenarioResult
from .scenarios import ScenarioDefinition


class CapabilityUnavailable(Exception):
    """Raised by an adapter when a required environment feature is absent."""


class ScenarioExecutionError(Exception):
    """Raised by an adapter for a stable, expected execution failure."""

    def __init__(self, reason_code: str) -> None:
        super().__init__(reason_code)
        self.reason_code = reason_code


class ScenarioAdapter(Protocol):
    """Semantic operations implemented by one hosted or local adapter."""

    @property
    def capabilities(self) -> frozenset[Capability]: ...

    def execute_scenario(self, scenario: ScenarioDefinition) -> Mapping[str, object]: ...


CleanupProvider = Callable[[], object]


def capability_unavailable_reason(
    missing: frozenset[Capability], reasons: object,
) -> str:
    """Return one stable adapter-owned reason for missing capabilities."""

    if isinstance(reasons, Mapping):
        for capability in sorted(missing, key=lambda value: value.value):
            reason = reasons.get(capability)
            if isinstance(reason, str) and reason:
                return reason
    return "CAPABILITY_UNAVAILABLE"


@dataclass(frozen=True)
class FunctionalEngine:
    """Execute a definition without owning platform commands or result identity."""

    def run(
        self,
        scenario: ScenarioDefinition,
        adapter: ScenarioAdapter,
        provenance: RunProvenance,
        connection: ConnectionIdentity,
        *,
        cleanup_provider: CleanupProvider | None = None,
    ) -> ScenarioResult:
        started_ns = time.monotonic_ns()
        missing = scenario.required_capabilities - adapter.capabilities
        if missing:
            return self._result(
                scenario,
                provenance,
                connection,
                outcome="unavailable",
                reason_code=capability_unavailable_reason(
                    missing,
                    getattr(adapter, "capability_unavailable_reasons", {}),
                ),
                assertions=(),
                cleanup={"required": False, "verified": True},
                measurements={},
                started_ns=started_ns,
                cleanup_provider=cleanup_provider,
            )

        observations: dict[str, object] = {}
        try:
            if self._expired(started_ns, scenario.max_duration_seconds):
                raise ScenarioExecutionError("SCENARIO_TIMEOUT")
            result = adapter.execute_scenario(scenario)
            if not isinstance(result, Mapping):
                raise TypeError("scenario adapter returned a non-mapping result")
            observations.update(result)
            if self._expired(started_ns, scenario.max_duration_seconds):
                raise ScenarioExecutionError("SCENARIO_TIMEOUT")
        except CapabilityUnavailable:
            return self._result(
                scenario,
                provenance,
                connection,
                outcome="unavailable",
                reason_code=capability_unavailable_reason(
                    scenario.required_capabilities - adapter.capabilities,
                    getattr(adapter, "capability_unavailable_reasons", {}),
                ),
                assertions=(),
                cleanup={"required": False, "verified": True},
                measurements={},
                started_ns=started_ns,
                cleanup_provider=cleanup_provider,
            )
        assertions = evaluate_assertions(scenario.assertion_ids, observations)
        cleanup_required = "cleanup.restored" in scenario.assertion_ids
        cleanup_verified = observations.get("cleanup_verified") is True
        metrics = self._metrics(observations)
        if not all(assertion.passed for assertion in assertions):
            outcome = "failed"
            reason_code = "ASSERTION_FAILED"
        else:
            outcome = "passed"
            reason_code = None
        return self._result(
            scenario,
            provenance,
            connection,
            outcome=outcome,
            reason_code=reason_code,
            assertions=assertions,
            cleanup={"required": cleanup_required, "verified": cleanup_verified},
            measurements=metrics,
            started_ns=started_ns,
            cleanup_provider=cleanup_provider,
        )

    @staticmethod
    def _duration_ms(started_ns: int, ended_ns: int) -> int:
        return max(0, (ended_ns - started_ns) // 1_000_000)

    @staticmethod
    def _expired(started_ns: int, max_duration_seconds: int) -> bool:
        return time.monotonic_ns() - started_ns > max_duration_seconds * 1_000_000_000

    def _result(
        self,
        scenario: ScenarioDefinition,
        provenance: RunProvenance,
        connection: ConnectionIdentity,
        *,
        outcome: str,
        reason_code: str | None,
        assertions,
        cleanup: Mapping[str, bool],
        measurements: Mapping[str, float | int],
        started_ns: int,
        cleanup_provider: CleanupProvider | None,
    ) -> ScenarioResult:
        if cleanup_provider is not None:
            # Cleanup is an adapter-owned operation. Its private return value
            # is intentionally not part of the ordinary functional result.
            cleanup_provider()
        final_ended_ns = time.monotonic_ns()
        if final_ended_ns - started_ns > scenario.max_duration_seconds * 1_000_000_000:
            raise ScenarioExecutionError("SCENARIO_TIMEOUT")
        return ScenarioResult(
            scenario_id=scenario.id,
            provenance=provenance,
            connection=connection,
            outcome=outcome,
            failure_code=reason_code,
            assertions=assertions,
            cleanup=cleanup,
            measurements=measurements,
            elapsed_ms=self._duration_ms(started_ns, final_ended_ns),
        )

    @staticmethod
    def _metrics(observations: Mapping[str, object]) -> dict[str, float | int]:
        result: dict[str, float | int] = {}
        for key in (
            "latency_ms",
            "download_mbps",
            "upload_mbps",
            "stability_sample_count",
            "stability_sample_interval_seconds",
        ):
            value = observations.get(key)
            if (
                isinstance(value, (int, float))
                and not isinstance(value, bool)
                and math.isfinite(float(value))
                and value > 0
            ):
                # Results keep only positive measurements. A failed
                # assertion still records its stable outcome and failure code;
                # invalid telemetry must not prevent that result from being
                # serialized.
                result[key] = value
        return result
