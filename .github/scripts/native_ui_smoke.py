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
from dataclasses import dataclass
import getpass
import json
import math
import os
from pathlib import Path
import plistlib
import re
import signal
import shutil
import subprocess
import sys
import time
from typing import TypeVar


class NativeUISmokeError(RuntimeError):
    pass


class NativeUIElementNotFound(NativeUISmokeError):
    """The bounded AX walk completed but did not contain the requested name."""


class NativeUIWindowNotReady(NativeUISmokeError):
    """The process exists but its AX window collection is not ready yet."""


class NativeUIWaitTimeout(NativeUISmokeError):
    """A requested UI state was not observed within its bounded wait."""


_T = TypeVar("_T")


_FILETIME_TO_DATETIME_TICKS = 504911232000000000
_MACOS_UI_PROCESS_NAME = "Dobby Vpn"
_MACOS_AX_HELPER = Path(__file__).with_name("macos_ax.py")
_MACOS_INPUT_SENTINEL = b"DobbyVPN-native-input-sentinel-v1"
_MACOS_COPY_MARKER = b"DobbyVPN-native-copy-marker-v1"
# Keep the parent subprocess alive long enough to receive a helper's final
# JSON after the helper's own AX deadline expires.  Without this separation a
# residual retry can be killed at the same instant it is writing diagnostics.
_MACOS_AX_HELPER_EXIT_RESERVE_SECONDS = 0.25
_MACOS_AX_MIN_HELPER_DEADLINE_SECONDS = 0.1
_MACOS_AX_MESSAGE_TIMEOUT_SECONDS = 1.0
_MACOS_ACCESSIBILITY_PROBE = '''tell application "System Events"
    if not (exists process "Finder") then error "Finder is unavailable"
    if (visible of process "Finder") is false then error "Finder is not visible"
    return "Finder"
end tell'''


class _MacCGPoint(ctypes.Structure):
    """CoreGraphics point passed by value to CGEventCreateMouseEvent."""

    _fields_ = [("x", ctypes.c_double), ("y", ctypes.c_double)]


@dataclass(frozen=True)
class _MacOSProcessIdentity:
    """Stable enough identity for one product process between two observations.

    macOS does not expose a process handle that can be retained by this Python
    helper.  The PID alone is therefore unsafe: the kernel may recycle it
    after the product exits.  ``lstart`` is the process start identity exposed
    by ``ps``; pairing it with the exact executable path and owner makes a
    replacement process fail closed before any signal is sent.
    """

    pid: int
    uid: int
    executable: str
    start: str


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


def _parse_macos_console_user_state(stdout: bytes) -> tuple[str, int] | None:
    """Extract the authoritative SystemConfiguration ConsoleUser identity."""

    if len(stdout) > 64 * 1024:
        return None
    user: str | None = None
    uid: int | None = None
    for line in stdout.decode("utf-8", errors="replace").splitlines():
        if ":" not in line:
            continue
        key, value = (part.strip() for part in line.split(":", 1))
        if key == "kCGSSessionUserNameKey":
            if user is not None or re.fullmatch(r"[^\s:{}]+", value) is None:
                return None
            user = value
        elif key == "kCGSSessionUserIDKey":
            if uid is not None or re.fullmatch(r"[1-9][0-9]*", value) is None:
                return None
            try:
                uid = int(value)
            except ValueError:
                return None
    if user is None or uid is None:
        return None
    return user, uid


def _macos_accessibility_preflight(timeout: float = 5.0) -> None:
    """Require System Events accessibility before launching the production UI."""

    try:
        result = subprocess.run(
            ["osascript", "-e", _MACOS_ACCESSIBILITY_PROBE],
            check=False,
            text=True,
            capture_output=True,
            timeout=timeout,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(
            "macOS native UI is unavailable: System Events accessibility preflight failed"
        ) from error
    output = (
        result.stdout.decode("utf-8", errors="replace")
        if isinstance(result.stdout, bytes)
        else str(result.stdout)
    )
    if result.returncode != 0 or output.strip() != "Finder":
        raise NativeUISmokeError(
            "macOS native UI is unavailable: System Events accessibility permission is unavailable"
        )


def _macos_interactive_identity() -> str:
    """Return the Aqua console user after checking the current launch context."""
    if os.name != "posix":
        raise NativeUISmokeError("macOS native UI qualification requires a macOS host")
    current_user = getpass.getuser()
    try:
        console = subprocess.run(
            ["scutil"],
            input=b"show State:/Users/ConsoleUser\nquit\n",
            check=False,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"could not inspect the macOS console user: {error}") from error
    console_output = console.stdout
    if isinstance(console_output, str):
        console_bytes = console_output.encode("utf-8")
    elif isinstance(console_output, bytes):
        console_bytes = console_output
    else:
        console_bytes = b""
    parsed_console = (
        _parse_macos_console_user_state(console_bytes)
        if console.returncode == 0
        else None
    )
    if parsed_console is None:
        raise NativeUISmokeError("macOS native UI is unavailable: no logged-in Aqua console user")
    console_user, console_uid = parsed_console
    if console_user.lower() in {"root", "loginwindow"} or console_uid <= 0:
        raise NativeUISmokeError("macOS native UI is unavailable: no logged-in Aqua console user")
    if current_user != console_user or os.getuid() != console_uid:
        raise NativeUISmokeError(
            "macOS native UI is unavailable: helper identity does not own the console"
        )
    uid = str(console_uid)
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
        raise NativeUISmokeError("macOS native UI is unavailable: no Aqua GUI launchd session")
    _macos_accessibility_preflight()
    return f"{current_user}|uid={uid}|console={console_user}"


def _wait_until(predicate, timeout: float, message: str) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.1)
    raise NativeUIWaitTimeout(message)


def _windows_rect(hwnd: int) -> tuple[int, int, int, int]:
    class RECT(ctypes.Structure):
        _fields_ = [("left", ctypes.c_long), ("top", ctypes.c_long), ("right", ctypes.c_long), ("bottom", ctypes.c_long)]

    rect = RECT()
    if not _windows_user32().GetWindowRect(hwnd, ctypes.byref(rect)):
        raise NativeUISmokeError("GetWindowRect failed")
    return rect.left, rect.top, rect.right, rect.bottom


def _windows_user32() -> object:
    """Return user32 with pointer-sized window APIs declared explicitly.

    ``ctypes`` otherwise converts undeclared integer arguments to C ``int``.
    HWND values are pointer-sized on 64-bit Windows, so that implicit
    conversion can truncate the handle before ``IsWindowVisible`` or
    ``GetWindowThreadProcessId`` sees it.  The resulting false negative looks
    exactly like a production UI that never created a window.
    """

    user32 = ctypes.windll.user32
    callback_factory = getattr(ctypes, "WINFUNCTYPE", ctypes.CFUNCTYPE)
    user32.EnumWindows.argtypes = [
        callback_factory(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM),
        wintypes.LPARAM,
    ]
    user32.EnumWindows.restype = wintypes.BOOL
    user32.GetWindowThreadProcessId.argtypes = [
        wintypes.HWND,
        ctypes.POINTER(wintypes.DWORD),
    ]
    user32.GetWindowThreadProcessId.restype = wintypes.DWORD
    user32.IsWindowVisible.argtypes = [wintypes.HWND]
    user32.IsWindowVisible.restype = wintypes.BOOL
    user32.IsWindow.argtypes = [wintypes.HWND]
    user32.IsWindow.restype = wintypes.BOOL
    # The RECT structure is local to ``_windows_rect``; a void pointer keeps
    # this declaration independent of that implementation detail while still
    # preserving the pointer-sized HWND argument.
    user32.GetWindowRect.argtypes = [wintypes.HWND, ctypes.c_void_p]
    user32.GetWindowRect.restype = wintypes.BOOL
    user32.SetForegroundWindow.argtypes = [wintypes.HWND]
    user32.SetForegroundWindow.restype = wintypes.BOOL
    return user32


