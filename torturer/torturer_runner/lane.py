"""Shared scenario execution and result reporting for local and hosted runs."""

from __future__ import annotations

from datetime import datetime, timezone
import json
from pathlib import Path
import time

from torturer_contract.results import ConnectionIdentity
from torturer_runner.diagnostics import add_exception_notes

_RESET_TIMEOUT_SECONDS = 5
_ANDROID_RESET_TIMEOUT_SECONDS = 15
_FINALIZE_TIMEOUT_SECONDS = 30


def _lane_remaining(deadline: float | None) -> float | None:
    if deadline is None:
        return None
    return max(0.0, deadline - time.monotonic())


def _discover_connections(
    adapter,
    *,
    deadline: float | None,
) -> tuple[ConnectionIdentity, ...]:
    """Discover and validate the complete DobbyVPN-reported inventory."""

    discover = getattr(adapter, "discover_connections", None)
    select = getattr(adapter, "select_connection", None)
    if not callable(discover) or not callable(select):
        raise ValueError("CONNECTION_DISCOVERY_UNAVAILABLE")
    remaining = _lane_remaining(deadline)
    if remaining is not None and remaining <= 0:
        raise ValueError("HOSTED_LANE_DEADLINE_EXCEEDED before connection discovery")
    timeout = 30.0 if remaining is None else min(30.0, remaining)
    connections = tuple(discover(timeout_seconds=timeout))
    if (
        not connections
        or any(not isinstance(value, ConnectionIdentity) for value in connections)
        or len(connections) != len(set(connections))
        or [value.index for value in connections] != list(range(len(connections)))
    ):
        raise ValueError("CONNECTION_INVENTORY_INVALID")
    return connections


def _emit_progress_event(event: str, fields: dict[str, object]) -> None:
    print(
        json.dumps(
            {
                "kind": "dobbyvpn.functional.progress",
                "event": event,
                "timestamp_utc": datetime.now(timezone.utc)
                .isoformat(timespec="milliseconds")
                .replace("+00:00", "Z"),
                **fields,
            },
            sort_keys=True,
            allow_nan=False,
        ),
        flush=True,
    )


def _finalize_adapter(adapter, deadline: float | None) -> None:
    """Release adapter-owned resources before the hosted process may exit."""

    finalize = getattr(adapter, "finalize", None)
    if not callable(finalize):
        raise ValueError("ADAPTER_FINALIZER_UNAVAILABLE")
    remaining = _lane_remaining(deadline)
    if remaining is not None:
        if remaining <= 0:
            raise ValueError("HOSTED_LANE_DEADLINE_EXCEEDED before adapter finalization")
        timeout_seconds = min(float(_FINALIZE_TIMEOUT_SECONDS), remaining)
    else:
        timeout_seconds = float(_FINALIZE_TIMEOUT_SECONDS)
    finalize(timeout_seconds=timeout_seconds, deadline=deadline)


def _finalize_adapter_with_progress(adapter, deadline: float | None) -> None:
    started = time.monotonic()
    _emit_progress_event(
        "finalization-start",
        {"timeout_seconds": _FINALIZE_TIMEOUT_SECONDS},
    )
    try:
        _finalize_adapter(adapter, deadline)
    except BaseException as error:
        _emit_progress_event(
            "finalization-finish",
            {
                "duration_seconds": time.monotonic() - started,
                "error": f"{type(error).__name__}: {error}",
                "finalized": False,
            },
        )
        raise
    _emit_progress_event(
        "finalization-finish",
        {
            "duration_seconds": time.monotonic() - started,
            "finalized": True,
        },
    )


