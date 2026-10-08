"""Drive the packaged frontend through the test-only native accessibility helper."""
from __future__ import annotations

import base64
from collections import Counter
from contextlib import contextmanager
from datetime import datetime, timezone
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
import traceback

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


_WINDOWS_TEST_THEME_VARIABLE = "DOBBYVPN_TEST_REQUESTED_THEME"
_WINDOWS_PALETTE_SEVERITIES = ("DEBUG", "INFO", "WARN", "ERROR")
_WINDOWS_TEXT_SIZE_SETTINGS_URI = "ms-settings:easeofaccess-display"


def _windows_color_orders(color: int) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
    """Return both channel orders accepted by the existing Windows UIA provider."""
    return (
        (color & 0xFF, (color >> 8) & 0xFF, (color >> 16) & 0xFF),
        ((color >> 16) & 0xFF, (color >> 8) & 0xFF, color & 0xFF),
    )


def _measure_windows_palette_pixels(
    screenshot: dict[str, object], row_bounds: object
) -> dict[str, object]:
    """Measure one visible row's composited foreground from its captured PNG."""
    path_value = screenshot.get("path")
    if not isinstance(path_value, str) or not path_value:
        raise NativeUISmokeError("Windows palette screenshot path was unavailable")
    path = Path(path_value)

    def rect(value: object, label: str) -> tuple[float, float, float, float]:
        if not isinstance(value, dict):
            raise NativeUISmokeError(f"Windows palette {label} rectangle was unavailable")
        result: list[float] = []
        for key in ("x", "y", "width", "height"):
            item = value.get(key)
            if type(item) not in (int, float) or not math.isfinite(item):
                raise NativeUISmokeError(f"Windows palette {label} has invalid {key}: {value!r}")
            result.append(float(item))
        if result[2] <= 0 or result[3] <= 0:
            raise NativeUISmokeError(f"Windows palette {label} rectangle is empty: {value!r}")
        return tuple(result)  # type: ignore[return-value]

    screen_x, screen_y, screen_width, screen_height = rect(
        screenshot.get("screen_bounds"), "screenshot screen-bounds"
    )
    expected_width, expected_height = screenshot.get("width"), screenshot.get("height")
    if (
        type(expected_width) is not int or type(expected_height) is not int
        or screen_width != expected_width or screen_height != expected_height
    ):
        raise NativeUISmokeError(
            "Windows palette screenshot dimensions do not match its physical screen rectangle: "
            f"image={expected_width!r}x{expected_height!r} "
            f"screen={screen_width!r}x{screen_height!r}"
        )
    row_x, row_y, row_width, row_height = rect(row_bounds, "row")
    if (
        row_x < screen_x or row_y < screen_y
        or row_x + row_width > screen_x + screen_width
        or row_y + row_height > screen_y + screen_height
    ):
        raise NativeUISmokeError(
            f"Windows palette row lies outside the captured window: "
            f"row={row_bounds!r} screen={screenshot.get('screen_bounds')!r}"
        )

    try:
        width, height = nonblank_png_dimensions(path)
        if (width, height) != (expected_width, expected_height):
            raise NativeUISmokeError(
                "Windows palette PNG dimensions do not match the capture response: "
                f"png={width}x{height} response={expected_width}x{expected_height}"
            )
        from PIL import Image

        with Image.open(path) as source:
            if source.format != "PNG":
                raise NativeUISmokeError(f"Windows palette screenshot is not PNG: {path}")
            source.load()
            image = source.convert("RGB")
    except NativeUISmokeError:
        raise
    except Exception as error:
        raise NativeUISmokeError(
            f"Windows palette screenshot could not be decoded: {path}: {error}"
        ) from error

    left = math.ceil(row_x - screen_x)
    top = math.ceil(row_y - screen_y)
    right = math.floor(row_x + row_width - screen_x)
    bottom = math.floor(row_y + row_height - screen_y)
    if left < 0 or top < 0 or right > width or bottom > height or left >= right or top >= bottom:
        raise NativeUISmokeError(
            f"Windows palette row produced an invalid screenshot crop: "
            f"crop=({left},{top},{right},{bottom}) image={width}x{height}"
        )
    crop = image.crop((left, top, right, bottom))
    if crop.size != (right - left, bottom - top):
        raise NativeUISmokeError("Windows palette row crop dimensions changed during measurement")
    counts = Counter(crop.get_flattened_data())
    background, background_count = counts.most_common(1)[0]
    candidates = [
        (
            sum((channel - background[index]) ** 2 for index, channel in enumerate(color)),
            color,
            count,
        )
        for color, count in counts.items()
        if count >= 3 and color != background
    ]
    if not candidates:
        raise NativeUISmokeError(
            "Windows palette row crop had no visible foreground color supported by three pixels"
        )
    distance_squared, foreground, foreground_count = max(candidates)

    def luminance(color: tuple[int, int, int]) -> float:
        channels = []
        for component in color:
            value = component / 255.0
            channels.append(
                value / 12.92
                if value <= 0.04045
                else ((value + 0.055) / 1.055) ** 2.4
            )
        return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]

    foreground_luminance, background_luminance = luminance(foreground), luminance(background)
    contrast_ratio = (
        max(foreground_luminance, background_luminance) + 0.05
    ) / (min(foreground_luminance, background_luminance) + 0.05)
    return {
        "foreground_rgb": list(foreground),
        "background_rgb": list(background),
        "foreground_pixels": foreground_count,
        "background_pixels": background_count,
        "distance_squared": distance_squared,
        "contrast_ratio": round(contrast_ratio, 4),
        "crop": {"x": left, "y": top, "width": right - left, "height": bottom - top},
    }


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


_WINDOWS_WER_PATH = r"SOFTWARE\Microsoft\Windows\Windows Error Reporting"
_WINDOWS_LOCAL_DUMPS_PATH = _WINDOWS_WER_PATH + r"\LocalDumps"
_WINDOWS_LOCAL_DUMPS_VALUES = ("DumpFolder", "DumpType", "DumpCount")


