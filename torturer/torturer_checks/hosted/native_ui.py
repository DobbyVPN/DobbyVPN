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
import os
from pathlib import Path
import queue
import subprocess
import sys
import threading
import time
from typing import Any

from torturer_contract.functional.scenarios import ScenarioStep

from .cli import SubprocessRunner, _ensure_directory
from .factory import adapter_for_platform


_REQUEST_TIMEOUT = 300.0


class NativeUIJourneyError(RuntimeError):
    """A native UI or base-adapter journey failure."""


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


class _NativeUIProcess:
    """Client for native_ui_smoke.py's bounded JSON command boundary."""

    def __init__(self, *, script: Path, platform: str, binary: Path, profile: Path,
                 timeout: float, raw_directory: Path) -> None:
        self.script = script
        self.platform = platform
        self.binary = binary
        self.profile = profile
        self.timeout = timeout
        self.raw_directory = raw_directory
        self.process: subprocess.Popen[str] | None = None
        self._stderr = None
        self._responses: queue.Queue[str | None] = queue.Queue()
        self._reader: threading.Thread | None = None
        self._lock = threading.Lock()

    def start(self) -> None:
        if self.process is not None:
            return
        stderr = (self.raw_directory / "native-ui-driver.stderr.log").open("ab")
        self._stderr = stderr
        self.process = subprocess.Popen(
            [
                sys.executable,
                str(self.script),
                "--platform", self.platform,
                "--ui", str(self.binary),
                "--profile", str(self.profile),
                "--timeout", str(min(self.timeout, _REQUEST_TIMEOUT)),
                "--serve",
            ],
            cwd=str(self.script.parents[2]),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=stderr,
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
        response = self._response(self.timeout)
        if response.get("ok") is not True or response.get("event") != "ready":
            self.close()
            raise NativeUIJourneyError(
                "native UI did not become ready: " + str(response.get("error", response))
            )

    def _response(self, timeout: float) -> dict[str, object]:
        try:
            line = self._responses.get(timeout=max(0.1, timeout))
        except queue.Empty as error:
            raise NativeUIJourneyError("native UI response timed out") from error
        if line is None:
            process = self.process
            code = None if process is None else process.poll()
            raise NativeUIJourneyError(f"native UI process exited before responding (code={code})")
        try:
            value = json.loads(line)
        except (TypeError, ValueError) as error:
            raise NativeUIJourneyError("native UI response is not valid JSON") from error
        if not isinstance(value, dict):
            raise NativeUIJourneyError("native UI response is not an object")
        return value

    def request(self, operation: str, *, timeout: float | None = None) -> dict[str, object]:
        limit = self.timeout if timeout is None else timeout
        with self._lock:
            if self.process is None:
                self.start()
            process = self.process
            if process is None or process.stdin is None:
                raise NativeUIJourneyError("native UI process is unavailable")
            try:
                process.stdin.write(json.dumps({"op": operation}, separators=(",", ":")) + "\n")
                process.stdin.flush()
            except (BrokenPipeError, OSError) as error:
                raise NativeUIJourneyError("native UI command pipe failed") from error
            response = self._response(limit)
            if response.get("ok") is not True:
                error = NativeUIJourneyError(str(response.get("error", "native UI action failed")))
                raise error
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
            for stream in (process.stdin, process.stdout, process.stderr, self._stderr):
                if stream is not None:
                    try:
                        stream.close()
                    except OSError:
                        pass
            self.process = None
            self._stderr = None


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


def run_journey(args: argparse.Namespace) -> dict[str, object]:
    _ensure_directory(args.raw_log_dir)
    runner = SubprocessRunner(
        args.raw_log_dir,
        environment={"DOBBY_CLI_LOG_PATH": str(args.raw_log_dir / "app.log")},
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
        checks.update(_base_execute(base, "native-tunnel", "observe_tunnel", args.timeout))
        checks.update(_base_execute(base, "native-routing", "observe_routing_identity", args.timeout))
        checks.update(_base_execute(base, "native-stability", "measure_stability", args.timeout))
        checks.update(_base_execute(base, "native-throughput", "measure_throughput", args.timeout))

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

        settings = ui.request("settings")
        checks["settings_version"] = settings.get("settings_version") is True
        checks["settings_source_commit"] = settings.get("settings_source_commit") is True

        ui.request("close-window")
        checks["close_window"] = True
        ui.request("reopen")
        checks["reopen_connected"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe service continuity after UI reopen")

        loss = _base_execute(base, "native-process-loss", "process_loss", args.timeout)
        checks.update(loss)
        recovery = ui.request("process_loss_recovery", timeout=args.timeout)
        checks["ui_process_loss_recovered"] = recovery.get("status") == "Connected"
        checks["ui_reconnecting_observed"] = recovery.get("reconnecting_seen") is True

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
            failure = {
                "suite": "full",
                "action_driver": "native-window",
                "platform": args.platform,
                "complete": False,
                "error": f"{type(error).__name__}: {error}",
                "notes": list(getattr(error, "__notes__", ())),
            }
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
