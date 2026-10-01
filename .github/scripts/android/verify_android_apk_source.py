#!/usr/bin/env python3
"""Verify release metadata and source identity embedded in a DobbyVPN APK."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess
import sys
import xml.etree.ElementTree as ElementTree

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bounded_process import (
    PROCESS_CLEANUP_GRACE_SECONDS,
    exception_output as _exception_output,
    run_bounded_capture as _run_bounded_capture,
    emit_process_diagnostic,
)

SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
APPLICATION_PACKAGE = "com.dobby.vpn"
BUILD_CONFIG = f"{APPLICATION_PACKAGE}.BuildConfig"
TEST_COMPANION_PACKAGE = f"{APPLICATION_PACKAGE}.test"
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


def verify_apk(
    apkanalyzer: str,
    apk: Path,
    source_sha: str,
    repository: str,
    version_name: str,
    version_code: int,
) -> None:
    verify_apk_manifest(apkanalyzer, apk, version_name, version_code)
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
    emit_process_diagnostic(result.stdout, result.stderr)
    verify_code(result.stdout, source_sha, repository)


def _manifest_output(
    apkanalyzer: str,
    apk: Path,
    operation: str,
    *,
    artifact: str,
) -> str:
    try:
        result = run_apkanalyzer([apkanalyzer, "manifest", operation, str(apk)])
    except subprocess.TimeoutExpired as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise VerificationError(
            f"apkanalyzer timed out reading {artifact} manifest {operation} "
            f"after {APK_ANALYZER_TIMEOUT_SECONDS} seconds",
        ) from error
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise
    emit_process_diagnostic(result.stdout, result.stderr)
    if result.returncode != 0:
        raise VerificationError(
            f"apkanalyzer could not read {artifact} manifest {operation} "
            f"(exit code {result.returncode})",
        )
    return result.stdout


def verify_apk_manifest(
    apkanalyzer: str,
    apk: Path,
    version_name: str,
    version_code: int,
) -> None:
    expected = (
        ("application-id", APPLICATION_PACKAGE),
        ("version-name", version_name),
        ("version-code", str(version_code)),
    )
    for operation, expected_value in expected:
        observed_value = _manifest_output(
            apkanalyzer,
            apk,
            operation,
            artifact="application APK",
        ).strip()
        if observed_value != expected_value:
            raise VerificationError(
                f"application APK manifest {operation} mismatch: "
                f"expected {expected_value!r}, got {observed_value!r}"
            )


def verify_test_companion(apkanalyzer: str, apk: Path, source_sha: str) -> None:
    if not SHA40.fullmatch(source_sha):
        raise VerificationError("source SHA must be full lowercase hexadecimal")
    package = _manifest_output(
        apkanalyzer,
        apk,
        "application-id",
        artifact="test companion",
    ).strip()
    if package != TEST_COMPANION_PACKAGE:
        raise VerificationError("Android test companion has an unexpected application ID")
    manifest = _manifest_output(apkanalyzer, apk, "print", artifact="test companion")
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
    parser.add_argument("--version-name", required=True)
    parser.add_argument("--version-code", required=True, type=int)
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    args = parser.parse_args()
    try:
        for apk in args.apk:
            verify_apk(
                args.apkanalyzer,
                apk,
                args.source_sha,
                args.repository,
                args.version_name,
                args.version_code,
            )
        for companion in args.test_companion:
            verify_test_companion(args.apkanalyzer, companion, args.source_sha)
    except (OSError, subprocess.SubprocessError, VerificationError) as error:
        print(
            f"Android APK release identity and source verification failed: {error}",
            file=sys.stderr,
        )
        return 1
    print(
        "Android APK release identity and embedded source verified for "
        f"{len(args.apk)} application artifact(s) and "
        f"{len(args.test_companion)} test companion artifact(s)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
