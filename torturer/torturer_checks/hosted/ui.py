"""Headless Fyne UI adapter for desktop functional qualification.

The companion process drives the production Fyne widgets with Fyne's test
driver.  This adapter deliberately delegates VPN observations, traffic, route
proof, process-loss handling, and cleanup to the existing platform adapter;
only the user-facing configure/connect/disconnect/reconnect actions cross the
UI boundary.
"""

from __future__ import annotations

from dataclasses import dataclass
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import threading
import time
from typing import Any, Callable, Mapping

from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import ScenarioStep
from torturer_checks.diagnostics import (
    StreamingRedactor,
    add_exception_notes,
    register_sensitive_values,
)

from ..windows_job import (
    close_for as close_windows_job,
    popen_with_windows_job,
    terminate_and_prove_empty as terminate_windows_job,
)
from ..screenshot_artifacts import (
    ScreenshotIntegrityError,
    assert_marker_matches,
    png_metadata,
)


_UI_START_TIMEOUT_SECONDS = 10.0
_UI_CONFIGURE_TIMEOUT_SECONDS = 60.0
_UI_CONNECT_TIMEOUT_SECONDS = 60.0


class _UIStreamFailure(RuntimeError):
    """A companion output reader failed and the JSON protocol is unreliable."""


@dataclass(frozen=True)
class _UIResponse:
    ok: bool
    error: str = ""
    status: str = ""
    details: str = ""
    button: str = ""
    path: str = ""
    mime: str = ""
    sha256: str = ""
    bytes: int = 0
    width: int = 0
    height: int = 0


def _response(value: object) -> _UIResponse:
    if not isinstance(value, dict):
        raise ScenarioExecutionError("UI_RESPONSE_INVALID")
    return _UIResponse(
        ok=value.get("ok") is True,
        error=value.get("error") if isinstance(value.get("error"), str) else "",
        status=value.get("status") if isinstance(value.get("status"), str) else "",
        details=value.get("details") if isinstance(value.get("details"), str) else "",
        button=value.get("button") if isinstance(value.get("button"), str) else "",
        path=value.get("path") if isinstance(value.get("path"), str) else "",
        mime=value.get("mime") if isinstance(value.get("mime"), str) else "",
        sha256=value.get("sha256") if isinstance(value.get("sha256"), str) else "",
        bytes=value.get("bytes") if type(value.get("bytes")) is int else 0,
        width=value.get("width") if type(value.get("width")) is int else 0,
        height=value.get("height") if type(value.get("height")) is int else 0,
    )


