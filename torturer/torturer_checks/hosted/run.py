#!/usr/bin/env python3
"""Run the canonical scenarios through one trusted hosted CLI adapter."""

from __future__ import annotations

import argparse
import math
import json
import os
from pathlib import Path
import re
import time

from torturer_contract.functional.engine import FunctionalEngine
from torturer_contract.functional.results import (
    ConnectionIdentity,
    RunProvenance,
)
from torturer_contract.functional.scenarios import (
    select_scenarios,
    suite_set,
    validate_suite,
)

from .cli import (
    HostedAdapterError,
    SubprocessRunner,
    _ensure_directory,
)
from .factory import adapter_for_platform


ROOT = Path(__file__).resolve().parents[2]
_SHA40 = set("0123456789abcdef")
_RESET_TIMEOUT_SECONDS = 5
_ANDROID_RESET_TIMEOUT_SECONDS = 15
_FINALIZE_TIMEOUT_SECONDS = 30
_HOSTED_ARCHITECTURE_BY_PLATFORM = {
    "linux": "amd64",
    "windows": "amd64",
    "macos": "arm64",
    "android": "x86_64",
}
def _full_sha(value: str, name: str) -> str:
    if len(value) != 40 or any(ch not in _SHA40 for ch in value):
        raise ValueError(f"{name} must be a full lowercase SHA")
    return value


def _parse_lane_timeout(value: str) -> float:
    """Parse the workflow's remaining canonical-lane budget strictly."""

    try:
        timeout = float(value)
    except (TypeError, ValueError) as error:
        raise argparse.ArgumentTypeError("lane timeout must be a finite number") from error
    if not math.isfinite(timeout) or timeout <= 0:
        raise argparse.ArgumentTypeError("lane timeout must be finite and greater than zero")
    return timeout


def _select_scenarios(
    scenario_ids: list[str] | None,
    *,
    platform: str | None = None,
    suite: str = "mini",
) -> tuple:
    # ``platform`` remains a caller-side preflight check.  Resolution itself
    # is shared with the local entrypoint so a diagnostic selection can still
    # name a deferred scenario; only a complete suite can qualify.
    scenarios = select_scenarios(suite=suite, scenario_ids=scenario_ids)
    if len({scenario.id for scenario in scenarios}) != len(scenarios):
        raise ValueError("scenario-id values must be unique")
    if platform is not None and platform not in _HOSTED_ARCHITECTURE_BY_PLATFORM:
        raise ValueError("unknown hosted platform")
    return scenarios


