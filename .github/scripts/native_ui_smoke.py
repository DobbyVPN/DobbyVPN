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
import ctypes
from ctypes import wintypes
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import time


class NativeUISmokeError(RuntimeError):
    pass


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


def _windows_accessibility_rect(hwnd: int, name: str) -> tuple[int, int, int, int]:
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
$element = $root.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $condition)
if ($null -eq $element) { exit 3 }
$rect = $element.Current.BoundingRectangle
if ($rect.Width -le 0 -or $rect.Height -le 0) { exit 4 }
Write-Output ("{0},{1},{2},{3}" -f $rect.Left, $rect.Top, $rect.Right, $rect.Bottom)
'''
    environment = os.environ.copy()
    environment["DOBBY_UI_HWND"] = str(hwnd)
    environment["DOBBY_UI_NAME"] = name
    result = subprocess.run(
        [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command],
        check=False,
        text=True,
        capture_output=True,
        env=environment,
        timeout=10,
    )
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


def _windows_has_element(hwnd: int, name: str) -> bool:
    try:
        _windows_accessibility_rect(hwnd, name)
        return True
    except NativeUISmokeError:
        return False


def _windows_paste(profile: Path) -> None:
    powershell = shutil.which("powershell") or shutil.which("pwsh")
    if not powershell:
        raise NativeUISmokeError("PowerShell is required for native clipboard input")
    environment = os.environ.copy()
    environment["DOBBY_UI_PROFILE"] = profile.read_text(encoding="utf-8")
    result = subprocess.run(
        [powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "Set-Clipboard -Value $env:DOBBY_UI_PROFILE"],
        check=False,
        text=True,
        capture_output=True,
        env=environment,
        timeout=10,
    )
    if result.returncode != 0:
        raise NativeUISmokeError(result.stderr.strip() or "Windows clipboard setup failed")


def _windows_smoke(binary: Path, profile: Path, timeout: float) -> None:
    user32 = ctypes.windll.user32
    process = subprocess.Popen([str(binary)])
    try:
        hwnd = 0

        def window_for_process() -> int:
            """Return the first visible top-level window owned by the UI process."""
            found = 0
            process_id = wintypes.DWORD()
            callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)

            @callback_type
            def callback(candidate: int, _lparam: int) -> bool:
                nonlocal found
                user32.GetWindowThreadProcessId(candidate, ctypes.byref(process_id))
                if process_id.value == process.pid and user32.IsWindowVisible(candidate):
                    found = int(candidate)
                    return False
                return True

            user32.EnumWindows(callback, 0)
            return found

        def find_window() -> bool:
            nonlocal hwnd
            hwnd = window_for_process()
            if not hwnd:
                # Keep the title fallback for launchers that hand the window
                # to a short-lived child process.
                hwnd = int(user32.FindWindowW(None, "Dobby VPN"))
            return hwnd != 0 and bool(user32.IsWindowVisible(hwnd))

        def wait_for_window() -> bool:
            if process.poll() is not None:
                raise NativeUISmokeError(
                    f"Dobby VPN exited with code {process.returncode} before creating a window"
                )
            return find_window()

        _wait_until(wait_for_window, timeout, "Dobby VPN window did not become visible")
        left, top, right, bottom = _windows_rect(hwnd)
        width = right - left
        height = bottom - top
        if width < 300 or height < 300:
            raise NativeUISmokeError("Dobby VPN window is unexpectedly small")
        user32.SetForegroundWindow(hwnd)
        _windows_paste(profile)
        input_bounds = _windows_accessibility_rect(hwnd, "Connection configuration")
        _windows_click(user32, input_bounds)
        user32.keybd_event(0x11, 0, 0, 0)
        user32.keybd_event(0x56, 0, 0, 0)
        user32.keybd_event(0x56, 0, 2, 0)
        user32.keybd_event(0x11, 0, 2, 0)
        connect_bounds = _windows_accessibility_rect(hwnd, "Connect")
        _windows_click(user32, connect_bounds)
        _wait_until(
            lambda: any(_windows_has_element(hwnd, status) for status in ("Connected", "Connecting", "Failed", "Error")),
            timeout,
            "Windows UI did not update its visible connection status",
        )
        _wait_until(
            lambda: _windows_has_element(hwnd, "Connected"),
            timeout,
            "Windows UI did not display Connected after native Connect",
        )
        _windows_click(user32, _windows_accessibility_rect(hwnd, "Settings"))
        _wait_until(
            lambda: _windows_has_element(hwnd, "Back"),
            timeout,
            "Windows UI did not open Settings through native input",
        )
        _windows_click(user32, _windows_accessibility_rect(hwnd, "Back"))
        # Alt+F4 is the native close gesture and exercises the close intercept.
        user32.keybd_event(0x12, 0, 0, 0)
        user32.keybd_event(0x73, 0, 0, 0)
        user32.keybd_event(0x73, 0, 2, 0)
        user32.keybd_event(0x12, 0, 2, 0)
        _wait_until(lambda: process.poll() is not None, timeout, "Dobby VPN window did not close")
        if process.returncode not in (0, 1):
            raise NativeUISmokeError(f"Dobby VPN exited with code {process.returncode}")
        # Reopen the UI while the service-owned session is still connected.
        process = subprocess.Popen([str(binary)])
        _wait_until(lambda: find_window(), timeout, "Dobby VPN did not reopen")
        _wait_until(
            lambda: _windows_has_element(hwnd, "Connected"),
            timeout,
            "reopened Windows UI did not reattach to the connected session",
        )
        _windows_click(user32, _windows_accessibility_rect(hwnd, "Disconnect"))
        _wait_until(
            lambda: _windows_has_element(hwnd, "Disconnected"),
            timeout,
            "Windows UI did not disconnect through native input",
        )
        user32.keybd_event(0x12, 0, 0, 0)
        user32.keybd_event(0x73, 0, 0, 0)
        user32.keybd_event(0x73, 0, 2, 0)
        user32.keybd_event(0x12, 0, 2, 0)
        _wait_until(lambda: process.poll() is not None, timeout, "reopened Windows UI did not close")
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()


def _macos_accessibility_rect(name: str, timeout: float) -> tuple[int, int, int, int]:
    apple_name = '"' + name.replace('"', '\\"') + '"'
    script = f'''tell application "System Events"
    tell process "Dobby Vpn"
        set matches to every UI element of entire contents of window 1 whose name is {apple_name}
        if (count of matches) is 0 then error "accessibility element not found: {name}"
        set target to item 1 of matches
        set p to position of target
        set s to size of target
        return ((item 1 of p) as integer) & "," & ((item 2 of p) as integer) & "," & ((item 1 of p) + (item 1 of s) as integer) & "," & ((item 2 of p) + (item 2 of s) as integer)
    end tell
    end tell'''
    completed = subprocess.run(["osascript", "-e", script], check=False, text=True, capture_output=True, timeout=timeout)
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
    completed = subprocess.run(["osascript", "-e", script], check=False, text=True, capture_output=True, timeout=10)
    if completed.returncode != 0:
        raise NativeUISmokeError(completed.stderr.strip() or "macOS native click failed")


def _macos_has_element(name: str) -> bool:
    try:
        _macos_accessibility_rect(name, 10)
        return True
    except NativeUISmokeError:
        return False


def _macos_paste(profile: Path) -> None:
    result = subprocess.run(["pbcopy"], input=profile.read_bytes(), check=False, capture_output=True, timeout=10)
    if result.returncode != 0:
        raise NativeUISmokeError("macOS clipboard setup failed")


def _macos_smoke(binary: Path, profile: Path, timeout: float) -> None:
    # LaunchServices supplies the Aqua session and pixel-format context that
    # a raw Contents/MacOS launch can miss on hosted runners.  -W keeps a
    # process handle that represents the app until its window is closed.
    bundle = binary
    if binary.parent.name == "MacOS" and binary.parent.parent.name == "Contents":
        bundle = binary.parent.parent.parent
    launch = ["open", "-W", "-n", str(bundle)] if bundle.suffix == ".app" else [str(binary)]
    process = subprocess.Popen(launch)
    try:
        _wait_until(lambda: _macos_has_element("Connection configuration"), timeout, "macOS UI did not expose the configuration input")
        _macos_paste(profile)
        _macos_click(_macos_accessibility_rect("Connection configuration", 10))
        subprocess.run(["osascript", "-e", 'tell application "System Events" to keystroke "v" using command down'], check=True, timeout=10)
        _macos_click(_macos_accessibility_rect("Connect", 10))
        _wait_until(lambda: _macos_has_element("Connected"), timeout, "macOS UI did not display Connected after native Connect")
        _macos_click(_macos_accessibility_rect("Settings", 10))
        _wait_until(lambda: _macos_has_element("Back"), timeout, "macOS UI did not open Settings through native input")
        _macos_click(_macos_accessibility_rect("Back", 10))
        subprocess.run(["osascript", "-e", 'tell application "System Events" to keystroke "w" using command down'], check=True, timeout=10)
        _wait_until(lambda: process.poll() is not None, timeout, "Dobby Vpn window did not close")
        process = subprocess.Popen(launch)
        _wait_until(lambda: _macos_has_element("Connected"), timeout, "reopened macOS UI did not reattach to connected session")
        _macos_click(_macos_accessibility_rect("Disconnect", 10))
        _wait_until(lambda: _macos_has_element("Disconnected"), timeout, "macOS UI did not disconnect through native input")
        subprocess.run(["osascript", "-e", 'tell application "System Events" to keystroke "w" using command down'], check=True, timeout=10)
        _wait_until(lambda: process.poll() is not None, timeout, "reopened macOS UI did not close")
    finally:
        if process.poll() is None:
            subprocess.run(
                ["osascript", "-e", 'tell application "Dobby Vpn" to quit'],
                check=False,
                timeout=5,
            )
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--ui", type=Path, required=True)
    parser.add_argument("--profile", type=Path, required=True, help="fresh synthetic/owner test profile to enter through the UI")
    parser.add_argument("--timeout", type=float, default=30.0)
    args = parser.parse_args(argv)
    if args.timeout <= 0 or not args.ui.is_file() or not args.profile.is_file():
        raise SystemExit("native UI qualification requires regular UI and profile files")
    if args.platform == "windows":
        _windows_smoke(args.ui, args.profile, args.timeout)
    else:
        _macos_smoke(args.ui, args.profile, args.timeout)
    print(f"native-ui-smoke platform={args.platform} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
