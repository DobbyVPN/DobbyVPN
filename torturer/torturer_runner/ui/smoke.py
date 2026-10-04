"""Drive the packaged frontend through the test-only native accessibility helper."""
from __future__ import annotations

from contextlib import contextmanager
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

# Also importable by the standalone diagnostic test loader.
_TORTURER_ROOT = Path(__file__).resolve().parents[2]
if str(_TORTURER_ROOT) not in sys.path:
    sys.path.insert(0, str(_TORTURER_ROOT))
from torturer_runner.diagnostics import add_exception_notes, add_stream_notes, emit_streams
from torturer_runner.process_capture import run_finite_capture
from torturer_runner.screenshot_artifacts import nonblank_png_dimensions
from torturer_runner.windows_job import (
    WindowsJobError,
    close_for as close_windows_job,
    popen_with_windows_job,
    terminate_and_prove_empty as terminate_windows_job,
)


class NativeUISmokeError(RuntimeError):
    pass


def _windows_job_capture_callbacks(deadline: float):
    stage = "native-ui-helper"
    timeout_stage = f"{stage}-timeout"

    def spawn(command: list[str], **popen_kwargs):
        return popen_with_windows_job(
            subprocess.Popen,
            command,
            stage=stage,
            deadline=deadline,
            **popen_kwargs,
        )

    def terminate(process: subprocess.Popen[bytes], cleanup_deadline: float) -> None:
        cleanup = terminate_windows_job(
            process,
            deadline=cleanup_deadline,
            stage=timeout_stage,
        )
        if not cleanup.process_tree_proven:
            diagnostics = cleanup.diagnostics or (
                "api=JobObject detail=tree-not-proven "
                f"active_processes={cleanup.active_processes}",
            )
            raise WindowsJobError(timeout_stage, diagnostics)

    def close(process: subprocess.Popen[bytes], cleanup_deadline: float) -> None:
        result = close_windows_job(
            process,
            stage=f"{stage}-cleanup",
            deadline=cleanup_deadline,
        )
        if result.failed:
            diagnostics = result.diagnostics or ("api=JobObject detail=close-failed",)
            raise WindowsJobError(f"{stage}-cleanup", diagnostics)

    return spawn, terminate, close


def _native_run(command: list[str], **kwargs) -> subprocess.CompletedProcess[bytes]:
    """Deliver complete helper streams before interpreting its response."""
    try:
        result = run_finite_capture(command, **kwargs)
    except BaseException as error:
        try:
            emit_streams("native-helper", getattr(error, "stdout", None), getattr(error, "stderr", None))
        except BaseException as delivery:
            add_stream_notes(
                error,
                "native-helper",
                getattr(error, "stdout", None),
                getattr(error, "stderr", None),
            )
            add_exception_notes(error, "diagnostic-delivery", delivery)
        raise
    try:
        emit_streams("native-helper", result.stdout, result.stderr)
    except BaseException as delivery:
        add_stream_notes(delivery, "native-helper", result.stdout, result.stderr)
        delivery.stdout = result.stdout
        delivery.stderr = result.stderr
        raise
    return result


