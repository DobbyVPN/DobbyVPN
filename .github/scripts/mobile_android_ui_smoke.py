#!/usr/bin/env python3
"""Drive the Go/Fyne migration APK through a real Android accessibility tree.

This is intentionally a renderer test. The migration APK does not contain the
production VpnService, so this check exercises launch, visible labels, and
native taps without pretending to qualify a VPN tunnel.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import xml.etree.ElementTree as ET


class SmokeError(RuntimeError):
    pass


def run(command: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, check=False, text=True, capture_output=True)
    if check and result.returncode:
        detail = result.stderr.strip() or result.stdout.strip()
        raise SmokeError(f"{' '.join(command)} failed ({result.returncode}): {detail}")
    return result


def nodes(adb: str) -> list[tuple[str, str]]:
    run([adb, "shell", "uiautomator", "dump", "/sdcard/dobbyvpn-ui.xml"])
    result = run([adb, "exec-out", "cat", "/sdcard/dobbyvpn-ui.xml"])
    try:
        tree = ET.fromstring(result.stdout)
    except ET.ParseError as error:
        raise SmokeError(f"Android accessibility XML was invalid: {result.stdout!r}") from error
    return [
        (label, node.attrib.get("bounds", ""))
        for node in tree.iter("node")
        if (label := node.attrib.get("content-desc") or node.attrib.get("text"))
    ]


def wait_for_labels(adb: str, required: set[str], timeout: float) -> list[tuple[str, str]]:
    deadline = time.monotonic() + timeout
    last: list[tuple[str, str]] = []
    while time.monotonic() < deadline:
        last = nodes(adb)
        if required.issubset({label for label, _ in last}):
            return last
        time.sleep(0.2)
    raise SmokeError(f"Android UI did not expose {sorted(required)}; observed {last}")


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
    raise SmokeError(f"Android UI node {label!r} has no usable bounds: {current}")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--apk", type=Path, required=True)
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
        settings = center("Settings", initial)
        run([adb, "shell", "input", "tap", str(settings[0]), str(settings[1])])
        settings_view = wait_for_labels(adb, {"Back", "Version: development"}, args.timeout)
        back = center("Back", settings_view)
        run([adb, "shell", "input", "tap", str(back[0]), str(back[1])])
        restored = wait_for_labels(adb, {"Settings", "Disconnected"}, args.timeout)
        print(
            json.dumps(
                {
                    "initial_labels": sorted({label for label, _ in initial}),
                    "settings_labels": sorted({label for label, _ in settings_view}),
                    "restored_labels": sorted({label for label, _ in restored}),
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