def _windows_process_creation_ticks(pid: int) -> str:
    """Return the process creation time in the WMI/.NET tick epoch."""

    if pid <= 0:
        raise NativeUISmokeError("Windows native UI process identity is invalid")

    class FILETIME(ctypes.Structure):
        _fields_ = [("dwLowDateTime", wintypes.DWORD), ("dwHighDateTime", wintypes.DWORD)]

    kernel32 = ctypes.windll.kernel32
    kernel32.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    kernel32.OpenProcess.restype = wintypes.HANDLE
    kernel32.GetProcessTimes.argtypes = [
        wintypes.HANDLE,
        ctypes.POINTER(FILETIME), ctypes.POINTER(FILETIME),
        ctypes.POINTER(FILETIME), ctypes.POINTER(FILETIME),
    ]
    kernel32.GetProcessTimes.restype = wintypes.BOOL
    kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel32.CloseHandle.restype = wintypes.BOOL
    handle = kernel32.OpenProcess(0x1000, False, pid)  # PROCESS_QUERY_LIMITED_INFORMATION
    if not handle:
        raise NativeUISmokeError("Windows native UI process identity is unavailable")
    creation = FILETIME()
    exit_time = FILETIME()
    kernel_time = FILETIME()
    user_time = FILETIME()
    try:
        if not kernel32.GetProcessTimes(
            handle,
            ctypes.byref(creation), ctypes.byref(exit_time),
            ctypes.byref(kernel_time), ctypes.byref(user_time),
        ):
            raise NativeUISmokeError("Windows native UI process identity is unavailable")
        value = (int(creation.dwHighDateTime) << 32) | int(creation.dwLowDateTime)
    finally:
        kernel32.CloseHandle(handle)
    return _windows_filetime_to_datetime_ticks(value)


def _windows_filetime_to_datetime_ticks(filetime: int) -> str:
    """Convert Win32 FILETIME units to ``DateTime.Ticks``.

    FILETIME is measured from 1601-01-01, while .NET ``DateTime`` ticks are
    measured from year 1.  The offset is therefore the 1601-to-year-1 value,
    not the smaller 1601-to-Unix-epoch offset.
    """

    if isinstance(filetime, bool) or not isinstance(filetime, int) or filetime < 0:
        raise NativeUISmokeError("Windows native UI process identity is invalid")
    return str(filetime + _FILETIME_TO_DATETIME_TICKS)


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
$rawHwnd = [string]$env:DOBBY_UI_HWND
if ($rawHwnd -notmatch '^[1-9][0-9]*$') {
    throw "invalid positive HWND"
}
try {
    $hwndValue = [Int64]::Parse($rawHwnd, [Globalization.CultureInfo]::InvariantCulture)
} catch {
    throw "invalid positive HWND"
}
if ($hwndValue -le 0) {
    throw "invalid positive HWND"
}
$root = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]::new($hwndValue))
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


def _macos_ax_request(
    process_pid: int,
    timeout: float,
    *,
    name: str | None = None,
    prefix: bool = False,
    window: bool = False,
) -> tuple[int, int, int, int]:
    """Run one bounded public AXUIElement lookup in a killable child process."""

    if process_pid <= 0:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    if window == (name is not None):
        raise NativeUISmokeError("macOS accessibility lookup requires a window or element name")
    if not _MACOS_AX_HELPER.is_file():
        raise NativeUISmokeError("macOS native AX helper is missing")
    command = [
        sys.executable,
        str(_MACOS_AX_HELPER),
        "--pid", str(process_pid),
        "--deadline", str(max(
            _MACOS_AX_MIN_HELPER_DEADLINE_SECONDS,
            min(timeout - _MACOS_AX_HELPER_EXIT_RESERVE_SECONDS, 4.0),
        )),
    ]
    if window:
        command.append("--window")
    else:
        command.extend(("--name", str(name)))
        if prefix:
            command.append("--prefix")
    try:
        completed = subprocess.run(
            command,
            check=False,
            text=True,
            capture_output=True,
            timeout=max(0.1, min(timeout, 6.0)),
        )
    except (OSError, subprocess.SubprocessError) as error:
        detail = "window" if window else repr(name)
        raise NativeUISmokeError(f"macOS native AX lookup failed for {detail}: {error}") from error
    try:
        payload = json.loads(completed.stdout.strip() or "{}")
    except (TypeError, ValueError) as error:
        raise NativeUISmokeError(
            f"macOS native AX helper returned invalid JSON for {name or 'window'}"
        ) from error
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        stage = payload.get("stage", "helper") if isinstance(payload, dict) else "helper"
        detail = payload.get("error", "lookup failed") if isinstance(payload, dict) else "lookup failed"
        if isinstance(payload, dict) and stage == "ax-windows" and payload.get("transient") is True:
            raise NativeUIWindowNotReady(f"macOS AX {stage}: {detail}")
        if stage == "control":
            raise NativeUIElementNotFound(f"macOS AX {stage}: {detail}")
        raise NativeUISmokeError(f"macOS AX {stage}: {detail}")
    raw_bounds = payload.get("bounds")
    if not isinstance(raw_bounds, list) or len(raw_bounds) != 4:
        raise NativeUISmokeError(f"macOS AX returned invalid bounds for {name or 'window'}")
    try:
        values = tuple(int(value) for value in raw_bounds)
    except (TypeError, ValueError) as error:
        raise NativeUISmokeError(f"macOS AX returned invalid bounds for {name or 'window'}") from error
    if values[2] <= values[0] or values[3] <= values[1]:
        raise NativeUISmokeError(f"macOS AX returned invalid bounds for {name or 'window'}")
    return values  # type: ignore[return-value]


