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

from torturer_contract.engine import FunctionalEngine
from torturer_contract.coverage import (
    coverage_contract,
    qualification_exit_code,
)
from torturer_contract.results import (
    RunProvenance,
)
from torturer_contract.scenarios import select_scenarios, validate_suite

from .adapters.cli import (
    AdapterError,
    SubprocessRunner,
    _ensure_directory,
)
from .adapters.factory import adapter_for_platform
from .hosted import (
    _execute_lane,
    _emit_progress_event,
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
    parser.add_argument("--adb", type=Path, help="ADB executable for Android")
    parser.add_argument(
        "--profile", dest="profile", type=Path,
        required=True, help="Plaintext synthetic VPN test profile",
    )
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--raw-log-dir", type=Path, required=True,
        help="Disposable scratch directory for the functional run",
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
    parser.add_argument("--service-pipe")
    parser.add_argument("--service-library-path", type=Path)
    parser.add_argument("--service-pid-file", type=Path)
    parser.add_argument("--service-identity-file", type=Path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=Path)
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


def _prepare_output_path(path: Path) -> None:
    """Prepare the result directory."""

    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except OSError as error:
        raise ValueError("RESULT_DIRECTORY_UNAVAILABLE") from error


def _local_exit_code(results: list[dict[str, object]]) -> int:
    """Focused local diagnostics pass when every returned result passed."""

    return 0 if results and all(
        isinstance(result, dict) and result.get("outcome") == "passed"
        for result in results
    ) else 2


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
    # Validate before touching scratch, candidate setup, or the adapter.
    validate_suite(args.suite, platform=args.platform, entrypoint="local")
    lane_deadline = time.monotonic() + args.lane_timeout_seconds
    local_architecture = args.architecture or (
        "x86_64" if args.platform == "macos" and host_platform.machine().lower() in {"x86_64", "amd64"}
        else "arm64" if args.platform == "macos" and host_platform.machine().lower() in {"aarch64", "arm64"}
        else _ARCHITECTURES[args.platform]
    )
    selected = select_scenarios(
        suite=args.suite,
        scenario_ids=args.scenario_ids,
    )
    if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", local_architecture) is None:
        raise ValueError("architecture has an invalid format")
    raw_dir = args.raw_log_dir
    supervised_root = _supervised_request_root()
    if supervised_root is not None:
        try:
            raw_dir.resolve().relative_to(supervised_root.resolve())
        except ValueError as error:
            raise ValueError("RAW_LOG_DIRECTORY_OUTSIDE_REQUEST") from error
    _ensure_directory(raw_dir)
    _prepare_output_path(args.output)
    if args.source_sha is not None and _SHA40.fullmatch(args.source_sha) is None:
        raise ValueError("source SHA must be a full lowercase SHA")
    cli = args.cli
    # Keep adapter-owned rendered artifacts (including hosted UI screenshots)
    # in the retained raw-log tree.  The supervised-root check above keeps
    # this path inside the disposable request even when the caller supplies
    # command-line paths.
    runner = SubprocessRunner(raw_dir)
    adb = args.adb or (Path(shutil.which("adb")) if shutil.which("adb") else None)
    if args.platform == "linux" and args.routing_firewall_helper is None:
        raise AdapterError("ROUTING_FIREWALL_HELPER_UNAVAILABLE")
    adapter = adapter_for_platform(
        args.platform,
        cli=cli,
        adb=adb,
        profile=args.profile,
        runner=runner,
        source_sha=args.source_sha,
        local_mode=True,
        service_pid=args.service_pid,
        service_binary=args.service_binary,
        service_socket=args.service_socket,
        service_pipe=args.service_pipe,
        service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
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
    coverage = coverage_contract(
        connections,
        selected,
        results,
        suite=args.suite,
        explicit_scenario_selection=bool(args.scenario_ids),
    )
    document["coverage"] = coverage
    # Preserve the existing field for consumers while making its semantics
    # explicit: focused diagnostics can pass but never claim qualification.
    document["complete_test_set"] = coverage["selection_complete"]
    _write_json(args.output, document)
    if args.scenario_ids:
        return _local_exit_code(results)
    return qualification_exit_code(coverage)


if __name__ == "__main__":
    raise SystemExit(main())
