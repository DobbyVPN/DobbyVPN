"""Compose independent native desktop cases with real Go observations."""
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
import sys
import time
import traceback
from typing import Any

from torturer_runner.native_cases import (
    AUTO_RECOVERY_STOP_CASE, CONNECTED_SESSION_CASE, CONNECTED_LAYOUT_CASE, DESKTOP_LOG_CASES,
    WINDOWS_PRESENTATION_CASES, MACOS_PRESENTATION_CASES,
    WINDOWS_LOG_SEVERITY_CASE, DESKTOP_PERFORMANCE_CASE, validate_native_cases,
)
from ..adapters.cli import SubprocessRunner, _ensure_directory
from ..adapters.factory import adapter_for_platform


class NativeUIJourneyError(RuntimeError):
    """A native desktop case or its shared observation setup failed."""


def _exception_details(error: BaseException) -> str:
    return "".join(traceback.format_exception(error)).rstrip()


def run_journey(args: argparse.Namespace) -> dict[str, object]:
    """The full suite invokes the same independently selectable cases once."""
    one = argparse.Namespace(**vars(args))
    presentation = (WINDOWS_PRESENTATION_CASES if args.platform == "windows"
                    else MACOS_PRESENTATION_CASES)
    one.native_cases = [*DESKTOP_LOG_CASES, *presentation]
    if args.platform == "windows":
        one.native_cases.append(WINDOWS_LOG_SEVERITY_CASE)
    one.native_cases.extend((CONNECTED_LAYOUT_CASE, CONNECTED_SESSION_CASE))
    one.repeat = 1
    result = run_native_cases(one)
    result["coverage"]["native_case_selection"] = "default"
    return result


def _base_adapter(args: argparse.Namespace, directory: Path) -> Any:
    return adapter_for_platform(
        args.platform, cli=args.cli, profile=args.profile,
        runner=SubprocessRunner(directory), local_mode=True,
        service_pid=args.service_pid, service_binary=args.service_binary,
        service_socket=Path(args.service_socket) if args.service_socket else None,
        service_pipe=args.service_pipe, service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
    )


def _run_case(args: argparse.Namespace, case: str, attempt: int, deadline: float,
              record: dict[str, Any]) -> None:
    from .native_case import run_desktop_case
    from .observations import for_base_adapter
    from ..subscription_fixture import SubscriptionFixture

    presentation = (WINDOWS_PRESENTATION_CASES if args.platform == "windows"
                    else MACOS_PRESENTATION_CASES)
    observed = case in {CONNECTED_SESSION_CASE, CONNECTED_LAYOUT_CASE, AUTO_RECOVERY_STOP_CASE}
    needs_fixture = (observed or case in (*presentation, DESKTOP_PERFORMANCE_CASE)) and case not in {
        "about", "text-size-eligibility",
    }
    directory = args.raw_log_dir / "cases" / f"{case}-{attempt}"
    subscription = None
    base = None
    primary = None
    backend_cleanup_safe = True
    native_started = False
    setup_started = time.monotonic()
    context = {}
    source_sha = getattr(args, "source_sha", None)
    if source_sha:
        context["source_sha"] = source_sha
    try:
        if needs_fixture:
            subscription = SubscriptionFixture(
                args.profile, args.raw_log_dir / "fixtures" / f"{case}-{attempt}",
                args.platform, certificate_helper=args.ui_helper,
            )
            context["subscription_url"] = subscription.start()
            if case in {"profiles-layout", "text-size-layout"}:
                subscription.replace_response(b"\n\n".join(
                    (b'[[Outline]]\nDescription = "' +
                     (b"" if index == 0 else f"Layout profile {index + 1}".encode()) +
                     b'"\nServer = "127.0.0.1"\nPort = 9\nPassword = "never-connect-layout-' +
                     str(index + 1).encode() + b'"') for index in range(24)
                ))
            context.update(subscription_profile_path=str(args.profile),
                           subscription_failure_url=subscription.failure_url,
                           subscription_control_url=subscription.control_url,
                           subscription_control_key=subscription.control_key)
        if observed:
            base = _base_adapter(args, args.raw_log_dir / "observations" / f"{case}-{attempt}")
            context["auto_recovery_marker"] = str(args.raw_log_dir / "recovery-stop.arm")
        record["fixture_setup_seconds"] = time.monotonic() - setup_started
        reserve = 75 if case == "text-size-layout" else 75 if observed else 15
        limit = 600 if observed else 240 if case in {"text-size-layout", DESKTOP_PERFORMANCE_CASE} else 120
        timeout = min(float(limit), deadline - time.monotonic() - reserve)
        if timeout <= 0:
            raise TimeoutError("invocation deadline exhausted during native case setup")
        keyword = {"context": context} if context else {}
        if base is not None:
            keyword["observer"] = for_base_adapter(base)
        native_started = True
        record.update(run_desktop_case(args.ui_helper, args.ui, case, directory, timeout, **keyword))
    except BaseException as error:
        primary = error
        backend_cleanup_safe = getattr(error, "observer_quiescent", True) is True
        if not backend_cleanup_safe:
            error.add_note("backend cleanup unconfirmed because the native observation reader is still active")
            record["backend_cleanup_confirmed"] = False
            record.setdefault("cleanup_errors", []).append(
                "backend cleanup unconfirmed: native observation reader remained active"
            )
        traceback.print_exception(error, file=sys.stderr)
        record.update(outcome="failed", error=_exception_details(error), failure_category="unknown")
        if native_started:
            record.setdefault("cleanup_errors", []).append(
                "native case did not provide a valid result; app cleanup is unconfirmed"
            )
    finally:
        if base is not None and backend_cleanup_safe:
            for label, callback in (("base-reset", base.reset), ("base-finalize", base.finalize)):
                try:
                    callback(timeout_seconds=min(30.0, max(1.0, deadline - time.monotonic())))
                except BaseException as cleanup_error:
                    traceback.print_exception(cleanup_error, file=sys.stderr)
                    record.setdefault("cleanup_errors", []).append(
                        f"{label}: {_exception_details(cleanup_error)}"
                    )
                    record["outcome"] = "failed"
                    if primary is not None:
                        primary.add_note(f"{label}: {_exception_details(cleanup_error)}")
        if subscription is not None:
            try:
                subscription.close()
            except BaseException as cleanup_error:
                traceback.print_exception(cleanup_error, file=sys.stderr)
                record.setdefault("cleanup_errors", []).append(_exception_details(cleanup_error))
                record["outcome"] = "failed"


