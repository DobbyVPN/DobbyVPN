#!/usr/bin/env python3
"""Analyze and inspect an unsigned production iPhone archive on a Mac."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".github" / "scripts" / "desktop"))
sys.path.insert(0, str(ROOT / ".github" / "scripts" / "release"))
sys.path.insert(0, str(ROOT / ".github" / "scripts" / "android"))
import desktop_build
from android_dependency_provenance import MOBILE_MODULE, MOBILE_VERSION
from version_metadata import parse_version


def run(command: list[str], *, cwd: Path, environment: dict[str, str]) -> None:
    print("$ " + " ".join(command), flush=True)
    subprocess.run(command, cwd=cwd, env=environment, check=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    if re.fullmatch(r"[0-9a-f]{40}", args.source_sha) is None:
        parser.error("--source-sha must be a full lowercase commit SHA")
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    xcode = subprocess.run(
        ["xcodebuild", "-version"], check=False, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True,
    )
    print(xcode.stdout, end="", flush=True)
    if xcode.stderr:
        print(xcode.stderr, end="", file=sys.stderr, flush=True)
    if xcode.returncode:
        raise RuntimeError(f"xcodebuild -version exited {xcode.returncode}")
    if xcode.stdout.strip() != "Xcode 26.3\nBuild version 17C529":
        raise RuntimeError("production iOS check requires Xcode 26.3 (17C529)")
    go = str(desktop_build.prepare_go(skip_deps=False))
    tools = output / "downloaded-tools"
    tools.mkdir()
    version = parse_version((ROOT / "VERSION").read_text(encoding="utf-8"))
    env = os.environ.copy()
    env.update(
        GOTOOLCHAIN="local",
        GOBIN=str(tools),
        GOMOBILE_BIN=str(tools / "gomobile"),
        GOBIND_BIN=str(tools / "gobind"),
        GOMOBILE=str(output / "gomobile-cache"),
        SOURCE_COMMIT=args.source_sha,
        VERSION_NAME=version.version_name,
        APP_BUILD=str(version.android_version_code),
    )
    module = ROOT / "core"
    run([go, "mod", "download"], cwd=module, environment=env)
    for tool in ("gomobile", "gobind"):
        run([go, "install", f"{MOBILE_MODULE}/cmd/{tool}@{MOBILE_VERSION}"], cwd=module, environment=env)
    run([str(module / "scripts" / "build_ios_xcframework.sh")], cwd=module, environment=env)
    runtime = module / "DobbyVPNRuntime.xcframework"
    package = module / "scripts" / "package_ios_app.sh"
    run([str(package), "iosanalyze", str(output / "analysis"), str(runtime)], cwd=ROOT, environment=env)
    run([str(package), "iosarchive", str(output / "DobbyVPN.xcarchive.tar.gz"), str(runtime)], cwd=ROOT, environment=env)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"iOS production check failed: {type(error).__name__}: {error}", file=sys.stderr)
        raise SystemExit(1)
