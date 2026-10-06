"""Drive the packaged frontend through the test-only native accessibility helper."""
from __future__ import annotations

from contextlib import contextmanager
import json
import math
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import sys
import time

# Also importable by the standalone diagnostic test loader.
_TORTURER_ROOT = Path(__file__).resolve().parents[2]
if str(_TORTURER_ROOT) not in sys.path:
    sys.path.insert(0, str(_TORTURER_ROOT))
from torturer_runner.native_cases import WINDOWS_FINDALL_PROBE_CASE
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
                 *, helper: Path, screenshot_dir: Path,
                 native_cases: tuple[str, ...] = ()) -> None:
        if platform not in {"macos", "windows"} or not math.isfinite(timeout) or timeout <= 0:
            raise ValueError("native UI requires a desktop platform and a finite positive timeout")
        if not helper.is_file():
            raise NativeUISmokeError(f"prepared native helper is missing: {helper}")
        self.platform, self.binary, self.profile, self.helper = platform, binary, profile, helper
        self.native_cases = frozenset(native_cases)
        self.native_case_results: dict[str, dict[str, object]] = {}
        self.executable = (binary / "Contents/MacOS/DobbyVPNMacApp" if platform == "macos" else binary).resolve()
        self.screenshot_dir = screenshot_dir
        screenshot_dir.mkdir(parents=True, exist_ok=True)
        self.logs = screenshot_dir.parent
        self._timeout = timeout
        self._deadline: float | None = None
        self.process: subprocess.Popen | None = None
        self.pid: int | None = None
        self.identity: str | None = None
        self.window_id: str | None = None
        self.last_window_readiness: dict[str, object] | None = None
        self.last_paste_invoked_at_unix_ms: int | None = None
        self.launch_count = self.capture_count = 0
        self.reconnecting_seen = False
        self.cleared_record: str | None = None

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

    def _call(self, operation: str, *, unbound: bool = False, **fields) -> dict:
        request = {"operation": operation, "executable": str(self.executable), **fields}
        if self.pid is not None and not unbound:
            request["pid"] = self.pid
        if self.identity is not None and not unbound:
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
        if self.process is not None or self._alive():
            raise NativeUISmokeError("native UI is already running")
        if self.platform == "macos":
            self._call("preflight")
            if self._call("probe").get("alive"):
                raise NativeUISmokeError("candidate UI is already running before launch")
        self.launch_count += 1
        self.window_id = None
        self.last_window_readiness = None
        prefix = self.logs / f"{self.platform}-app-{self.launch_count:02d}"
        command = [str(self.binary)]
        if self.platform == "macos":
            command = ["open", "-W", "-n"]
            for name in ("HOME", "DOBBYVPN_CONTROL_SOCKET", "DOBBY_LOG_PATH"):
                if value := os.environ.get(name):
                    command.extend(("--env", f"{name}={value}"))
        link = None
        if import_url is not None:
            from urllib.parse import quote
            link = "dobbyvpn://import?url=" + quote(import_url, safe="")
        if self.platform == "macos":
            if link is None:
                command.extend(("--stdout", str(prefix) + ".stdout.log", "--stderr", str(prefix) + ".stderr.log", str(self.binary)))
            else:
                command.append(link)
            with Path(str(prefix) + ".launcher.stdout.log").open("xb") as stdout, Path(str(prefix) + ".launcher.stderr.log").open("xb") as stderr:
                self.process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr)
        elif self.platform == "windows":
            os.startfile(link if link is not None else str(self.binary))  # type: ignore[attr-defined]
            self.process = None
            time.sleep(0.25)
        else:
            with Path(str(prefix) + ".launcher.stdout.log").open("xb") as stdout, Path(str(prefix) + ".launcher.stderr.log").open("xb") as stderr:
                self.process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr)
        if self.platform == "windows":
            if self.process is not None:
                self.pid = self.process.pid

            def identified():
                code = None if self.process is None else self.process.poll()
                if code is not None:
                    raise NativeUISmokeError(f"native UI launcher exited {code}; streams: {prefix}")
                response = self._call("probe")
                return response.get("alive") is True and self.identity is not None

            self._wait(identified, "native UI process identity unavailable")
            # Keep the diagnostic opt-in and ahead of the first acceptance tree snapshot.
            if self.launch_count == 1 and WINDOWS_FINDALL_PROBE_CASE in self.native_cases:
                result = self._call("findall-probe")
                if result.get("ready") is not True:
                    raise NativeUISmokeError("Windows UI Automation FindAll probe could not inspect the visible control")
                self.native_case_results[WINDOWS_FINDALL_PROBE_CASE] = result
                return {}

        def ready():
            code = None if self.process is None else self.process.poll()
            if code is not None and (self.platform == "windows" or code != 0):
                raise NativeUISmokeError(f"native UI launcher exited {code}; streams: {prefix}")
            state = self.snapshot()
            return state["status"] != "Unknown" and "Connection configuration" in state["labels"]

        try:
            self._wait(ready, "native UI did not expose its connection page")
        except NativeUISmokeError as error:
            if self.platform == "windows" and self.last_window_readiness is not None:
                details = json.dumps(self.last_window_readiness, sort_keys=True, separators=(",", ":"))
                raise NativeUISmokeError(
                    f"{error}; last Windows window-readiness response: {details}"
                ) from error
            raise
        self._call("focus")
        self.capture("startup")
        return self.snapshot()

    def _open_link(self, link: str) -> None:
        if self.platform == "windows":
            os.startfile(link)  # type: ignore[attr-defined]
            return
        command = ["open"]
        if self.platform == "macos":
            for name in ("HOME", "DOBBYVPN_CONTROL_SOCKET", "DOBBY_LOG_PATH"):
                if value := os.environ.get(name):
                    command.extend(("--env", f"{name}={value}"))
        command.append(link)
        result = _native_run(command, timeout_seconds=self.timeout)
        if result.returncode:
            raise subprocess.CalledProcessError(result.returncode, command, result.stdout, result.stderr)

    def bare_link(self) -> dict:
        before = self._call("probe")
        self._open_link("dobbyvpn://")
        time.sleep(0.25)
        after = self._call("probe")
        if after.get("pid") != before.get("pid") or after.get("identity") != before.get("identity"):
            raise NativeUISmokeError("bare deep link did not reuse the existing UI process")
        return self.snapshot()

    def snapshot(self) -> dict:
        value = self._call("tree")
        if self.platform == "windows" and value.get("ready") is False:
            diagnostic_keys = (
                "windowHandle",
                "visible",
                "minimized",
                "ownerPid",
                "candidateSessionId",
                "helperSessionId",
                "mainWindowTitle",
                "windowDescription",
                "processTopLevelWindows",
            )
            self.last_window_readiness = {
                key: value[key] for key in diagnostic_keys if key in value
            }
        elif self.platform == "windows" and value.get("ready") is True:
            self.last_window_readiness = None
        if self.platform == "macos" and value.get("ready") is True:
            window_id = value.get("window_id")
            if value.get("window_count") != 1 or not window_id:
                raise NativeUISmokeError(f"expected one identifiable native window: {value}")
            if self.window_id is not None and window_id != self.window_id:
                raise NativeUISmokeError(f"native window changed within one launch: {self.window_id} -> {window_id}")
            self.window_id = window_id
        labels = value.get("labels", [])
        status = next((name for name in ("Connected", "Connecting", "Reconnecting", "Disconnected", "Stopping", "Failed", "Error") if name in labels), "Unknown")
        self.reconnecting_seen |= status == "Reconnecting"
        return {
            "status": status,
            "labels": labels,
            "enabled_controls": value.get("enabled_controls", []),
            "help_texts": value.get("help_texts", []),
            "link_urls": value.get("link_urls", []),
            "reconnecting_seen": self.reconnecting_seen,
        }

    def paste_source(self) -> dict:
        result = self._call("paste", source=str(self.profile))
        if result.get("ready") is not True:
            raise NativeUISmokeError("native Paste control is unavailable")
        paste_invoked_at = result.get("paste_invoked_at_unix_ms")
        if self.platform == "windows":
            if type(paste_invoked_at) is not int:
                raise NativeUISmokeError("Windows Paste helper did not report its button-invocation time")
            self.last_paste_invoked_at_unix_ms = paste_invoked_at
        return self.snapshot()

    def configure(self) -> dict:
        self.paste_source()
        self._wait(lambda: "Profile 1 action" in self.snapshot()["labels"], "subscription profiles did not load automatically")
        result = {"input_verified": True, **self.snapshot()}
        if self.last_paste_invoked_at_unix_ms is not None:
            result["paste_invoked_at_unix_ms"] = self.last_paste_invoked_at_unix_ms
        return result

    def type_source(self, source: str) -> dict:
        self.profile.write_text(source, encoding="utf-8")
        result = self._call("type", source=str(self.profile))
        if result.get("ready") is not True:
            raise NativeUISmokeError("configuration input is unavailable")
        return self.snapshot()

    def select_profile(self, index: int) -> dict:
        self.activate_profile(index)
        return self.wait_status("Connected")

    def activate_profile(self, index: int) -> None:
        self._click(f"Profile {index + 1} action")

    def open_deep_link(self, link: str) -> dict:
        self._open_link(link)
        return self.snapshot()

    def cold_deep_link(self, url: str) -> dict:
        if self.platform not in {"macos", "windows"}:
            raise NativeUISmokeError("cold external-scheme launch is only available on desktop platforms")
        if self.process is not None or self._alive():
            raise NativeUISmokeError("native UI must be stopped before a cold external-scheme launch")
        from urllib.parse import quote

        if self.platform == "macos":
            self._call("preflight")
        self.launch_count += 1
        self.window_id = None
        self.last_window_readiness = None
        link = "dobbyvpn://import?url=" + quote(url, safe="")
        self._open_link(link)
        self._wait(
            lambda: "Connection configuration" in self.snapshot()["labels"],
            "cold external-scheme launch did not open the native connection page",
        )
        return self.wait_status("Connected")

    def failing_subscription(self, url: str) -> dict:
        original = self.profile.read_bytes()
        try:
            self.type_source(url)
            self._wait(lambda: "Retry" in self.snapshot()["labels"], "failed subscription did not expose Retry")
            return self.snapshot()
        finally:
            self.profile.write_bytes(original)

    def retry(self) -> dict:
        self._click("Retry")
        self._wait(
            lambda: "Retry" not in self.snapshot()["labels"] and "Profile 1 action" in self.snapshot()["labels"],
            "Retry did not load subscription profiles",
        )
        return self.snapshot()

    def import_link(self, url: str) -> dict:
        from urllib.parse import quote
        link = "dobbyvpn://import?url=" + quote(url, safe="")
        self.profile.write_text(url, encoding="utf-8")
        before = self._call("probe")
        identity_before, pid_before = before.get("identity"), before.get("pid")
        for _ in range(2):
            self._open_link(link)
        time.sleep(0.25)
        # Windows' helper rejects duplicate matching processes when it probes
        # without a PID, then returns the existing process identity.
        after = self._call("probe", unbound=self.platform == "windows")
        if after.get("pid") != pid_before or after.get("identity") != identity_before:
            raise NativeUISmokeError("warm deep link did not reuse the existing UI process")
        self._wait(lambda: "Retry" not in self.snapshot()["labels"], "import did not replace failed subscription")
        self._wait(lambda: "Profile 1 action" in self.snapshot()["labels"], "imported profiles are unavailable")
        return self.snapshot()

    def clear_logs(self) -> dict:
        previous = ""
        initial_view: dict = {}
        def live_logs():
            nonlocal previous, initial_view
            initial_view = self._call("logs")
            text = initial_view.get("text", "")
            previous = next((line for line in text.splitlines() if " · " in line), "")
            return bool(previous)
        self._wait(live_logs, "native log view did not show structured live records")
        if self.platform == "windows":
            rendered = initial_view.get("entries", [])
            if not any(isinstance(entry, dict) and " · " in entry.get("text", "") for entry in rendered):
                raise NativeUISmokeError("Windows log entries did not expose rendered structured text")
            if not initial_view.get("expansion_verified"):
                raise NativeUISmokeError("Windows structured log Details did not reveal the original record")
            try:
                original_record = json.loads(initial_view.get("expanded_record", ""))
            except (json.JSONDecodeError, TypeError) as error:
                raise NativeUISmokeError("Windows log Details did not preserve a JSON record") from error
            if not isinstance(original_record, dict):
                raise NativeUISmokeError("Windows log Details did not preserve a structured record")
            selected = self._call("select-log-text").get("selected", "")
            if " · " not in selected:
                raise NativeUISmokeError("Windows native log text could not be selected")
            palette: dict[str, int] = {}
            for entry in rendered:
                if not isinstance(entry, dict) or not isinstance(entry.get("foreground"), int):
                    continue
                line = entry.get("text", "").splitlines()[0]
                fields = line.split(" · ")
                if len(fields) >= 3 and fields[1] in {"DEBUG", "TRACE", "INFO", "WARN", "WARNING", "ERROR", "FATAL", "PANIC"}:
                    palette.setdefault(fields[1], entry["foreground"])
            if not palette:
                raise NativeUISmokeError("Windows rendered log severity color was unavailable through native accessibility")
            def color_orders(color: int) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
                return (
                    (color & 0xFF, (color >> 8) & 0xFF, (color >> 16) & 0xFF),
                    ((color >> 16) & 0xFF, (color >> 8) & 0xFF, color & 0xFF),
                )
            for severity in ("ERROR", "FATAL", "PANIC"):
                if severity in palette:
                    if not any(red > green and red > blue for red, green, blue in color_orders(palette[severity])):
                        raise NativeUISmokeError(f"Windows {severity} logs are not rendered in a red severity color")
            for severity in ("WARN", "WARNING"):
                if severity in palette:
                    if not any(red >= green > blue for red, green, blue in color_orders(palette[severity])):
                        raise NativeUISmokeError(f"Windows {severity} logs are not rendered in an amber severity color")
            if "INFO" in palette:
                for warning in ("WARN", "WARNING"):
                    if warning in palette and palette[warning] == palette["INFO"]:
                        raise NativeUISmokeError("Windows warning logs use the information text color")
                for error_level in ("ERROR", "FATAL", "PANIC"):
                    if error_level in palette and palette[error_level] == palette["INFO"]:
                        raise NativeUISmokeError("Windows error logs use the information text color")
                for quiet in ("DEBUG", "TRACE"):
                    if quiet in palette and palette[quiet] == palette["INFO"]:
                        raise NativeUISmokeError("Windows debug/trace logs are not visually muted")

            self._call("scroll-logs", position="top")
            frozen_position = self._call("log-position")
            frozen = self._call("logs").get("text", "")
            original_url = self.profile.read_text(encoding="utf-8").strip()
            failure_url = original_url.rsplit("/", 1)[0] + "/missing"
            try:
                self.failing_subscription(failure_url)
            finally:
                self.profile.write_text(original_url, encoding="utf-8")
            if self._call("logs").get("text", "") != frozen:
                raise NativeUISmokeError("Windows log entries changed while the view was scrolled up")
            current_position = self._call("log-position")
            before_percent = frozen_position.get("vertical_scroll_percent")
            after_percent = current_position.get("vertical_scroll_percent")
            if (not isinstance(before_percent, (int, float)) or isinstance(before_percent, bool)
                    or not isinstance(after_percent, (int, float)) or isinstance(after_percent, bool)
                    or abs(float(after_percent) - float(before_percent)) > 1.0):
                raise NativeUISmokeError(
                    "Windows reading position changed while log following was frozen: "
                    f"before={frozen_position} after={current_position}"
                )
            self._call("scroll-logs", position="bottom")
            latest = ""
            self._wait(
                lambda: bool((latest := self._call("logs").get("text", ""))) and latest != frozen,
                "Windows log following did not resume at the bottom",
            )
        elif self.platform == "macos":
            selected = self._call("select-log-text").get("selected", "")
            if " · " not in selected:
                raise NativeUISmokeError("macOS native log text could not be selected")
            self._call("scroll-logs", position="top")
            frozen_position = self._call("log-position")
            frozen = self._call("logs").get("text", "")
            original_url = self.profile.read_text(encoding="utf-8").strip()
            failure_url = original_url.rsplit("/", 1)[0] + "/missing"
            try:
                self.failing_subscription(failure_url)
            finally:
                self.profile.write_text(original_url, encoding="utf-8")
            if self._call("logs").get("text", "") != frozen:
                raise NativeUISmokeError("macOS log entries changed while the view was scrolled up")
            current_position = self._call("log-position")
            if any(current_position.get(key) != frozen_position.get(key)
                   for key in ("visible_range_start", "visible_range_end")):
                raise NativeUISmokeError(
                    "macOS reading position changed while log following was frozen: "
                    f"before={frozen_position} after={current_position}"
                )
            self._call("scroll-logs", position="bottom")
            latest = ""
            self._wait(
                lambda: bool((latest := self._call("logs").get("text", ""))) and latest != frozen,
                "macOS log following did not resume at the bottom",
            )

        self._click("Clear")
        def cleared():
            view = self._call("logs")
            if view.get("ready") is not True:
                return False
            text = view.get("text", "")
            return not text.strip() if self.platform == "windows" else previous not in text
        self._wait(cleared, "Clear did not empty the Windows log view" if self.platform == "windows" else "Clear left the previous records visible")
        self.cleared_record = previous
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
        candidate_metadata = None
        if self.platform == "macos":
            try:
                with (self.binary / "Contents" / "Info.plist").open("rb") as stream:
                    metadata = plistlib.load(stream)
            except (OSError, plistlib.InvalidFileException, ValueError) as error:
                raise NativeUISmokeError("candidate macOS About metadata is unavailable") from error
            version = metadata.get("CFBundleShortVersionString") if isinstance(metadata, dict) else None
            commit = metadata.get("DobbySourceCommit") if isinstance(metadata, dict) else None
            if not isinstance(version, str) or not version or not isinstance(commit, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", commit):
                raise NativeUISmokeError("candidate macOS About metadata is incomplete")
            candidate_metadata = (version, commit)

        def metadata():
            state = self.snapshot()
            labels = state["labels"]
            if self.platform == "macos":
                assert candidate_metadata is not None
                version, commit = candidate_metadata
                source_url = f"https://github.com/DobbyVPN/DobbyVPN/tree/{commit}"
                return (
                    "About version metadata" in labels
                    and f"Version: {version}" in labels
                    and f"Commit: {commit[:12]}" in labels
                    and "About source commit metadata" in labels
                    and f"Source commit: {commit}" in labels
                    and source_url in state["link_urls"]
                )
            if self.platform != "windows":
                return all(any(label.startswith(prefix) for label in labels) for prefix in ("Version:", "Source commit:"))
            version = next((label.removeprefix("Version: ") for label in labels if label.startswith("Version: ")), "")
            compact = next((label.removeprefix("Commit: ") for label in labels if label.startswith("Commit: ")), "")
            commit = next((label.removeprefix("Source commit: ") for label in labels if label.startswith("Source commit: ")), "")
            source_url = f"https://github.com/DobbyVPN/DobbyVPN/tree/{commit}"
            return (
                re.fullmatch(r"\d+\.\d+\.\d+", version) is not None
                and re.fullmatch(r"[0-9a-fA-F]{40}", commit) is not None
                and compact == commit[:12]
                and source_url in state["help_texts"]
                and "About source link" in labels
            )
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
        if self.process is None and not self._alive():
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
        if self.process is not None or self._alive():
            if self._alive():
                self._call("close")
                self._wait(lambda: not self._alive(), "native UI did not close")
            self._reap()
        return {"closed": True}

    def reopen(self) -> dict:
        self.start(import_url=self.profile.read_text(encoding="utf-8").strip() + "?cold=1")
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
        if self.process is None and not self._alive():
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