class NativeUIController:
    def __init__(self, platform: str, binary: Path, profile: Path, timeout: float,
                 *, helper: Path, screenshot_dir: Path) -> None:
        if platform not in {"macos", "windows"} or not math.isfinite(timeout) or timeout <= 0:
            raise ValueError("native UI requires a desktop platform and a finite positive timeout")
        if not helper.is_file():
            raise NativeUISmokeError(f"prepared native helper is missing: {helper}")
        self.platform, self.binary, self.profile, self.helper = platform, binary, profile, helper
        self.executable = (binary / "Contents/MacOS/DobbyVPNMacApp" if platform == "macos" else binary).resolve()
        self.screenshot_dir = screenshot_dir
        screenshot_dir.mkdir(parents=True, exist_ok=True)
        self.logs = screenshot_dir.parent
        self._timeout = timeout
        self._deadline: float | None = None
        self.process: subprocess.Popen | None = None
        self.pid: int | None = None
        self.identity: str | None = None
        self.launch_count = self.capture_count = 0
        self.reconnecting_seen = False

    @property
    def timeout(self) -> float:
        remaining = self._timeout if self._deadline is None else min(self._timeout, self._deadline - time.monotonic())
        if remaining <= 0:
            raise NativeUISmokeError("native UI action deadline expired")
        return remaining

    @contextmanager
    def bounded_by(self, timeout: float):
        previous = self._deadline
        deadline = time.monotonic() + timeout
        self._deadline = deadline if previous is None else min(previous, deadline)
        try:
            yield
        finally:
            self._deadline = previous

    def _call(self, operation: str, **fields) -> dict:
        request = {"operation": operation, "executable": str(self.executable), **fields}
        if self.pid is not None:
            request["pid"] = self.pid
        if self.identity is not None:
            request["identity"] = self.identity
        available = self.timeout
        cleanup_timeout = min(2.0, available / 3)
        operation_timeout = min(10.0, available - cleanup_timeout)
        capture_callbacks = {}
        if self.platform == "windows":
            operation_deadline = time.monotonic() + operation_timeout
            spawn, terminate, close = _windows_job_capture_callbacks(operation_deadline)
            capture_callbacks = {
                "popen_factory": spawn,
                "terminate": terminate,
                "close_boundary": close,
            }
        result = _native_run(
            [str(self.helper)],
            timeout_seconds=operation_timeout,
            input_bytes=json.dumps(request).encode("utf-8"),
            termination_grace_seconds=cleanup_timeout / 2,
            cleanup_timeout_seconds=cleanup_timeout,
            **capture_callbacks,
        )
        if result.returncode:
            raise subprocess.CalledProcessError(result.returncode, result.args, result.stdout, result.stderr)
        response = json.loads(result.stdout)
        if not isinstance(response, dict):
            raise NativeUISmokeError("invalid native helper response")
        if "pid" in response:
            self.pid, self.identity = int(response["pid"]), str(response["identity"])
            marker = os.environ.get("DOBBYVPN_NATIVE_UI_CHILD_PID_FILE")
            if marker and self.platform == "windows":
                Path(marker).write_text(f"{self.pid}|{self.identity}", encoding="ascii")
        return response

    def _wait(self, predicate, message: str) -> None:
        with self.bounded_by(self.timeout):
            while not predicate():
                if self.timeout < 0.1:
                    raise NativeUISmokeError(message)
                time.sleep(min(0.1, self.timeout))

    def start(self, import_url: str | None = None) -> dict:
        if self.process is not None:
            raise NativeUISmokeError("native UI is already running")
        if self.platform == "macos":
            self._call("preflight")
            if self._call("probe").get("alive"):
                raise NativeUISmokeError("candidate UI is already running before launch")
        self.launch_count += 1
        prefix = self.logs / f"{self.platform}-app-{self.launch_count:02d}"
        command = [str(self.binary)]
        if self.platform == "macos":
            command = ["open", "-W", "-n"]
            for name in ("HOME", "DOBBYVPN_CONTROL_SOCKET"):
                if value := os.environ.get(name):
                    command.extend(("--env", f"{name}={value}"))
            command.extend(("--stdout", str(prefix) + ".stdout.log", "--stderr", str(prefix) + ".stderr.log", str(self.binary)))
        if import_url is not None:
            from urllib.parse import quote
            link = "dobbyvpn://import?url=" + quote(import_url, safe="")
            if self.platform == "macos":
                command.insert(len(command) - 1, "-a")
            command.append(link)
        with Path(str(prefix) + ".launcher.stdout.log").open("xb") as stdout, Path(str(prefix) + ".launcher.stderr.log").open("xb") as stderr:
            self.process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr)
        if self.platform == "windows":
            self.pid = self.process.pid

            def identified():
                code = self.process.poll()
                if code is not None:
                    raise NativeUISmokeError(f"native UI launcher exited {code}; streams: {prefix}")
                response = self._call("probe")
                return response.get("alive") is True and self.identity is not None

            self._wait(identified, "native UI process identity unavailable")

        def ready():
            code = self.process.poll()
            if code is not None and (self.platform == "windows" or code != 0):
                raise NativeUISmokeError(f"native UI launcher exited {code}; streams: {prefix}")
            state = self.snapshot()
            return state["status"] != "Unknown" and "Connection configuration" in state["labels"]

        self._wait(ready, "native UI did not expose its connection page")
        self._call("focus")
        self.capture("startup")
        return self.snapshot()

    def snapshot(self) -> dict:
        value = self._call("tree")
        labels = value.get("labels", [])
        status = next((name for name in ("Connected", "Connecting", "Reconnecting", "Disconnected", "Stopping", "Failed", "Error") if name in labels), "Unknown")
        self.reconnecting_seen |= status == "Reconnecting"
        return {"status": status, "labels": labels, "enabled_controls": value.get("enabled_controls", []), "reconnecting_seen": self.reconnecting_seen}

    def configure(self) -> dict:
        result = self._call("type", source=str(self.profile))
        if result.get("ready") is not True:
            raise NativeUISmokeError("configuration input is unavailable")
        self._wait(lambda: "Profile 1 action" in self.snapshot()["labels"], "subscription profiles did not load automatically")
        return {"input_verified": True, **self.snapshot()}

    def select_profile(self, index: int) -> dict:
        self._click(f"Profile {index + 1} action")
        return self.wait_status("Connected")

    def failing_subscription(self, url: str) -> dict:
        original = self.profile.read_bytes()
        try:
            self.profile.write_text(url, encoding="utf-8")
            result = self._call("type", source=str(self.profile))
            if result.get("ready") is not True:
                raise NativeUISmokeError("subscription input is unavailable")
            self._wait(lambda: "Retry" in self.snapshot()["labels"], "failed subscription did not expose Retry")
            return self.snapshot()
        finally:
            self.profile.write_bytes(original)

    def import_link(self, url: str) -> dict:
        from urllib.parse import quote
        link = "dobbyvpn://import?url=" + quote(url, safe="")
        command = ["open", link] if self.platform == "macos" else [str(self.binary), link]
        result = _native_run(command, timeout_seconds=self.timeout)
        if result.returncode:
            raise subprocess.CalledProcessError(result.returncode, command, result.stdout, result.stderr)
        self._wait(lambda: "Retry" not in self.snapshot()["labels"], "import did not replace failed subscription")
        self._wait(lambda: "Profile 1 action" in self.snapshot()["labels"], "imported profiles are unavailable")
        return self.snapshot()

    def clear_logs(self) -> dict:
        self._click("Clear")
        return self.snapshot()

    def wait_status(self, expected: str, *, allow_errors: bool = False) -> dict:
        state = {}
        def reached():
            nonlocal state
            state = self.snapshot()
            if not allow_errors and state["status"] in {"Error", "Failed"}:
                raise NativeUISmokeError(f"native UI reported {state['status']} while waiting for {expected}: {state}")
            return state["status"] == expected
        self._wait(reached, f"native UI did not display {expected}")
        return state

    def _click(self, name: str) -> None:
        self._wait(lambda: name in self.snapshot()["enabled_controls"], f"native control did not become enabled: {name}")
        if self._call("click", target=name).get("ready") is not True:
            raise NativeUISmokeError(f"native control unavailable: {name}")

    def connect(self) -> dict:
        self._click("VPN connection action")
        return self.wait_status("Connected")

    def disconnect(self) -> dict:
        self._click("VPN connection action")
        return self.wait_status("Disconnected")

    def recover_after_process_loss(self) -> dict:
        self.reconnecting_seen = False
        self.wait_status("Disconnected", allow_errors=True)
        self._wait(lambda: "Auto connect" in self.snapshot()["labels"], "replacement service did not expose Connect")
        self.configure()
        return self.connect()

    def about(self) -> dict:
        self._click("About")
        def metadata():
            labels = self.snapshot()["labels"]
            if self.platform == "macos":
                return all(name in labels for name in ("About version metadata", "About source commit metadata"))
            return all(any(label.startswith(prefix) for label in labels) for prefix in ("Version:", "Source commit:"))
        self._wait(metadata, "About metadata unavailable")
        self.capture("about")
        self._click("Done")
        self._wait(
            lambda: "Connection configuration" in self.snapshot()["labels"],
            "native UI did not return to its connection page after About",
        )
        return {"about_version": True, "about_source_commit": True}

    def capture(self, milestone: str) -> dict:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,79}", milestone):
            raise ValueError("invalid screenshot milestone")
        if self.process is None:
            return {"unavailable": "native window is closed"}
        self.capture_count += 1
        path = self.screenshot_dir / f"{self.capture_count:03d}-{milestone}.png"
        if self._call("capture", path=str(path)).get("ready") is not True:
            raise NativeUISmokeError("native window unavailable for screenshot")
        width, height = nonblank_png_dimensions(path)
        return {"path": str(path), "width": width, "height": height}

    def _alive(self) -> bool:
        if self.platform == "windows" and self.process is not None and self.process.poll() is not None:
            return False
        return self._call("probe").get("alive") is True

    def _reap(self) -> None:
        if self.process is not None:
            self.process.wait(timeout=self.timeout)
        self.process = None
        self.pid = None
        self.identity = None

    def close(self) -> dict:
        if self.process is not None:
            if self._alive():
                self._call("close")
                self._wait(lambda: not self._alive(), "native UI did not close")
            self._reap()
        return {"closed": True}

    def reopen(self) -> dict:
        self.start(import_url=self.profile.read_text(encoding="utf-8").strip())
        return self.wait_status("Connected")

    def collect_diagnostics(self) -> None:
        if self.platform == "macos":
            import pwd

            # LaunchServices uses the account's real home even when the test
            # driver has a disposable HOME for Go's configuration store.
            path = Path(pwd.getpwuid(os.getuid()).pw_dir) / "Library/Logs/DobbyVPN/ui_diagnostics.jsonl"
        else:
            path = Path(os.environ["LOCALAPPDATA"]) / "DobbyVPN/Logs/ui_diagnostics.jsonl"
        for retained in (path, path.with_name(path.name + ".previous")):
            try:
                source = retained.open("rb")
            except FileNotFoundError:
                # Native diagnostics and their previous generation are created
                # only when needed; do not manufacture empty placeholders.
                continue
            with source, (self.logs / retained.name).open("wb") as destination:
                shutil.copyfileobj(source, destination)

    def close_for_cleanup(self) -> None:
        if self.process is None:
            return
        try:
            with self.bounded_by(min(5, self.timeout / 2)):
                self.close()
        except BaseException as error:
            try:
                if self._alive():
                    self._call("kill")
                    self._wait(lambda: not self._alive(), "owned UI did not terminate")
                self._reap()
            except BaseException as cleanup:
                add_exception_notes(error, "forced-ui-cleanup", cleanup)
            raise
