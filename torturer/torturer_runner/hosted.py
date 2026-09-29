#!/usr/bin/env python3
"""Run canonical scenarios against installed packages during hosted qualification."""

from __future__ import annotations

import argparse
import math
from pathlib import Path
import time

from torturer_contract.engine import FunctionalEngine
from torturer_contract.coverage import (
    coverage_contract,
    qualification_exit_code,
)
from torturer_contract.results import RunProvenance
from torturer_contract.scenarios import (
    select_scenarios,
    validate_suite,
)
from torturer_runner.diagnostics import add_exception_notes

from .adapters.cli import (
    SubprocessRunner,
    _ensure_directory,
)
from .adapters.factory import adapter_for_platform
from .lane import (
    _emit_progress_event,
    _execute_lane,
    _finalize_adapter_with_progress,
    _write_json,
)


_SHA40 = set("0123456789abcdef")
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
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    # Reject unsupported suite requests before creating scratch, constructing an
    # adapter, or doing any candidate setup.  In particular, Android full is
    # a physical-device extension and must not silently become mini.
    validate_suite(args.suite, platform=args.platform, entrypoint="hosted")
    selected_scenarios = select_scenarios(
        suite=args.suite,
        scenario_ids=args.scenario_ids,
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
        architecture = _HOSTED_ARCHITECTURE_BY_PLATFORM[args.platform]
        provenance = RunProvenance(
            platform=args.platform,
            platform_version=args.platform_version,
            architecture=architecture,
        )
        engine = FunctionalEngine()
        rendered_ui: dict[str, object] | None = None
        if args.platform == "android":
            # Run the production Compose Auto journey independently. Its
            # action identity never enters or shifts the real profile matrix.
            adapter = adapter_for_platform(
                args.platform,
                profile=args.profile,
                runner=runner,
                adb=args.adb,
                source_sha=source_sha,
                android_ui_mode="gui-auto",
            )
            set_progress_sink = getattr(adapter, "set_progress_sink", None)
            if callable(set_progress_sink):
                set_progress_sink(_emit_progress_event)
            finalization_attempted = True
            ui_connections, ui_results = _execute_lane(
                engine,
                selected_scenarios,
                adapter,
                provenance,
                deadline=lane_deadline,
            )
            if len(ui_connections) != 1 or ui_connections[0].protocol != "AUTO":
                raise ValueError("ANDROID_RENDERED_UI_INVENTORY_INVALID")
            rendered_ui = {
                "required": True,
                "connection": ui_connections[0].to_dict(),
                "scenarios": ui_results,
            }
            adapter = adapter_for_platform(
                args.platform,
                profile=args.profile,
                runner=runner,
                adb=args.adb,
                source_sha=source_sha,
                android_ui_mode="protocol-matrix",
            )
            finalization_attempted = False
        else:
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
            )
        set_progress_sink = getattr(adapter, "set_progress_sink", None)
        if callable(set_progress_sink):
            set_progress_sink(_emit_progress_event)
        # Keep every selected scenario in the engine run. Unsupported behavior
        # becomes an ordinary unavailable result, not an omitted test. On
        # Android this pass covers only the real Go binding inventory.
        finalization_attempted = True
        connections, results = _execute_lane(
            engine,
            selected_scenarios,
            adapter,
            provenance,
            deadline=lane_deadline,
        )
        coverage = coverage_contract(
            connections,
            selected_scenarios,
            results,
            suite=args.suite,
            explicit_scenario_selection=bool(args.scenario_ids),
        )
        if rendered_ui is not None:
            ui_results = rendered_ui["scenarios"]
            assert isinstance(ui_results, list)
            ui_passed = bool(ui_results) and all(
                isinstance(item, dict) and item.get("outcome") == "passed"
                for item in ui_results
            )
            rendered_ui["passed"] = ui_passed
            coverage["rendered_ui_required"] = True
            coverage["rendered_ui_passed"] = ui_passed
            coverage["rendered_ui_scenario_count"] = len(ui_results)
            if not ui_passed:
                coverage["status"] = "coverage-contract-failed"
                coverage["complete"] = False
                coverage["passed"] = False
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
        if rendered_ui is not None:
            document["rendered_ui"] = rendered_ui
        document["coverage"] = coverage
        _write_json(args.output, document)
        reported_results = results + (
            rendered_ui["scenarios"] if rendered_ui is not None else []
        )
        failed = [item for item in reported_results if item.get("outcome") == "failed"]
        unavailable = [
            item for item in reported_results if item.get("outcome") == "unavailable"
        ]
        print(
            f"hosted-functional platform={args.platform} scenarios={len(reported_results)} "
            f"failed={len(failed)} unavailable={len(unavailable)} "
            f"coverage={coverage['status']}"
        )
        return qualification_exit_code(coverage)
    except BaseException as error:
        if adapter is not None and not finalization_attempted:
            finalization_attempted = True
            try:
                _finalize_adapter_with_progress(adapter, lane_deadline)
            except BaseException as finalization_error:
                add_exception_notes(error, "adapter_finalization", finalization_error)
        raise


if __name__ == "__main__":
    raise SystemExit(main())