class _WindowsLocalDumps:
    """Temporarily enable per-executable WER dumps and restore prior values."""

    def __init__(self, registry=None) -> None:
        if registry is None:
            import winreg as registry
        self.registry = registry
        self._key_states: dict[str, tuple[bool, dict[str, tuple[object, int] | None]]] = {}
        self._wer_path_existed = False
        self._local_dumps_path_existed = False
        self._active = False

    def _access(self, flags: int) -> int:
        return flags | self.registry.KEY_WOW64_64KEY

    def _key_exists(self, path: str) -> bool:
        try:
            key = self.registry.OpenKey(
                self.registry.HKEY_LOCAL_MACHINE,
                path,
                0,
                self._access(self.registry.KEY_READ),
            )
        except FileNotFoundError:
            return False
        key.Close()
        return True

    def _snapshot(self, executable: str) -> tuple[bool, dict[str, tuple[object, int] | None]]:
        path = _WINDOWS_LOCAL_DUMPS_PATH + "\\" + executable
        try:
            key = self.registry.OpenKey(
                self.registry.HKEY_LOCAL_MACHINE,
                path,
                0,
                self._access(self.registry.KEY_READ),
            )
        except FileNotFoundError:
            return False, {name: None for name in _WINDOWS_LOCAL_DUMPS_VALUES}
        values: dict[str, tuple[object, int] | None] = {}
        with key:
            for name in _WINDOWS_LOCAL_DUMPS_VALUES:
                try:
                    value, value_type = self.registry.QueryValueEx(key, name)
                except FileNotFoundError:
                    values[name] = None
                else:
                    values[name] = (value, value_type)
        return True, values

    def enable(self, executable_names: tuple[str, ...], dump_folder: Path) -> None:
        if self._active:
            raise NativeUISmokeError("Windows LocalDumps capture is already active")
        if not executable_names or len(set(executable_names)) != len(executable_names):
            raise ValueError("Windows LocalDumps executable names must be non-empty and unique")
        if any(Path(name).name != name or not name.lower().endswith(".exe") for name in executable_names):
            raise ValueError("Windows LocalDumps executable name is invalid")

        self._wer_path_existed = self._key_exists(_WINDOWS_WER_PATH)
        self._local_dumps_path_existed = self._key_exists(_WINDOWS_LOCAL_DUMPS_PATH)
        for executable in executable_names:
            self._key_states[executable] = self._snapshot(executable)
        self._active = True
        try:
            for executable in executable_names:
                path = _WINDOWS_LOCAL_DUMPS_PATH + "\\" + executable
                with self.registry.CreateKeyEx(
                    self.registry.HKEY_LOCAL_MACHINE,
                    path,
                    0,
                    self._access(self.registry.KEY_READ | self.registry.KEY_WRITE),
                ) as key:
                    self.registry.SetValueEx(
                        key,
                        "DumpFolder",
                        0,
                        self.registry.REG_EXPAND_SZ,
                        str(dump_folder),
                    )
                    self.registry.SetValueEx(key, "DumpType", 0, self.registry.REG_DWORD, 1)
                    self.registry.SetValueEx(key, "DumpCount", 0, self.registry.REG_DWORD, 1)
        except Exception as error:
            try:
                self.restore()
            except Exception as cleanup_error:
                add_exception_notes(error, "LocalDumps setup rollback", cleanup_error)
            raise

    def _delete_key(self, path: str) -> None:
        try:
            self.registry.DeleteKeyEx(
                self.registry.HKEY_LOCAL_MACHINE,
                path,
                self.registry.KEY_WOW64_64KEY,
                0,
            )
        except FileNotFoundError:
            pass

    def restore(self) -> None:
        if not self._active:
            return
        errors: list[str] = []
        for executable, (key_existed, values) in reversed(tuple(self._key_states.items())):
            path = _WINDOWS_LOCAL_DUMPS_PATH + "\\" + executable
            try:
                if key_existed:
                    with self.registry.CreateKeyEx(
                        self.registry.HKEY_LOCAL_MACHINE,
                        path,
                        0,
                        self._access(self.registry.KEY_READ | self.registry.KEY_WRITE),
                    ) as key:
                        for name in _WINDOWS_LOCAL_DUMPS_VALUES:
                            prior = values[name]
                            if prior is None:
                                try:
                                    self.registry.DeleteValue(key, name)
                                except FileNotFoundError:
                                    pass
                            else:
                                value, value_type = prior
                                self.registry.SetValueEx(key, name, 0, value_type, value)
                else:
                    self._delete_key(path)
            except Exception as error:
                errors.append(f"{executable}: {type(error).__name__}: {error}")

        if not self._local_dumps_path_existed:
            try:
                self._delete_key(_WINDOWS_LOCAL_DUMPS_PATH)
            except Exception as error:
                errors.append(f"LocalDumps key: {type(error).__name__}: {error}")
        if not self._wer_path_existed:
            try:
                self._delete_key(_WINDOWS_WER_PATH)
            except Exception as error:
                errors.append(f"Windows Error Reporting key: {type(error).__name__}: {error}")
        if errors:
            raise NativeUISmokeError("Windows LocalDumps restoration failed: " + "; ".join(errors))
        self._active = False
        self._key_states.clear()


