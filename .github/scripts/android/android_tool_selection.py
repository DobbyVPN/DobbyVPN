#!/usr/bin/env python3
"""Select Android tools from explicit overrides, the selected SDK, or PATH."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import sys
from typing import Mapping


class AndroidToolSelectionError(ValueError):
    """Raised when an Android tool override or SDK installation is invalid."""


def _executable(path: Path, label: str) -> Path:
    if not path.is_file() or not os.access(path, os.X_OK):
        raise AndroidToolSelectionError(f"{label} is not an executable file: {path}")
    return path


def select_apkanalyzer(environment: Mapping[str, str] | None = None) -> Path:
    env = os.environ if environment is None else environment
    override = env.get("APKANALYZER", "")
    if override:
        return _executable(Path(override).expanduser(), "APKANALYZER override")

    sdk_root = env.get("ANDROID_SDK_ROOT") or env.get("ANDROID_HOME")
    if sdk_root:
        candidate = Path(sdk_root).expanduser() / "cmdline-tools" / "latest" / "bin" / "apkanalyzer"
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return candidate

    path_candidate = shutil.which("apkanalyzer", path=env.get("PATH"))
    if path_candidate:
        return _executable(Path(path_candidate), "apkanalyzer on PATH")
    raise AndroidToolSelectionError("apkanalyzer is unavailable from the selected SDK or PATH")


def select_readelf(
    ndk_root: str | Path | None = None,
    environment: Mapping[str, str] | None = None,
) -> Path:
    env = os.environ if environment is None else environment
    override = env.get("ANDROID_READELF", "")
    if override:
        return _executable(Path(override).expanduser(), "ANDROID_READELF override")

    selected_ndk = ndk_root or env.get("ANDROID_NDK_HOME") or env.get("ANDROID_NDK_ROOT")
    if not selected_ndk:
        raise AndroidToolSelectionError("Android NDK root is required to select llvm-readelf")
    toolchains_root = Path(selected_ndk).expanduser() / "toolchains" / "llvm" / "prebuilt"
    try:
        candidates = sorted(path for path in toolchains_root.iterdir() if path.is_dir())
    except FileNotFoundError:
        candidates = []
    except OSError as error:
        raise AndroidToolSelectionError(
            f"Android NDK host toolchains are unavailable: {toolchains_root}"
        ) from error
    if len(candidates) != 1:
        raise AndroidToolSelectionError(
            "Android NDK must contain exactly one host toolchain; "
            f"found {len(candidates)} under {toolchains_root}: "
            f"{[str(candidate) for candidate in candidates]}"
        )
    return _executable(candidates[0] / "bin" / "llvm-readelf", "Android NDK llvm-readelf")


def main(arguments: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tool", required=True, choices=("apkanalyzer", "readelf"))
    parser.add_argument("--ndk-root")
    args = parser.parse_args(arguments)
    try:
        tool = (
            select_apkanalyzer()
            if args.tool == "apkanalyzer"
            else select_readelf(args.ndk_root)
        )
    except (OSError, AndroidToolSelectionError) as error:
        print(f"Android tool selection failed: {error}", file=sys.stderr)
        return 2
    print(tool)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