def _coverage_contract(
    platform: str,
    connections: tuple[ConnectionIdentity, ...],
    selected_scenarios,
    results: list[dict[str, object]],
    *,
    suite: str = "mini",
    explicit_scenario_selection: bool = False,
) -> dict[str, object]:
    """Summarize results; only a complete suite can qualify.

    Unavailable results are never accepted by the hosted coverage contract.
    A focused ``--scenario`` invocation remains useful for diagnostics, but
    is explicitly incomplete even if it names every scenario by hand.
    """

    suite_scenarios = suite_set(suite)
    test_ids = {scenario.id for scenario in suite_scenarios}
    selected_ids = {scenario.id for scenario in selected_scenarios}
    actual_unavailable = {
        (item["scenario"]["id"], item["failure"]["code"])
        for item in results
        if item["outcome"] == "unavailable"
    }
    matrix_complete = (
        selected_ids == test_ids
        and not explicit_scenario_selection
        and len(results) == len(connections) * len(selected_scenarios)
    )
    accepted = (
        not actual_unavailable
        and all(item["outcome"] == "passed" for item in results)
        and bool(connections)
    )
    valid = matrix_complete and accepted
    complete = valid
    status = "complete" if complete else "coverage-contract-failed"
    return {
        "suite": suite,
        "status": status,
        "complete": complete,
        "test_set_scenario_count": len(test_ids),
        "required_scenario_ids": sorted(test_ids),
        "connection_count": len(connections),
        "expected_result_count": len(connections) * len(selected_scenarios),
        "selected_scenario_count": len(selected_ids),
        "selected_scenario_ids": sorted(selected_ids),
        "explicit_scenario_selection": explicit_scenario_selection,
        "result_count": len(results),
        "actual_unavailable": [
            {"scenario_id": scenario_id, "reason_code": reason}
            for scenario_id, reason in sorted(actual_unavailable)
        ],
    }


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
            {"kind": "dobbyvpn.functional.progress", "event": event, **fields},
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
            _emit_progress_event(
                "scenario-cleanup-start",
                {
                    "connection_index": connection.index,
                    "protocol": connection.protocol,
                    "scenario": scenario.id,
                },
            )
            reset_timeout = _lane_remaining(deadline)
            if reset_timeout is not None:
                if reset_timeout <= 0:
                    raise ValueError(
                        "HOSTED_LANE_DEADLINE_EXCEEDED before scenario reset"
                    )
                reset_timeout = min(float(reset_timeout_seconds), reset_timeout)
            else:
                reset_timeout = float(reset_timeout_seconds)
            reset_error: Exception | None = None
            try:
                adapter.reset(timeout_seconds=reset_timeout)
            except Exception as error:
                reset_error = error
            _emit_progress_event(
                "scenario-cleanup-finish",
                {
                    "connection_index": connection.index,
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
                primary_error.add_note(
                    f"scenario_cleanup_error={type(cleanup_error).__name__}"
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
            adapter.reset()
        _emit_progress_event(
            "connection-discovery-start",
            {"platform": provenance.platform},
        )
        connections = _discover_connections(adapter, deadline=deadline)
        _emit_progress_event(
            "connection-discovery-finish",
            {"connection_count": len(connections), "platform": provenance.platform},
        )
        results = _run_connection_matrix(
            engine,
            scenarios,
            adapter,
            provenance,
            connections,
            deadline=deadline,
        )
        finalization_started = time.monotonic()
        _emit_progress_event(
            "finalization-start",
            {"timeout_seconds": _FINALIZE_TIMEOUT_SECONDS},
        )
        finalization_attempted = True
        _finalize_adapter(adapter, deadline)
        _emit_progress_event(
            "finalization-finish",
            {"duration_seconds": time.monotonic() - finalization_started},
        )
        return connections, results
    except Exception as error:
        if not finalization_attempted:
            finalization_attempted = True
            try:
                _finalize_adapter(adapter, deadline)
            except Exception as finalization_error:
                error.add_note(
                    f"adapter_finalization_error={type(finalization_error).__name__}"
                )
        raise


def _qualification_exit_code(coverage: dict[str, object]) -> int:
    """Fail qualification unless the reviewed platform coverage contract matches."""
    return 0 if coverage.get("status") == "complete" else 2


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


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("linux", "windows", "macos", "android"), required=True)
    parser.add_argument("--cli", type=Path)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--source-sha", default=None, help="Optional checkout identity checked before hosted execution")
    parser.add_argument(
        "--platform-version",
        required=True,
        help="Observed target OS/emulator version; never inferred as unknown",
    )
    parser.add_argument(
        "--lane-timeout-seconds",
        type=_parse_lane_timeout,
        required=True,
        help="Workflow-provided remaining canonical lane budget",
    )
    parser.add_argument(
        "--suite",
        choices=("mini", "full"),
        default="mini",
        help="Qualification suite; hosted runs currently support mini only",
    )
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--raw-log-dir", type=Path)
    parser.add_argument("--adb", type=Path)
    parser.add_argument("--scenario", action="append", dest="scenario_ids", help="Run one canonical scenario; repeat to select a diagnostic subset.")
    parser.add_argument("--service-pid", type=int)
    parser.add_argument("--service-binary", type=Path)
    parser.add_argument("--service-socket", type=Path)
    parser.add_argument("--service-pipe")
    parser.add_argument("--service-library-path", type=Path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=Path)
    parser.add_argument("--network-transition-helper", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    # Reject unsupported suite requests before creating scratch, constructing an
    # adapter, or doing any candidate setup.  In particular, Android full is
    # a physical-device extension and must not silently become mini.
    validate_suite(args.suite, platform=args.platform, entrypoint="hosted")
    selected_scenarios = _select_scenarios(
        args.scenario_ids,
        platform=args.platform,
        suite=args.suite,
    )
    adapter = None
    # The workflow's remaining budget is also the outer deadline's budget.
    # Start one clock before preflight so adapter reset and finalization stay
    # inside the budget left by the workflow.
    lane_deadline: float | None = time.monotonic() + args.lane_timeout_seconds
    finalization_attempted = False
    try:
        source_sha = (
            _full_sha(args.source_sha, "source SHA")
            if args.source_sha is not None
            else None
        )
        raw_dir = args.raw_log_dir or args.output.parent / "hosted-scratch"
        _ensure_directory(raw_dir)
        runner = SubprocessRunner(raw_dir)
        adapter = adapter_for_platform(
            args.platform,
            cli=args.cli,
            profile=args.profile,
            runner=runner,
            adb=args.adb,
            source_sha=source_sha,
            service_pid=args.service_pid,
            service_binary=args.service_binary,
            service_socket=args.service_socket,
            service_pipe=args.service_pipe,
            service_library_path=args.service_library_path,
            network_interface=args.network_interface,
            routing_firewall_helper=args.routing_firewall_helper,
            network_transition_helper=args.network_transition_helper,
        )
        set_progress_sink = getattr(adapter, "set_progress_sink", None)
        if callable(set_progress_sink):
            set_progress_sink(_emit_progress_event)
        architecture = _HOSTED_ARCHITECTURE_BY_PLATFORM[args.platform]
        provenance = RunProvenance(
            platform=args.platform,
            platform_version=args.platform_version,
            architecture=architecture,
        )
        engine = FunctionalEngine()
        # Keep every selected scenario in the engine run. Unsupported behavior
        # becomes an ordinary unavailable result, not an omitted test.
        finalization_attempted = True
        connections, results = _execute_lane(
            engine,
            selected_scenarios,
            adapter,
            provenance,
            deadline=lane_deadline,
        )
        coverage = _coverage_contract(
            args.platform,
            connections,
            selected_scenarios,
            results,
            suite=args.suite,
            explicit_scenario_selection=bool(args.scenario_ids),
        )
        document = {
            "environment": {
                "platform": args.platform,
                "platform_version": args.platform_version,
                "architecture": architecture,
                "suite": args.suite,
            },
            "connections": [connection.to_dict() for connection in connections],
            "scenarios": results,
        }
        document["coverage"] = coverage
        _write_json(args.output, document)
        failed = [item for item in results if item.get("outcome") == "failed"]
        unavailable = [
            item for item in results if item.get("outcome") == "unavailable"
        ]
        print(
            f"hosted-functional platform={args.platform} scenarios={len(results)} "
            f"failed={len(failed)} unavailable={len(unavailable)} "
            f"coverage={coverage['status']}"
        )
        return _qualification_exit_code(coverage)
    except Exception as error:
        if adapter is not None and not finalization_attempted:
            finalization_attempted = True
            try:
                _finalize_adapter(adapter, lane_deadline)
            except Exception as finalization_error:
                error.add_note(
                    f"adapter_finalization_error={type(finalization_error).__name__}"
                )
        raise


if __name__ == "__main__":
    raise SystemExit(main())
