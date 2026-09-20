#!/usr/bin/env python3
"""Exercise the packaged desktop UI through the operating system.

The headless companion owns the detailed VPN assertions.  This process proves
the other half of the contract: a real rendered Fyne window exposes its
controls, accepts keyboard/paste and pointer input, updates its visible state,
survives close/reopen, and can disconnect through the UI.  Linux remains
CLI/service-only.
"""

from __future__ import annotations

import argparse
import base64
from collections.abc import Callable
import ctypes
from ctypes import wintypes
import getpass
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time


class NativeUISmokeError(RuntimeError):
    pass


def _windows_interactive_identity() -> str:
    """Return the user/session owning this process or fail before launching a GUI.

    A GitHub/owner runner may be a service session (session 0), where Fyne can
    start and exit without ever creating a visible window.  Reporting that as a
    UI pass would be dishonest, so require the current process to be attached
    to an interactive session and record the identity used for the launch.
    """
    powershell = shutil.which("powershell") or shutil.which("pwsh")
    if not powershell:
        raise NativeUISmokeError("Windows UI qualification requires PowerShell to inspect the interactive session")
    command = r'''
$ErrorActionPreference = "Stop"
$process = Get-Process -Id ([int]$env:DOBBY_UI_PARENT_PID)
if ($process.SessionId -eq 0 -or -not [Environment]::UserInteractive) {
    throw "GUI process is not in an interactive console session (session=$($process.SessionId))"
}
$identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
Write-Output ("{0}|session={1}|userInteractive={2}" -f $identity, $process.SessionId, [Environment]::UserInteractive)
'''
    environment = os.environ.copy()
    environment["DOBBY_UI_PARENT_PID"] = str(os.getpid())
    try:
        result = subprocess.run(
            [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
            check=False,
            text=True,
            capture_output=True,
            env=environment,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"Windows interactive session inspection failed: {error}") from error
    identity = result.stdout.strip()
    if result.returncode != 0 or not identity:
        detail = result.stderr.strip() or "current process is not attached to an interactive user session"
        raise NativeUISmokeError(f"Windows native UI is unavailable: {detail}")
    return identity


def _macos_interactive_identity() -> str:
    """Return the Aqua console user after checking the current launch context."""
    if os.name != "posix":
        raise NativeUISmokeError("macOS native UI qualification requires a macOS host")
    current_user = getpass.getuser()
    try:
        console = subprocess.run(
            ["stat", "-f", "%Su", "/dev/console"],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"could not inspect the macOS console user: {error}") from error
    console_user = console.stdout.strip()
    if console.returncode != 0 or not console_user or console_user in {"root", "loginwindow"}:
        raise NativeUISmokeError(
            "macOS native UI is unavailable: no logged-in Aqua console user "
            f"(observed {console_user or 'none'})"
        )
    if current_user != console_user:
        raise NativeUISmokeError(
            "macOS native UI is unavailable: helper identity does not own the console "
            f"(process={current_user!r}, console={console_user!r})"
        )
    uid = str(os.getuid())
    try:
        session = subprocess.run(
            ["launchctl", "print", f"gui/{uid}"],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS GUI session inspection failed: {error}") from error
    if session.returncode != 0:
        detail = session.stderr.strip() or "launchctl has no GUI session for the current user"
        raise NativeUISmokeError(f"macOS native UI is unavailable: {detail}")
    return f"{current_user}|uid={uid}|console={console_user}"


def _wait_until(predicate, timeout: float, message: str) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.1)
    raise NativeUISmokeError(message)


def _windows_rect(hwnd: int) -> tuple[int, int, int, int]:
    class RECT(ctypes.Structure):
        _fields_ = [("left", ctypes.c_long), ("top", ctypes.c_long), ("right", ctypes.c_long), ("bottom", ctypes.c_long)]

    rect = RECT()
    if not ctypes.windll.user32.GetWindowRect(hwnd, ctypes.byref(rect)):
        raise NativeUISmokeError("GetWindowRect failed")
    return rect.left, rect.top, rect.right, rect.bottom


def _windows_accessibility_rect(
    hwnd: int, name: str, *, prefix: bool = False
) -> tuple[int, int, int, int]:
    """Get a Fyne element's bounds from the real UI Automation tree."""
    powershell = shutil.which("powershell") or shutil.which("pwsh")
    if not powershell:
        raise NativeUISmokeError("PowerShell is required for Windows UI Automation")
    command = r'''
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$root = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$env:DOBBY_UI_HWND)
$condition = New-Object -TypeName System.Windows.Automation.PropertyCondition -ArgumentList @(
    [System.Windows.Automation.AutomationElement]::NameProperty, $env:DOBBY_UI_NAME)
if ($env:DOBBY_UI_PREFIX -eq "1") {
    $element = $root.FindAll(
        [System.Windows.Automation.TreeScope]::Descendants,
        [System.Windows.Automation.Condition]::TrueCondition) |
        Where-Object { $_.Current.Name.StartsWith($env:DOBBY_UI_NAME, [System.StringComparison]::Ordinal) } |
        Select-Object -First 1
} else {
    $element = $root.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $condition)
}
if ($null -eq $element) { exit 3 }
$rect = $element.Current.BoundingRectangle
if ($rect.Width -le 0 -or $rect.Height -le 0) { exit 4 }
Write-Output ("{0},{1},{2},{3}" -f $rect.Left, $rect.Top, $rect.Right, $rect.Bottom)
'''
    environment = os.environ.copy()
    environment["DOBBY_UI_HWND"] = str(hwnd)
    environment["DOBBY_UI_NAME"] = name
    environment["DOBBY_UI_PREFIX"] = "1" if prefix else "0"
    try:
        result = subprocess.run(
            [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
            check=False,
            text=True,
            capture_output=True,
            env=environment,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"Windows accessibility lookup failed for {name!r}: {error}") from error
    if result.returncode != 0:
        detail = result.stderr.strip() or f"element {name!r} was not found"
        raise NativeUISmokeError(f"Windows accessibility lookup failed for {name!r}: {detail}")
    try:
        values = tuple(round(float(value)) for value in result.stdout.strip().split(","))
    except ValueError as error:
        raise NativeUISmokeError(f"invalid Windows accessibility bounds for {name!r}") from error
    if len(values) != 4 or values[2] <= values[0] or values[3] <= values[1]:
        raise NativeUISmokeError(f"invalid Windows accessibility bounds for {name!r}")
    return values  # type: ignore[return-value]


def _windows_click(user32: object, bounds: tuple[int, int, int, int]) -> None:
    left, top, right, bottom = bounds
    user32.SetCursorPos((left + right) // 2, (top + bottom) // 2)
    user32.mouse_event(0x0002, 0, 0, 0, 0)
    user32.mouse_event(0x0004, 0, 0, 0, 0)


def _windows_has_element(hwnd: int, name: str, *, prefix: bool = False) -> bool:
    try:
        _windows_accessibility_rect(hwnd, name, prefix=prefix)
        return True
    except NativeUISmokeError:
        return False


def _windows_clipboard_snapshot(powershell: str) -> str | None:
    """Return the current text clipboard, or None when it cannot be saved.

    The value crosses the PowerShell boundary as base64 so neither command
    arguments nor diagnostics contain clipboard text.  A missing snapshot is
    intentionally treated as an empty/unknown clipboard and is cleared during
    cleanup.
    """
    command = r'''
$ErrorActionPreference = "Stop"
try {
    $value = Get-Clipboard -Raw -Format Text
    if ($null -eq $value) { exit 3 }
    [Console]::Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$value)))
} catch {
    exit 3
}
'''
    try:
        result = subprocess.run(
            [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
            check=False,
            text=True,
            capture_output=True,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    try:
        return base64.b64decode(result.stdout.strip(), validate=True).decode("utf-8")
    except (UnicodeDecodeError, ValueError):
        return None


def _windows_set_clipboard(powershell: str, value: str) -> None:
    """Set text clipboard content without putting the value in command text."""
    command = r'''
$ErrorActionPreference = "Stop"
$encoded = [Console]::In.ReadToEnd()
$value = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encoded))
Set-Clipboard -Value $value
'''
    encoded = base64.b64encode(value.encode("utf-8")).decode("ascii")
    try:
        result = subprocess.run(
            [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
            input=encoded,
            check=False,
            text=True,
            capture_output=True,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError("Windows clipboard setup failed") from error
    if result.returncode != 0:
        raise NativeUISmokeError("Windows clipboard setup failed")


def _windows_restore_clipboard(powershell: str, previous: str | None) -> None:
    """Restore text clipboard content, falling back to clearing it silently."""
    try:
        _windows_set_clipboard(powershell, previous if previous is not None else "")
        return
    except (NativeUISmokeError, OSError, subprocess.SubprocessError):
        pass
    try:
        _windows_set_clipboard(powershell, "")
    except (NativeUISmokeError, OSError, subprocess.SubprocessError):
        pass


def _windows_paste(profile: Path) -> Callable[[], None]:
    powershell = shutil.which("powershell") or shutil.which("pwsh")
    if not powershell:
        raise NativeUISmokeError("PowerShell is required for native clipboard input")
    previous = _windows_clipboard_snapshot(powershell)
    try:
        _windows_set_clipboard(powershell, profile.read_text(encoding="utf-8"))
    except Exception:
        _windows_restore_clipboard(powershell, previous)
        raise
    restored = False

    def restore() -> None:
        nonlocal restored
        if restored:
            return
        restored = True
        _windows_restore_clipboard(powershell, previous)

    return restore


def _macos_accessibility_rect(
    name: str, timeout: float, *, prefix: bool = False
) -> tuple[int, int, int, int]:
    apple_name = '"' + name.replace('"', '\\"') + '"'
    match = f'name begins with {apple_name}' if prefix else f'name is {apple_name}'
    script = f'''tell application "System Events"
    tell process "Dobby Vpn"
        set matches to every UI element of entire contents of window 1 whose {match}
        if (count of matches) is 0 then error "accessibility element not found: {name}"
        set target to item 1 of matches
        set p to position of target
        set s to size of target
        return ((item 1 of p) as integer) & "," & ((item 2 of p) as integer) & "," & ((item 1 of p) + (item 1 of s) as integer) & "," & ((item 2 of p) + (item 2 of s) as integer)
    end tell
    end tell'''
    try:
        completed = subprocess.run(["osascript", "-e", script], check=False, text=True, capture_output=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS accessibility lookup failed for {name!r}: {error}") from error
    if completed.returncode != 0:
        raise NativeUISmokeError(completed.stderr.strip() or f"macOS accessibility element {name!r} was not found")
    try:
        values = tuple(int(value) for value in completed.stdout.strip().split(","))
    except ValueError as error:
        raise NativeUISmokeError(f"invalid macOS accessibility bounds for {name!r}") from error
    if len(values) != 4 or values[2] <= values[0] or values[3] <= values[1]:
        raise NativeUISmokeError(f"invalid macOS accessibility bounds for {name!r}")
    return values  # type: ignore[return-value]


def _macos_click(bounds: tuple[int, int, int, int]) -> None:
    x = (bounds[0] + bounds[2]) // 2
    y = (bounds[1] + bounds[3]) // 2
    script = f'tell application "System Events" to click at {{{x}, {y}}}'
    try:
        completed = subprocess.run(["osascript", "-e", script], check=False, text=True, capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS native click failed: {error}") from error
    if completed.returncode != 0:
        raise NativeUISmokeError(completed.stderr.strip() or "macOS native click failed")


def _macos_has_element(name: str, *, prefix: bool = False) -> bool:
    try:
        _macos_accessibility_rect(name, 10, prefix=prefix)
        return True
    except NativeUISmokeError:
        return False


def _macos_clipboard_snapshot() -> bytes | None:
    try:
        result = subprocess.run(["pbpaste"], check=False, capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return None
    return result.stdout if result.returncode == 0 else None


def _macos_set_clipboard(value: bytes) -> None:
    try:
        result = subprocess.run(["pbcopy"], input=value, check=False, capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError("macOS clipboard setup failed") from error
    if result.returncode != 0:
        raise NativeUISmokeError("macOS clipboard setup failed")


def _macos_restore_clipboard(previous: bytes | None) -> None:
    """Restore text clipboard content, falling back to clearing it silently."""
    try:
        _macos_set_clipboard(previous if previous is not None else b"")
        return
    except (NativeUISmokeError, OSError, subprocess.SubprocessError):
        pass
    try:
        _macos_set_clipboard(b"")
    except (NativeUISmokeError, OSError, subprocess.SubprocessError):
        pass


def _macos_paste(profile: Path) -> Callable[[], None]:
    previous = _macos_clipboard_snapshot()
    try:
        _macos_set_clipboard(profile.read_bytes())
    except Exception:
        _macos_restore_clipboard(previous)
        raise
    restored = False

    def restore() -> None:
        nonlocal restored
        if restored:
            return
        restored = True
        _macos_restore_clipboard(previous)

    return restore


class NativeUIController:
    """Keep one real desktop window available to a functional adapter.

    The controller is deliberately a small command/response boundary.  The
    process that owns it must be in the logged-in desktop session; callers
    remain responsible for VPN observations, fault injection, and cleanup.
    This lets a full local lane interleave native user actions with the
    existing platform adapter without replacing CLI/gRPC operations with
    test-only UI hooks.
    """

    def __init__(self, platform: str, binary: Path, profile: Path, timeout: float) -> None:
        if platform not in {"windows", "macos"}:
            raise NativeUISmokeError(f"native UI controller is unsupported on {platform}")
        if timeout <= 0:
            raise NativeUISmokeError("native UI controller timeout must be positive")
        self.platform = platform
        self.binary = binary
        self.profile = profile
        self.timeout = timeout
        self.process: subprocess.Popen[bytes] | None = None
        self.hwnd = 0
        self._reconnecting_seen = False

    def _wait(self, predicate, message: str, timeout: float | None = None) -> None:
        _wait_until(predicate, self.timeout if timeout is None else timeout, message)

    def _windows_process_window(self) -> int:
        if self.process is None:
            return 0
        user32 = ctypes.windll.user32
        found = 0
        process_id = wintypes.DWORD()
        callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)

        @callback_type
        def callback(candidate: int, _lparam: int) -> bool:
            nonlocal found
            user32.GetWindowThreadProcessId(candidate, ctypes.byref(process_id))
            if process_id.value == self.process.pid and user32.IsWindowVisible(candidate):
                found = int(candidate)
                return False
            return True

        user32.EnumWindows(callback, 0)
        return found

    def _windows_find_window(self) -> bool:
        user32 = ctypes.windll.user32
        self.hwnd = self._windows_process_window()
        if not self.hwnd:
            # A launcher may hand the window to a short-lived child.  The
            # title fallback is retained only after process ownership lookup.
            self.hwnd = int(user32.FindWindowW(None, "Dobby VPN"))
        return self.hwnd != 0 and bool(user32.IsWindowVisible(self.hwnd))

    def _windows_key(self, *virtual_keys: int) -> None:
        user32 = ctypes.windll.user32
        for key in virtual_keys:
            user32.keybd_event(key, 0, 0, 0)
        for key in reversed(virtual_keys):
            user32.keybd_event(key, 0, 2, 0)

    def _windows_click_name(self, name: str, *, prefix: bool = False) -> None:
        user32 = ctypes.windll.user32
        if not self.hwnd:
            raise NativeUISmokeError("Windows native UI window is unavailable")
        _windows_click(user32, _windows_accessibility_rect(self.hwnd, name, prefix=prefix))

    def _windows_has_name(self, name: str, *, prefix: bool = False) -> bool:
        return bool(self.hwnd and _windows_has_element(self.hwnd, name, prefix=prefix))

    def _launch_windows(self) -> None:
        user32 = ctypes.windll.user32
        self.process = subprocess.Popen([str(self.binary)])

        def visible() -> bool:
            if self.process is not None and self.process.poll() is not None:
                raise NativeUISmokeError(
                    f"Dobby VPN exited with code {self.process.returncode} before creating a window"
                )
            return self._windows_find_window()

        self._wait(visible, "Dobby VPN window did not become visible")
        left, top, right, bottom = _windows_rect(self.hwnd)
        if right - left < 300 or bottom - top < 300:
            raise NativeUISmokeError("Dobby VPN window is unexpectedly small")
        user32.SetForegroundWindow(self.hwnd)

    def _launch_macos(self) -> None:
        bundle = self.binary
        if self.binary.parent.name == "MacOS" and self.binary.parent.parent.name == "Contents":
            bundle = self.binary.parent.parent.parent
        launch = ["open", "-W", "-n", str(bundle)] if bundle.suffix == ".app" else [str(self.binary)]
        self.process = subprocess.Popen(launch)
        self._wait(
            lambda: _macos_has_element("Connection configuration"),
            "macOS UI did not expose the configuration input",
        )

    def start(self) -> dict[str, object]:
        if self.process is not None:
            raise NativeUISmokeError("native UI is already running")
        if self.platform == "windows":
            self._launch_windows()
        else:
            self._launch_macos()
        return self.snapshot()

    def snapshot(self) -> dict[str, object]:
        if self.platform == "windows":
            names = ("Connected", "Connecting", "Reconnecting", "Disconnected", "Failed", "Error")
            status = next((name for name in names if self._windows_has_name(name)), "Unknown")
        else:
            names = ("Connected", "Connecting", "Reconnecting", "Disconnected", "Failed", "Error")
            status = next((name for name in names if _macos_has_element(name)), "Unknown")
        return {
            "status": status,
            "reconnecting_seen": self._reconnecting_seen,
        }

    def configure(self) -> dict[str, object]:
        restore_clipboard: Callable[[], None]
        if self.platform == "windows":
            restore_clipboard = _windows_paste(self.profile)
            try:
                self._windows_click_name("Connection configuration")
                self._windows_key(0x11, 0x56)  # Ctrl+V
            finally:
                restore_clipboard()
        else:
            restore_clipboard = _macos_paste(self.profile)
            try:
                _macos_click(_macos_accessibility_rect("Connection configuration", 10))
                completed = subprocess.run(
                    ["osascript", "-e", 'tell application "System Events" to keystroke "v" using command down'],
                    check=False,
                    text=True,
                    capture_output=True,
                    timeout=10,
                )
                if completed.returncode != 0:
                    raise NativeUISmokeError("macOS native paste failed")
            finally:
                restore_clipboard()
        return self.snapshot()

    def wait_status(self, status: str, *, timeout: float | None = None) -> dict[str, object]:
        if not status:
            raise NativeUISmokeError("native UI wait state is empty")
        if self.platform == "windows":
            self._wait(lambda: self._windows_has_name(status), f"Windows UI did not display {status}", timeout)
        else:
            self._wait(lambda: _macos_has_element(status), f"macOS UI did not display {status}", timeout)
        if status == "Reconnecting":
            self._reconnecting_seen = True
        return self.snapshot()

    def wait_recovered(self) -> dict[str, object]:
        # A very fast service replacement can leave the visible status at
        # Connected.  Record Reconnecting when it is observable, while still
        # requiring the authoritative post-loss Connected state.
        reconnect_timeout = min(2.0, self.timeout)
        try:
            self.wait_status("Reconnecting", timeout=reconnect_timeout)
        except NativeUISmokeError:
            pass
        self.wait_status("Connected")
        return self.snapshot()

    def connect(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Connect")
        else:
            _macos_click(_macos_accessibility_rect("Connect", 10))
        return self.wait_status("Connected")

    def disconnect(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Disconnect")
        else:
            _macos_click(_macos_accessibility_rect("Disconnect", 10))
        return self.wait_status("Disconnected")

    def settings(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Settings")
            self._wait(lambda: self._windows_has_name("Back"), "Windows UI did not open Settings")
            version = self._windows_has_name("Version:", prefix=True)
            commit = self._windows_has_name("Source commit:", prefix=True)
            self._windows_click_name("Back")
        else:
            _macos_click(_macos_accessibility_rect("Settings", 10))
            self._wait(lambda: _macos_has_element("Back"), "macOS UI did not open Settings")
            version = _macos_has_element("Version:", prefix=True)
            commit = _macos_has_element("Source commit:", prefix=True)
            _macos_click(_macos_accessibility_rect("Back", 10))
        if not version or not commit:
            raise NativeUISmokeError("Settings did not expose version and source-commit metadata")
        return {"settings_version": True, "settings_source_commit": True, **self.snapshot()}

    def close(self) -> dict[str, object]:
        process = self.process
        if process is None:
            return self.snapshot()
        if self.platform == "windows":
            self._windows_key(0x12, 0x73)  # Alt+F4
        else:
            completed = subprocess.run(
                ["osascript", "-e", 'tell application "System Events" to keystroke "w" using command down'],
                check=False,
                text=True,
                capture_output=True,
                timeout=10,
            )
            if completed.returncode != 0:
                raise NativeUISmokeError(completed.stderr.strip() or "macOS native close failed")
        self._wait(lambda: process.poll() is not None, f"{self.platform} UI did not close")
        if process.returncode not in (0, 1):
            raise NativeUISmokeError(f"{self.platform} UI exited with code {process.returncode}")
        self.process = None
        self.hwnd = 0
        return self.snapshot()

    def reopen(self) -> dict[str, object]:
        result = self.start()
        self.wait_status("Connected")
        return result | self.snapshot()

    def close_for_cleanup(self) -> None:
        if self.process is None:
            return
        try:
            self.close()
        except Exception:
            process = self.process
            if process is not None and process.poll() is None:
                if self.platform == "macos":
                    subprocess.run(
                        ["osascript", "-e", 'tell application "Dobby Vpn" to quit'],
                        check=False,
                        timeout=5,
                    )
                process.terminate()
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    process.kill()
            self.process = None
            self.hwnd = 0


def _controller_for(platform: str, binary: Path, profile: Path, timeout: float) -> NativeUIController:
    return NativeUIController(platform, binary, profile, timeout)


def serve_native_ui(
    platform: str,
    binary: Path,
    profile: Path,
    timeout: float,
    input_stream,
    output_stream,
) -> int:
    """Serve native actions for the local full adapter over JSON lines."""
    import json

    controller = _controller_for(platform, binary, profile, timeout)
    encoder = json.JSONEncoder(separators=(",", ":"))
    try:
        controller.start()
        output_stream.write(encoder.encode({"ok": True, "event": "ready", **controller.snapshot()}) + "\n")
        output_stream.flush()
        for line in input_stream:
            try:
                request = json.loads(line)
                if not isinstance(request, dict):
                    raise ValueError("request is not an object")
                operation = request.get("op")
                if operation == "configure":
                    result = controller.configure()
                elif operation == "connect":
                    result = controller.connect()
                elif operation == "disconnect":
                    result = controller.disconnect()
                elif operation == "reconnect":
                    result = controller.connect()
                elif operation == "settings":
                    result = controller.settings()
                elif operation == "wait":
                    result = controller.wait_status(str(request.get("state", "")))
                elif operation in {"recovery", "process_loss_recovery"}:
                    result = controller.wait_recovered()
                elif operation == "close-window":
                    result = controller.close()
                elif operation == "reopen":
                    result = controller.reopen()
                elif operation == "close":
                    result = controller.close()
                    output_stream.write(encoder.encode({"ok": True, **result}) + "\n")
                    output_stream.flush()
                    return 0
                else:
                    raise NativeUISmokeError(f"unsupported native UI operation {operation!r}")
                response = {"ok": True, **result}
            except Exception as error:
                response = {"ok": False, "error": str(error), **controller.snapshot()}
            output_stream.write(encoder.encode(response) + "\n")
            output_stream.flush()
    except Exception as error:
        output_stream.write(encoder.encode({"ok": False, "error": str(error)}) + "\n")
        output_stream.flush()
        return 1
    finally:
        controller.close_for_cleanup()
    return 0


def _standalone_smoke(platform: str, binary: Path, profile: Path, timeout: float) -> None:
    """Run the native-only portion when invoked without a base adapter."""
    controller = _controller_for(platform, binary, profile, timeout)
    try:
        controller.start()
        controller.configure()
        controller.connect()
        controller.settings()
        controller.close()
        controller.reopen()
        controller.disconnect()
        # Reconnect is intentionally a GUI action, even in this standalone
        # diagnostic mode; the full local lane additionally supplies the
        # independent base-adapter evidence around it.
        controller.connect()
        controller.close()
    finally:
        controller.close_for_cleanup()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--ui", type=Path, required=True)
    parser.add_argument("--profile", type=Path, required=True, help="fresh synthetic/owner test profile to enter through the UI")
    parser.add_argument("--timeout", type=float, default=30.0)
    parser.add_argument(
        "--serve",
        action="store_true",
        help="keep the real window open and serve native actions over JSON lines",
    )
    args = parser.parse_args(argv)
    if args.timeout <= 0 or not args.ui.is_file() or not args.profile.is_file():
        raise SystemExit("native UI qualification requires regular UI and profile files")
    if args.platform == "windows":
        identity = _windows_interactive_identity()
        if args.serve:
            return serve_native_ui(args.platform, args.ui, args.profile, args.timeout, sys.stdin, sys.stdout)
        _standalone_smoke(args.platform, args.ui, args.profile, args.timeout)
    else:
        identity = _macos_interactive_identity()
        if args.serve:
            return serve_native_ui(args.platform, args.ui, args.profile, args.timeout, sys.stdin, sys.stdout)
        _standalone_smoke(args.platform, args.ui, args.profile, args.timeout)
    print(f"native-ui-smoke platform={args.platform} identity={identity} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