def _macos_ax_raise_window_once(process_pid: int, timeout: float) -> None:
    """Raise the exact product window through the public AX window action."""

    if process_pid <= 0:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    if not _MACOS_AX_HELPER.is_file():
        raise NativeUISmokeError("macOS native AX helper is missing")
    command = [
        sys.executable,
        str(_MACOS_AX_HELPER),
        "--pid", str(process_pid),
        "--raise-window",
        "--deadline", str(max(0.1, min(timeout, 4.0))),
    ]
    try:
        completed = subprocess.run(
            command,
            check=False,
            text=True,
            capture_output=True,
            timeout=max(0.1, min(timeout, 6.0)),
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS native AX window raise failed: {error}") from error
    try:
        payload = json.loads(completed.stdout.strip() or "{}")
    except (TypeError, ValueError) as error:
        raise NativeUISmokeError("macOS native AX helper returned invalid JSON for window raise") from error
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        stage = payload.get("stage", "helper") if isinstance(payload, dict) else "helper"
        detail = payload.get("error", "window raise failed") if isinstance(payload, dict) else "window raise failed"
        if isinstance(payload, dict) and stage == "ax-windows" and payload.get("transient") is True:
            raise NativeUIWindowNotReady(f"macOS AX {stage}: {detail}")
        raise NativeUISmokeError(f"macOS AX {stage}: {detail}")


def _macos_retry_ax_request(
    timeout: float,
    request: Callable[[float], _T],
    description: str,
) -> _T:
    """Retry only WindowServer readiness failures under one absolute budget."""

    deadline = time.monotonic() + max(timeout, 0.1)
    attempts = 0
    last_transient: NativeUIWindowNotReady | None = None
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            if last_transient is not None:
                raise NativeUIWindowNotReady(
                    f"macOS AX {description} remained transient: "
                    f"{last_transient} (attempts={attempts})"
                )
            raise NativeUISmokeError(
                f"macOS AX {description} deadline expired (attempts={attempts})"
            )
        # Once a transient has consumed the budget, do not start a child that
        # cannot receive its own final JSON before the parent deadline.
        if remaining <= _MACOS_AX_HELPER_EXIT_RESERVE_SECONDS + _MACOS_AX_MESSAGE_TIMEOUT_SECONDS:
            if last_transient is not None:
                raise NativeUIWindowNotReady(
                    f"macOS AX {description} remained transient: "
                    f"{last_transient} (attempts={attempts})"
                )
            raise NativeUISmokeError(
                f"macOS AX {description} has no safely supervised attempt "
                f"within the remaining deadline (attempts={attempts})"
            )
        attempts += 1
        try:
            # Every attempt gets a fresh bounded helper process.  A hung AX
            # walk therefore cannot leak into the next operation.
            return request(min(remaining, 4.0))
        except NativeUIWindowNotReady as error:
            last_transient = error
            time.sleep(min(0.05, max(0.0, deadline - time.monotonic())))


def _macos_ax_raise_window(process_pid: int, timeout: float) -> None:
    """Raise the exact window within one absolute AX readiness budget."""

    _macos_retry_ax_request(
        timeout,
        lambda request_timeout: _macos_ax_raise_window_once(process_pid, request_timeout),
        "window raise",
    )


def _macos_window_rect(process_pid: int, timeout: float) -> tuple[int, int, int, int]:
    return _macos_retry_ax_request(
        timeout,
        lambda request_timeout: _macos_ax_request(process_pid, request_timeout, window=True),
        "window lookup",
    )


def _macos_startup_diagnostic(process_pid: int) -> str:
    """Collect bounded, complete diagnostics for a window that never surfaced."""

    commands = [
        ["ps", "-ww", "-p", str(process_pid), "-o", "pid=,ppid=,state=,etime=,command="],
        ["/usr/bin/sample", str(process_pid), "2", "1"],
    ]
    sections: list[str] = []
    for command in commands:
        try:
            result = subprocess.run(
                command,
                check=False,
                text=True,
                capture_output=True,
                timeout=5,
            )
            stdout = result.stdout if isinstance(result.stdout, str) else ""
            stderr = result.stderr if isinstance(result.stderr, str) else ""
            sections.append(
                "$ " + " ".join(command) + f" (exit={result.returncode})\n"
                + stdout
                + stderr
            )
        except (OSError, subprocess.SubprocessError) as error:
            sections.append("$ " + " ".join(command) + f" (error={error!r})\n")
    return "\n".join(sections)


def _macos_accessibility_rect(
    process_pid: int,
    name: str,
    timeout: float,
    *,
    prefix: bool = False,
) -> tuple[int, int, int, int]:
    """Get a Fyne element through a bounded exact-PID AXUIElement walk."""

    return _macos_retry_ax_request(
        timeout,
        lambda request_timeout: _macos_ax_request(
            process_pid,
            request_timeout,
            name=name,
            prefix=prefix,
        ),
        f"lookup for {name!r}",
    )


def _macos_frontmost_pid() -> int:
    """Read the frontmost GUI process from System Events without guessing."""

    script = '''tell application "System Events"
    return unix id of (first process whose frontmost is true)
end tell'''
    try:
        completed = subprocess.run(
            ["osascript", "-e", script],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS frontmost-process query failed: {error}") from error
    if completed.returncode != 0:
        raise NativeUISmokeError(
            completed.stderr.strip() or "macOS frontmost-process query failed"
        )
    value = completed.stdout.strip()
    if not re.fullmatch(r"[1-9][0-9]*", value):
        raise NativeUISmokeError("macOS frontmost-process query returned invalid output")
    return int(value)


def _macos_focus_window(process_pid: int, timeout: float = 3.0) -> None:
    """Raise and verify one exact-PID window before sending input events."""

    _macos_ax_raise_window(process_pid, min(max(timeout, 0.1), 3.0))
    _wait_until(
        lambda: _macos_frontmost_pid() == process_pid,
        min(max(timeout, 0.1), 3.0),
        f"macOS process {process_pid} did not become frontmost",
    )


def _macos_bounds_contained(
    outer: tuple[int, int, int, int], inner: tuple[int, int, int, int]
) -> bool:
    return (
        inner[0] >= outer[0]
        and inner[1] >= outer[1]
        and inner[2] <= outer[2]
        and inner[3] <= outer[3]
        and inner[2] > inner[0]
        and inner[3] > inner[1]
    )


def _macos_core_graphics() -> tuple[object, object]:
    """Load only public CoreGraphics/CoreFoundation event APIs."""

    try:
        graphics = ctypes.CDLL(
            "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
        )
        core = ctypes.CDLL(
            "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
        )
    except OSError as error:
        raise NativeUISmokeError(f"macOS CoreGraphics input is unavailable: {error}") from error
    void_p = ctypes.c_void_p
    graphics.CGEventCreateMouseEvent.argtypes = [
        void_p,
        ctypes.c_uint32,
        _MacCGPoint,
        ctypes.c_uint32,
    ]
    graphics.CGEventCreateMouseEvent.restype = void_p
    graphics.CGEventCreate.argtypes = [void_p]
    graphics.CGEventCreate.restype = void_p
    graphics.CGEventGetLocation.argtypes = [void_p]
    graphics.CGEventGetLocation.restype = _MacCGPoint
    graphics.CGEventPost.argtypes = [ctypes.c_uint32, void_p]
    graphics.CGEventPost.restype = None
    core.CFRelease.argtypes = [void_p]
    core.CFRelease.restype = None
    return graphics, core


def _macos_click(bounds: tuple[int, int, int, int], process_pid: int) -> None:
    if process_pid <= 0:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    window = _macos_window_rect(process_pid, 5.0)
    if not _macos_bounds_contained(window, bounds):
        raise NativeUISmokeError(
            "macOS native control bounds are outside the exact process window"
        )
    x = (bounds[0] + bounds[2]) // 2
    y = (bounds[1] + bounds[3]) // 2
    _macos_focus_window(process_pid)
    graphics, core = _macos_core_graphics()
    point = _MacCGPoint(float(x), float(y))
    width = bounds[2] - bounds[0]
    # Cocoa/GLFW updates its cached mouse position from mouseMoved, not from
    # mouseDown.  A distinct in-control move followed by a settled center
    # move prevents the down event from being dispatched at the prior cursor
    # location when a synthetic move and down arrive in the same run-loop turn.
    lead_x = bounds[0] + max(1, min(width - 1, width // 4))
    lead_point = _MacCGPoint(float(lead_x), float(y))

    def post_move(move_point: _MacCGPoint) -> None:
        event = graphics.CGEventCreateMouseEvent(None, 5, move_point, 0)
        if not event:
            raise NativeUISmokeError(
                f"macOS CoreGraphics could not create mouse move at "
                f"({move_point.x},{move_point.y})"
            )
        try:
            graphics.CGEventPost(0, event)  # kCGHIDEventTap
        finally:
            core.CFRelease(event)

    post_move(lead_point)
    time.sleep(0.1)
    post_move(point)
    time.sleep(0.1)
    cursor_event = graphics.CGEventCreate(None)
    if not cursor_event:
        raise NativeUISmokeError("macOS CoreGraphics could not read the current cursor")
    try:
        cursor = graphics.CGEventGetLocation(cursor_event)
        if not all(math.isfinite(value) for value in (cursor.x, cursor.y)):
            raise NativeUISmokeError("macOS CoreGraphics returned an invalid cursor location")
        if abs(cursor.x - x) > 2 or abs(cursor.y - y) > 2:
            raise NativeUISmokeError(
                "macOS CoreGraphics cursor did not reach the validated control center "
                f"(ax_center=({x},{y}), cursor=({cursor.x:.1f},{cursor.y:.1f}))"
            )
    finally:
        core.CFRelease(cursor_event)

    # CGEventCreateMouseEvent/CGEventPost are physical input synthesis at the
    # AX-validated control center.  System Events ``click at`` is intentionally
    # not used: on official Fyne Darwin controls it resolves to unsupported
    # AXPress rather than delivering a widget mouse event.
    for event_type in (1, 2):  # left-down, left-up
        event = graphics.CGEventCreateMouseEvent(None, event_type, point, 0)
        if not event:
            raise NativeUISmokeError(
                f"macOS CoreGraphics could not create mouse event at ({x},{y})"
            )
        try:
            graphics.CGEventPost(0, event)  # kCGHIDEventTap
        finally:
            core.CFRelease(event)
        if event_type == 1:
            time.sleep(0.05)
        else:
            time.sleep(0.05)


def _macos_keystroke(process_pid: int, key: str) -> None:
    """Send one exact-PID shortcut after the caller establishes native focus."""
    if process_pid <= 0 or len(key) != 1 or key not in {"a", "v", "c"}:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    script = f'''tell application "System Events"
    tell (first process whose unix id is {process_pid})
        set frontmost to true
        keystroke "{key}" using command down
    end tell
end tell'''
    try:
        completed = subprocess.run(
            ["osascript", "-e", script],
            check=False,
            text=True,
            capture_output=True,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS native keystroke failed for {key!r}: {error}") from error
    if completed.returncode != 0:
        raise NativeUISmokeError(completed.stderr.strip() or "macOS native keystroke failed")


def _macos_focus_next(process_pid: int) -> None:
    """Move focus once through the real Fyne canvas keyboard chain."""

    if process_pid <= 0:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    _macos_focus_window(process_pid)
    script = f'''tell application "System Events"
    tell (first process whose unix id is {process_pid})
        set frontmost to true
        key code 48
    end tell
end tell'''
    try:
        completed = subprocess.run(
            ["osascript", "-e", script],
            check=False,
            text=True,
            capture_output=True,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(f"macOS native focus traversal failed: {error}") from error
    if completed.returncode != 0:
        raise NativeUISmokeError(completed.stderr.strip() or "macOS native focus traversal failed")


def _macos_has_element(
    process_pid: int,
    name: str,
    *,
    prefix: bool = False,
    timeout: float = 10,
) -> bool:
    try:
        _macos_accessibility_rect(process_pid, name, timeout, prefix=prefix)
        return True
    except NativeUIElementNotFound:
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


_MACOS_PASTEBOARD_CHANGE_COUNT_SCRIPT = '''ObjC.import("AppKit");
const count = ObjC.unwrap($.NSPasteboard.generalPasteboard.changeCount);
count.toString();'''


def _macos_profile_bytes(profile: Path) -> bytes:
    try:
        value = profile.read_bytes()
    except OSError as error:
        raise NativeUISmokeError("macOS native configuration profile could not be read") from error
    if b"\x00" in value:
        raise NativeUISmokeError(
            "macOS native configuration profile contains NUL "
            f"(bytes={len(value)}, first_nul={value.index(b'\x00')})"
        )
    try:
        value.decode("utf-8")
    except UnicodeDecodeError as error:
        raise NativeUISmokeError(
            "macOS native configuration profile is not UTF-8 "
            f"(bytes={len(value)}, invalid_byte={error.start})"
        ) from error
    return value


def _macos_clipboard_set_verified(value: bytes, *, timeout: float = 3.0) -> None:
    _macos_set_clipboard(value)
    observed: bytes | None = None
    try:
        _wait_until(
            lambda: (lambda current: current == value)(
                _macos_clipboard_snapshot()
            ),
            min(max(timeout, 0.1), 5.0),
            "macOS clipboard did not retain requested input",
        )
        return
    except NativeUIWaitTimeout as error:
        observed = _macos_clipboard_snapshot()
        observed_length = len(observed) if observed is not None else None
        raise NativeUISmokeError(
            "macOS clipboard did not retain requested input "
            f"(expected_bytes={len(value)}, observed_bytes={observed_length})"
        ) from error


def _macos_pasteboard_change_count() -> int:
    try:
        completed = subprocess.run(
            [
                "osascript", "-l", "JavaScript", "-e",
                _MACOS_PASTEBOARD_CHANGE_COUNT_SCRIPT,
            ],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError(
            f"macOS pasteboard change-count query failed: {error}"
        ) from error
    if completed.returncode != 0:
        raise NativeUISmokeError(
            completed.stderr.strip() or "macOS pasteboard change-count query failed"
        )
    value = completed.stdout.strip()
    if not re.fullmatch(r"[0-9]+", value):
        raise NativeUISmokeError(
            "macOS pasteboard change-count query returned invalid output"
        )
    return int(value)


def _macos_copy_selection_verified(
    expected: bytes,
    process_pid: int,
    *,
    timeout: float,
    control_bounds: tuple[int, int, int, int] | None = None,
) -> None:
    """Exercise Cmd+A/C and compare after pasteboard generation changes."""

    _macos_keystroke(process_pid, "a")
    # Make a copy a detectable state transition even when the field already
    # equals the expected value.  The marker is synthetic and never logged.
    _macos_clipboard_set_verified(_MACOS_COPY_MARKER)
    before = _macos_pasteboard_change_count()
    _macos_keystroke(process_pid, "c")
    try:
        _wait_until(
            lambda: _macos_pasteboard_change_count() != before,
            min(max(timeout, 0.1), 30.0),
            "macOS Entry did not complete Cmd+C",
        )
    except NativeUIWaitTimeout as error:
        raise NativeUISmokeError(
            "macOS Entry did not complete Cmd+C "
            f"(expected_bytes={len(expected)}, pasteboard_changed=false)"
        ) from error
    observed = _macos_clipboard_snapshot()
    if observed != expected:
        observed_length = len(observed) if observed is not None else None
        try:
            frontmost = _macos_frontmost_pid()
        except NativeUISmokeError as error:
            frontmost = f"unavailable:{error}"
        details = (
            f", control_bounds={control_bounds},"
            f" control_center={None if control_bounds is None else ((control_bounds[0] + control_bounds[2]) // 2, (control_bounds[1] + control_bounds[3]) // 2)},"
            f" frontmost_pid={frontmost}"
        )
        raise NativeUISmokeError(
            "macOS native configuration copy-back mismatch "
            f"(expected_bytes={len(expected)}, observed_bytes={observed_length}{details})"
        )


def _macos_current_uid() -> int:
    """Return a usable non-root UID for scoped macOS process operations."""

    getuid = getattr(os, "getuid", None)
    if not callable(getuid):
        raise NativeUISmokeError("macOS native UI process ownership is unavailable")
    try:
        uid = getuid()
    except OSError as error:
        raise NativeUISmokeError("macOS native UI process ownership is unavailable") from error
    if isinstance(uid, bool) or not isinstance(uid, int) or uid <= 0:
        raise NativeUISmokeError("macOS native UI process ownership is unavailable")
    return uid


def _macos_process_pids() -> tuple[int, ...]:
    """Return exact product UI PIDs owned by this user, failing closed."""

    uid = _macos_current_uid()

    try:
        result = subprocess.run(
            ["pgrep", "-x", "-u", str(uid), _MACOS_UI_PROCESS_NAME],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError("macOS native UI process discovery failed") from error
    if result.returncode not in {0, 1}:
        raise NativeUISmokeError("macOS native UI process discovery failed")
    if not isinstance(result.stdout, str):
        raise NativeUISmokeError("macOS native UI process discovery returned invalid output")
    if result.returncode == 1:
        if not isinstance(result.stderr, str) or result.stdout.strip() or result.stderr.strip():
            raise NativeUISmokeError("macOS native UI process discovery returned invalid output")
    pids: list[int] = []
    for line in result.stdout.splitlines():
        value = line.strip()
        if not value or not value.isdigit() or int(value) <= 0:
            raise NativeUISmokeError("macOS native UI process discovery returned invalid identity")
        pids.append(int(value))
    return tuple(dict.fromkeys(pids))


def _macos_process_identity(pid: int) -> _MacOSProcessIdentity | None:
    """Read one process's exact path/start identity, or ``None`` if it exited.

    ``comm`` is truncated/path-like on macOS, so it cannot identify the
    executable.  ``ps -ww`` with ``command`` gives the complete executable
    path; because the path may contain spaces, the output is parsed from the
    fixed ``lstart`` field rather than split into shell-like words.  The
    resulting command field is intentionally retained as one exact string:
    an expected bundle path is compared by the caller, so a command with
    arguments or another same-name executable cannot pass.
    """

    if isinstance(pid, bool) or not isinstance(pid, int) or pid <= 0:
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    try:
        result = subprocess.run(
            ["ps", "-ww", "-p", str(pid), "-o", "pid=,uid=,lstart=,command="],
            check=False,
            text=True,
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativeUISmokeError("macOS native UI process identity check failed") from error
    if not isinstance(result.stdout, str) or not isinstance(result.stderr, str):
        raise NativeUISmokeError("macOS native UI process identity check returned invalid output")
    if result.returncode == 1 and not result.stdout.strip() and not result.stderr.strip():
        # ps uses status 1 with no output when the process exited between the
        # scoped discovery and this identity check.
        return None
    if result.returncode != 0:
        raise NativeUISmokeError("macOS native UI process identity check failed")
    values = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    if len(values) != 1:
        raise NativeUISmokeError("macOS native UI process identity check returned invalid output")
    match = re.fullmatch(
        r"\s*(?P<pid>[1-9][0-9]*)\s+"
        r"(?P<uid>[1-9][0-9]*)\s+"
        r"(?P<start>(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun)\s+"
        r"[A-Za-z]{3}\s+[0-9]{1,2}\s+[0-9]{2}:[0-9]{2}:[0-9]{2}\s+[0-9]{4})\s+"
        r"(?P<command>/\S.*)\s*",
        values[0],
    )
    if match is None:
        raise NativeUISmokeError("macOS native UI process identity check returned invalid identity")
    pid_text = match.group("pid")
    uid_text = match.group("uid")
    executable = match.group("command")
    start = match.group("start")
    if int(pid_text) != pid:
        raise NativeUISmokeError("macOS native UI process identity check returned invalid PID")
    return _MacOSProcessIdentity(pid, int(uid_text), executable, start)


def _macos_process_identities(expected_executable: str) -> tuple[_MacOSProcessIdentity, ...]:
    """Snapshot exact product identities without widening the process scope."""

    uid = _macos_current_uid()
    identities: list[_MacOSProcessIdentity] = []
    for pid in _macos_process_pids():
        identity = _macos_process_identity(pid)
        if identity is None:
            continue
        if identity.executable != expected_executable:
            # A previous interrupted run may leave another same-name binary
            # alive.  It is outside this run's exact executable scope and is
            # intentionally ignored; only an exact path may be terminated or
            # selected as this run's child.
            continue
        # A PID can have changed between pgrep and ps.  Treat that process as
        # gone rather than ever allowing a different owner to be signalled.
        if identity.uid != uid:
            continue
        identities.append(identity)
    return tuple(identities)


def _macos_identity_is_alive(identity: _MacOSProcessIdentity) -> bool:
    current = _macos_process_identity(identity.pid)
    return current is not None and current == identity


def _terminate_macos_process(identity: _MacOSProcessIdentity, sig: int) -> None:
    if sig not in {signal.SIGTERM, signal.SIGKILL}:
        raise NativeUISmokeError("macOS native UI process signal is invalid")
    if not isinstance(identity, _MacOSProcessIdentity):
        raise NativeUISmokeError("macOS native UI process identity is unavailable")
    current = _macos_process_identity(identity.pid)
    if current is None or current != identity:
        # The expected process exited, or its PID was reused.  In both cases
        # the original process is gone and the replacement must not be touched.
        return
    if current.uid != _macos_current_uid():
        raise NativeUISmokeError("macOS native UI process ownership could not be verified")
    try:
        os.kill(identity.pid, sig)
    except ProcessLookupError:
        return
    except OSError as error:
        raise NativeUISmokeError("macOS native UI process cleanup failed") from error


def _terminate_macos_process_tree(identity: _MacOSProcessIdentity, timeout: float) -> None:
    """Stop the exact app process even when its ``open`` launcher exited."""

    if not _macos_identity_is_alive(identity):
        return
    _terminate_macos_process(identity, signal.SIGTERM)
    try:
        _wait_until(
            lambda: not _macos_identity_is_alive(identity),
            min(max(timeout, 0.1), 5.0),
            "macOS native UI process did not exit after verified cleanup",
        )
        return
    except NativeUIWaitTimeout:
        _terminate_macos_process(identity, signal.SIGKILL)
        _wait_until(
            lambda: not _macos_identity_is_alive(identity),
            2.0,
            "macOS native UI process survived verified termination",
        )


def _terminate_existing_macos_instances(timeout: float, expected_executable: str) -> None:
    """Clear exact product UI instances before starting a disposable run."""

    existing = _macos_process_identities(expected_executable)
    for identity in existing:
        _terminate_macos_process(identity, signal.SIGTERM)
    if not existing:
        return
    try:
        _wait_until(
            lambda: not any(_macos_identity_is_alive(identity) for identity in existing),
            timeout,
            "pre-existing macOS Dobby Vpn process did not exit",
        )
    except NativeUIWaitTimeout:
        remaining = [identity for identity in existing if _macos_identity_is_alive(identity)]
        for identity in remaining:
            _terminate_macos_process(identity, signal.SIGKILL)
        _wait_until(
            lambda: not any(_macos_identity_is_alive(identity) for identity in existing),
            timeout,
            "pre-existing macOS Dobby Vpn process could not be terminated",
        )


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
        self.macos_pid: int | None = None
        self.macos_process_identity: _MacOSProcessIdentity | None = None
        self.macos_expected_executable: str | None = None
        self._reconnecting_seen = False
        marker = os.environ.get("DOBBYVPN_NATIVE_UI_CHILD_PID_FILE")
        self._windows_child_pid_file = (
            Path(marker) if platform == "windows" and marker else None
        )

    def _wait(self, predicate, message: str, timeout: float | None = None) -> None:
        _wait_until(predicate, self.timeout if timeout is None else timeout, message)

    def _windows_matching_windows(self) -> list[dict[str, object]]:
        if self.process is None:
            return []
        user32 = _windows_user32()
        matched: list[dict[str, object]] = []
        callback_factory = getattr(ctypes, "WINFUNCTYPE", ctypes.CFUNCTYPE)
        callback_type = callback_factory(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)

        @callback_type
        def callback(candidate: int, _lparam: int) -> bool:
            process_id = wintypes.DWORD()
            user32.GetWindowThreadProcessId(candidate, ctypes.byref(process_id))
            if process_id.value != self.process.pid:
                return True
            is_window = bool(user32.IsWindow(candidate))
            is_visible = bool(user32.IsWindowVisible(candidate))
            matched.append({
                "hwnd": int(candidate),
                "is_window": is_window,
                "is_visible": is_visible,
            })
            return True

        if not user32.EnumWindows(callback, 0):
            raise NativeUISmokeError("EnumWindows failed during native UI discovery")
        return matched

    def _windows_process_window(self) -> int:
        for candidate in self._windows_matching_windows():
            if candidate["is_window"] and candidate["is_visible"]:
                return int(candidate["hwnd"])
        return 0

    def _windows_window_diagnostics(self) -> dict[str, object]:
        """Describe only the launched process's top-level windows."""

        process = self.process
        diagnostics: dict[str, object] = {
            "child_alive": bool(process is not None and process.poll() is None),
            "matching_window_count": 0,
            "matching_windows": [],
        }
        try:
            matched = self._windows_matching_windows()
        except Exception:
            diagnostics["matching_window_count"] = None
            diagnostics["window_enumeration"] = "failed"
            return diagnostics
        diagnostics["window_enumeration"] = "ok"
        diagnostics["matching_window_count"] = len(matched)
        windows: list[dict[str, object]] = []
        for candidate in matched[:8]:
            record = dict(candidate)
            if record.get("is_window") is True:
                try:
                    record["rect"] = _windows_rect(int(record["hwnd"]))
                except Exception:
                    record["rect"] = None
            else:
                record["rect"] = None
            windows.append(record)
        diagnostics["matching_windows"] = windows
        diagnostics["matching_windows_truncated"] = len(matched) > len(windows)
        return diagnostics

    def _windows_find_window(self) -> bool:
        user32 = _windows_user32()
        self.hwnd = self._windows_process_window()
        return self.hwnd != 0 and bool(user32.IsWindowVisible(self.hwnd))

    def _windows_validate_window(self) -> None:
        """Reject a dead child or HWND no longer owned by that exact child."""

        process = self.process
        if process is None:
            raise NativeUISmokeError("Windows native UI process is unavailable")
        try:
            if process.poll() is not None:
                raise NativeUISmokeError("Windows native UI process has exited")
        except NativeUISmokeError:
            raise
        except Exception as error:
            raise NativeUISmokeError("Windows native UI process state is unavailable") from error
        if not self.hwnd:
            raise NativeUISmokeError("Windows native UI window is unavailable")
        try:
            matched = self._windows_matching_windows()
        except NativeUISmokeError:
            raise
        except Exception as error:
            raise NativeUISmokeError("Windows native UI window ownership is unavailable") from error
        current = next(
            (candidate for candidate in matched if int(candidate.get("hwnd", 0)) == self.hwnd),
            None,
        )
        if current is None or current.get("is_window") is not True or current.get("is_visible") is not True:
            raise NativeUISmokeError(
                "Windows native UI window is stale or is not owned by the launched process"
            )

    def _windows_key(self, *virtual_keys: int) -> None:
        self._windows_validate_window()
        user32 = ctypes.windll.user32
        for key in virtual_keys:
            user32.keybd_event(key, 0, 0, 0)
        for key in reversed(virtual_keys):
            user32.keybd_event(key, 0, 2, 0)

    def _windows_click_name(self, name: str, *, prefix: bool = False) -> None:
        self._windows_validate_window()
        user32 = ctypes.windll.user32
        bounds = _windows_accessibility_rect(self.hwnd, name, prefix=prefix)
        self._windows_validate_window()
        _windows_click(user32, bounds)

    def _windows_has_name(self, name: str, *, prefix: bool = False) -> bool:
        if not self.hwnd:
            return False
        self._windows_validate_window()
        return bool(_windows_has_element(self.hwnd, name, prefix=prefix))

    def _launch_windows(self) -> None:
        user32 = _windows_user32()
        self.process = subprocess.Popen([str(self.binary)])
        if self._windows_child_pid_file is not None:
            try:
                creation_ticks = _windows_process_creation_ticks(self.process.pid)
                self._windows_child_pid_file.write_text(
                    f"{self.process.pid}|{creation_ticks}", encoding="ascii"
                )
            except (OSError, NativeUISmokeError) as error:
                process = self.process
                if process.poll() is None:
                    process.terminate()
                self.process = None
                raise NativeUISmokeError(
                    "Windows native UI process identity could not be recorded"
                ) from error

        def visible() -> bool:
            if self.process is not None and self.process.poll() is not None:
                raise NativeUISmokeError(
                    f"Dobby VPN exited with code {self.process.returncode} before creating a window"
                )
            return self._windows_find_window()

        try:
            self._wait(visible, "Dobby VPN window did not become visible")
        except NativeUISmokeError as error:
            diagnostic = json.dumps(
                self._windows_window_diagnostics(),
                sort_keys=True,
                separators=(",", ":"),
            )
            raise NativeUISmokeError(
                f"{error}; window-discovery={diagnostic}"
            ) from error
        self._windows_validate_window()
        left, top, right, bottom = _windows_rect(self.hwnd)
        if right - left < 300 or bottom - top < 300:
            raise NativeUISmokeError("Dobby VPN window is unexpectedly small")
        self._windows_validate_window()
        user32.SetForegroundWindow(self.hwnd)

    def _launch_macos(self) -> None:
        bundle = self.binary
        if bundle.suffix != ".app":
            raise NativeUISmokeError(
                "macOS native UI requires a product-shaped .app bundle"
            )
        executable = bundle / "Contents" / "MacOS" / _MACOS_UI_PROCESS_NAME
        info = bundle / "Contents" / "Info.plist"
        if not executable.is_file() or not info.is_file():
            raise NativeUISmokeError("macOS native UI bundle is incomplete")
        try:
            with info.open("rb") as stream:
                metadata = plistlib.load(stream)
        except (OSError, plistlib.InvalidFileException, ValueError) as error:
            raise NativeUISmokeError("macOS native UI bundle metadata is invalid") from error
        if not isinstance(metadata, dict) or metadata.get("CFBundleExecutable") != _MACOS_UI_PROCESS_NAME:
            raise NativeUISmokeError("macOS native UI bundle executable metadata is invalid")
        self.macos_expected_executable = str(executable.resolve())
        if self.macos_expected_executable is None:
            raise NativeUISmokeError("macOS native UI executable identity is unavailable")
        _terminate_existing_macos_instances(self.timeout, self.macos_expected_executable)
        launch = ["open", "-W", "-n"]
        for name in ("HOME", "DOBBYVPN_CONTROL_SOCKET"):
            value = os.environ.get(name)
            if value:
                launch.extend(("--env", f"{name}={value}"))
        output_root = os.environ.get("DOBBYVPN_NATIVE_UI_LOG_DIR")
        if output_root:
            root = Path(output_root)
            root.mkdir(mode=0o700, parents=True, exist_ok=True)
            launch.extend(("--stdout", str(root / "macos-app.stdout.log")))
            launch.extend(("--stderr", str(root / "macos-app.stderr.log")))
        launch.append(str(bundle))
        self.process = subprocess.Popen(launch)
        last_window_not_ready: NativeUIWindowNotReady | None = None

        def visible() -> bool:
            nonlocal last_window_not_ready
            if self.process is not None and self.process.poll() is not None:
                raise NativeUISmokeError(
                    f"Dobby VPN exited with code {self.process.returncode} before creating a window"
                )
            if self.macos_pid is None:
                if self.macos_expected_executable is None:
                    raise NativeUISmokeError("macOS native UI executable identity is unavailable")
                identities = _macos_process_identities(self.macos_expected_executable)
                if len(identities) > 1:
                    raise NativeUISmokeError("multiple Dobby Vpn processes appeared during launch")
                if len(identities) == 1:
                    self.macos_process_identity = identities[0]
                    self.macos_pid = identities[0].pid
            elif self.macos_process_identity is None:
                identity = _macos_process_identity(self.macos_pid)
                if identity is not None:
                    if (
                        self.macos_expected_executable is None
                        or identity.executable != self.macos_expected_executable
                    ):
                        raise NativeUISmokeError(
                            "macOS native UI process identity returned an unexpected executable"
                        )
                    if identity.uid != _macos_current_uid():
                        raise NativeUISmokeError("macOS native UI process ownership could not be verified")
                    self.macos_process_identity = identity
            if self.macos_process_identity is None:
                return False
            process_pid = self._macos_pid_or_error()
            # Keep window existence and control discovery as separate bounded
            # stages.  The helper uses CoreGraphics only for diagnostics and
            # never turns screen coordinates into an interaction fallback.
            try:
                _macos_window_rect(process_pid, min(2.0, self.timeout))
                return _macos_has_element(process_pid, "Connection configuration")
            except NativeUIWindowNotReady as error:
                # A process can be discoverable a few milliseconds before
                # Fyne's OnStarted callback calls Show and attaches AX roots.
                # Retry only this explicit no-value/empty-collection result;
                # disabled AX, malformed helper output, and timeouts remain
                # hard failures.
                last_window_not_ready = error
                return False

        try:
            self._wait(
                visible,
                "macOS UI did not expose the configuration input",
            )
        except NativeUIWaitTimeout as error:
            failure: NativeUISmokeError = error
            if last_window_not_ready is not None:
                failure = NativeUISmokeError(
                    f"{error}: {last_window_not_ready}"
                )
            if self.macos_process_identity is not None:
                diagnostic = _macos_startup_diagnostic(self.macos_process_identity.pid)
                if diagnostic:
                    failure = NativeUISmokeError(
                        f"{failure}; startup-diagnostic:\n{diagnostic}"
                    )
            raise failure from error

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
            status = "Unknown"
            for name in names:
                if self.macos_pid is None:
                    break
                try:
                    process_pid = self._macos_pid_or_error()
                except NativeUISmokeError:
                    break
                if _macos_has_element(process_pid, name):
                    status = name
                    break
        return {
            "status": status,
            "reconnecting_seen": self._reconnecting_seen,
        }

    def _macos_action_state(self, process_pid: int) -> str | None:
        """Return the first current post-activation state, without secrets."""

        # Probe the positive transition first.  Each AX query is a fresh,
        # bounded helper process; a sub-second deadline can expire during
        # Python/ctypes startup before the tree walk begins.
        for name in ("Connecting", "Disconnect"):
            if _macos_has_element(process_pid, name, timeout=1.5):
                return name
        # Error/Failed are checked only when no accepted transition is
        # visible, so a slow negative lookup cannot hide a real Connect.
        for name in ("Error", "Failed"):
            if _macos_has_element(process_pid, name, timeout=1.5):
                return name
        return None

    def _macos_failure_state(self, process_pid: int) -> str | None:
        for name in ("Error", "Failed"):
            if _macos_has_element(process_pid, name, timeout=1.5):
                return name
        return None

    def _macos_wait_for_activation(
        self,
        process_pid: int,
        bounds: tuple[int, int, int, int],
    ) -> None:
        """Require a real Connect input acknowledgement before long polling."""

        observed: dict[str, str | None] = {"state": None}

        def accepted() -> bool:
            state = self._macos_action_state(process_pid)
            observed["state"] = state
            if state in {"Error", "Failed"}:
                raise NativeUISmokeError(
                    f"macOS UI reported {state} immediately after activation"
                )
            return state in {"Connecting", "Disconnect"}

        try:
            self._wait(
                accepted,
                "macOS UI did not acknowledge Connect activation within 2 seconds",
                timeout=min(2.0, self.timeout),
            )
        except NativeUIWaitTimeout as error:
            try:
                frontmost = _macos_frontmost_pid()
            except NativeUISmokeError as frontmost_error:
                frontmost = f"unavailable:{frontmost_error}"
            raise NativeUISmokeError(
                f"{error}; click_center=({(bounds[0] + bounds[2]) // 2},"
                f"{(bounds[1] + bounds[3]) // 2}); frontmost_pid={frontmost}; "
                f"visible_state={observed['state'] or 'none'}"
            ) from error

    def configure(self) -> dict[str, object]:
        restore_clipboard: Callable[[], None]
        if self.platform == "windows":
            restore_clipboard = _windows_paste(self.profile)
            try:
                self._windows_click_name("Connection configuration")
                self._windows_key(0x11, 0x41)  # Ctrl+A
                self._windows_key(0x11, 0x56)  # Ctrl+V
            finally:
                restore_clipboard()
        else:
            profile_bytes = _macos_profile_bytes(self.profile)
            previous_clipboard = _macos_clipboard_snapshot()
            try:
                process_pid = self._macos_pid_or_error()
                bounds = _macos_accessibility_rect(process_pid, "Connection configuration", 10)
                process_pid = self._macos_pid_or_error()
                # Fyne's first focusable object is the configuration Entry.
                # A single native Tab reaches it without depending on the
                # renderer's cached mouse position; Connect/Disconnect below
                # remain physical AX-discovered clicks.
                _macos_focus_next(process_pid)
                _macos_clipboard_set_verified(_MACOS_INPUT_SENTINEL)
                process_pid = self._macos_pid_or_error()
                _macos_keystroke(process_pid, "a")
                process_pid = self._macos_pid_or_error()
                _macos_keystroke(process_pid, "v")
                process_pid = self._macos_pid_or_error()
                _macos_copy_selection_verified(
                    _MACOS_INPUT_SENTINEL,
                    process_pid,
                    timeout=min(5.0, self.timeout),
                    control_bounds=bounds,
                )
                _macos_clipboard_set_verified(profile_bytes)
                process_pid = self._macos_pid_or_error()
                _macos_keystroke(process_pid, "a")
                process_pid = self._macos_pid_or_error()
                _macos_keystroke(process_pid, "v")
                process_pid = self._macos_pid_or_error()
                _macos_copy_selection_verified(
                    profile_bytes,
                    process_pid,
                    timeout=min(30.0, self.timeout),
                    control_bounds=bounds,
                )
            finally:
                _macos_restore_clipboard(previous_clipboard)
        return {"input_verified": True}

    def wait_status(self, status: str, *, timeout: float | None = None) -> dict[str, object]:
        if not status:
            raise NativeUISmokeError("native UI wait state is empty")
        if self.platform == "windows":
            self._wait(lambda: self._windows_has_name(status), f"Windows UI did not display {status}", timeout)
        else:
            def visible() -> bool:
                process_pid = self._macos_pid_or_error()
                if status not in {"Error", "Failed"}:
                    failure = self._macos_failure_state(process_pid)
                    if failure in {"Error", "Failed"}:
                        raise NativeUISmokeError(
                            f"macOS UI reported {failure} while waiting for {status}"
                        )
                return _macos_has_element(
                    process_pid,
                    status,
                    timeout=min(1.0, self.timeout),
                )

            self._wait(
                visible,
                f"macOS UI did not display {status}",
                timeout,
            )
        if status == "Reconnecting":
            self._reconnecting_seen = True
        return self.snapshot()

    def _connect_control_visible(self) -> bool:
        if self.platform == "windows":
            return self._windows_has_name("Connect")
        if self.macos_pid is None:
            return False
        try:
            return _macos_has_element(self._macos_pid_or_error(), "Connect")
        except NativeUISmokeError:
            return False

    def recover_after_process_loss(self) -> dict[str, object]:
        """Reconfigure and reconnect through the visible production UI.

        Restarting the desktop service invalidates its predecessor session;
        merely waiting for a rendered Connected label would therefore prove
        only stale UI state.  Wait for the replacement session to expose its
        Connect control, enter the profile through the real native input path,
        and click that control.  The caller owns the independent VPN
        observations after this action.
        """
        self._reconnecting_seen = False
        reconnect_timeout = min(2.0, self.timeout)
        reconnect_probe_error: NativeUIWaitTimeout | None = None
        try:
            self.wait_status("Reconnecting", timeout=reconnect_timeout)
        except NativeUIWaitTimeout as error:
            # A fast replacement may never render this intermediate state.
            # Keep the bounded probe diagnostic without making it a pass
            # condition; non-timeout driver/accessibility errors still abort.
            reconnect_probe_error = error
        self._wait(
            self._connect_control_visible,
            f"{self.platform} UI did not expose Connect after service recovery",
        )
        self.configure()
        result = self.connect()
        result["reconnecting_seen"] = self._reconnecting_seen
        if reconnect_probe_error is not None:
            result["reconnecting_probe_error"] = str(reconnect_probe_error)
        return result

    def connect(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Connect")
        else:
            process_pid = self._macos_pid_or_error()
            bounds = _macos_accessibility_rect(process_pid, "Connect", 10)
            process_pid = self._macos_pid_or_error()
            _macos_click(bounds, process_pid)
            process_pid = self._macos_pid_or_error()
            self._macos_wait_for_activation(process_pid, bounds)
        return self.wait_status("Connected")

    def disconnect(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Disconnect")
        else:
            process_pid = self._macos_pid_or_error()
            bounds = _macos_accessibility_rect(process_pid, "Disconnect", 10)
            process_pid = self._macos_pid_or_error()
            _macos_click(bounds, process_pid)
        return self.wait_status("Disconnected")

    def settings(self) -> dict[str, object]:
        if self.platform == "windows":
            self._windows_click_name("Settings")
            self._wait(lambda: self._windows_has_name("Back"), "Windows UI did not open Settings")
            version = self._windows_has_name("Version:", prefix=True)
            commit = self._windows_has_name("Source commit:", prefix=True)
            self._windows_click_name("Back")
        else:
            process_pid = self._macos_pid_or_error()
            bounds = _macos_accessibility_rect(process_pid, "Settings", 10)
            process_pid = self._macos_pid_or_error()
            _macos_click(bounds, process_pid)
            self._wait(
                lambda: _macos_has_element(self._macos_pid_or_error(), "Back"),
                "macOS UI did not open Settings",
            )
            version = _macos_has_element(
                self._macos_pid_or_error(), "Version:", prefix=True
            )
            commit = _macos_has_element(
                self._macos_pid_or_error(), "Source commit:", prefix=True
            )
            process_pid = self._macos_pid_or_error()
            bounds = _macos_accessibility_rect(process_pid, "Back", 10)
            process_pid = self._macos_pid_or_error()
            _macos_click(bounds, process_pid)
        if not version or not commit:
            raise NativeUISmokeError("Settings did not expose version and source-commit metadata")
        return {"settings_version": True, "settings_source_commit": True, **self.snapshot()}

    def close(self) -> dict[str, object]:
        process = self.process
        if process is None:
            if self.platform == "macos" and self.macos_process_identity is not None:
                _terminate_macos_process_tree(self.macos_process_identity, self.timeout)
                self.macos_pid = None
                self.macos_process_identity = None
                self.macos_expected_executable = None
            return self.snapshot()
        if self.platform == "windows":
            self._windows_key(0x12, 0x73)  # Alt+F4
        elif process.poll() is None:
            process_pid = self._macos_pid_or_error()
            completed = subprocess.run(
                [
                    "osascript", "-e",
                    f'''tell application "System Events"
    tell (first process whose unix id is {process_pid})
        set frontmost to true
        keystroke "w" using command down
    end tell
end tell''',
                ],
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
        if self.platform == "macos" and self.macos_process_identity is not None:
            _terminate_macos_process_tree(self.macos_process_identity, self.timeout)
        self.process = None
        self.hwnd = 0
        self.macos_pid = None
        self.macos_process_identity = None
        self.macos_expected_executable = None
        return self.snapshot()

    def reopen(self) -> dict[str, object]:
        result = self.start()
        self.wait_status("Connected")
        return result | self.snapshot()

    def close_for_cleanup(self) -> None:
        if self.platform == "macos":
            # LaunchServices' ``open`` process is only a launcher. It may
            # exit while the exact product process remains alive, and an
            # AppleScript close can block when startup failed. Kill the
            # verified product identity first, then reap only our launcher.
            process = self.process
            cleanup_error: BaseException | None = None
            try:
                if self.macos_process_identity is not None:
                    _terminate_macos_process_tree(
                        self.macos_process_identity, self.timeout
                    )
                elif self.macos_expected_executable is not None:
                    _terminate_existing_macos_instances(
                        self.timeout, self.macos_expected_executable
                    )
            except BaseException as error:
                cleanup_error = error
            if process is not None and process.poll() is None:
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    try:
                        process.terminate()
                        process.wait(timeout=2)
                    except BaseException as error:
                        cleanup_error = cleanup_error or error
                    if process.poll() is None:
                        try:
                            process.kill()
                            process.wait(timeout=1)
                        except BaseException as error:
                            cleanup_error = cleanup_error or error
                except BaseException as error:
                    cleanup_error = cleanup_error or error
            self.process = None
            self.hwnd = 0
            self.macos_pid = None
            self.macos_process_identity = None
            self.macos_expected_executable = None
            if cleanup_error is not None:
                raise cleanup_error
            return
        if self.process is None and not (
            self.platform == "macos" and self.macos_process_identity is not None
        ):
            return
        try:
            self.close()
        except Exception:
            process = self.process
            cleanup_error: BaseException | None = None
            if self.platform == "macos":
                if self.macos_process_identity is None:
                    raise NativeUISmokeError(
                        "macOS native UI process identity is unavailable during cleanup"
                    )
                try:
                    _terminate_macos_process_tree(self.macos_process_identity, self.timeout)
                except BaseException as error:
                    cleanup_error = error
            if process is not None and process.poll() is None:
                if self.platform == "macos":
                    try:
                        process.wait(timeout=2)
                    except subprocess.TimeoutExpired as error:
                        # The Popen object is the ``open`` launcher for an
                        # app bundle, not the product process.  Never signal
                        # that PID without an independently verified identity.
                        raise NativeUISmokeError(
                            "macOS native UI launcher did not exit after verified cleanup"
                        ) from error
                else:
                    process.terminate()
                    try:
                        process.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        process.kill()
            self.process = None
            self.hwnd = 0
            self.macos_pid = None
            self.macos_process_identity = None
            self.macos_expected_executable = None
            if cleanup_error is not None:
                raise cleanup_error

    def _macos_pid_or_error(self) -> int:
        identity = self.macos_process_identity
        if self.macos_pid is None or self.macos_pid <= 0 or identity is None:
            raise NativeUISmokeError("macOS native UI process identity is unavailable")
        if identity.pid != self.macos_pid:
            raise NativeUISmokeError("macOS native UI process identity is inconsistent")
        current = _macos_process_identity(self.macos_pid)
        if current is None:
            raise NativeUISmokeError("macOS native UI process has exited")
        if current != identity:
            raise NativeUISmokeError("macOS native UI process identity changed")
        if self.macos_expected_executable is not None and current.executable != self.macos_expected_executable:
            raise NativeUISmokeError("macOS native UI process executable changed")
        if current.uid != _macos_current_uid():
            raise NativeUISmokeError("macOS native UI process ownership could not be verified")
        return self.macos_pid


def _controller_for(platform: str, binary: Path, profile: Path, timeout: float) -> NativeUIController:
    return NativeUIController(platform, binary, profile, timeout)


def _ui_path_is_launchable(platform: str, path: Path) -> bool:
    if path.is_file():
        return True
    if platform != "macos" or path.suffix != ".app":
        return False
    return (path / "Contents" / "MacOS" / _MACOS_UI_PROCESS_NAME).is_file()


def serve_native_ui(
    platform: str,
    binary: Path,
    profile: Path,
    timeout: float,
    input_stream,
    output_stream,
) -> int:
    """Serve native actions for the local full adapter over JSON lines."""
    controller = _controller_for(platform, binary, profile, timeout)
    encoder = json.JSONEncoder(separators=(",", ":"))

    def progress(operation: str, stage: str) -> None:
        output_stream.write(encoder.encode({
            "ok": True,
            "event": "progress",
            "operation": operation,
            "stage": stage,
        }) + "\n")
        output_stream.flush()

    start_failed = False
    cleanup_done = False
    try:
        progress("start", "window-discovery")
        try:
            controller.start()
        except BaseException as error:
            # Finish exact app cleanup before reporting the startup error. The
            # parent receives the failure only after this child has removed
            # its detached LaunchServices app, so it cannot kill the cleanup
            # worker in the middle of its graceful close path.
            start_failed = True
            try:
                controller.close_for_cleanup()
            except BaseException as cleanup_error:
                error.add_note(
                    f"native-ui-cleanup: {type(cleanup_error).__name__}: {cleanup_error}"
                )
            raise
        output_stream.write(encoder.encode({"ok": True, "event": "ready", **controller.snapshot()}) + "\n")
        output_stream.flush()
        for line in input_stream:
            try:
                request = json.loads(line)
                if not isinstance(request, dict):
                    raise ValueError("request is not an object")
                operation = request.get("op")
                stages = {
                    "configure": "native-input",
                    "connect": "visible-connect",
                    "disconnect": "visible-disconnect",
                    "reconnect": "visible-reconnect",
                    "settings": "settings-window",
                    "wait": "visible-status",
                    "process_loss_recovery": "process-loss-recovery",
                    "close-window": "close-window",
                    "reopen": "window-reopen",
                    "close": "close-window",
                }
                if operation in stages:
                    progress(str(operation), stages[operation])
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
                elif operation == "process_loss_recovery":
                    result = controller.recover_after_process_loss()
                elif operation == "close-window":
                    result = controller.close()
                elif operation == "reopen":
                    result = controller.reopen()
                elif operation == "close":
                    # ``close`` is the terminal cleanup handshake, not the
                    # visible close-window assertion.  On macOS the normal
                    # close path sends Cmd-W through AppleScript and then
                    # waits for a product process that may be stuck in a
                    # native/network call for the full smoke timeout.  If we
                    # did that here, the parent could kill this child before
                    # its finally block reaches exact-identity cleanup.  The
                    # journey's separate ``close-window`` operation owns the
                    # graceful visible-close assertion; terminal teardown
                    # must clean the verified product identity first.
                    try:
                        controller.close_for_cleanup()
                        cleanup_error = None
                    except BaseException as error:
                        cleanup_error = error
                    cleanup_done = True
                    try:
                        result = controller.snapshot()
                    except BaseException:
                        result = {}
                    if cleanup_error is None:
                        response = {"ok": True, **result}
                    else:
                        response = {
                            "ok": False,
                            "error": str(cleanup_error),
                            **result,
                        }
                    output_stream.write(encoder.encode(response) + "\n")
                    output_stream.flush()
                    return 0 if cleanup_error is None else 1
                else:
                    raise NativeUISmokeError(f"unsupported native UI operation {operation!r}")
                response = {"ok": True, **result}
            except Exception as error:
                # Diagnostics must never replace the operation that failed.
                # A status snapshot is another AX walk and may fail or time
                # out while the original error is still actionable (for
                # example, a profile round-trip mismatch).  Keep this error
                # response bounded and preserve the primary failure verbatim;
                # the complete child stderr stream remains available through
                # the normal redacted run output.
                response = {"ok": False, "error": str(error)}
            output_stream.write(encoder.encode(response) + "\n")
            output_stream.flush()
    except Exception as error:
        output_stream.write(encoder.encode({"ok": False, "error": str(error)}) + "\n")
        output_stream.flush()
        return 1
    finally:
        if not start_failed and not cleanup_done:
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
    if args.timeout <= 0 or not _ui_path_is_launchable(args.platform, args.ui) or not args.profile.is_file():
        raise SystemExit("native UI qualification requires a launchable UI and profile file")
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
