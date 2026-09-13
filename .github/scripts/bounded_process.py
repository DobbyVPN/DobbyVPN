"""Run bounded subprocesses and clean up their process groups."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from pathlib import Path

PROCESS_CLEANUP_GRACE_SECONDS = 5


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


def merge_output_fragments(*outputs: str | bytes | None) -> bytes:
    """Merge cumulative or incremental subprocess output without duplication."""
    merged = b""
    for output in outputs:
        data = output_bytes(output)
        if not data:
            continue
        if not merged:
            merged = data
        elif data == merged:
            continue
        elif data.startswith(merged):
            merged = data
        else:
            # Subprocess APIs expose aliases and cumulative snapshots, handled
            # above; otherwise partial reads are appended in order.
            merged += data
    return merged


def exception_output(error: BaseException) -> tuple[bytes, bytes]:
    """Return all partial streams exposed by a subprocess exception once."""
    return (
        merge_output_fragments(
            getattr(error, "stdout", None),
            getattr(error, "output", None),
        ),
        merge_output_fragments(getattr(error, "stderr", None)),
    )


def set_exception_output(error: BaseException, stdout: bytes, stderr: bytes) -> None:
    """Make merged streams available to callers handling the original error."""
    try:
        error.stdout = output_text(stdout)  # type: ignore[attr-defined]
        error.output = output_text(stdout)  # type: ignore[attr-defined]
        error.stderr = output_text(stderr)  # type: ignore[attr-defined]
    except (AttributeError, TypeError) as attachment_error:
        error.add_note(f"subprocess output could not be attached: {attachment_error}")


class ProcessCleanupError(RuntimeError):
    """Raised when bounded process cleanup itself fails."""


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
        stdout, stderr = exception_output(error)
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
    """Terminate a child process and its process group, escalating if needed."""
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
        deadline = time.monotonic() + max(grace_seconds, 0)
        while True:
            try:
                os.killpg(group_id, 0)
            except ProcessLookupError:
                break
            except OSError as error:
                raise ProcessCleanupError(
                    f"could not inspect process group={group_id}: {error}"
                ) from error
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                try:
                    os.killpg(group_id, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except OSError as error:
                    raise ProcessCleanupError(
                        f"could not kill process group={group_id}: {error}"
                    ) from error
                break
            wait_slice = min(remaining, 0.05)
            if process.poll() is None:
                try:
                    process.wait(timeout=wait_slice)
                except subprocess.TimeoutExpired:
                    pass
            else:
                time.sleep(wait_slice)
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
    """Read complete streams after the process group has been stopped."""
    try:
        drained_stdout, drained_stderr = process.communicate(timeout=grace_seconds)
    except (subprocess.TimeoutExpired, OSError) as error:
        partial_stdout, partial_stderr = exception_output(error)
        stdout = merge_output_fragments(stdout, partial_stdout)
        stderr = merge_output_fragments(stderr, partial_stderr)
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
    return (
        merge_output_fragments(stdout, drained_stdout),
        merge_output_fragments(stderr, drained_stderr),
    )


def run_bounded_capture(
    command: list[str],
    *,
    timeout_seconds: int,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    cleanup_grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
) -> subprocess.CompletedProcess[str]:
    popen_args: dict[str, object] = {
        "text": False,
        "stdout": subprocess.PIPE,
        "stderr": subprocess.PIPE,
        **process_group_options(),
    }
    if cwd is not None:
        popen_args["cwd"] = str(cwd)
    if env is not None:
        popen_args["env"] = env
    process = subprocess.Popen(command, **popen_args)
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    try:
        stdout, stderr = process.communicate(timeout=timeout_seconds)
    except (subprocess.TimeoutExpired, OSError) as error:
        captured_stdout, captured_stderr = exception_output(error)
        cleanup_errors: list[ProcessCleanupError] = []
        try:
            terminate_process_group(process, grace_seconds=cleanup_grace_seconds)
        except ProcessCleanupError as secondary_error:
            cleanup_errors.append(secondary_error)
        try:
            stdout, stderr = _drain_after_cleanup(
                process,
                captured_stdout,
                captured_stderr,
                grace_seconds=cleanup_grace_seconds,
            )
        except ProcessCleanupError as secondary_error:
            cleanup_errors.append(secondary_error)
            stdout, stderr = captured_stdout, captured_stderr
        set_exception_output(error, stdout, stderr)
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


def emit_process_diagnostic(*outputs: str | bytes | None) -> None:
    for output in outputs:
        if not output:
            continue
        text = output_text(output)
        sys.stderr.write(text)
        if not text.endswith("\n"):
            sys.stderr.write("\n")
    sys.stderr.flush()
