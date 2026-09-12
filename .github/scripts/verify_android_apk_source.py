#!/usr/bin/env python3
"""Verify the source commit embedded in a built DobbyVPN APK."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import xml.etree.ElementTree as ElementTree

SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
BUILD_CONFIG = "com.dobby.vpn.BuildConfig"
TEST_COMPANION_PACKAGE = "com.dobby.vpn.test"
TEST_COMPANION_SOURCE_METADATA = "com.dobby.test.source_sha"
ANDROID_NAMESPACE = "http://schemas.android.com/apk/res/android"
APK_ANALYZER_TIMEOUT_SECONDS = 120
PROCESS_CLEANUP_GRACE_SECONDS = 5


class VerificationError(ValueError):
    pass


def output_text(output: str | bytes | None) -> str:
    if output is None:
        return ""
    if isinstance(output, bytes):
        return output.decode("utf-8", errors="replace")
    return output


def output_bytes(output: str | bytes | None) -> bytes:
    if output is None:
        return b""
    if isinstance(output, bytes):
        return output
    return output.encode("utf-8", errors="surrogatepass")


def _merge_output_fragments(*outputs: str | bytes | None) -> bytes:
    merged = b""
    for output in outputs:
        data = output_bytes(output)
        if not data:
            continue
        if not merged:
            merged = data
            continue
        if data == merged:
            continue
        if data.startswith(merged):
            merged = data
            continue
        merged += data
    return merged


def _exception_output(error: BaseException) -> tuple[bytes, bytes]:
    return (
        _merge_output_fragments(
            getattr(error, "stdout", None),
            getattr(error, "output", None),
        ),
        _merge_output_fragments(getattr(error, "stderr", None)),
    )


def _set_exception_output(error: BaseException, stdout: bytes, stderr: bytes) -> None:
    try:
        error.stdout = output_text(stdout)  # type: ignore[attr-defined]
        error.output = output_text(stdout)  # type: ignore[attr-defined]
        error.stderr = output_text(stderr)  # type: ignore[attr-defined]
    except (AttributeError, TypeError) as attachment_error:
        error.add_note(f"subprocess output could not be attached: {attachment_error}")


class ProcessCleanupError(RuntimeError):
    """Raised when bounded process cleanup itself fails."""


def emit_process_diagnostic(*outputs: str | bytes | None) -> None:
    for output in outputs:
        if not output:
            continue
        text = output_text(output)
        sys.stderr.write(text)
        if not text.endswith("\\n"):
            sys.stderr.write("\\n")
    sys.stderr.flush()


def process_group_options() -> dict[str, int | bool]:
    if os.name == "nt":
        return {"creationflags": getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)}
    return {"start_new_session": True}


def _run_windows_taskkill(pid: int, timeout_seconds: float) -> None:
    try:
        result = subprocess.run(
            ["taskkill", "/T", "/F", "/PID", str(pid)],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False,
            timeout=timeout_seconds,
        )
    except (OSError, subprocess.SubprocessError) as error:
        stdout, stderr = _exception_output(error)
        raise ProcessCleanupError(
            f"taskkill failed: {error} stdout={output_text(stdout).strip()} "
            f"stderr={output_text(stderr).strip()}"
        ) from error
    if result.returncode != 0:
        raise ProcessCleanupError(
            f"taskkill exited with code {result.returncode} "
            f"stdout={output_text(result.stdout).strip()} "
            f"stderr={output_text(result.stderr).strip()}"
        )


def terminate_process_group(
    process: subprocess.Popen[str],
    grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
) -> str:
    group_id = getattr(process, "_dobby_process_group_id", process.pid)
    if os.name == "nt":
        if process.poll() is None:
            _run_windows_taskkill(process.pid, grace_seconds)
    else:
        try:
            os.killpg(group_id, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessCleanupError(
                f"could not terminate process group={group_id}: {error}"
            ) from error
        try:
            process.wait(timeout=grace_seconds)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(group_id, signal.SIGKILL)
            except ProcessLookupError:
                pass
            except OSError as error:
                raise ProcessCleanupError(
                    f"could not kill process group={group_id}: {error}"
                ) from error
            try:
                process.wait(timeout=grace_seconds)
            except subprocess.TimeoutExpired as error:
                raise ProcessCleanupError(
                    f"process group {group_id} did not terminate after escalation"
                ) from error
    return "process-group=terminated"


def _drain_after_cleanup(
    process: subprocess.Popen[str],
    stdout: bytes,
    stderr: bytes,
    *,
    grace_seconds: float,
) -> tuple[bytes, bytes]:
    try:
        drained_stdout, drained_stderr = process.communicate(timeout=grace_seconds)
    except (subprocess.TimeoutExpired, OSError) as error:
        partial_stdout, partial_stderr = _exception_output(error)
        stdout = _merge_output_fragments(stdout, partial_stdout)
        stderr = _merge_output_fragments(stderr, partial_stderr)
        try:
            process.kill()
        except ProcessLookupError:
            pass
        except OSError as kill_error:
            raise ProcessCleanupError(
                f"could not kill process while draining output: {kill_error} "
                f"stdout={output_text(stdout).strip()} stderr={output_text(stderr).strip()}"
            ) from error
        try:
            drained_stdout, drained_stderr = process.communicate(timeout=grace_seconds)
        except (subprocess.TimeoutExpired, OSError) as drain_error:
            raise ProcessCleanupError(
                f"could not drain process output: {drain_error} "
                f"stdout={output_text(stdout).strip()} stderr={output_text(stderr).strip()}"
            ) from error
        return _merge_output_fragments(stdout, drained_stdout), _merge_output_fragments(
            stderr, drained_stderr
        )
    return _merge_output_fragments(stdout, drained_stdout), _merge_output_fragments(
        stderr, drained_stderr
    )


def run_bounded_capture(
    command: list[str],
    *,
    timeout_seconds: int,
) -> subprocess.CompletedProcess[str]:
    process = subprocess.Popen(
        command,
        text=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        **process_group_options(),
    )
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    try:
        stdout, stderr = process.communicate(timeout=timeout_seconds)
    except (subprocess.TimeoutExpired, OSError) as error:
        captured_stdout, captured_stderr = _exception_output(error)
        cleanup_errors: list[ProcessCleanupError] = []
        try:
            terminate_process_group(process)
        except ProcessCleanupError as secondary_error:
            cleanup_errors.append(secondary_error)
        try:
            stdout, stderr = _drain_after_cleanup(
                process,
                captured_stdout,
                captured_stderr,
                grace_seconds=PROCESS_CLEANUP_GRACE_SECONDS,
            )
        except ProcessCleanupError as secondary_error:
            cleanup_errors.append(secondary_error)
            stdout, stderr = captured_stdout, captured_stderr
        _set_exception_output(error, stdout, stderr)
        if cleanup_errors:
            error.add_note(
                "process cleanup failed: "
                + "; ".join(str(cleanup_error) for cleanup_error in cleanup_errors)
            )
        raise
    return subprocess.CompletedProcess(
        command,
        process.returncode,
        output_text(stdout),
        output_text(stderr),
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
