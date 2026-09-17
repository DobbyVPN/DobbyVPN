#!/usr/bin/env python3
"""Exercise one real desktop Fyne window with native input injection.

This is intentionally a small window/input smoke, not a second functional
engine.  The headless companion owns VPN semantics; this check proves that a
packaged binary creates a real window, accepts one native click, and closes.
It is only supported on Windows and macOS, where GUI qualification is in
scope.  Linux remains CLI/service-only.
"""

from __future__ import annotations

import argparse
import ctypes
from pathlib import Path
import signal
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


def _windows_smoke(binary: Path, timeout: float) -> None:
    user32 = ctypes.windll.user32
    process = subprocess.Popen([str(binary)])
    try:
        hwnd = 0

        def find_window() -> bool:
            nonlocal hwnd
            hwnd = int(user32.FindWindowW(None, "Dobby VPN"))
            return hwnd != 0 and bool(user32.IsWindowVisible(hwnd))

        _wait_until(find_window, timeout, "Dobby VPN window did not become visible")
        left, top, right, bottom = _windows_rect(hwnd)
        width = right - left
        height = bottom - top
        if width < 300 or height < 300:
            raise NativeUISmokeError("Dobby VPN window is unexpectedly small")
        user32.SetForegroundWindow(hwnd)
        # The production layout keeps the primary Connect button below the
        # multiline input.  This is a real mouse click, not Fyne's test driver.
        user32.SetCursorPos(left + width // 2, top + min(height - 80, 250))
        user32.mouse_event(0x0002, 0, 0, 0, 0)
        user32.mouse_event(0x0004, 0, 0, 0, 0)
        time.sleep(0.25)
        # Alt+F4 is the native close gesture and exercises the close intercept.
        user32.keybd_event(0x12, 0, 0, 0)
        user32.keybd_event(0x73, 0, 0, 0)
        user32.keybd_event(0x73, 0, 2, 0)
        user32.keybd_event(0x12, 0, 2, 0)
        _wait_until(lambda: process.poll() is not None, timeout, "Dobby VPN window did not close")
        if process.returncode not in (0, 1):
            raise NativeUISmokeError(f"Dobby VPN exited with code {process.returncode}")
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()


def _macos_smoke(binary: Path, timeout: float) -> None:
    process = subprocess.Popen([str(binary)])
    script = f'''tell application "System Events"
    tell process "Dobby Vpn"
        repeat until exists window 1
            delay 0.1
        end repeat
        set p to position of window 1
        set s to size of window 1
        if item 1 of s < 300 or item 2 of s < 300 then error "window is unexpectedly small"
        click at {{(item 1 of p) + (item 1 of s) / 2, (item 2 of p) + 250}}
    end tell
    end tell'''
    try:
        completed = subprocess.run(["osascript", "-e", script], check=False, text=True, capture_output=True, timeout=timeout)
        if completed.returncode != 0:
            raise NativeUISmokeError(completed.stderr.strip() or "macOS native input injection failed")
        subprocess.run(
            ["osascript", "-e", 'tell application "System Events" to keystroke "w" using command down'],
            check=False,
            timeout=5,
        )
        _wait_until(lambda: process.poll() is not None, timeout, "Dobby Vpn window did not close")
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--ui", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=30.0)
    args = parser.parse_args(argv)
    if args.timeout <= 0 or not args.ui.is_file():
        raise SystemExit("native UI smoke requires a regular UI executable and positive timeout")
    if args.platform == "windows":
        _windows_smoke(args.ui, args.timeout)
    else:
        _macos_smoke(args.ui, args.timeout)
    print(f"native-ui-smoke platform={args.platform} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
