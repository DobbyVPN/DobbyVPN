#!/usr/bin/env python3
"""Verify the source commit embedded in a built DobbyVPN APK."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess
import sys
import xml.etree.ElementTree as ElementTree

from bounded_process import (
    PROCESS_CLEANUP_GRACE_SECONDS,
    exception_output as _exception_output,
    run_bounded_capture as _run_bounded_capture,
    emit_process_diagnostic,
)

SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
BUILD_CONFIG = "com.dobby.vpn.BuildConfig"
TEST_COMPANION_PACKAGE = "com.dobby.vpn.test"
TEST_COMPANION_SOURCE_METADATA = "com.dobby.test.source_sha"
ANDROID_NAMESPACE = "http://schemas.android.com/apk/res/android"
APK_ANALYZER_TIMEOUT_SECONDS = 120


class VerificationError(ValueError):
    pass


def run_bounded_capture(
    command: list[str],
    *,
    timeout_seconds: int,
) -> subprocess.CompletedProcess[str]:
    return _run_bounded_capture(
        command,
        timeout_seconds=timeout_seconds,
        cleanup_grace_seconds=PROCESS_CLEANUP_GRACE_SECONDS,
    )


def run_apkanalyzer(command: list[str]) -> subprocess.CompletedProcess[str]:
    return run_bounded_capture(command, timeout_seconds=APK_ANALYZER_TIMEOUT_SECONDS)


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
    command = [apkanalyzer, "dex", "code", "--class", BUILD_CONFIG, str(apk)]
    try:
        result = run_apkanalyzer(command)
    except subprocess.TimeoutExpired as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise VerificationError(
            f"apkanalyzer timed out after {APK_ANALYZER_TIMEOUT_SECONDS} seconds",
        ) from error
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise
    if result.returncode != 0:
        emit_process_diagnostic(result.stdout, result.stderr)
        raise VerificationError(
            f"apkanalyzer could not read APK BuildConfig (exit code {result.returncode})",
        )
    if result.stderr:
        emit_process_diagnostic(result.stderr)
    verify_code(result.stdout, source_sha, repository)


def _manifest_output(apkanalyzer: str, apk: Path, operation: str) -> str:
    try:
        result = run_apkanalyzer([apkanalyzer, "manifest", operation, str(apk)])
    except subprocess.TimeoutExpired as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise VerificationError(
            f"apkanalyzer timed out after {APK_ANALYZER_TIMEOUT_SECONDS} seconds",
        ) from error
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise
    if result.returncode != 0:
        emit_process_diagnostic(result.stdout, result.stderr)
        raise VerificationError(
            f"apkanalyzer could not read test companion manifest (exit code {result.returncode})",
        )
    if result.stderr:
        emit_process_diagnostic(result.stderr)
    return result.stdout


def verify_test_companion(apkanalyzer: str, apk: Path, source_sha: str) -> None:
    if not SHA40.fullmatch(source_sha):
        raise VerificationError("source SHA must be full lowercase hexadecimal")
    package = _manifest_output(apkanalyzer, apk, "application-id").strip()
    if package != TEST_COMPANION_PACKAGE:
        raise VerificationError("Android test companion has an unexpected application ID")
    manifest = _manifest_output(apkanalyzer, apk, "print")
    try:
        document = ElementTree.fromstring(manifest)
    except ElementTree.ParseError as error:
        raise VerificationError("Android test companion manifest is invalid XML") from error
    name_key = f"{{{ANDROID_NAMESPACE}}}name"
    value_key = f"{{{ANDROID_NAMESPACE}}}value"
    values = [
        element.attrib.get(value_key)
        for element in document.iter("meta-data")
        if element.attrib.get(name_key) == TEST_COMPANION_SOURCE_METADATA
    ]
    if values != [source_sha]:
        raise VerificationError(
            "Android test companion source identity does not match selected source"
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", action="append", required=True, type=Path)
    parser.add_argument("--test-companion", action="append", default=[], type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    args = parser.parse_args()
    try:
        for apk in args.apk:
            verify_apk(args.apkanalyzer, apk, args.source_sha, args.repository)
        for companion in args.test_companion:
            verify_test_companion(args.apkanalyzer, companion, args.source_sha)
    except (OSError, subprocess.SubprocessError, VerificationError) as error:
        print(f"Android APK source verification failed: {error}", file=sys.stderr)
        return 1
    print(
        "Android APK embedded source verified for "
        f"{len(args.apk)} application artifact(s) and "
        f"{len(args.test_companion)} test companion artifact(s)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
