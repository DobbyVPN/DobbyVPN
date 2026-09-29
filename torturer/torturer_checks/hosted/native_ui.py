"""Interleave real desktop-window actions with the existing platform adapter.

The ordinary functional lane remains the canonical headless mini suite.  This
module is the additional local desktop-full lane: it owns only native user
actions and asks the platform adapter for independent tunnel, routing, traffic,
process-loss, and cleanup evidence.  It is intentionally a command-line
entrypoint so Windows can run the complete journey through the existing
interactive scheduled-task boundary.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import math
import os
from pathlib import Path
import sys
from typing import Any

from torturer_contract.functional.scenarios import ScenarioStep

from .cli import SubprocessRunner, _ensure_directory
from .factory import adapter_for_platform


# Keep each native action below the hosted journey's step deadline, leaving
# time for screenshots and exact UI-process cleanup.
_REQUEST_TIMEOUT = 300.0
_SMOKE_DIAGNOSTIC_RESERVE_SECONDS = 30.0


class NativeUIJourneyError(RuntimeError):
    """A native UI or base-adapter journey failure.

    ``operation`` and ``stage`` identify the failed step within a bounded
    timeout.
    """

    def __init__(
        self,
        message: str,
        *,
        operation: str | None = None,
        stage: str | None = None,
    ) -> None:
        super().__init__(message)
        self.operation = operation
        self.stage = stage


def _exception_details(error: BaseException) -> str:
    details = [f"{type(error).__name__}: {error}"]
    details.extend(
        f"note: {note}"
        for note in getattr(error, "__notes__", ())
    )
    return "\n".join(details)


_REQUIRED_TRUE_CHECKS = frozenset({
    "configure_native",
    "connect_native",
    "tunnel_interface",
    "routing_verified",
    "stability_verified",
    "throughput_positive",
    "disconnect_native",
    "disconnect_clean",
    "reconnect_native",
    "reconnect_completed",
    "settings_version",
    "settings_source_commit",
    "close_window",
    "reopen_connected",
    "process_loss_verified",
    "ui_process_loss_recovered",
    "reconnect_tunnel_interface",
    "reconnect_routing_verified",
    "reconnect_stability_verified",
    "reconnect_throughput_positive",
    "process_loss_tunnel_interface",
    "process_loss_routing_verified",
    "process_loss_stability_verified",
    "process_loss_throughput_positive",
    "final_disconnect_native",
    "final_cleanup_verified",
})


def _require_complete_checks(checks: dict[str, object]) -> None:
    """Fail closed when an evidence-producing step returned false/missing data."""
    failed = sorted(key for key in _REQUIRED_TRUE_CHECKS if checks.get(key) is not True)
    if failed:
        raise NativeUIJourneyError(
            "required native UI checks did not pass: " + ", ".join(failed)
        )


def _timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a finite number") from error
    if result <= 0 or result == float("inf"):
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


def _smoke_timeout(response_timeout: float) -> float:
    """Keep native UI work below its hosted step deadline for cleanup."""

    if response_timeout <= 0:
        raise ValueError("native UI response timeout must be positive")
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, response_timeout / 3.0)
    return min(_REQUEST_TIMEOUT, response_timeout - reserve)


def _load_native_ui_smoke(script: Path) -> Any:
    spec = importlib.util.spec_from_file_location("dobbyvpn_native_ui_smoke_runtime", script)
    if spec is None or spec.loader is None:
        raise NativeUIJourneyError(f"could not load native UI driver {script}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    try:
        spec.loader.exec_module(module)
    except BaseException:
        sys.modules.pop(spec.name, None)
        raise
    return module


def _native_ui_action(
    controller: Any,
    operation: str,
    stage: str,
    timeout: float,
    action: Any,
    *,
    milestone: str | None = None,
) -> dict[str, object]:
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, timeout / 3.0)
    work_timeout = timeout - reserve
    try:
        with controller.bounded_by(work_timeout):
            result = action()
            if not isinstance(result, dict):
                raise NativeUIJourneyError(f"native UI {operation} returned an invalid result")
    except Exception as error:
        failure = NativeUIJourneyError(
            f"{type(error).__name__}: {error}",
            operation=operation,
            stage=stage,
        )
        for note in getattr(error, "__notes__", ()):
            failure.add_note(note)
        try:
            with controller.bounded_by(reserve):
                screenshot = controller.capture(f"failure-{operation}")
            failure.add_note(
                "native-ui-failure-screenshot: "
                + json.dumps(screenshot, sort_keys=True, separators=(",", ":"))
            )
        except Exception as screenshot_error:
            failure.add_note(
                "native-ui-failure-screenshot: "
                f"{type(screenshot_error).__name__}: {screenshot_error}"
            )
        raise failure from error
    if milestone is not None:
        try:
            with controller.bounded_by(reserve):
                screenshot = controller.capture(milestone)
        except Exception as screenshot_error:
            result = {**result, "screenshot_error": str(screenshot_error)}
        else:
            if screenshot is not None:
                result = {**result, "screenshot": screenshot}
    return result


def _start_native_ui(controller: Any, timeout: float) -> None:
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, timeout / 3.0)
    work_timeout = timeout - reserve
    try:
        with controller.bounded_by(work_timeout):
            controller.start()
    except BaseException as error:
        with controller.bounded_by(reserve):
            try:
                screenshot = controller.capture("failure-start")
                error.add_note(
                    "native-ui-failure-screenshot: "
                    + json.dumps(screenshot, sort_keys=True, separators=(",", ":"))
                )
            except BaseException as screenshot_error:
                error.add_note(
                    "native-ui-failure-screenshot: "
                    f"{type(screenshot_error).__name__}: {screenshot_error}"
                )
            try:
                controller.close_for_cleanup()
            except BaseException as cleanup_error:
                error.add_note(
                    f"native-ui-cleanup: {type(cleanup_error).__name__}: {cleanup_error}"
                )
        failure = NativeUIJourneyError(
            f"{type(error).__name__}: {error}",
            operation="start",
            stage="window-discovery",
        )
        for note in getattr(error, "__notes__", ()):
            failure.add_note(note)
        raise failure from error


def _step(identifier: str, operation: str, timeout: float) -> ScenarioStep:
    # ScenarioStep's public contract uses integer operation bounds.  The
    # native lane's remaining budget is bounded separately by this process.
    return ScenarioStep(id=identifier, operation=operation, timeout_seconds=max(1, int(timeout)))


def _base_execute(base: Any, identifier: str, operation: str, timeout: float) -> dict[str, object]:
    try:
        value = base.execute(_step(identifier, operation, timeout))
    except Exception as error:
        raise NativeUIJourneyError(f"base adapter {operation} failed: {error}") from error
    if not isinstance(value, dict):
        raise NativeUIJourneyError(f"base adapter {operation} returned an invalid result")
    return value


def _restart_service_for_native_ui(base: Any, timeout: float) -> dict[str, object]:
    method = getattr(base, "restart_service_for_native_ui", None)
    if not callable(method):
        raise NativeUIJourneyError(
            "base adapter has no service-only process-loss preparation"
        )
    try:
        value = method(timeout)
    except Exception as error:
        raise NativeUIJourneyError(
            f"base adapter service-only process loss failed: {error}"
        ) from error
    if not isinstance(value, dict):
        raise NativeUIJourneyError(
            "base adapter service-only process loss returned an invalid result"
        )
    return value


def _positive_throughput(observations: dict[str, object]) -> bool:
    """Derive the shared throughput assertion from measured values."""
    values = tuple(observations.get(name) for name in (
        "latency_ms", "download_mbps", "upload_mbps"
    ))
    if all(isinstance(value, (int, float)) and not isinstance(value, bool) for value in values):
        return all(math.isfinite(float(value)) and float(value) > 0 for value in values)
    return False


def _record_native_observations(
    base: Any,
    checks: dict[str, object],
    timeout: float,
    *,
    prefix: str = "",
) -> None:
    """Run one independent tunnel/routing/stability/throughput observation set."""
    def key(name: str) -> str:
        return name if not prefix else f"{prefix}_{name}"

    tunnel = _base_execute(base, f"{prefix or 'native'}-tunnel", "observe_tunnel", timeout)
    checks[key("tunnel_interface")] = tunnel.get("tunnel_interface") is True
    routing = _base_execute(base, f"{prefix or 'native'}-routing", "observe_routing_identity", timeout)
    checks[key("routing_verified")] = routing.get("routing_verified") is True
    stability = _base_execute(base, f"{prefix or 'native'}-stability", "measure_stability", timeout)
    checks[key("stability_verified")] = stability.get("stability_verified") is True
    if "stability_sample_count" in stability:
        checks[key("stability_sample_count")] = stability["stability_sample_count"]
    throughput = _base_execute(base, f"{prefix or 'native'}-throughput", "measure_throughput", timeout)
    for name in ("latency_ms", "download_mbps", "upload_mbps"):
        if name in throughput:
            checks[key(name)] = throughput[name]
    checks[key("throughput_positive")] = _positive_throughput(throughput)


def run_journey(args: argparse.Namespace) -> dict[str, object]:
    _ensure_directory(args.raw_log_dir)
    runner = SubprocessRunner(
        args.raw_log_dir,
    )
    base = adapter_for_platform(
        args.platform,
        cli=args.cli,
        profile=args.profile,
        runner=runner,
        local_mode=True,
        service_pid=args.service_pid,
        service_binary=args.service_binary,
        service_socket=Path(args.service_socket) if args.service_socket else None,
        service_pipe=args.service_pipe,
        service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
    )
    previous_log_directory = os.environ.get("DOBBYVPN_NATIVE_UI_LOG_DIR")
    os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = str(args.raw_log_dir)
    ui: Any | None = None
    request_timeout = min(args.timeout, _REQUEST_TIMEOUT)
    checks: dict[str, object] = {}
    primary: BaseException | None = None
    try:
        smoke = _load_native_ui_smoke(args.smoke_script)
        smoke.verify_interactive_session(args.platform)
        ui = smoke.NativeUIController(
            args.platform,
            args.ui,
            args.profile,
            _smoke_timeout(request_timeout),
            screenshot_dir=args.raw_log_dir / "screenshots",
        )
        _start_native_ui(ui, request_timeout)
        _native_ui_action(
            ui, "configure", "native-input", request_timeout,
            ui.configure, milestone="configured",
        )
        accepted = base._snapshot(min(30.0, args.timeout), "NATIVE_CONFIGURE_STATUS_FAILED")
        if accepted.get("configured") is not True or not accepted.get("profiles"):
            raise NativeUIJourneyError("native UI did not establish an accepted configuration")
        checks["configure_native"] = True

        # The base adapter prepares its independent observation/proof state;
        # it never substitutes a CLI connect action for the native Connect
        # click.  Routing-enabled desktop adapters use this seam to install
        # their temporary probe firewall before the UI action.
        prepare = getattr(base, "prepare_native_connect", None)
        if not callable(prepare):
            raise NativeUIJourneyError("base adapter has no native connect preparation")
        prepare(args.timeout)
        _native_ui_action(
            ui, "connect", "visible-connect", request_timeout,
            ui.connect, milestone="connected",
        )
        active = base._snapshot(min(30.0, args.timeout), "NATIVE_CONNECT_STATUS_FAILED")
        if active.get("active_profile") is None:
            raise NativeUIJourneyError("native UI Auto selection reported no active profile")
        checks["connect_native"] = True
        _record_native_observations(base, checks, args.timeout)

        _native_ui_action(
            ui, "disconnect", "visible-disconnect", request_timeout,
            ui.disconnect, milestone="disconnected",
        )
        checks["disconnect_native"] = True
        connected = getattr(base, "_connected", None)
        if not callable(connected) or connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after native Disconnect")
        cleanup_verified = getattr(base, "_cleanup_verified", None)
        if not callable(cleanup_verified) or not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("base adapter could not verify native Disconnect cleanup")
        checks["disconnect_clean"] = True

        prepare(args.timeout)
        _native_ui_action(
            ui, "reconnect", "visible-reconnect", request_timeout,
            ui.connect, milestone="reconnected",
        )
        checks["reconnect_native"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe native reconnect")
        checks["reconnect_completed"] = True
        _record_native_observations(base, checks, args.timeout, prefix="reconnect")

        settings = _native_ui_action(
            ui, "settings", "settings-window", request_timeout,
            ui.settings, milestone="settings",
        )
        checks["settings_version"] = settings.get("settings_version") is True
        checks["settings_source_commit"] = settings.get("settings_source_commit") is True

        _native_ui_action(
            ui, "close-window", "close-window", request_timeout,
            ui.close, milestone="closed",
        )
        checks["close_window"] = True
        _native_ui_action(
            ui, "reopen", "window-reopen", request_timeout,
            ui.reopen, milestone="reopened",
        )
        checks["reopen_connected"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe service continuity after UI reopen")

        loss = _restart_service_for_native_ui(base, args.timeout)
        checks.update(loss)
        prepare(args.timeout)
        recovery = _native_ui_action(
            ui, "process_loss_recovery", "process-loss-recovery", args.timeout,
            ui.recover_after_process_loss, milestone="process-recovered",
        )
        checks["ui_process_loss_recovered"] = recovery.get("status") == "Connected"
        checks["ui_reconnecting_observed"] = recovery.get("reconnecting_seen") is True
        if "reconnecting_probe_error" in recovery:
            checks["ui_reconnecting_probe_error"] = recovery["reconnecting_probe_error"]
        if not connected(args.timeout):
            raise NativeUIJourneyError(
                "base adapter did not observe native UI process-loss recovery"
            )
        _record_native_observations(base, checks, args.timeout, prefix="process_loss")

        _native_ui_action(
            ui, "disconnect", "visible-disconnect", request_timeout,
            ui.disconnect, milestone="disconnected",
        )
        checks["final_disconnect_native"] = True
        if connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after final native Disconnect")
        if not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("final native Disconnect cleanup was not verified")
        checks["final_cleanup_verified"] = True
        _require_complete_checks(checks)
    except BaseException as error:
        primary = error
        error.native_ui_checks = dict(checks)
        raise
    finally:
        cleanup_errors: list[str] = []
        if ui is not None:
            try:
                with ui.bounded_by(min(request_timeout, 15.0)):
                    ui.close_for_cleanup()
            except BaseException as error:
                cleanup_errors.append(f"native-ui: {_exception_details(error)}")
        try:
            base.reset(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-reset: {_exception_details(error)}")
        try:
            base.finalize(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-finalize: {_exception_details(error)}")
        if previous_log_directory is None:
            os.environ.pop("DOBBYVPN_NATIVE_UI_LOG_DIR", None)
        else:
            os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = previous_log_directory
        if cleanup_errors and primary is not None:
            for value in cleanup_errors:
                primary.add_note(value)
        elif cleanup_errors:
            failure = NativeUIJourneyError(
                "native journey cleanup failed: " + "; ".join(cleanup_errors)
            )
            failure.native_ui_checks = dict(checks)
            raise failure
    return {"platform": args.platform, "checks": checks, "complete": True}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--cli", type=_path, required=True)
    parser.add_argument("--ui", type=_path, required=True)
    parser.add_argument("--profile", type=_path, required=True)
    parser.add_argument("--smoke-script", type=_path, required=True)
    parser.add_argument("--raw-log-dir", type=_path, required=True)
    parser.add_argument("--output", type=_path)
    parser.add_argument("--timeout", type=_timeout, default=900.0)
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
    args = build_parser().parse_args(argv)
    if (args.platform == "windows" and (args.service_pipe != "DobbyVPN.Control" or args.service_socket)) or (
        args.platform == "macos" and (not args.service_socket or args.service_pipe)
    ):
        raise SystemExit("native UI journey requires the platform's configured local control endpoint")
    if (
        not args.cli.is_file()
        or not _ui_path_is_launchable(args.platform, args.ui)
        or not args.profile.is_file()
        or not args.smoke_script.is_file()
    ):
        raise SystemExit("native UI journey requires regular CLI, launchable UI, profile, and smoke-script files")
    try:
        result = run_journey(args)
    except Exception as error:
        rendered_error = f"{type(error).__name__}: {error}"
        if args.output is not None:
            operation = getattr(error, "operation", None)
            stage = getattr(error, "stage", None)
            failure = {
                "suite": "full",
                "action_driver": "native-window",
                "platform": args.platform,
                "complete": False,
                "error": rendered_error,
                "notes": list(getattr(error, "__notes__", ())),
            }
            checks = getattr(error, "native_ui_checks", None)
            if isinstance(checks, dict):
                failure["checks"] = checks
            if isinstance(operation, str) and operation:
                failure["operation"] = operation
            if isinstance(stage, str) and stage:
                failure["stage"] = stage
            try:
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps(failure, sort_keys=True) + "\n", encoding="utf-8")
            except OSError as output_error:
                error.add_note(
                    "native_ui_failure_result_write_error="
                    f"{type(output_error).__name__}: {output_error}"
                )
        print(f"native-ui-journey failed: {rendered_error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(
                "native-ui-journey note: " + note,
                file=sys.stderr,
            )
        return 1
    result = {
        "suite": "full",
        "action_driver": "native-window",
        **result,
    }
    if args.output is not None:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps(result, sort_keys=True, separators=(",", ":")), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
