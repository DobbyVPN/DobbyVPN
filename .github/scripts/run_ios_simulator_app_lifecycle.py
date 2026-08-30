#!/usr/bin/env python3
"""Exercise the complete unsigned iOS app lifecycle on one Simulator.

The script deliberately uses public Simulator APIs only.  It proves the app
can be installed fresh, launched repeatedly, backgrounded/foregrounded,
forcibly terminated/relaunched, and reinstalled without losing retained app
data.  It does not claim that NetworkExtension traffic ran on the Simulator.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import sys
import time

from public_output import emit_diagnostic as emit_public_diagnostic
from public_output import public_actions


SETTINGS_BUNDLE_ID = "com.apple.Preferences"
LAUNCH_PID = re.compile(r"^[^:\n]+:\s*([1-9][0-9]*)\s*$")
# Some rendering/runtime failures occur after launchd has kept the process
# alive for several seconds.  Keep every lifecycle launch alive through a
# bounded 35-second observation window so a delayed startup crash cannot be
# hidden by the next launch, background, or termination action.
STARTUP_SETTLE_SECONDS = 35


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        ["xcrun", "simctl", *args],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    stdout = completed.stdout or ""
    stderr = completed.stderr or ""
    if stdout or stderr:
        if public_actions():
            emit_public_diagnostic(
                "ios-simulator",
                ("--- stdout ---\n", stdout, "--- stderr ---\n", stderr),
                root_dir=Path(__file__).resolve().parents[2],
            )
        else:
            if stdout:
                sys.stdout.write(stdout)
                sys.stdout.flush()
            if stderr:
                sys.stderr.write(stderr)
                sys.stderr.flush()
    if check and completed.returncode != 0:
        raise RuntimeError(f"simctl command failed: {' '.join(args[:2])}")
    return completed


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def screenshot(device: str, target: Path) -> dict[str, object]:
    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    run("io", device, "screenshot", str(target))
    if not target.is_file() or target.stat().st_size <= 0:
        raise RuntimeError("Simulator screenshot is missing or empty")
    os.chmod(target, 0o600)
    return {
        "name": target.name,
        "subject": "app",
        "state": target.stem.removeprefix("ios-app-"),
        "bytes": target.stat().st_size,
        "sha256": sha256(target),
    }


def app_bundle_id(app: Path) -> str:
    with (app / "Info.plist").open("rb") as source:
        info = plistlib.load(source)
    value = info.get("CFBundleIdentifier")
    if not isinstance(value, str) or not value:
        raise RuntimeError("app bundle identifier is missing")
    return value


def launch_app(device: str, bundle_id: str, *, terminate_running: bool = False) -> int:
    arguments = ["launch"]
    if terminate_running:
        arguments.append("--terminate-running-process")
    completed = run(*arguments, device, bundle_id)
    match = LAUNCH_PID.fullmatch(completed.stdout.strip())
    if match is None:
        raise RuntimeError("Simulator app launch did not return a process identifier")
    pid = int(match.group(1))
    # A successful simctl launch only proves that launchd accepted the request.
    # Give startup a bounded opportunity to fail, then prove the process still
    # exists. This catches the prior immediate physical-device-style startup
    # crash class without reading app data or requiring UI vision.
    time.sleep(STARTUP_SETTLE_SECONDS)
    run("spawn", device, "/bin/kill", "-0", str(pid))
    return pid


def lifecycle(device: str, app: Path, screenshots: Path | None) -> dict[str, object]:
    bundle_id = app_bundle_id(app)
    frames: list[dict[str, object]] = []

    def capture(name: str) -> None:
        if screenshots is not None:
            frames.append(screenshot(device, screenshots / name))

    run("terminate", device, bundle_id, check=False)
    run("uninstall", device, bundle_id, check=False)

    run("install", device, str(app))
    cold_pid = launch_app(device, bundle_id, terminate_running=True)
    capture("ios-app-cold-start.png")

    # Repeated launch is intentionally issued while the app is already alive.
    # CoreSimulator may either reactivate that process or replace it as part of
    # servicing the launch request.  In both cases launch_app proves the process
    # returned by Simulator survived the bounded startup window.
    repeated_pid = launch_app(device, bundle_id)
    repeated_process_reused = repeated_pid == cold_pid
    capture("ios-app-repeated-start.png")

    # Opening Settings puts DobbyVPN into the background; launching DobbyVPN
    # again proves foreground recovery.  iOS may keep, suspend, or terminate a
    # background app at its discretion, so record whether the current process
    # survived rather than treating an OS lifecycle decision as an app crash.
    # launch_app still requires the foregrounded/relaunched process to survive
    # the bounded startup window.
    run("launch", device, SETTINGS_BUNDLE_ID)
    background_process_survived = run(
        "spawn", device, "/bin/kill", "-0", str(repeated_pid), check=False,
    ).returncode == 0
    foreground_pid = launch_app(device, bundle_id)
    foreground_process_reused = foreground_pid == repeated_pid
    capture("ios-app-foreground-return.png")

    run("terminate", device, bundle_id)
    launch_app(device, bundle_id)
    capture("ios-app-forced-relaunch.png")

    run("terminate", device, bundle_id)
    data_result = run("get_app_container", device, bundle_id, "data")
    data_container = Path(data_result.stdout.strip())
    if not data_container.is_dir():
        raise RuntimeError("Simulator app data container is unavailable")
    marker = data_container / "Library" / "Application Support" / "dobby-retained-install-marker"
    marker.parent.mkdir(parents=True, exist_ok=True)
    marker.write_bytes(b"retained-install-v1\n")
    os.chmod(marker, 0o600)

    # Installing over the existing app is the retained-data upgrade case.  A
    # same-build reinstall is intentional: no historical binary is required.
    # CoreSimulator may relocate the app's data container during install, so
    # resolve its current path again before checking the retained marker.
    run("install", device, str(app))
    upgraded_data_result = run("get_app_container", device, bundle_id, "data")
    upgraded_data_container = Path(upgraded_data_result.stdout.strip())
    if not upgraded_data_container.is_dir():
        raise RuntimeError("Simulator app data container is unavailable after reinstall")
    upgraded_marker = upgraded_data_container / marker.relative_to(data_container)
    if upgraded_marker.read_bytes() != b"retained-install-v1\n":
        raise RuntimeError("retained app data did not survive reinstall")
    launch_app(device, bundle_id)
    capture("ios-app-retained-data-relaunch.png")
    run("terminate", device, bundle_id)

    return {
        "schema": 1,
        "platform": "ios-simulator",
        "bundle_id": bundle_id,
        "fresh_install": True,
        "repeated_start": True,
        "repeated_start_process_reused": repeated_process_reused,
        "background_foreground": True,
        "background_process_survived": background_process_survived,
        "foreground_process_reused": foreground_process_reused,
        "forced_termination_relaunch": True,
        "retained_data_reinstall": True,
        "data_container_relocated_on_reinstall": upgraded_data_container != data_container,
        "app_view_screenshots": screenshots is not None,
        "screenshots": frames,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--device", required=True)
    parser.add_argument("--app", required=True, type=Path)
    parser.add_argument(
        "--screenshots", type=Path,
        help="local-owner execution only: retain real app screenshots in this private directory",
    )
    parser.add_argument("--result", required=True, type=Path)
    args = parser.parse_args()

    if not args.app.is_dir() or not (args.app / "Info.plist").is_file():
        raise SystemExit("--app must be a built .app directory")
    if args.result.exists():
        raise SystemExit("refusing to overwrite lifecycle result")
    args.result.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    result = lifecycle(args.device, args.app, args.screenshots)
    descriptor = os.open(args.result, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as output:
        json.dump(result, output, sort_keys=True, indent=2)
        output.write("\n")
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
