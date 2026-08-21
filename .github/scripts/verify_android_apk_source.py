#!/usr/bin/env python3
"""Verify the source commit embedded in a built DobbyVPN APK."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess
import sys


SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
BUILD_CONFIG = "com.dobby.vpn.BuildConfig"


class VerificationError(ValueError):
    pass


def dex_string(code: str, field: str) -> str:
    pattern = re.compile(
        rf'^\.field public static final {re.escape(field)}:Ljava/lang/String; = "([^"]*)"$',
        re.MULTILINE,
    )
    values = pattern.findall(code)
    if len(values) != 1:
        raise VerificationError(f"APK BuildConfig must contain exactly one {field} value")
    return values[0]


def verify_code(code: str, source_sha: str, repository: str) -> None:
    if not SHA40.fullmatch(source_sha):
        raise VerificationError("source SHA must be full lowercase hexadecimal")
    if not REPOSITORY.fullmatch(repository):
        raise VerificationError("repository must be OWNER/NAME")
    expected_link = f"https://github.com/{repository}/tree/{source_sha}"
    if dex_string(code, "PROJECT_REPOSITORY_COMMIT") != source_sha:
        raise VerificationError("APK embedded source commit does not match selected source")
    if dex_string(code, "PROJECT_REPOSITORY_COMMIT_LINK") != expected_link:
        raise VerificationError("APK embedded source link does not match selected source")


def verify_apk(apkanalyzer: str, apk: Path, source_sha: str, repository: str) -> None:
    if apk.is_symlink() or not apk.is_file() or apk.stat().st_size <= 0:
        raise VerificationError("APK must be a nonempty regular file")
    result = subprocess.run(
        [apkanalyzer, "dex", "code", "--class", BUILD_CONFIG, str(apk)],
        check=False,
        stdout=subprocess.PIPE,
        stderr=None,
        text=True,
        timeout=120,
    )
    if result.returncode != 0:
        raise VerificationError("apkanalyzer could not read APK BuildConfig")
    verify_code(result.stdout, source_sha, repository)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", action="append", required=True, type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    args = parser.parse_args()
    try:
        for apk in args.apk:
            verify_apk(args.apkanalyzer, apk, args.source_sha, args.repository)
    except (OSError, subprocess.SubprocessError, VerificationError) as error:
        print(f"Android APK source verification failed: {error}", file=sys.stderr)
        return 1
    print(f"Android APK embedded source verified for {len(args.apk)} artifact(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
