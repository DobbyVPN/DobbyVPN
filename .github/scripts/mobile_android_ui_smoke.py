#!/usr/bin/env python3
"""Drive a Go/Fyne Android APK through a real accessibility tree.

This smoke check exercises launch, visible labels, and native taps. It is
deliberately separate from the hosted VPN scenario, which also verifies the
production VpnService and real network observations.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time
import xml.etree.ElementTree as ET


class SmokeError(RuntimeError):
    pass


def run(
    command: list[str],
    *,
    check: bool = True,
    redact_error: bool = False,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, check=False, text=True, capture_output=True)
    if check and result.returncode:
        detail = result.stderr.strip() or result.stdout.strip()
        shown = "<redacted>" if redact_error else " ".join(command)
        if redact_error:
            detail = "<redacted>"
        raise SmokeError(f"{shown} failed ({result.returncode}): {detail}")
    return result


def _dump(adb: str) -> ET.Element:
    run([adb, "shell", "uiautomator", "dump", "/sdcard/dobbyvpn-ui.xml"])
    result = run([adb, "exec-out", "cat", "/sdcard/dobbyvpn-ui.xml"])
    try:
        return ET.fromstring(result.stdout)
    except ET.ParseError as error:
        raise SmokeError("Android accessibility XML was invalid") from error


def nodes(adb: str) -> list[tuple[str, str]]:
    tree = _dump(adb)
    return [
        (label, node.attrib.get("bounds", ""))
        for node in tree.iter("node")
        if (label := node.attrib.get("content-desc") or node.attrib.get("text"))
    ]


def app_visible(adb: str, package: str) -> bool:
    tree = _dump(adb)
    return any(node.attrib.get("package") == package for node in tree.iter("node"))


def wait_for_labels(adb: str, required: set[str], timeout: float) -> list[tuple[str, str]]:
    deadline = time.monotonic() + timeout
    last: list[tuple[str, str]] = []
    while time.monotonic() < deadline:
        last = nodes(adb)
        if required.issubset({label for label, _ in last}):
            return last
        time.sleep(0.2)
    observed = sorted(label for label, _ in last if label in required)
    raise SmokeError(f"Android UI did not expose {sorted(required)}; observed labels={observed}")


def wait_for_any(adb: str, required: set[str], timeout: float) -> list[tuple[str, str]]:
    deadline = time.monotonic() + timeout
    last: list[tuple[str, str]] = []
    while time.monotonic() < deadline:
        last = nodes(adb)
        if {label for label, _ in last} & required:
            return last
        time.sleep(0.2)
    observed = sorted(label for label, _ in last if label in required)
    raise SmokeError(f"Android UI did not expose any of {sorted(required)}; observed labels={observed}")


def center(label: str, current: list[tuple[str, str]]) -> tuple[int, int]:
    for candidate, bounds in current:
        if candidate != label:
            continue
        try:
            left, top, right, bottom = (
                int(value)
                for value in bounds.replace("[", ",").replace("]", ",").split(",")
                if value
            )
        except ValueError as error:
            raise SmokeError(f"Android node {label!r} has invalid bounds {bounds!r}") from error
        if right <= left or bottom <= top:
            break
        return (left + right) // 2, (top + bottom) // 2
    raise SmokeError(f"Android UI node {label!r} has no usable bounds")


def tap(adb: str, label: str, current: list[tuple[str, str]] | None = None) -> None:
    current = current if current is not None else nodes(adb)
    point = center(label, current)
    run([adb, "shell", "input", "tap", str(point[0]), str(point[1])])


def leave_activity(adb: str, package: str, timeout: float) -> None:
    """Finish the Go/Fyne activity, accounting for an open software keyboard."""
    run([adb, "shell", "input", "keyevent", "KEYCODE_BACK"], check=False)
    deadline = time.monotonic() + min(timeout, 1.0)
    while time.monotonic() < deadline:
        if not app_visible(adb, package):
            return
        time.sleep(0.1)
    # Back first dismisses the keyboard on many Android releases. Send it a
    # second time only when the app's accessibility tree is still present, so
    # the following launch cannot merely resume the old activity instance.
    run([adb, "shell", "input", "keyevent", "KEYCODE_BACK"], check=False)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not app_visible(adb, package):
            return
        time.sleep(0.1)
    raise SmokeError("Android Go/Fyne activity did not finish before reopen")


def _encode_input_text(value: str) -> str:
    """Encode the ASCII subset accepted by ``adb shell input text``.

    ``input text`` uses ``%s`` for spaces and treats ``%`` as an escape
    introducer.  The remaining punctuation in TOML is passed through after
    shell quoting; the caller supplies it as one argv item, so it is not
    interpreted by the controller shell.
    """
    encoded = value.replace("%", "%25").replace(" ", "%s")
    return encoded.replace("\n", "")


def enter_profile(adb: str, profile: Path) -> None:
    try:
        text = profile.read_text(encoding="utf-8")
    except OSError as error:
        raise SmokeError(f"could not read Android UI profile: {error}") from error
    if not text.strip():
        raise SmokeError("Android UI profile is empty")
    # The released Go/Fyne widget is multiline. Clear any source loaded by a
    # prior run, then send each line through the platform input channel and
    # preserve line breaks with the native Enter key.
    run([adb, "shell", "input", "keycombination", "KEYCODE_CTRL_LEFT", "KEYCODE_A"], check=False)
    run([adb, "shell", "input", "keyevent", "KEYCODE_DEL"], check=False)
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if line:
            run(
                [adb, "shell", "input", "text", _encode_input_text(line)],
                redact_error=True,
            )
        if index + 1 < len(lines):
            run([adb, "shell", "input", "keyevent", "KEYCODE_ENTER"])


_VERSION = re.compile(r"Version:\s*[0-9]+\.[0-9]+\.[0-9]+(?:[-+][^\s]+)?\Z")
_CONSENT_DENY = ("Cancel", "Deny", "Don’t allow", "Don't allow", "NO")
_CONSENT_ALLOW = ("Allow", "OK", "Start now", "Allow VPN")


def _tap_first(adb: str, candidates: tuple[str, ...], timeout: float) -> str:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        current = nodes(adb)
        for label in candidates:
            if label in {item for item, _ in current}:
                tap(adb, label, current)
                return label
        time.sleep(0.2)
    raise SmokeError(f"Android VPN consent control was not exposed: {candidates}")


def _wait_status(adb: str, accepted: set[str], timeout: float) -> list[tuple[str, str]]:
    return wait_for_any(adb, accepted, timeout)


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--apk", type=Path, required=True)
    result.add_argument("--profile", type=Path, required=True, help="fresh release profile to type into the production UI")
    result.add_argument("--adb", default=os.environ.get("ADB", "adb"))
    result.add_argument("--package", default="com.dobby.vpn")
    result.add_argument("--activity", default="org.golang.app.GoNativeActivity")
    result.add_argument("--timeout", type=float, default=15.0)
    result.add_argument("--keep-installed", action="store_true")
    return result


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    if not args.apk.is_file():
        raise SystemExit(f"APK does not exist: {args.apk}")
    if not args.profile.is_file():
        raise SystemExit(f"Android UI profile does not exist: {args.profile}")
    adb = args.adb
    installed = False
    try:
        run([adb, "install", "-r", str(args.apk)])
        installed = True
        run([adb, "shell", "am", "force-stop", args.package], check=False)
        run([adb, "shell", "am", "start", "-n", f"{args.package}/{args.activity}"])

        initial = wait_for_labels(
            adb,
            {"Connection configuration", "Connect", "Settings", "Disconnected"},
            args.timeout,
        )
        tap(adb, "Settings", initial)
        settings_view = wait_for_labels(adb, {"Back"}, args.timeout)
        version_labels = [label for label, _ in settings_view if label.startswith("Version:")]
        if not version_labels or not any(_VERSION.fullmatch(label) for label in version_labels):
            raise SmokeError(f"release Settings view did not expose a numeric version: {version_labels}")
        tap(adb, "Back", settings_view)
        restored = wait_for_labels(adb, {"Settings", "Disconnected"}, args.timeout)
        configuration = wait_for_labels(adb, {"Connection configuration"}, args.timeout)
        tap(adb, "Connection configuration", configuration)
        enter_profile(adb, args.profile)
        tap(adb, "Connect", wait_for_labels(adb, {"Connect"}, args.timeout))

        # A fresh emulator must show Android's VPN consent dialog. Deny once
        # and prove the production UI retains the source and offers retry;
        # the second click is the only path on which this smoke approves the
        # system dialog. The hosted lane separately observes the real VPN.
        denied = _tap_first(adb, _CONSENT_DENY, args.timeout)
        after_denial = _wait_status(adb, {"Error", "Disconnected", "Connect"}, args.timeout)
        if "Connect" not in {label for label, _ in after_denial}:
            raise SmokeError(f"Android UI did not retain a retryable Connect control after {denied}")
        tap(adb, "Connect", after_denial)
        approved = _tap_first(adb, _CONSENT_ALLOW, args.timeout)
        connected = _wait_status(adb, {"Connected"}, args.timeout)
        if "Connected" not in {label for label, _ in connected}:
            raise SmokeError(f"Android UI did not display Connected after consent control {approved}")

        # Relaunch the activity without force-stopping the package. The VPN
        # service is a separate lifecycle owner, so a genuine reopen must
        # recover the connected presentation before disconnecting via UI.
        leave_activity(adb, args.package, args.timeout)
        run([adb, "shell", "am", "start", "-n", f"{args.package}/{args.activity}"])
        reopened = wait_for_labels(adb, {"Connected", "Disconnect"}, args.timeout)
        tap(adb, "Disconnect", reopened)
        disconnected = wait_for_labels(adb, {"Disconnected", "Connect"}, args.timeout)
        print(
            json.dumps(
                {
                    "initial_labels": sorted({label for label, _ in initial}),
                    "settings_labels": sorted({label for label, _ in settings_view}),
                    "restored_labels": sorted({label for label, _ in restored}),
                    "consent_denied": denied,
                    "consent_approved": approved,
                    "connected_labels": sorted({label for label, _ in connected}),
                    "reopened_labels": sorted({label for label, _ in reopened}),
                    "disconnected_labels": sorted({label for label, _ in disconnected}),
                },
                sort_keys=True,
            )
        )
        return 0
    finally:
        if installed and not args.keep_installed:
            run([adb, "shell", "am", "force-stop", args.package], check=False)
            run([adb, "uninstall", args.package], check=False)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except SmokeError as error:
        raise SystemExit(f"mobile Android UI smoke failed: {error}") from error