def run_native_cases(args: argparse.Namespace) -> dict[str, object]:
    """Keep every predetermined attempt, including failures and skipped cases."""
    selected = validate_native_cases(args.platform, "full", args.native_cases)
    repeat = getattr(args, "repeat", 1)
    if repeat < 1:
        raise NativeUIJourneyError("repeat must be positive")
    _ensure_directory(args.raw_log_dir)
    deadline = time.monotonic() + args.timeout
    results = []
    unsafe_cleanup = False
    for attempt in range(1, repeat + 1):
        for case in selected:
            record = {"case_id": case, "attempt": attempt}
            if unsafe_cleanup or deadline - time.monotonic() <= 15:
                record.update(outcome="not-run", reason="preceding cleanup failed" if unsafe_cleanup
                              else "invocation deadline exhausted")
            else:
                _run_case(args, case, attempt, deadline, record)
                unsafe_cleanup = bool(record.get("cleanup_errors"))
            results.append(record)
    return {
        "platform": args.platform, "native_cases": list(selected), "repeat": repeat,
        "coverage": {"platform": args.platform, "suite": "full", "kind": "native-cases",
                     "native_cases": list(selected),
                     "native_case_selection": "explicit" if args.native_cases is not None else "default"},
        "cases": results,
        "complete": all(result["outcome"] == "passed" for result in results),
    }


def _timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a finite number") from error
    if result <= 0 or not math.isfinite(result):
        raise argparse.ArgumentTypeError("timeout must be positive and finite")
    return result


def _path(value: str) -> Path:
    result = Path(value)
    if not result.is_absolute():
        raise argparse.ArgumentTypeError("path must be absolute")
    return result


def _ui_path_is_launchable(platform: str, path: Path) -> bool:
    if path.is_file():
        return True
    if platform != "macos" or path.suffix != ".app":
        return False
    return (path / "Contents" / "MacOS" / "DobbyVPNMacApp").is_file()


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--cli", type=_path, required=True)
    parser.add_argument("--ui", type=_path, required=True)
    parser.add_argument("--ui-helper", type=_path, required=True)
    parser.add_argument("--profile", type=_path, required=True)
    parser.add_argument("--raw-log-dir", type=_path, required=True)
    parser.add_argument("--output", type=_path)
    parser.add_argument("--timeout", type=_timeout, default=900.0)
    parser.add_argument("--candidate-version")
    parser.add_argument("--source-sha")
    parser.add_argument("--native-case", action="append", dest="native_cases")
    parser.add_argument("--repeat", type=int, default=1)
    parser.add_argument("--service-pid", type=int, required=True)
    parser.add_argument("--service-binary", type=_path, required=True)
    parser.add_argument("--service-socket", type=str)
    parser.add_argument("--service-pipe")
    parser.add_argument("--service-library-path", type=_path)
    parser.add_argument("--service-pid-file", type=_path)
    parser.add_argument("--service-identity-file", type=_path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=_path)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.native_cases is not None:
        try:
            validate_native_cases(args.platform, "full", args.native_cases)
        except ValueError as error:
            parser.error(str(error))
    if (args.platform == "windows" and (args.service_pipe != "DobbyVPN.Control" or args.service_socket)) or (
        args.platform == "macos" and (not args.service_socket or args.service_pipe)
    ):
        parser.error("native desktop cases require the platform's local control endpoint")
    if (not args.cli.is_file() or not _ui_path_is_launchable(args.platform, args.ui)
            or not args.profile.is_file() or not args.ui_helper.is_file()):
        parser.error("native desktop cases require a CLI, launchable UI, helper, and profile")
    result = {"suite": "native-case" if args.native_cases is not None else "full",
              "action_driver": "native-window", "platform": args.platform}
    try:
        result.update(run_native_cases(args) if args.native_cases is not None else run_journey(args))
    except BaseException as error:
        traceback.print_exception(error, file=sys.stderr)
        result.update(complete=False, error=_exception_details(error))
    if args.output is not None:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps(result, sort_keys=True, separators=(",", ":")), flush=True)
    return 0 if result.get("complete") is True else 1


if __name__ == "__main__":
    raise SystemExit(main())