class HeadlessUIAdapter:
    """Wrap one normal platform adapter with production Fyne UI actions."""

    adapter_id = "hosted-fyne-ui"
    adapter_version = "v1"

    def __init__(
        self,
        *,
        base: Any,
        ui_test: Path,
        profile: Path,
        runner: Any,
    ) -> None:
        if not ui_test.is_file():
            raise ValueError("UI_TEST_UNAVAILABLE")
        if not profile.is_file():
            raise ValueError("PROFILE_UNAVAILABLE")
        self.base = base
        self.ui_test = ui_test
        self.profile = profile
        self.runner = runner
        try:
            self._profile_text = profile.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as error:
            raise ValueError("PROFILE_INVALID") from error
        self._process: subprocess.Popen[bytes] | None = None
        self._responses: queue.Queue[bytes | str | None | BaseException] = queue.Queue()
        self._reader_thread: threading.Thread | None = None
        self._stderr_thread: threading.Thread | None = None
        self._stdout_done = threading.Event()
        self._stderr_done = threading.Event()
        self._request_lock = threading.Lock()
        self._base_selected: ConnectionIdentity | None = None
        self._ui_connected = False
        self._capture_counter = 0
        self._captured_startup = False
        register_sensitive_values(runner, self._profile_text)

    @property
    def capabilities(self):
        return self.base.capabilities

    @property
    def capability_unavailable_reasons(self):
        return getattr(self.base, "capability_unavailable_reasons", {})

    def set_progress_sink(self, sink: Callable[[str, dict[str, object]], None]) -> None:
        setter = getattr(self.base, "set_progress_sink", None)
        if callable(setter):
            setter(sink)

    def discover_connections(self, timeout_seconds: float = 30.0) -> tuple[ConnectionIdentity, ...]:
        """Validate the profile with the normal adapter, then expose one UI lane.

        The GUI intentionally selects its own active profile.  Reporting one
        synthetic connection avoids pretending that a single GUI action
        exercised every profile in a multi-profile document; the CLI matrix
        remains responsible for that coverage.
        """

        connections = tuple(self.base.discover_connections(timeout_seconds=timeout_seconds))
        if not connections:
            raise ScenarioExecutionError("CONNECTION_INVENTORY_INVALID")
        self._base_selected = connections[0]
        return (ConnectionIdentity(index=0, protocol="AUTO"),)

    def select_connection(self, connection: ConnectionIdentity) -> None:
        if connection != ConnectionIdentity(index=0, protocol="AUTO"):
            raise ScenarioExecutionError("CONNECTION_NOT_DISCOVERED")
        if self._base_selected is None:
            raise ScenarioExecutionError("CONNECTION_NOT_SELECTED")
        self.base.select_connection(self._base_selected)

    def execute_scenario(self, scenario: Any) -> dict[str, object]:
        observations: dict[str, object] = {}
        for step in scenario.steps:
            self._emit("operation-start", scenario=scenario.id, operation=step.operation, operation_id=step.id)
            try:
                result = self._execute_step(step)
            except Exception as error:
                self._emit(
                    "operation-error",
                    scenario=scenario.id,
                    operation=step.operation,
                    operation_id=step.id,
                    code=getattr(error, "reason_code", type(error).__name__),
                )
                try:
                    self._capture("failure")
                except BaseException as capture_error:
                    add_exception_notes(error, "ui_failure_capture", capture_error)
                raise
            observations.update(result)
            self._emit(
                "operation-finish",
                scenario=scenario.id,
                operation=step.operation,
                operation_id=step.id,
                observations=result,
            )
        return observations

    def _execute_step(self, step: ScenarioStep) -> dict[str, object]:
        timeout = float(step.timeout_seconds)
        if step.operation == "configure":
            # Keep semantic configure separate from the visible Connect
            # action.  The next step clicks Connect and therefore exercises
            # the complete UI start path without spending the configure
            # step's CLI-oriented bound on a VPN startup.
            self._configure_ui(max(timeout, _UI_CONFIGURE_TIMEOUT_SECONDS))
            return {"configured": True}
        if step.operation == "connect":
            self._prepare_native_connect(timeout)
            self._connect_ui(timeout)
            return {}
        if step.operation == "disconnect":
            self._disconnect_ui(timeout)
            clean = self.base._cleanup_verified(timeout)
            key = "final_disconnect_clean" if step.id == "final-disconnect" else "disconnect_clean"
            return {key: clean}
        if step.operation == "reconnect":
            self._prepare_native_connect(timeout)
            self._connect_ui(timeout)
            if not self.base._connected(timeout):
                raise ScenarioExecutionError("RECONNECT_NOT_ESTABLISHED")
            return {"restart_verified": True, "reconnect_completed": True}
        if step.operation == "process_loss":
            result = self.base.execute(step)
            if self._ui_connected:
                # Watch/reconnect is part of the UI contract.  The state may
                # already be Connected when the service restart is too quick,
                # so the service-side assertion remains authoritative.
                self._request(
                    {
                        "op": "wait",
                        "state": "Connected",
                        "timeout_ms": max(1, int(timeout * 1000)),
                    },
                    timeout,
                )
                self._capture("process-loss-recovery")
            return result
        return self.base.execute(step)

    def _prepare_native_connect(self, timeout: float) -> None:
        prepare = getattr(self.base, "prepare_native_connect", None)
        if not callable(prepare):
            raise ScenarioExecutionError("NATIVE_CONNECT_PREPARATION_UNAVAILABLE")
        prepare(timeout)

    def _connect_ui(self, timeout: float) -> None:
        if self._ui_connected:
            return
        operation_timeout = max(timeout, _UI_CONNECT_TIMEOUT_SECONDS)
        result = self._request(
            {
                "op": "connect",
                "config": self._profile_text,
                "timeout_ms": max(1, int(operation_timeout * 1000)),
            },
            operation_timeout,
        )
        if not result.ok or result.status != "Connected":
            raise ScenarioExecutionError("UI_CONNECT_FAILED")
        self._ui_connected = True
        self._capture("connected")

    def _configure_ui(self, timeout: float) -> None:
        if not self._captured_startup:
            self._capture("startup")
            self._capture("settings", settings=True)
            self._captured_startup = True
        result = self._request(
            {
                "op": "configure",
                "config": self._profile_text,
                "timeout_ms": max(1, int(timeout * 1000)),
            },
            timeout,
        )
        if not result.ok or result.status not in {"Ready", "Disconnected"}:
            raise ScenarioExecutionError("UI_CONFIGURE_FAILED")
        self._capture("configured")

    def _disconnect_ui(self, timeout: float) -> None:
        if not self._ui_connected:
            return
        result = self._request(
            {"op": "disconnect", "timeout_ms": max(1, int(timeout * 1000))},
            timeout,
        )
        if not result.ok or result.status not in {"Disconnected", "Failed"}:
            raise ScenarioExecutionError("UI_DISCONNECT_FAILED")
        self._ui_connected = False
        self._capture("disconnected")

    def _capture(self, label: str, *, settings: bool = False) -> Path:
        self._capture_counter += 1
        capture_label = f"{self._capture_counter:03d}-{label}"
        directory = self.runner.raw_directory / "screenshots" / "desktop-mini"
        operation = "capture-settings" if settings else "capture"
        result = self._request(
            {"op": operation, "path": str(directory), "label": capture_label},
            _UI_START_TIMEOUT_SECONDS,
        )
        path = Path(result.path)
        if (
            not result.ok
            or result.mime != "image/png"
            or path != directory / f"{capture_label}.png"
            or not path.is_file()
            or result.width <= 0
            or result.height <= 0
            or result.bytes <= 0
        ):
            raise ScenarioExecutionError("UI_CAPTURE_INVALID")
        try:
            metadata = png_metadata(path)
            assert_marker_matches(
                metadata,
                bytes_count=result.bytes,
                sha256_value=result.sha256,
                width=result.width,
                height=result.height,
            )
        except ScreenshotIntegrityError as error:
            raise ScenarioExecutionError("UI_CAPTURE_INTEGRITY_FAILED") from error
        if result.mime != metadata["mime"]:
            raise ScenarioExecutionError("UI_CAPTURE_INVALID")
        return path

    def reset(self, timeout_seconds: float = 30.0) -> None:
        error: BaseException | None = None
        try:
            self._disconnect_ui(timeout_seconds)
        except BaseException as failure:
            error = failure
        try:
            self.base.reset(timeout_seconds)
        except BaseException as failure:
            if error is None:
                error = failure
            else:
                add_exception_notes(error, "platform_reset", failure)
        if error is not None:
            raise error

    def finalize(self, timeout_seconds: float = 30.0, *, deadline: float | None = None) -> None:
        error: BaseException | None = None
        if self._process is not None:
            try:
                self._capture("final")
            except BaseException as failure:
                error = failure
        try:
            self._close_process(timeout_seconds, deadline=deadline)
        except BaseException as failure:
            if error is None:
                error = failure
            else:
                add_exception_notes(error, "ui_close", failure)
        try:
            self.base.finalize(timeout_seconds, deadline=deadline)
        except BaseException as failure:
            if error is None:
                error = failure
            else:
                add_exception_notes(error, "platform_finalization", failure)
        if error is not None:
            raise error

    def _emit(self, event: str, **fields: object) -> None:
        emitter = getattr(self.base, "_emit_progress", None)
        if callable(emitter):
            emitter(event, **fields)

    def _start_process(self, timeout: float) -> None:
        if self._process is not None:
            return
        self._stdout_done.clear()
        self._stderr_done.clear()
        environment = dict(getattr(self.runner, "environment", os.environ))
        deadline = time.monotonic() + min(max(timeout, 0.1), _UI_START_TIMEOUT_SECONDS)
        try:
            self._process = popen_with_windows_job(
                subprocess.Popen,
                [str(self.ui_test)],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                # The companion's JSON protocol stays on stdout. Stderr is
                # drained separately and forwarded to the invoking process.
                stderr=subprocess.PIPE,
                env=environment,
                text=False,
                bufsize=0,
                start_new_session=(os.name != "nt"),
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) if os.name == "nt" else 0,
                stage="ui-companion-start",
                deadline=deadline,
            )
        except Exception as error:
            raise ScenarioExecutionError("UI_TEST_LAUNCH_FAILED") from error

        def read_responses() -> None:
            assert self._process is not None
            stdout = self._process.stdout
            redactor = StreamingRedactor((self._profile_text,))
            try:
                sys.stderr.write("[headless-ui stdout begin]\n")
                sys.stderr.flush()
                if stdout is None:
                    raise _UIStreamFailure("stdout pipe is unavailable")
                while True:
                    line = stdout.readline()
                    if not line:
                        break
                    rendered = redactor.feed(line)
                    if rendered:
                        sys.stderr.write(rendered)
                        sys.stderr.flush()
                    self._responses.put(line)
            except BaseException as error:
                self._responses.put(_UIStreamFailure(f"stdout reader failed: {type(error).__name__}: {error}"))
            finally:
                rendered = redactor.finish()
                if rendered:
                    sys.stderr.write(rendered)
                sys.stderr.write("[headless-ui stdout end]\n")
                sys.stderr.flush()
                self._responses.put(None)
                self._stdout_done.set()

        self._reader_thread = threading.Thread(target=read_responses, name="dobbyvpn-ui-test-reader", daemon=True)
        self._reader_thread.start()

        def read_stderr() -> None:
            process = self._process
            stderr = None if process is None else process.stderr
            redactor = StreamingRedactor((self._profile_text,))
            try:
                sys.stderr.write("[headless-ui stderr begin]\n")
                sys.stderr.flush()
                if stderr is None:
                    raise _UIStreamFailure("stderr pipe is unavailable")
                while True:
                    chunk = stderr.read(64 * 1024)
                    if not chunk:
                        break
                    rendered = redactor.feed(chunk)
                    if rendered:
                        sys.stderr.write(rendered)
                        sys.stderr.flush()
            except BaseException as error:
                # The protocol reader must still fail closed if diagnostics
                # cannot be drained; otherwise a full stderr pipe can hang
                # the companion while stdout appears healthy.
                self._responses.put(_UIStreamFailure(f"stderr reader failed: {type(error).__name__}: {error}"))
            finally:
                rendered = redactor.finish()
                if rendered:
                    sys.stderr.write(rendered)
                sys.stderr.write("[headless-ui stderr end]\n")
                sys.stderr.flush()
                self._stderr_done.set()

        self._stderr_thread = threading.Thread(
            target=read_stderr,
            name="dobbyvpn-ui-test-stderr-reader",
            daemon=True,
        )
        self._stderr_thread.start()

    def _request(self, document: Mapping[str, object], timeout: float) -> _UIResponse:
        if timeout <= 0:
            raise ScenarioExecutionError("UI_TIMEOUT")
        with self._request_lock:
            self._start_process(timeout)
            process = self._process
            if process is None or process.stdin is None:
                raise ScenarioExecutionError("UI_TEST_UNAVAILABLE")
            try:
                process.stdin.write(
                    (json.dumps(document, separators=(",", ":")) + "\n").encode("utf-8")
                )
                process.stdin.flush()
            except (BrokenPipeError, OSError) as error:
                raise ScenarioExecutionError("UI_TEST_PIPE_FAILED") from error
            deadline = time.monotonic() + timeout
            try:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise queue.Empty
                line = self._responses.get(timeout=remaining)
            except queue.Empty as error:
                raise ScenarioExecutionError("UI_RESPONSE_TIMEOUT") from error
            if line is None:
                self._stderr_done.wait(timeout=1.0)
                raise ScenarioExecutionError("UI_TEST_EXITED")
            if isinstance(line, BaseException):
                raise ScenarioExecutionError("UI_TEST_OUTPUT_FAILED") from line
            try:
                if isinstance(line, bytes):
                    line = line.decode("utf-8")
                result = _response(json.loads(line))
            except (UnicodeDecodeError, TypeError, ValueError) as error:
                raise ScenarioExecutionError("UI_RESPONSE_INVALID") from error
            if not result.ok and result.error:
                failure = ScenarioExecutionError("UI_ACTION_FAILED")
                failure.add_note("ui_status=" + result.status)
                raise failure
            return result

    def _close_process(self, timeout: float, *, deadline: float | None) -> None:
        process = self._process
        if process is None:
            return
        close_deadline = deadline if deadline is not None else time.monotonic() + max(timeout, 0.1)
        errors: list[BaseException] = []
        try:
            if process.poll() is None:
                try:
                    self._request({"op": "close"}, max(0.1, close_deadline - time.monotonic()))
                except BaseException as error:
                    errors.append(error)
                if process.poll() is None:
                    if os.name == "nt":
                        try:
                            cleanup = terminate_windows_job(
                                process, deadline=close_deadline, stage="ui-companion-finalize"
                            )
                            if not cleanup.process_tree_proven:
                                errors.append(ScenarioExecutionError("UI_TEST_CLEANUP_FAILED"))
                        except BaseException as error:
                            errors.append(error)
                    else:
                        try:
                            # close asks the companion to exit cleanly.  Give
                            # that response a short chance to reach process
                            # reaping before escalating; on macOS the process
                            # can otherwise be between exit and wait, where a
                            # group signal reports EPERM even though cleanup
                            # is already completing.
                            process.wait(timeout=min(1.0, max(0.1, close_deadline - time.monotonic())))
                        except ProcessLookupError:
                            pass
                        except subprocess.TimeoutExpired:
                            try:
                                os.killpg(process.pid, 15)
                            except ProcessLookupError:
                                pass
                            except PermissionError:
                                if process.poll() is None:
                                    process.terminate()
                            try:
                                process.wait(timeout=max(0.1, close_deadline - time.monotonic()))
                            except subprocess.TimeoutExpired:
                                process.kill()
                                try:
                                    process.wait(timeout=1)
                                except BaseException as error:
                                    errors.append(error)
                        except BaseException as error:
                            errors.append(error)
            if os.name == "nt":
                try:
                    close_windows_job(process, stage="ui-companion-finalize", deadline=close_deadline)
                except BaseException as error:
                    errors.append(error)
        finally:
            self._stdout_done.wait(timeout=max(1.0, min(timeout, 15.0)))
            self._stderr_done.wait(timeout=max(1.0, min(timeout, 15.0)))
            for thread in (self._reader_thread, self._stderr_thread):
                if thread is not None and thread.is_alive():
                    errors.append(ScenarioExecutionError("UI_TEST_OUTPUT_DRAIN_FAILED"))
            for stream in (process.stdin, process.stdout, process.stderr):
                if stream is not None:
                    try:
                        stream.close()
                    except OSError as error:
                        errors.append(error)
            self._process = None
            self._reader_thread = None
            self._stderr_thread = None
        if errors:
            failure = ScenarioExecutionError("UI_TEST_CLEANUP_FAILED")
            for error in errors:
                add_exception_notes(failure, "ui_cleanup", error)
            raise failure


__all__ = ["HeadlessUIAdapter"]