def _run_scenarios(
    engine,
    scenarios,
    adapter,
    provenance,
    connection: ConnectionIdentity,
    *,
    deadline: float | None = None,
) -> list[dict[str, object]]:
    results: list[dict[str, object]] = []
    reset_timeout_seconds = (
        _ANDROID_RESET_TIMEOUT_SECONDS
        if getattr(provenance, "platform", None) == "android"
        else _RESET_TIMEOUT_SECONDS
    )
    set_progress_sink = getattr(adapter, "set_progress_sink", None)
    if callable(set_progress_sink):
        set_progress_sink(_emit_progress_event)
    for scenario in scenarios:
        missing = scenario.required_capabilities - adapter.capabilities
        started = time.monotonic()
        _emit_progress_event(
            "scenario-start",
            {
                "connection_index": connection.index,
                "missing_capabilities": len(missing),
                "protocol": connection.protocol,
                "scenario": scenario.id,
            },
        )
        reset_called = False

        def cleanup_scenario() -> None:
            nonlocal reset_called
            if reset_called:
                return
            reset_called = True
            cleanup_started = time.monotonic()
            _emit_progress_event(
                "scenario-cleanup-start",
                {
                    "connection_index": connection.index,
                    "protocol": connection.protocol,
                    "scenario": scenario.id,
                },
            )
            reset_error: BaseException | None = None
            try:
                reset_timeout = _lane_remaining(deadline)
                if reset_timeout is not None:
                    if reset_timeout <= 0:
                        raise ValueError(
                            "HOSTED_LANE_DEADLINE_EXCEEDED before scenario reset"
                        )
                    reset_timeout = min(float(reset_timeout_seconds), reset_timeout)
                else:
                    reset_timeout = float(reset_timeout_seconds)
                adapter.reset(timeout_seconds=reset_timeout)
            except BaseException as error:
                reset_error = error
            _emit_progress_event(
                "scenario-cleanup-finish",
                {
                    "connection_index": connection.index,
                    "error": (
                        f"{type(reset_error).__name__}: {reset_error}"
                        if reset_error is not None else None
                    ),
                    "duration_seconds": time.monotonic() - cleanup_started,
                    "protocol": connection.protocol,
                    "reset": reset_error is None,
                    "scenario": scenario.id,
                },
            )
            if reset_error is not None:
                raise reset_error

        try:
            result = engine.run(
                scenario,
                adapter,
                provenance,
                connection,
                cleanup_provider=cleanup_scenario,
            )
        except BaseException as primary_error:
            # FunctionalEngine invokes the provider on ordinary result paths,
            # but adapter execution errors are deliberately propagated before
            # it can build a result.  Make the same reset guarantee hold for
            # those paths without replacing the useful primary exception.
            try:
                cleanup_scenario()
            except BaseException as cleanup_error:
                add_exception_notes(primary_error, "scenario_cleanup", cleanup_error)
            _emit_progress_event(
                "scenario-finish",
                {
                    "connection_index": connection.index,
                    "duration_seconds": time.monotonic() - started,
                    "error": f"{type(primary_error).__name__}: {primary_error}",
                    "outcome": "error",
                    "protocol": connection.protocol,
                    "scenario": scenario.id,
                },
            )
            raise
        payload = result.to_dict()
        results.append(payload)
        assertions = payload.get("assertions")
        if isinstance(assertions, list):
            for assertion in assertions:
                if not isinstance(assertion, dict):
                    continue
                assertion_id = assertion.get("id")
                passed = assertion.get("passed")
                if isinstance(assertion_id, str) and isinstance(passed, bool):
                    _emit_progress_event(
                        "assertion-result",
                        {
                            "assertion": assertion_id,
                            "connection_index": connection.index,
                            "passed": passed,
                            "protocol": connection.protocol,
                            "scenario": scenario.id,
                        },
                    )
        remaining = _lane_remaining(deadline)
        if remaining is not None and remaining <= 0:
            raise ValueError(
                f"HOSTED_LANE_DEADLINE_EXCEEDED after scenario {scenario.id} cleanup"
            )
        outcome = payload.get("outcome")
        if not isinstance(outcome, str):
            raise ValueError(f"scenario {scenario.id} result outcome is invalid")
        _emit_progress_event(
            "scenario-finish",
            {
                "connection_index": connection.index,
                "duration_seconds": time.monotonic() - started,
                "outcome": outcome,
                "protocol": connection.protocol,
                "scenario": scenario.id,
            },
        )
    return results


def _run_connection_matrix(
    engine,
    scenarios,
    adapter,
    provenance,
    connections: tuple[ConnectionIdentity, ...],
    *,
    deadline: float | None = None,
) -> list[dict[str, object]]:
    """Run the selected test set once for every dynamically discovered connection."""

    results: list[dict[str, object]] = []
    for connection in connections:
        adapter.select_connection(connection)
        connection_results = _run_scenarios(
            engine,
            scenarios,
            adapter,
            provenance,
            connection,
            deadline=deadline,
        )
        results.extend(connection_results)
    return results


def _execute_lane(
    engine,
    scenarios,
    adapter,
    provenance,
    *,
    deadline: float | None,
    reset_before_discovery: bool = False,
) -> tuple[tuple[ConnectionIdentity, ...], list[dict[str, object]]]:
    """Discover, run, and finalize one adapter lane on every outcome."""

    finalization_attempted = False
    try:
        if reset_before_discovery:
            reset_started = time.monotonic()
            _emit_progress_event("adapter-reset-start", {"platform": provenance.platform})
            try:
                adapter.reset()
            except BaseException as error:
                _emit_progress_event(
                    "adapter-reset-finish",
                    {
                        "duration_seconds": time.monotonic() - reset_started,
                        "error_type": type(error).__name__,
                        "platform": provenance.platform,
                        "reset": False,
                    },
                )
                raise
            _emit_progress_event(
                "adapter-reset-finish",
                {
                    "duration_seconds": time.monotonic() - reset_started,
                    "platform": provenance.platform,
                    "reset": True,
                },
            )
        discovery_started = time.monotonic()
        _emit_progress_event(
            "connection-discovery-start",
            {"platform": provenance.platform},
        )
        try:
            connections = _discover_connections(adapter, deadline=deadline)
        except BaseException as error:
            _emit_progress_event(
                "connection-discovery-finish",
                {
                    "duration_seconds": time.monotonic() - discovery_started,
                    "error_type": type(error).__name__,
                    "platform": provenance.platform,
                },
            )
            raise
        _emit_progress_event(
            "connection-discovery-finish",
            {
                "connection_count": len(connections),
                "duration_seconds": time.monotonic() - discovery_started,
                "platform": provenance.platform,
            },
        )
        results = _run_connection_matrix(
            engine,
            scenarios,
            adapter,
            provenance,
            connections,
            deadline=deadline,
        )
        finalization_attempted = True
        _finalize_adapter_with_progress(adapter, deadline)
        return connections, results
    except BaseException as error:
        if not finalization_attempted:
            finalization_attempted = True
            try:
                _finalize_adapter_with_progress(adapter, deadline)
            except BaseException as finalization_error:
                add_exception_notes(error, "adapter_finalization", finalization_error)
        raise


def _write_json(
    path: Path,
    payload: dict[str, object],
) -> None:
    """Write the current result, replacing a previous attempt at this path."""

    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
