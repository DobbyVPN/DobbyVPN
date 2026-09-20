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
import json
import math
import os
from pathlib import Path
import queue
import re
import subprocess
import sys
import threading
import time
from typing import Any

from torturer_contract.functional.scenarios import ScenarioStep

from .cli import SubprocessRunner, _ensure_directory
from .factory import adapter_for_platform


# The hosted journey waits for the smoke driver's response. Keep a small
# explicit reserve inside that response deadline so cleanup can complete
# before the caller's task deadline. The result JSON is the only retained
# native-UI output.
_REQUEST_TIMEOUT = 300.0
_SMOKE_DIAGNOSTIC_RESERVE_SECONDS = 30.0
_CONTEXT_VALUE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")


class NativeUIJourneyError(RuntimeError):
    """A native UI or base-adapter journey failure.

    ``operation`` and ``stage`` are deliberately short, fixed vocabulary
    values supplied by the harness.  They make a bounded timeout actionable
    without ever including the profile, command line, or another private
    value in the retained failure record.
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


def _smoke_timeout(response_timeout: float) -> float:
    """Bound the real-window driver below its response deadline.

    ``native_ui_smoke.py`` owns the actual HWND/accessibility wait. If it
    receives the same timeout as ``_NativeUIProcess._response``, the outer
    reader can time out and kill the task before cleanup. Keep one bounded
    reserve for the response and cleanup path while preserving a positive
    timeout for focused/unit-test invocations.
    """

    if response_timeout <= 0:
        raise ValueError("native UI response timeout must be positive")
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, response_timeout / 3.0)
    return min(_REQUEST_TIMEOUT, response_timeout - reserve)


class _NativeUIProcess:
    """Client for native_ui_smoke.py's bounded JSON command boundary."""

    def __init__(self, *, script: Path, platform: str, binary: Path, profile: Path,
                 timeout: float, raw_directory: Path) -> None:
        self.script = script
        self.platform = platform
        self.binary = binary
        self.profile = profile
        self.timeout = timeout
        # Keep the constructor boundary stable for callers that provide a
        # disposable scratch root, but never turn it into a log sink.
        self.raw_directory = raw_directory
        self.process: subprocess.Popen[str] | None = None
        self._responses: queue.Queue[str | None] = queue.Queue()
        self._reader: threading.Thread | None = None
        self._lock = threading.Lock()
        self._operation = "start"
        self._stage = "launching-driver"

    def _error(self, message: str) -> NativeUIJourneyError:
        operation = self._operation
        stage = self._stage
        return NativeUIJourneyError(
            f"{message} (operation={operation}, stage={stage})",
            operation=operation,
            stage=stage,
        )

    def start(self) -> None:
        if self.process is not None:
            return
        self.process = subprocess.Popen(
            [
                sys.executable,
                str(self.script),
                "--platform", self.platform,
                "--ui", str(self.binary),
                "--profile", str(self.profile),
                "--timeout", str(_smoke_timeout(self.timeout)),
                "--serve",
            ],
            cwd=str(self.script.parents[2]),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            # JSON responses on stdout are the protocol. Native-driver stderr
            # is intentionally discarded so it cannot become a retained
            # diagnostic artifact.
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
            bufsize=1,
            start_new_session=(os.name != "nt"),
            creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) if os.name == "nt" else 0,
        )

        def read() -> None:
            process = self.process
            if process is None or process.stdout is None:
                self._responses.put(None)
                return
            try:
                for line in process.stdout:
                    self._responses.put(line)
            finally:
                self._responses.put(None)

        self._reader = threading.Thread(target=read, name="dobbyvpn-native-ui-reader", daemon=True)
        self._reader.start()
        self._operation = "start"
        self._stage = "waiting-for-ready-window"
        response = self._response(self.timeout)
        if response.get("ok") is not True or response.get("event") != "ready":
            operation = self._operation
            stage = self._stage
            self.close()
            raise NativeUIJourneyError(
                "native UI did not become ready: "
                + str(response.get("error", response))
                + f" (operation={operation}, stage={stage})",
                operation=operation,
                stage=stage,
            )

    def _response(self, timeout: float) -> dict[str, object]:
        try:
            deadline = time.monotonic() + max(0.1, timeout)
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise queue.Empty
                line = self._responses.get(timeout=remaining)
                if line is None:
                    break
                try:
                    value = json.loads(line)
                except (TypeError, ValueError) as error:
                    raise self._error("native UI response is not valid JSON") from error
                if not isinstance(value, dict):
                    raise self._error("native UI response is not an object")
                if value.get("event") == "progress":
                    operation = value.get("operation")
                    stage = value.get("stage")
                    if isinstance(operation, str) and _CONTEXT_VALUE.fullmatch(operation):
                        self._operation = operation
                    if isinstance(stage, str) and _CONTEXT_VALUE.fullmatch(stage):
                        self._stage = stage
                    continue
                return value
        except queue.Empty as error:
            raise self._error("native UI response timed out") from error
        if line is None:
            process = self.process
            code = None if process is None else process.poll()
            raise self._error(
                f"native UI process exited before responding (code={code})"
            )
        # The loop above either returns a response or raises.  Keep this
        # defensive branch so a future queue implementation cannot silently
        # turn a missing response into a successful operation.
        raise self._error("native UI response was empty")

    def request(self, operation: str, *, timeout: float | None = None) -> dict[str, object]:
        limit = self.timeout if timeout is None else timeout
        with self._lock:
            if self.process is None:
                self.start()
            process = self.process
            if process is None or process.stdin is None:
                raise self._error("native UI process is unavailable")
            self._operation = operation
            self._stage = "waiting-for-operation"
            try:
                process.stdin.write(json.dumps({"op": operation}, separators=(",", ":")) + "\n")
                process.stdin.flush()
            except (BrokenPipeError, OSError) as error:
                raise self._error("native UI command pipe failed") from error
            response = self._response(limit)
            if response.get("ok") is not True:
                raise self._error(str(response.get("error", "native UI action failed")))
            return response

    def close(self) -> None:
        process = self.process
        if process is None:
            return
        try:
            if process.poll() is None and process.stdin is not None:
                try:
                    process.stdin.write('{"op":"close"}\n')
                    process.stdin.flush()
                    self._response(min(self.timeout, 15.0))
                except BaseException:
                    pass
            if process.poll() is None:
                try:
                    process.wait(timeout=min(self.timeout, 5.0))
                except subprocess.TimeoutExpired:
                    if os.name != "nt":
                        try:
                            os.killpg(process.pid, 15)
                        except (ProcessLookupError, PermissionError):
                            process.terminate()
                    else:
                        process.terminate()
                    try:
                        process.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=1)
        finally:
            for stream in (process.stdin, process.stdout, process.stderr):
                if stream is not None:
                    try:
                        stream.close()
                    except OSError:
                        pass
            self.process = None


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
    service_socket: Path | str = args.service_socket
    if args.platform == "macos":
        service_socket = Path(args.service_socket)
    base = adapter_for_platform(
        args.platform,
        cli=args.cli,
        profile=args.profile,
        runner=runner,
        local_mode=True,
        service_pid=args.service_pid,
        service_binary=args.service_binary,
        service_socket=service_socket,
        service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
        network_transition_helper=args.network_transition_helper,
    )
    ui = _NativeUIProcess(
        script=args.smoke_script,
        platform=args.platform,
        binary=args.ui,
        profile=args.profile,
        timeout=min(args.timeout, _REQUEST_TIMEOUT),
        raw_directory=args.raw_log_dir,
    )
    checks: dict[str, object] = {}
    primary: BaseException | None = None
    try:
        connections = tuple(base.discover_connections(timeout_seconds=min(30.0, args.timeout)))
        if not connections:
            raise NativeUIJourneyError("base adapter reported no connections")
        base.select_connection(connections[0])
        ui.start()
        ui.request("configure")
        checks["configure_native"] = True

        # The base adapter prepares its independent observation/proof state;
        # it never substitutes a CLI connect action for the native Connect
        # click.  Routing-enabled desktop adapters use this seam to install
        # their temporary probe firewall before the UI action.
        prepare = getattr(base, "prepare_native_connect", None)
        if not callable(prepare):
            raise NativeUIJourneyError("base adapter has no native connect preparation")
        prepare(args.timeout)
        ui.request("connect")
        checks["connect_native"] = True
        _record_native_observations(base, checks, args.timeout)

        ui.request("disconnect")
        checks["disconnect_native"] = True
        connected = getattr(base, "_connected", None)
        if not callable(connected) or connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after native Disconnect")
        cleanup_verified = getattr(base, "_cleanup_verified", None)
        if not callable(cleanup_verified) or not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("base adapter could not verify native Disconnect cleanup")
        checks["disconnect_clean"] = True

        prepare(args.timeout)
        ui.request("reconnect")
        checks["reconnect_native"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe native reconnect")
        checks["reconnect_completed"] = True
        _record_native_observations(base, checks, args.timeout, prefix="reconnect")

        settings = ui.request("settings")
        checks["settings_version"] = settings.get("settings_version") is True
        checks["settings_source_commit"] = settings.get("settings_source_commit") is True

        ui.request("close-window")
        checks["close_window"] = True
        ui.request("reopen")
        checks["reopen_connected"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe service continuity after UI reopen")

        loss = _restart_service_for_native_ui(base, args.timeout)
        checks.update(loss)
        prepare(args.timeout)
        recovery = ui.request("process_loss_recovery", timeout=args.timeout)
        checks["ui_process_loss_recovered"] = recovery.get("status") == "Connected"
        checks["ui_reconnecting_observed"] = recovery.get("reconnecting_seen") is True
        if "reconnecting_probe_error" in recovery:
            checks["ui_reconnecting_probe_error"] = recovery["reconnecting_probe_error"]
        if not connected(args.timeout):
            raise NativeUIJourneyError(
                "base adapter did not observe native UI process-loss recovery"
            )
        _record_native_observations(base, checks, args.timeout, prefix="process_loss")

        ui.request("disconnect")
        checks["final_disconnect_native"] = True
        if connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after final native Disconnect")
        if not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("final native Disconnect cleanup was not verified")
        checks["final_cleanup_verified"] = True
        _require_complete_checks(checks)
    except BaseException as error:
        primary = error
        raise
    finally:
        cleanup_errors: list[str] = []
        try:
            ui.close()
        except BaseException as error:
            cleanup_errors.append(f"native-ui: {type(error).__name__}: {error}")
        try:
            base.reset(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-reset: {type(error).__name__}: {error}")
        try:
            base.finalize(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-finalize: {type(error).__name__}: {error}")
        if cleanup_errors and primary is not None:
            for value in cleanup_errors:
                primary.add_note(value)
        elif cleanup_errors:
            raise NativeUIJourneyError("native journey cleanup failed: " + "; ".join(cleanup_errors))
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
    # Windows uses a loopback host:port while macOS uses a filesystem socket.
    # Leave platform-specific validation to the existing adapter.
    parser.add_argument("--service-socket", type=str, required=True)
    parser.add_argument("--service-library-path", type=_path)
    parser.add_argument("--service-pid-file", type=_path)
    parser.add_argument("--service-identity-file", type=_path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=_path)
    parser.add_argument("--network-transition-helper", type=_path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    if not args.cli.is_file() or not args.ui.is_file() or not args.profile.is_file() or not args.smoke_script.is_file():
        raise SystemExit("native UI journey requires regular CLI, UI, profile, and smoke-script files")
    try:
        result = run_journey(args)
    except Exception as error:
        if args.output is not None:
            operation = getattr(error, "operation", None)
            stage = getattr(error, "stage", None)
            failure = {
                "suite": "full",
                "action_driver": "native-window",
                "platform": args.platform,
                "complete": False,
                "error": f"{type(error).__name__}: {error}",
                "notes": list(getattr(error, "__notes__", ())),
            }
            if isinstance(operation, str) and operation:
                failure["operation"] = operation
            if isinstance(stage, str) and stage:
                failure["stage"] = stage
            try:
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps(failure, sort_keys=True) + "\n", encoding="utf-8")
            except OSError:
                pass
        print(f"native-ui-journey failed: {type(error).__name__}: {error}", file=sys.stderr)
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
