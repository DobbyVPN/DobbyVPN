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
import threading
import time
from typing import Any, Callable, Mapping

from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import ScenarioStep

from ..windows_job import (
    close_for as close_windows_job,
    popen_with_windows_job,
    terminate_and_prove_empty as terminate_windows_job,
)


_UI_START_TIMEOUT_SECONDS = 10.0


@dataclass(frozen=True)
class _UIResponse:
    ok: bool
    error: str = ""
    status: str = ""
    details: str = ""
    button: str = ""


def _response(value: object) -> _UIResponse:
    if not isinstance(value, dict):
        raise ScenarioExecutionError("UI_RESPONSE_INVALID")
    return _UIResponse(
        ok=value.get("ok") is True,
        error=value.get("error") if isinstance(value.get("error"), str) else "",
        status=value.get("status") if isinstance(value.get("status"), str) else "",
        details=value.get("details") if isinstance(value.get("details"), str) else "",
        button=value.get("button") if isinstance(value.get("button"), str) else "",
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
        self._process: subprocess.Popen[str] | None = None
        self._stderr: Any = None
        self._responses: queue.Queue[str | None] = queue.Queue()
        self._reader_thread: threading.Thread | None = None
        self._request_lock = threading.Lock()
        self._base_selected: ConnectionIdentity | None = None
        self._ui_connected = False

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
            # The production desktop UI intentionally has one primary action:
            # Connect configures and starts the selected session.  Exercise
            # that action, then leave the scenario clean for the next step.
            self._capture_ui_connection(timeout)
            self._disconnect_ui(timeout)
            return {"configured": True}
        if step.operation == "connect":
            self._capture_baseline(timeout)
            self._connect_ui(timeout)
            return {}
        if step.operation == "disconnect":
            self._disconnect_ui(timeout)
            clean = self.base._cleanup_verified(timeout)
            key = "final_disconnect_clean" if step.id == "final-disconnect" else "disconnect_clean"
            return {key: clean}
        if step.operation == "reconnect":
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
            return result
        return self.base.execute(step)

    def _capture_baseline(self, timeout: float) -> None:
        capture = getattr(self.base, "_capture_baseline", None)
        if not callable(capture):
            raise ScenarioExecutionError("ROUTING_BASELINE_UNAVAILABLE")
        capture(timeout)

    def _capture_ui_connection(self, timeout: float) -> None:
        self._connect_ui(timeout)

    def _connect_ui(self, timeout: float) -> None:
        if self._ui_connected:
            return
        result = self._request(
            {
                "op": "connect",
                "config": self._profile_text,
                "timeout_ms": max(1, int(timeout * 1000)),
            },
            timeout,
        )
        if not result.ok or result.status != "Connected":
            raise ScenarioExecutionError("UI_CONNECT_FAILED")
        self._ui_connected = True

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
                error.add_note(f"platform reset also failed: {type(failure).__name__}")
        if error is not None:
            raise error

    def finalize(self, timeout_seconds: float = 30.0, *, deadline: float | None = None) -> None:
        error: BaseException | None = None
        try:
            self._close_process(timeout_seconds, deadline=deadline)
        except BaseException as failure:
            error = failure
        try:
            self.base.finalize(timeout_seconds, deadline=deadline)
        except BaseException as failure:
            if error is None:
                error = failure
            else:
                error.add_note(f"platform finalization also failed: {type(failure).__name__}")
        if error is not None:
            raise error

    def _emit(self, event: str, **fields: object) -> None:
        emitter = getattr(self.base, "_emit_progress", None)
        if callable(emitter):
            emitter(event, **fields)

    def _raw_directory(self) -> Path:
        raw = getattr(self.runner, "raw_directory", None)
        if isinstance(raw, Path):
            raw.mkdir(parents=True, exist_ok=True)
            return raw
        return Path.cwd()

    def _start_process(self, timeout: float) -> None:
        if self._process is not None:
            return
        stderr_path = self._raw_directory() / "ui-companion.stderr"
        self._stderr = stderr_path.open("ab")
        environment = dict(getattr(self.runner, "environment", os.environ))
        deadline = time.monotonic() + min(max(timeout, 0.1), _UI_START_TIMEOUT_SECONDS)
        try:
            self._process = popen_with_windows_job(
                subprocess.Popen,
                [str(self.ui_test)],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=self._stderr,
                env=environment,
                text=True,
                encoding="utf-8",
                bufsize=1,
                start_new_session=(os.name != "nt"),
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) if os.name == "nt" else 0,
                stage="ui-companion-start",
                deadline=deadline,
            )
        except Exception as error:
            self._close_stderr()
            raise ScenarioExecutionError("UI_TEST_LAUNCH_FAILED") from error

        def read_responses() -> None:
            assert self._process is not None
            stdout = self._process.stdout
            if stdout is None:
                self._responses.put(None)
                return
            try:
                for line in stdout:
                    self._responses.put(line)
            finally:
                self._responses.put(None)

        self._reader_thread = threading.Thread(target=read_responses, name="dobbyvpn-ui-test-reader", daemon=True)
        self._reader_thread.start()

    def _request(self, document: Mapping[str, object], timeout: float) -> _UIResponse:
        if timeout <= 0:
            raise ScenarioExecutionError("UI_TIMEOUT")
        with self._request_lock:
            self._start_process(timeout)
            process = self._process
            if process is None or process.stdin is None:
                raise ScenarioExecutionError("UI_TEST_UNAVAILABLE")
            try:
                process.stdin.write(json.dumps(document, separators=(",", ":")) + "\n")
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
                raise ScenarioExecutionError("UI_TEST_EXITED")
            try:
                result = _response(json.loads(line))
            except (TypeError, ValueError) as error:
                raise ScenarioExecutionError("UI_RESPONSE_INVALID") from error
            if not result.ok and result.error:
                failure = ScenarioExecutionError("UI_ACTION_FAILED")
                failure.add_note("ui_status=" + result.status)
                raise failure
            return result

    def _close_process(self, timeout: float, *, deadline: float | None) -> None:
        process = self._process
        if process is None:
            self._close_stderr()
            return
        close_deadline = deadline if deadline is not None else time.monotonic() + max(timeout, 0.1)
        try:
            if process.poll() is None:
                try:
                    self._request({"op": "close"}, max(0.1, close_deadline - time.monotonic()))
                except BaseException:
                    pass
                if process.poll() is None:
                    if os.name == "nt":
                        cleanup = terminate_windows_job(
                            process, deadline=close_deadline, stage="ui-companion-finalize"
                        )
                        if not cleanup.process_tree_proven:
                            raise ScenarioExecutionError("UI_TEST_CLEANUP_FAILED")
                    else:
                        try:
                            os.killpg(process.pid, 15)
                        except ProcessLookupError:
                            pass
                    try:
                        process.wait(timeout=max(0.1, close_deadline - time.monotonic()))
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=1)
            if os.name == "nt":
                close_windows_job(process, stage="ui-companion-finalize", deadline=close_deadline)
        finally:
            for stream in (process.stdin, process.stdout):
                if stream is not None:
                    try:
                        stream.close()
                    except OSError:
                        pass
            self._process = None
            self._close_stderr()

    def _close_stderr(self) -> None:
        stream = self._stderr
        self._stderr = None
        if stream is not None:
            try:
                stream.close()
            except OSError:
                pass


__all__ = ["HeadlessUIAdapter"]
