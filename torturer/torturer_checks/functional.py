"""Run the canonical Torturer lane against a local prepared installed candidate.

It deliberately calls the same scenario engine, adapter factory, result
validator, and command runner used by the hosted lane; platform setup supplies
only the installed candidate paths and required inputs.
"""

from __future__ import annotations

import argparse
import json
import os
import platform as host_platform
from pathlib import Path
import re
import shutil
import time

from torturer_contract.functional.engine import FunctionalEngine
from torturer_contract.functional.results import (
    RunProvenance,
)
from torturer_contract.functional.scenarios import (
    suite_set,
    validate_suite,
)

from .hosted.cli import (
    HostedAdapterError,
    SubprocessRunner,
    _ensure_directory,
)
from .hosted.factory import adapter_for_platform
from .hosted.run import (
    _execute_lane,
    _emit_progress_event,
    _select_scenarios,
    _write_json,
)
_SHA40 = re.compile(r"^[0-9a-f]{40}$")
_ARCHITECTURES = {
    "linux": "amd64",
    "windows": "amd64",
    "macos": "arm64",
    "android": "x86_64",
}
def _parse_timeout(value: str) -> float:
    try:
        timeout = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("lane timeout must be a number") from error
    if not 0 < timeout < float("inf"):
        raise argparse.ArgumentTypeError("lane timeout must be a positive finite number")
    return timeout


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=tuple(_ARCHITECTURES), required=True)
    parser.add_argument("--cli", type=Path, help="Installed candidate CLI for desktop platforms")
    parser.add_argument(
        "--ui-test", type=Path,
        help="Headless production Fyne UI companion for Windows/macOS UI lanes",
    )
    parser.add_argument("--adb", type=Path, help="ADB executable for Android")
    parser.add_argument(
        "--profile", dest="profile", type=Path,
        required=True, help="Plaintext synthetic VPN test profile",
    )
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--raw-log-dir", type=Path, required=True,
        help="Directory for command output and diagnostics",
    )
    parser.add_argument("--platform-version", default="local")
    parser.add_argument(
        "--architecture",
        help="Observed local guest architecture (defaults to the host architecture for macOS)",
    )
    parser.add_argument(
        "--source-sha", default=None,
        help="Optional candidate source SHA; dirty local trees do not require one",
    )
    parser.add_argument("--lane-timeout-seconds", type=_parse_timeout, default=1800.0)
    parser.add_argument(
        "--suite",
        choices=("mini", "full"),
        default="mini",
        help="Functional suite; full is orchestrated by local_vm only",
    )
    parser.add_argument("--scenario", action="append", dest="scenario_ids")
    parser.add_argument("--service-pid", type=int)
    parser.add_argument("--service-binary", type=Path)
    parser.add_argument("--service-socket", type=Path)
    parser.add_argument("--service-library-path", type=Path)
    parser.add_argument("--service-pid-file", type=Path)
    parser.add_argument("--service-identity-file", type=Path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=Path)
    parser.add_argument("--network-transition-helper", type=Path)
    return parser


def _supervised_request_root() -> Path | None:
    if os.environ.get("DOBBYVPN_SUPERVISED_REQUEST") != "1":
        return None
    value = os.environ.get("DOBBYVPN_REQUEST_ROOT")
    if not value:
        raise ValueError("SUPERVISED_REQUEST_ROOT_UNAVAILABLE")
    root = Path(value)
    if not root.is_dir():
        raise ValueError("SUPERVISED_REQUEST_ROOT_UNAVAILABLE")
    return root


def _create_log(path: Path) -> None:
    """Create or reopen one VPN log."""

    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.touch(exist_ok=True)
    except OSError as error:
        raise ValueError("LOG_UNAVAILABLE") from error


def _prepare_output_path(path: Path) -> None:
    """Prepare the result directory."""

    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except OSError as error:
        raise ValueError("RESULT_DIRECTORY_UNAVAILABLE") from error