class NativeUIController:
    def __init__(self, platform: str, binary: Path, profile: Path, timeout: float,
                 *, helper: Path, screenshot_dir: Path,
                 expected_version: str | None = None,
                 expected_source_sha: str | None = None) -> None:
        if platform not in {"macos", "windows"} or not math.isfinite(timeout) or timeout <= 0:
            raise ValueError("native UI requires a desktop platform and a finite positive timeout")
        if not helper.is_file():
            raise NativeUISmokeError(f"prepared native helper is missing: {helper}")
        self.platform, self.binary, self.profile, self.helper = platform, binary, profile, helper
        self.expected_version = expected_version
        self.expected_source_sha = expected_source_sha
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
        self.log_resize_verified = False
        self.windows_content_root_diagnostics: dict[str, object] | None = None
        self.windows_no_uia_diagnostics: dict[str, object] | None = None
        self._windows_local_dumps: _WindowsLocalDumps | None = None
        self._windows_wer_started_at_utc: datetime | None = None
        self._windows_wer_dump_dir: Path | None = None

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
        operation_limit = 30.0 if operation in {
            "profile-list-layout", "scroll-profile-list", "connection-action-details",
            "settings-text-size",
        } or (
            self.platform == "windows" and operation in {"tree", "resize-window", "windows-baseline", "uia-point"}
        ) else 10.0
        operation_timeout = min(operation_limit, available - cleanup_timeout)
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

    def start(
        self,
        import_url: str | None = None,
        *,
        windows_content_root_diagnostics: bool = False,
        windows_no_uia_hold_seconds: float | None = None,
        windows_requested_theme: str | None = None,
    ) -> dict:
        if windows_content_root_diagnostics and (
            self.platform != "windows" or import_url is not None
        ):
            raise ValueError("XAML content-root diagnostics require a cold Windows launch")
        if windows_no_uia_hold_seconds is not None and (
            self.platform != "windows"
            or import_url is not None
            or windows_content_root_diagnostics
            or not math.isfinite(windows_no_uia_hold_seconds)
            or not 0 <= windows_no_uia_hold_seconds <= 30
        ):
            raise ValueError(
                "Windows no-UIA stability check requires a cold launch "
                "and a hold from 0 to 30 seconds"
            )
        if windows_requested_theme is not None and (
            self.platform != "windows"
            or import_url is not None
            or windows_requested_theme not in {"Light", "Dark"}
        ):
            raise ValueError("a requested Windows test theme requires a cold Windows launch with Light or Dark")
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
        content_root_diagnostic_path = self.logs / "windows-content-root-peers.json"
        if windows_content_root_diagnostics and content_root_diagnostic_path.exists():
            raise NativeUISmokeError(
                f"XAML content-root diagnostic already exists: {content_root_diagnostic_path}"
            )
        app_environment = None
        if self.platform == "windows":
            app_environment = os.environ.copy()
            # Never let a test theme leak into ordinary or diagnostic launches.
            app_environment.pop(_WINDOWS_TEST_THEME_VARIABLE, None)
        if windows_content_root_diagnostics:
            assert app_environment is not None
            app_environment["DOBBYVPN_NATIVE_UI_CONTENT_ROOT_PEERS_PATH"] = str(
                content_root_diagnostic_path
            )
        if windows_requested_theme is not None:
            assert app_environment is not None
            app_environment[_WINDOWS_TEST_THEME_VARIABLE] = windows_requested_theme
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
            if link is not None:
                # Reopen with an import uses the actual OS scheme activation.
                os.startfile(link)  # type: ignore[attr-defined]
                self.process = None
                time.sleep(0.25)
            else:
                stdout_path = Path(str(prefix) + ".stdout.log")
                stderr_path = Path(str(prefix) + ".stderr.log")
                # Launch the unpackaged WinUI executable in this interactive
                # task directly. Shell activation can be serviced by Explorer
                # and redirect to a different AppInstance; Popen gives the
                # controller the exact process and run-scoped environment.
                # Protocol links continue through os.startfile above.
                with stdout_path.open("xb") as stdout, stderr_path.open("xb") as stderr:
                    self.process = subprocess.Popen(
                        command,
                        stdin=subprocess.DEVNULL,
                        stdout=stdout,
                        stderr=stderr,
                        env=app_environment,
                    )
                self.pid = self.process.pid
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

        if windows_no_uia_hold_seconds is not None:
            self.windows_no_uia_diagnostics = self._run_windows_no_uia_hold(
                windows_no_uia_hold_seconds
            )
            return self.windows_no_uia_diagnostics

        if windows_content_root_diagnostics:
            self.windows_content_root_diagnostics = self._run_windows_content_root_diagnostics()
            return self.windows_content_root_diagnostics

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

    def enable_windows_crash_diagnostics(self) -> None:
        if self.platform != "windows":
            raise ValueError("Windows crash diagnostics require the Windows platform")
        if self._windows_local_dumps is not None:
            raise NativeUISmokeError("Windows crash diagnostics are already enabled")
        dump_dir = self.logs / "windows-wer-dumps"
        dump_dir.mkdir(parents=True, exist_ok=True)
        local_dumps = _WindowsLocalDumps()
        self._windows_local_dumps = local_dumps
        self._windows_wer_dump_dir = dump_dir
        local_dumps.enable((self.executable.name, self.helper.name), dump_dir)
        self._windows_wer_started_at_utc = datetime.now(timezone.utc)

    def restore_windows_crash_diagnostics(self) -> None:
        if self._windows_local_dumps is None:
            return
        self._windows_local_dumps.restore()
        self._windows_local_dumps = None

    def _run_windows_content_root_diagnostics(self) -> dict[str, object]:
        diagnostics: dict[str, object] = {
            "started_at_utc": datetime.now(timezone.utc).isoformat(),
        }
        try:
            baseline = self._call("windows-baseline")
            diagnostics["win32_baseline"] = baseline
        except Exception as error:
            diagnostics["win32_baseline_exception"] = "".join(
                traceback.format_exception(error)
            )
            baseline = None

        if isinstance(baseline, dict) and baseline.get("ready") is True:
            try:
                path = self.logs / "windows-content-root-peers.json"
                deadline = time.monotonic() + min(15.0, self.timeout)
                while not path.is_file():
                    if self.process is not None and self.process.poll() is not None:
                        raise NativeUISmokeError(
                            "Windows UI process exited before writing its XAML content-root peers"
                        )
                    if time.monotonic() >= deadline:
                        raise NativeUISmokeError(
                            "Windows UI did not write its XAML content-root peer diagnostic"
                        )
                    time.sleep(0.1)
                xaml_probe = json.loads(
                    path.read_text(encoding="utf-8")
                )
                diagnostics["xaml_content_root_peers"] = xaml_probe
                try:
                    editor = xaml_probe["editor"]
                    screen_geometry = editor["screenGeometry"]
                    if screen_geometry["coordinateSpace"] != "physical-screen-pixels":
                        raise NativeUISmokeError(
                            "SourceEditor screen geometry is not in physical screen pixels"
                        )
                    point_fields = {
                        "windowHandle": baseline["windowHandle"],
                        "x": screen_geometry["centerX"],
                        "y": screen_geometry["centerY"],
                        "clientOriginX": screen_geometry["clientOrigin"]["x"],
                        "clientOriginY": screen_geometry["clientOrigin"]["y"],
                        "expectedAutomationId": editor["automationId"],
                        "expectedName": editor["name"],
                        "expectedControlType": editor["controlType"],
                        "expectedProcessId": baseline["pid"],
                    }
                    dump_directory = getattr(self, "_windows_wer_dump_dir", None)
                    if dump_directory is not None:
                        point_fields["dumpDirectory"] = str(dump_directory)
                    diagnostics["external_uia_point"] = self._call(
                        "uia-point",
                        clientApi="com",
                        **point_fields,
                    )
                except Exception as error:
                    diagnostics["external_uia_point_exception"] = "".join(
                        traceback.format_exception(error)
                    )
                    if isinstance(error, subprocess.CalledProcessError):
                        for stream_name in ("stdout", "stderr"):
                            payload = getattr(error, stream_name)
                            if isinstance(payload, str):
                                payload = payload.encode("utf-8")
                            diagnostics[f"external_uia_point_{stream_name}_base64"] = (
                                None if payload is None else base64.b64encode(payload).decode("ascii")
                            )
                try:
                    diagnostics["windows_text_size_settings"] = self.inspect_windows_text_size_settings()
                except Exception as error:
                    diagnostics["windows_text_size_settings_exception"] = "".join(
                        traceback.format_exception(error)
                    )
                    if isinstance(error, subprocess.CalledProcessError):
                        for stream_name in ("stdout", "stderr"):
                            payload = getattr(error, stream_name)
                            if isinstance(payload, str):
                                payload = payload.encode("utf-8")
                            diagnostics[f"windows_text_size_settings_{stream_name}_base64"] = (
                                None if payload is None else base64.b64encode(payload).decode("ascii")
                            )
            except Exception as error:
                diagnostics["xaml_content_root_exception"] = "".join(
                    traceback.format_exception(error)
                )
        else:
            diagnostics["xaml_content_root_peers"] = "not-run: no healthy Win32 window baseline"

        try:
            diagnostics["post_probe_process"] = self._call("probe")
        except Exception as error:
            diagnostics["post_probe_process_exception"] = "".join(
                traceback.format_exception(error)
            )

        output_path = self.logs / "windows-content-root-diagnostics.json"
        output_path.write_text(json.dumps(diagnostics, indent=2) + "\n", encoding="utf-8")
        return diagnostics

    def _run_windows_no_uia_hold(self, hold_seconds: float) -> dict[str, object]:
        """Check launch stability using only Win32 process/window probes."""
        diagnostics: dict[str, object] = {
            "started_at_utc": datetime.now(timezone.utc).isoformat(),
            "hold_seconds": hold_seconds,
            "automation_queries": 0,
            "process_probes": [],
        }
        path = self.logs / "windows-configure-tree-no-uia-diagnostics.json"
        probes: list[dict[str, object]] = []
        try:
            baseline = self._call("windows-baseline")
            diagnostics["win32_baseline"] = baseline
            if baseline.get("ready") is not True:
                raise NativeUISmokeError(
                    "no-UIA stability check did not obtain a healthy Win32 window baseline"
                )

            deadline = time.monotonic() + hold_seconds
            while True:
                probe = self._call("probe")
                probes.append({"at_utc": datetime.now(timezone.utc).isoformat(), **probe})
                diagnostics["process_probes"] = probes
                if probe.get("alive") is not True:
                    raise NativeUISmokeError(
                        "Windows UI process was not alive during the no-UIA hold"
                    )
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                time.sleep(min(1.0, remaining))
            diagnostics["complete"] = True
            return diagnostics
        except Exception as error:
            diagnostics["failure"] = "".join(traceback.format_exception(error))
            diagnostics["complete"] = False
            raise
        finally:
            diagnostics["completed_at_utc"] = datetime.now(timezone.utc).isoformat()
            path.write_text(json.dumps(diagnostics, indent=2) + "\n", encoding="utf-8")

    def _open_link(self, link: str) -> None:
        if self.platform == "windows":
            def record_dispatch(event_name: str, **fields: object) -> None:
                print(json.dumps({
                    "event": event_name,
                    "uri": link,
                    "timestamp": datetime.now(timezone.utc).isoformat(),
                    **fields,
                }), file=sys.stderr, flush=True)

            record_dispatch("windows.shell.dispatch.start")
            try:
                os.startfile(link)  # type: ignore[attr-defined]
            except Exception as error:
                record_dispatch("windows.shell.dispatch.return", exception=repr(error))
                raise
            record_dispatch("windows.shell.dispatch.return")
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
        if self.platform == "windows" and (
            not before.get("windowHandle") or after.get("windowHandle") != before.get("windowHandle")
        ):
            raise NativeUISmokeError("bare deep link did not reuse the existing Windows window")
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
                "uiaError",
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
            "source_text": value.get("source_text"),
            "help_texts": value.get("help_texts", []),
            "link_urls": value.get("link_urls", []),
            "reconnecting_seen": self.reconnecting_seen,
        }

    def paste_source(self, *, expected_source: str | None = None) -> dict:
        fields: dict[str, object] = {"source": str(self.profile)}
        if expected_source is not None:
            fields["expectedSource"] = expected_source
        result = self._call("paste", **fields)
        if result.get("ready") is not True:
            raise NativeUISmokeError("native Paste control is unavailable")
        paste_invoked_at = result.get("paste_invoked_at_unix_ms")
        if self.platform in {"macos", "windows"}:
            if type(paste_invoked_at) is not int:
                raise NativeUISmokeError(f"{self.platform} Paste helper did not report its button-invocation time")
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

    def _profile_switch_action(
        self, index: int, competing_index: int, *, protocol_uri: str | None = None,
        observe_only: bool = False,
    ) -> dict[str, object]:
        if self.platform != "windows" or index < 0 or competing_index < 0 or index == competing_index or (
            observe_only and protocol_uri is not None
        ):
            raise ValueError("Windows profile transition requires two distinct profile indices")
        target, competing = f"Profile {index + 1} action", f"Profile {competing_index + 1} action"
        operation = "profile-switch-import" if protocol_uri is not None else "cancel-profile-switch"
        fields = {"target": target, "competing": competing}
        if protocol_uri is not None:
            fields["uri"] = protocol_uri
            dump_directory = getattr(self, "_windows_wer_dump_dir", None)
            if dump_directory is None:
                dump_directory = self.logs / "windows-wer-dumps"
            dump_directory.mkdir(parents=True, exist_ok=True)
            fields["dumpDirectory"] = str(dump_directory)
        if observe_only:
            fields["observe_only"] = True
        expected_pid = getattr(self, "pid", None)
        expected_identity = getattr(self, "identity", None)
        result = self._call(operation, **fields)
        selected = result.get("target_at_stop")
        other = result.get("competing_at_stop")
        connection = result.get("connection_action_at_stop")
        def action_state(value: object, automation_id: str, name: str, enabled: bool) -> bool:
            return (
                isinstance(value, dict) and value.get("found") is True
                and value.get("control_type") == "ControlType.Button"
                and value.get("is_control_element") is True
                and value.get("automation_id") == automation_id and value.get("name") == name
                and value.get("enabled") is enabled and value.get("offscreen") is False
            )
        def main_action_state(value: object) -> bool:
            return (
                isinstance(value, dict) and value.get("found") is True
                and value.get("automation_id") == "VPN connection action"
                and value.get("control_type") == "ControlType.Button"
                and value.get("is_control_element") is True
                and value.get("name") in {"Connect", "Auto connect", "Stop", "Disconnect"}
                and type(value.get("enabled")) is bool and type(value.get("offscreen")) is bool
                and value.get("offscreen") is False
                and (value.get("name") not in {"Connect", "Auto connect"} or value.get("enabled") is False)
            )
        if (
            result.get("ready") is not True
            or result.get("target_automation_id") != target
            or result.get("competing_automation_id") != competing
            or not action_state(selected, target, "Stop", True)
            or not action_state(other, competing, "Connect", False)
            or not main_action_state(connection)
        ):
            raise NativeUISmokeError(
                f"Windows profile transition did not observe an enabled Stop with competing Connect disabled: {result!r}"
            )
        if protocol_uri is not None:
            timestamp_keys = (
                "connect_invoked_at_utc", "stop_observed_at_utc",
                "protocol_dispatch_started_at_utc", "protocol_dispatch_returned_at_utc",
            )
        elif observe_only:
            timestamp_keys = ("connect_invoked_at_utc", "stop_observed_at_utc")
        else:
            timestamp_keys = ("connect_invoked_at_utc", "stop_observed_at_utc", "stop_invoked_at_utc")
        timestamps = [result.get(key) for key in timestamp_keys]
        try:
            parsed = [datetime.fromisoformat(value.replace("Z", "+00:00")) for value in timestamps]
        except (AttributeError, TypeError, ValueError) as error:
            raise NativeUISmokeError(f"Windows profile transition timestamps were invalid: {timestamps!r}") from error
        if any(value.tzinfo is None for value in parsed) or parsed != sorted(parsed):
            raise NativeUISmokeError(f"Windows profile transition timestamps were not chronological: {timestamps!r}")
        if expected_pid is not None and result.get("pid") != expected_pid:
            raise NativeUISmokeError(f"Windows profile transition changed the UI process: {result!r}")
        if expected_identity is not None and result.get("identity") != expected_identity:
            raise NativeUISmokeError(f"Windows profile transition changed the UI process identity: {result!r}")
        if observe_only and (
            result.get("observe_only") is not True or "stop_invoked_at_utc" in result
        ):
            raise NativeUISmokeError(f"Windows profile observation invoked Stop or lacked observe-only evidence: {result!r}")
        if protocol_uri is not None and (
            result.get("protocol_uri") != protocol_uri
            or not isinstance(result.get("window_handle"), str)
            or not result["window_handle"].startswith("0x")
            or type(result.get("shell_execute_result")) is not int
            or result["shell_execute_result"] <= 32
        ):
            raise NativeUISmokeError(f"Windows profile import was not dispatched by the observed Stop action: {result!r}")
        return result

    def cancel_profile_switch(self, index: int, competing_index: int) -> dict[str, object]:
        return self._profile_switch_action(index, competing_index)

    def observe_profile_switch(self, index: int, competing_index: int) -> dict[str, object]:
        return self._profile_switch_action(index, competing_index, observe_only=True)

    def switch_profile_and_dispatch_import(
        self, index: int, competing_index: int, url: str,
    ) -> dict[str, object]:
        from urllib.parse import quote

        uri = "dobbyvpn://import?url=" + quote(url, safe="")
        return self._profile_switch_action(index, competing_index, protocol_uri=uri)

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
        self.dispatch_import_link(url, repeats=2)
        self._wait(lambda: "Retry" not in self.snapshot()["labels"], "import did not replace failed subscription")
        self._wait(lambda: "Profile 1 action" in self.snapshot()["labels"], "imported profiles are unavailable")
        return self.snapshot()

    def dispatch_import_link(self, url: str, *, repeats: int = 1) -> dict:
        """Deliver a warm import and verify it stayed in the current window.

        Unlike ``import_link``, this returns before subscription loading
        completes so a caller can inspect a connection request that is still
        pending while the imported URL is being fetched.
        """
        if repeats < 1 or repeats > 2:
            raise ValueError("warm import delivery supports one or two identical links")
        from urllib.parse import quote
        link = "dobbyvpn://import?url=" + quote(url, safe="")
        self.profile.write_text(url, encoding="utf-8")
        before = self._call("probe")
        identity_before, pid_before = before.get("identity"), before.get("pid")
        for _ in range(repeats):
            self._open_link(link)
        if repeats > 1:
            time.sleep(0.25)
        # Windows' helper rejects duplicate matching processes when it probes
        # without a PID, then returns the existing process identity.
        after = self._call("probe", unbound=self.platform == "windows")
        if after.get("pid") != pid_before or after.get("identity") != identity_before:
            raise NativeUISmokeError("warm deep link did not reuse the existing UI process")
        if self.platform == "windows" and (
            not before.get("windowHandle") or after.get("windowHandle") != before.get("windowHandle")
        ):
            raise NativeUISmokeError("warm deep link did not reuse the existing Windows window")
        return {"ready": True, "pid": after.get("pid"), "identity": after.get("identity"),
                "windowHandle": after.get("windowHandle")}

    def verify_windows_palette_fixture(self, marker: str, severity: str) -> dict[str, object]:
        """Verify one newly appended severity row through the rendered WinUI log view."""
        if self.platform != "windows" or not marker or severity not in _WINDOWS_PALETTE_SEVERITIES:
            raise ValueError("rendered palette fixtures require a Windows UI, marker, and known severity")

        # Bring the appended records into the actual log viewport before
        # querying UIA colors or capturing the themed view.
        self._call("scroll-logs", position="bottom")
        latest: dict = {}

        def tagged_fixture_rows(entries: list[object]) -> list[dict]:
            rows = []
            for entry in entries:
                if not isinstance(entry, dict):
                    continue
                text = entry.get("text")
                if not isinstance(text, str) or marker not in text:
                    continue
                fields = text.splitlines()[0].split(" · ")
                # MainWindow.RenderLogs emits timestamp, severity, and source
                # in the heading; the source may itself contain a middle dot.
                if len(fields) >= 3 and fields[1] in _WINDOWS_PALETTE_SEVERITIES:
                    rows.append(entry)
            return rows

        def fixture_rows_ready() -> bool:
            nonlocal latest
            latest = self._call("logs", marker=marker)
            entries = latest.get("entries")
            if not isinstance(entries, list):
                raise NativeUISmokeError("Windows log helper did not return rendered entries")
            tagged = tagged_fixture_rows(entries)
            target = [
                entry for entry in tagged
                if entry["text"].splitlines()[0].split(" · ")[1] == severity
            ]
            return bool(target)

        self._wait(fixture_rows_ready, f"Windows rendered log view did not expose the tagged {severity} palette row")
        entries = latest.get("entries")
        assert isinstance(entries, list)
        tagged = tagged_fixture_rows(entries)
        target = [
            entry for entry in tagged
            if entry["text"].splitlines()[0].split(" · ")[1] == severity
        ]
        if len(target) != 1:
            raise NativeUISmokeError(
                f"expected exactly one tagged Windows {severity} palette row for {marker}, found {len(target)}"
            )
        entry = target[0]

        viewport = latest.get("logs_viewport")

        def rectangle(value: object, label: str) -> tuple[float, float, float, float]:
            if not isinstance(value, dict):
                raise NativeUISmokeError(f"Windows {label} bounds were unavailable")
            coordinates = []
            for key in ("x", "y", "width", "height"):
                coordinate = value.get(key)
                if type(coordinate) not in (int, float) or not math.isfinite(coordinate):
                    raise NativeUISmokeError(f"Windows {label} has invalid {key}: {value!r}")
                coordinates.append(float(coordinate))
            if coordinates[2] <= 0 or coordinates[3] <= 0:
                raise NativeUISmokeError(f"Windows {label} has an empty screen rectangle: {value!r}")
            return tuple(coordinates)  # type: ignore[return-value]

        vx, vy, vw, vh = rectangle(viewport, "log viewport")

        text = entry.get("text")
        if not isinstance(text, str) or not text:
            raise NativeUISmokeError(f"Windows palette fixture row has no rendered text: {entry!r}")
        if entry.get("offscreen") is not False or entry.get("visible_in_viewport") is not True:
            raise NativeUISmokeError(f"Windows rendered {severity} fixture row is not visible in the log viewport")
        x, y, width, height = rectangle(entry.get("bounds"), f"{severity} fixture row")
        if x < vx or y < vy or x + width > vx + vw or y + height > vy + vh:
            raise NativeUISmokeError(
                f"Windows rendered {severity} fixture row is outside the log viewport: "
                f"row={entry.get('bounds')!r} viewport={viewport!r}"
            )
        foreground = entry.get("foreground")
        uia_foreground = foreground if type(foreground) is int else None
        return {
            "marker": marker,
            # UIA exposes COLORREF (RGB) and cannot preserve the source brush's
            # alpha. Keep it as diagnostic evidence; captured pixels are the
            # palette acceptance measurement performed with the screenshot.
            "palette": {severity: uia_foreground},
            "rows": {severity: {
                "foreground": uia_foreground,
                "uia_foreground": uia_foreground,
                "offscreen": False,
                "bounds": entry["bounds"],
                "visible_in_viewport": True,
            }},
            "logs_viewport": viewport,
        }

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
            readiness_entries = initial_view.get("entries", [])
            if not any(
                isinstance(entry, dict) and " · " in entry.get("text", "")
                for entry in readiness_entries
            ):
                raise NativeUISmokeError("Windows log entries did not expose rendered structured text")
            self._call("scroll-logs", position="top")
            initial_view = self._call("logs", verify_details=True)
            rendered = initial_view.get("entries", [])
            if not initial_view.get("expansion_verified"):
                raise NativeUISmokeError("Windows structured log Details did not reveal the original record")
            capture_row = next((
                entry.get("text", "") for entry in rendered
                if isinstance(entry, dict) and "Stderr capture initialized" in entry.get("text", "")
            ), "")
            capture_header = capture_row.splitlines()[0] if capture_row else ""
            if (
                " · INFO · Backend stderr" not in capture_header
                or "Stderr capture initialized" not in capture_row
                or any(severity in capture_header for severity in (" · ERROR · ", " · FATAL · ", " · PANIC · "))
            ):
                raise NativeUISmokeError(
                    "Windows rendered stderr.capture row was missing its informational severity or friendly stream name"
                )
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
            for severity in ("ERROR", "FATAL", "PANIC"):
                if severity in palette:
                    if not any(red > green and red > blue for red, green, blue in _windows_color_orders(palette[severity])):
                        raise NativeUISmokeError(f"Windows {severity} logs are not rendered in a red severity color")
            for severity in ("WARN", "WARNING"):
                if severity in palette:
                    if not any(red >= green > blue for red, green, blue in _windows_color_orders(palette[severity])):
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

            frozen_position = self._call("log-position")
            frozen = self._call("logs").get("text", "")
            original_window = self.resize_window(640, 640)
            try:
                self.assert_primary_action_and_logs_visible()
                resized_view = self._call("logs")
                resized_position = self._call("log-position")
                if resized_view.get("text", "") != frozen:
                    raise NativeUISmokeError("Windows log entries changed when the window was resized while frozen")
                if resized_position.get("visible_first_record") != frozen_position.get("visible_first_record"):
                    raise NativeUISmokeError(
                        "Windows reading position changed when the window was resized while following was frozen: "
                        f"before={frozen_position} after={resized_position}"
                    )
            finally:
                self.resize_window(
                    original_window["width"], original_window["height"],
                    left=original_window["left"], top=original_window["top"],
                )
            self.log_resize_verified = True
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
            selected_while_frozen = self._call("select-log-text").get("selected", "")
            if " · " not in selected_while_frozen or self._call("logs").get("text", "") != frozen:
                raise NativeUISmokeError("Windows log text could not be selected while the rendered view was frozen")
            selected_position = self._call("log-position").get("vertical_scroll_percent")
            if (not isinstance(selected_position, (int, float)) or isinstance(selected_position, bool)
                    or abs(float(selected_position) - float(after_percent)) > 1.0):
                raise NativeUISmokeError("Windows text selection moved the frozen reading position")
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
            original_window = self.resize_window(640, 560)
            try:
                self.assert_primary_action_and_logs_visible()
                resized_view = self._call("logs")
                resized_position = self._call("log-position")
                if resized_view.get("text", "") != frozen:
                    raise NativeUISmokeError("macOS log entries changed when the window was resized while frozen")
                if resized_position.get("visible_range_start") != frozen_position.get("visible_range_start"):
                    raise NativeUISmokeError(
                        "macOS reading position changed when the window was resized while following was frozen: "
                        f"before={frozen_position} after={resized_position}"
                    )
            finally:
                self.resize_window(original_window["width"], original_window["height"])
            self.log_resize_verified = True
            original_url = self.profile.read_text(encoding="utf-8").strip()
            failure_url = original_url.rsplit("/", 1)[0] + "/missing"
            try:
                self.failing_subscription(failure_url)
            finally:
                self.profile.write_text(original_url, encoding="utf-8")
            if self._call("logs").get("text", "") != frozen:
                raise NativeUISmokeError("macOS log entries changed while the view was scrolled up")
            current_position = self._call("log-position")
            # visible_range_end is viewport extent and can change when Retry alters layout.
            if any(current_position.get(key) != frozen_position.get(key)
                   for key in ("visible_range_start", "scrollbar_position")):
                raise NativeUISmokeError(
                    "macOS reading position changed while log following was frozen: "
                    f"before={frozen_position} after={current_position}"
                )
            selected_while_frozen = self._call("select-log-text").get("selected", "")
            if " · " not in selected_while_frozen or self._call("logs").get("text", "") != frozen:
                raise NativeUISmokeError("macOS log text could not be selected while the rendered view was frozen")
            selected_position = self._call("log-position")
            if any(selected_position.get(key) != current_position.get(key)
                   for key in ("visible_range_start", "scrollbar_position")):
                raise NativeUISmokeError("macOS text selection moved the frozen reading position")
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

    def connect_with_auto_stop(self) -> dict:
        """Require an enabled Stop control while Auto selection is pending."""
        before = self.snapshot()
        labels = set(before.get("labels", []))
        enabled = set(before.get("enabled_controls", []))
        if not (
            "Auto connect" in labels
            and "Auto connect" in enabled
            and "VPN connection action" in enabled
        ):
            raise NativeUISmokeError(
                "Auto selection did not start from a rendered, enabled Auto connect action"
            )
        self._click("VPN connection action")
        deadline = time.monotonic() + self.timeout
        stop_seen = False
        latest: dict = {}
        while time.monotonic() < deadline:
            latest = self.snapshot()
            status = latest.get("status")
            if status in {"Stopping", "Connecting", "Reconnecting"}:
                labels = set(latest.get("labels", []))
                enabled = set(latest.get("enabled_controls", []))
                if "Stop" in labels and "Stop" in enabled:
                    competing = sorted(
                        name for name in enabled
                        if isinstance(name, str)
                        and name.startswith("Profile ")
                        and name.endswith(" action")
                    )
                    if competing:
                        raise NativeUISmokeError(
                            "Auto selection exposed Stop while profile Connect actions remained enabled: "
                            + ", ".join(competing)
                        )
                    stop_seen = True
            elif status == "Connected":
                if not stop_seen:
                    raise NativeUISmokeError(
                        "Auto selection completed without a rendered, enabled Stop control"
                    )
                return {**latest, "auto_stop_observed": True}
            elif status in {"Failed", "Error"}:
                raise NativeUISmokeError(
                    f"Auto selection failed before its Stop control could be verified: {latest}"
                )
            time.sleep(min(0.04, max(0.0, deadline - time.monotonic())))
        raise NativeUISmokeError("Auto selection did not complete before the native UI deadline")

    def disconnect(self) -> dict:
        self._click("VPN connection action")
        return self.wait_status("Disconnected")

    def recover_after_process_loss(self) -> dict:
        self.reconnecting_seen = False
        self.wait_status("Disconnected", allow_errors=True)
        self._wait(lambda: "Auto connect" in self.snapshot()["labels"], "replacement service did not expose Connect")
        self.configure()
        return self.connect()

    def resize_window(self, width: int, height: int, *, left: int | None = None, top: int | None = None) -> dict:
        if width < 560 or height < 460:
            raise ValueError("native desktop window size is below the supported minimum")
        request: dict[str, int] = {"width": width, "height": height}
        if left is not None:
            request["left"] = left
        if top is not None:
            request["top"] = top
        return self._call("resize-window", **request)

    def profile_list_layout(self) -> dict:
        result = self._call("profile-list-layout")
        if result.get("ready") is not True:
            raise NativeUISmokeError("native helper did not return profile-list layout evidence")
        return result

    def scroll_profile_list(self, position: str) -> dict:
        if position not in {"top", "bottom"}:
            try:
                percent = float(position)
            except (TypeError, ValueError) as error:
                raise ValueError("profile-list position must be top, bottom, or a finite percentage") from error
            if not math.isfinite(percent) or not 0 <= percent <= 100:
                raise ValueError("profile-list percentage must be between 0 and 100")
        result = self._call("scroll-profile-list", position=position)
        if result.get("ready") is not True:
            raise NativeUISmokeError(f"native helper did not scroll the profile list to {position}")
        return result

    def connection_action_details(self) -> dict:
        """Return a read-only Accessibility snapshot of Stop/connection-action nodes."""
        return self._call("connection-action-details")

    def inspect_windows_text_size_settings(self) -> dict[str, object]:
        """Open Windows Text size Settings and inspect its native controls without changing them."""
        if self.platform != "windows":
            raise ValueError("Windows Text size Settings inspection is only available on Windows")
        return self._call(
            "settings-text-size",
            unbound=True,
            action="inspect",
            uri=_WINDOWS_TEXT_SIZE_SETTINGS_URI,
        )

    def apply_windows_text_size(self, target: int, expected_current: int) -> dict[str, object]:
        """Apply a guarded Windows Text size value through the native Settings controls."""
        if self.platform != "windows":
            raise ValueError("Windows Text size Settings are only available on Windows")
        if type(target) is not int or target <= 0 or type(expected_current) is not int or expected_current <= 0:
            raise ValueError("Windows Text size apply requires positive integer values")
        return self._call(
            "settings-text-size",
            unbound=True,
            action="apply",
            uri=_WINDOWS_TEXT_SIZE_SETTINGS_URI,
            target=target,
            expectedCurrent=expected_current,
        )

    def assert_primary_action_and_logs_visible(self) -> dict:
        layout = self.profile_list_layout()

        def rectangle(name: str) -> tuple[float, float, float, float]:
            value = layout.get(name)
            if not isinstance(value, dict):
                raise NativeUISmokeError(f"native profile layout omitted {name} bounds")
            coordinates = tuple(value.get(key) for key in ("x", "y", "width", "height"))
            if any(type(item) not in (int, float) or not math.isfinite(item) for item in coordinates):
                raise NativeUISmokeError(f"native profile layout returned invalid {name} bounds: {value}")
            x, y, width, height = (float(item) for item in coordinates)
            if width <= 0 or height <= 0:
                raise NativeUISmokeError(f"native profile layout returned empty {name} bounds: {value}")
            return x, y, width, height

        def contained(inner: tuple[float, float, float, float], outer: tuple[float, float, float, float]) -> bool:
            ix, iy, iw, ih = inner
            ox, oy, ow, oh = outer
            return ix >= ox - 1 and iy >= oy - 1 and ix + iw <= ox + ow + 1 and iy + ih <= oy + oh + 1

        window = rectangle("window")
        action = rectangle("connection_action")
        logs = rectangle("logs")
        if not contained(action, window) or not contained(logs, window):
            raise NativeUISmokeError("resized native window clipped the main action or log viewport")
        ax, ay, aw, ah = action
        lx, ly, lw, lh = logs
        if ax < lx + lw and lx < ax + aw and ay < ly + lh and ly < ay + ah:
            raise NativeUISmokeError("resized native main action overlaps the log viewport")
        return layout

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
            if self.expected_version is not None and version != self.expected_version:
                raise NativeUISmokeError("candidate macOS About version does not match the selected build")
            if self.expected_source_sha is not None and commit.casefold() != self.expected_source_sha.casefold():
                raise NativeUISmokeError("candidate macOS About commit does not match the selected build")
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
                and (self.expected_version is None or version == self.expected_version)
                and re.fullmatch(r"[0-9a-fA-F]{40}", commit) is not None
                and (self.expected_source_sha is None or commit.casefold() == self.expected_source_sha.casefold())
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
        response = self._call("capture", path=str(path))
        if response.get("ready") is not True:
            raise NativeUISmokeError("native window unavailable for screenshot")
        width, height = nonblank_png_dimensions(path)
        result: dict[str, object] = {"path": str(path), "width": width, "height": height}
        if self.platform == "windows":
            result["screen_bounds"] = response.get("screen_bounds")
        return result

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
        if self._windows_wer_started_at_utc is not None:
            collection_error: BaseException | None = None
            try:
                self._collect_windows_crash_diagnostics()
            except BaseException as error:
                collection_error = error
            try:
                self._write_windows_wer_dump_inventory()
            except BaseException as error:
                if collection_error is None:
                    collection_error = error
                else:
                    add_exception_notes(collection_error, "WER dump inventory", error)
            if collection_error is not None:
                raise collection_error

    def _collect_windows_crash_diagnostics(self) -> None:
        if self._windows_wer_started_at_utc is None or self._windows_wer_dump_dir is None:
            return
        script = r"""$ErrorActionPreference = "Stop"
$since = [DateTimeOffset]::Parse($env:DOBBYVPN_NATIVE_UI_WER_SINCE_UTC).UtcDateTime
$pattern = '(?i)(DobbyVPN\.exe|NativeUI\.exe)'
$deadline = [DateTime]::UtcNow.AddSeconds(3)
$events = @()
do {
    try {
        $events = @(Get-WinEvent -FilterHashtable @{
            LogName = 'Application'
            Id = @(1000, 1001)
            StartTime = $since
        } -ErrorAction Stop | Where-Object { $_.Message -match $pattern })
    } catch {
        if ($_.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*') {
            [Console]::Error.WriteLine(($_ | Out-String))
            exit 1
        }
        $events = @()
    }
    if ($events.Count -gt 0 -or [DateTime]::UtcNow -ge $deadline) { break }
    Start-Sleep -Milliseconds 250
} while ($true)
if ($events.Count -eq 0) {
    [Console]::Out.WriteLine("wer_application_events=none since_utc=" + $since.ToString("o"))
} else {
    foreach ($event in $events) { [Console]::Out.WriteLine($event.ToXml()) }
}
"""
        environment = os.environ.copy()
        environment["DOBBYVPN_NATIVE_UI_WER_SINCE_UTC"] = (
            self._windows_wer_started_at_utc.isoformat()
        )
        deadline = time.monotonic() + 10.0
        spawn, terminate, close = _windows_job_capture_callbacks(deadline)
        command = [
            "powershell.exe",
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            script,
        ]
        try:
            result = _native_run(
                command,
                timeout_seconds=7.0,
                cwd=self.logs,
                env=environment,
                termination_grace_seconds=1.0,
                cleanup_timeout_seconds=2.0,
                popen_factory=spawn,
                terminate=terminate,
                close_boundary=close,
            )
        except BaseException as error:
            self._write_windows_wer_streams(
                getattr(error, "stdout", None),
                getattr(error, "stderr", None),
            )
            raise
        self._write_windows_wer_streams(result.stdout, result.stderr)
        if result.returncode:
            raise subprocess.CalledProcessError(
                result.returncode,
                result.args,
                result.stdout,
                result.stderr,
            )

    def _write_windows_wer_streams(self, stdout: bytes | None, stderr: bytes | None) -> None:
        if stdout:
            (self.logs / "windows-wer-application-events.stdout.log").write_bytes(stdout)
        if stderr:
            (self.logs / "windows-wer-application-events.stderr.log").write_bytes(stderr)

    def _write_windows_wer_dump_inventory(self) -> None:
        assert self._windows_wer_dump_dir is not None
        artifacts = sorted(
            path for path in self._windows_wer_dump_dir.iterdir()
            if path.is_file() and (
                path.suffix.casefold() == ".dmp" or path.name.casefold().endswith(".dmp.partial")
            )
        )
        lines = [f"wer_dump_directory={self._windows_wer_dump_dir}"]
        if not artifacts:
            lines.append("wer_dump_files=none")
        else:
            for path in artifacts:
                metadata = path.stat()
                modified = datetime.fromtimestamp(metadata.st_mtime, timezone.utc).isoformat()
                key = "wer_dump_partial_file" if path.name.casefold().endswith(".dmp.partial") else "wer_dump_file"
                lines.append(
                    f"{key}={path} bytes={metadata.st_size} modified_utc={modified}"
                )
        (self.logs / "windows-wer-dump-inventory.log").write_text(
            "\n".join(lines) + "\n",
            encoding="utf-8",
        )

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