def _local_exit_code(results: list[dict[str, object]]) -> int:
    """A run passes only when it returned at least one passing scenario."""

    return 0 if results and all(result["outcome"] == "passed" for result in results) else 2


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    # The direct functional runner owns only the shared semantic lane.  A
    # desktop full run is a local_vm orchestration: it runs this lane as mini
    # and then adds the real native-window journey.  Rejecting full here keeps
    # a direct invocation from claiming complete coverage while still letting
    # local_vm pass --suite full to its own orchestration entrypoint.
    if args.suite == "full":
        raise ValueError(
            "FULL_SUITE_REQUIRES_LOCAL_VM_ORCHESTRATOR: "
            "run local_vm --suite full for the Windows/macOS native-window lane"
        )
    # Validate before touching logs, candidate setup, or the adapter.
    validate_suite(args.suite, platform=args.platform, entrypoint="local")
    lane_deadline = time.monotonic() + args.lane_timeout_seconds
    local_architecture = args.architecture or (
        "x86_64" if args.platform == "macos" and host_platform.machine().lower() in {"x86_64", "amd64"}
        else "arm64" if args.platform == "macos" and host_platform.machine().lower() in {"aarch64", "arm64"}
        else _ARCHITECTURES[args.platform]
    )
    selected = _select_scenarios(
        args.scenario_ids,
        platform=args.platform,
        suite=args.suite,
    )
    if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", local_architecture) is None:
        raise ValueError("architecture has an invalid format")
    raw_dir = args.raw_log_dir
    supervised_root = _supervised_request_root()
    _ensure_directory(raw_dir)
    app_log = None if args.platform == "android" else raw_dir / "app.log"
    service_log = raw_dir / "service.log" if args.platform == "linux" else None
    for path in (app_log, service_log):
        if path is None:
            continue
        _create_log(path)
    _prepare_output_path(args.output)
    if args.source_sha is not None and _SHA40.fullmatch(args.source_sha) is None:
        raise ValueError("source SHA must be a full lowercase SHA")
    cli = args.cli
    runner = SubprocessRunner(
        supervised_root / "output" if supervised_root is not None else raw_dir,
        environment=(
            {"DOBBY_CLI_LOG_PATH": str(app_log)}
            if args.platform != "android" and cli is not None
            else None
        ),
    )
    adb = args.adb or (Path(shutil.which("adb")) if shutil.which("adb") else None)
    if args.platform == "linux" and args.routing_firewall_helper is None:
        raise HostedAdapterError("ROUTING_FIREWALL_HELPER_UNAVAILABLE")
    adapter = adapter_for_platform(
        args.platform,
        cli=cli,
        ui_test=args.ui_test,
        adb=adb,
        profile=args.profile,
        runner=runner,
        source_sha=args.source_sha,
        local_mode=True,
        service_pid=args.service_pid,
        service_binary=args.service_binary,
        service_socket=args.service_socket,
        service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        service_log=service_log,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
        network_transition_helper=args.network_transition_helper,
    )
    set_progress_sink = getattr(adapter, "set_progress_sink", None)
    if callable(set_progress_sink):
        set_progress_sink(_emit_progress_event)
    provenance = RunProvenance(
        platform=args.platform,
        platform_version=args.platform_version,
        architecture=local_architecture,
    )
    engine = FunctionalEngine()
    connections, results = _execute_lane(
        engine,
        selected,
        adapter,
        provenance,
        deadline=lane_deadline,
        reset_before_discovery=bool(args.scenario_ids),
    )
    document = {
        "environment": {
            "platform": args.platform,
            "platform_version": args.platform_version,
            "architecture": local_architecture,
            "suite": args.suite,
        },
        "connections": [connection.to_dict() for connection in connections],
        "scenarios": results,
    }
    suite_ids = {scenario.id for scenario in suite_set(args.suite)}
    selected_ids = {scenario.id for scenario in selected}
    complete_selection = not args.scenario_ids and selected_ids == suite_ids
    passed_results = bool(results) and all(
        result.get("outcome") == "passed" for result in results
    )
    document["coverage"] = {
        "suite": args.suite,
        "complete": complete_selection and passed_results,
        "selection_complete": complete_selection,
        "passed": passed_results,
        "selected_scenario_ids": sorted(selected_ids),
        "required_scenario_ids": sorted(suite_ids),
        "selected_scenario_count": len(selected_ids),
        "expected_scenario_count": len(suite_ids),
        "explicit_scenario_selection": bool(args.scenario_ids),
    }
    # Preserve the existing field for consumers while making its semantics
    # explicit: focused diagnostics can pass but never claim qualification.
    document["complete_test_set"] = complete_selection
    _write_json(args.output, document)
    return _local_exit_code(results)


if __name__ == "__main__":
    raise SystemExit(main())
