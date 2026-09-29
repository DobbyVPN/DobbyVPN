"""Run finite subprocesses and retain their complete output during cleanup."""

from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
import math
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

PROCESS_CLEANUP_GRACE_SECONDS = 5


def output_text(output: str | bytes | None) -> str:
    if output is None:
        return ""
    if isinstance(output, bytes):
        # Preserve every invalid byte reversibly in diagnostics.
        return output.decode("utf-8", errors="backslashreplace")
    return output


def output_bytes(output: str | bytes | None) -> bytes:
    if output is None:
        return b""
    if isinstance(output, bytes):
        return output
    return output.encode("utf-8", errors="surrogatepass")


def merge_output_fragments(*outputs: str | bytes | None) -> bytes:
    """Merge repeated cumulative captures without dropping stream bytes."""
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
            # Subprocess APIs expose cumulative snapshots or aliases; if a
            # caller supplies incremental fragments, retain them in order.
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
    """Keep complete stream bytes on the original subprocess exception."""
    try:
        error.stdout = stdout  # type: ignore[attr-defined]
        error.output = stdout  # type: ignore[attr-defined]
        error.stderr = stderr  # type: ignore[attr-defined]
    except (AttributeError, TypeError) as attachment_error:
        error.add_note(f"subprocess output could not be attached: {attachment_error}")


class ProcessCleanupError(RuntimeError):
    """Raised when bounded process cleanup itself fails."""


def process_group_options() -> dict[str, int | bool]:
    if os.name == "nt":
        return {"creationflags": getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)}
    return {"start_new_session": True}


def _run_windows_taskkill(pid: int, timeout_seconds: float) -> None:
    def emit_streams(stdout: object, stderr: object) -> None:
        for name, payload in (("stdout", stdout), ("stderr", stderr)):
            rendered = output_text(
                payload if isinstance(payload, (str, bytes)) else None
            )
            sys.stderr.write(f"[taskkill {pid} {name} begin]\n")
            sys.stderr.write(rendered)
            if rendered and not rendered.endswith("\n"):
                sys.stderr.write("\n")
            sys.stderr.write(f"[taskkill {pid} {name} end]\n")
        sys.stderr.flush()

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
        emit_streams(stdout, stderr)
        raise ProcessCleanupError(
            f"taskkill failed: {error} stdout={output_text(stdout).strip()} "
            f"stderr={output_text(stderr).strip()}"
        ) from error
    emit_streams(result.stdout, result.stderr)
    if result.returncode != 0:
        raise ProcessCleanupError(
            f"taskkill exited with code {result.returncode} "
            f"stdout={output_text(result.stdout).strip()} "
            f"stderr={output_text(result.stderr).strip()}"
        )


def terminate_process_group(
    process: subprocess.Popen[bytes],
    grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
) -> str:
    """Stop the command's process group, escalating to a forced stop."""
    group_id = getattr(process, "_dobby_process_group_id", process.pid)
    if os.name == "nt":
        if process.poll() is None:
            _run_windows_taskkill(process.pid, grace_seconds)
        return "process-group=terminated"

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
        process.wait(timeout=max(grace_seconds, 0))
    except subprocess.TimeoutExpired as error:
        raise ProcessCleanupError(
            f"process group {group_id} did not terminate after escalation"
        ) from error
    return "process-group=terminated"


def _remaining(deadline: float) -> float:
    return max(0.0, deadline - time.monotonic())


def _drain_after_cleanup(
    process: subprocess.Popen[bytes],
    stdout: bytes,
    stderr: bytes,
    *,
    deadline: float,
) -> tuple[bytes, bytes, list[BaseException]]:
    """Collect output after termination, with one final direct-child reap."""
    errors: list[BaseException] = []
    for attempt in range(2):
        try:
            drained_stdout, drained_stderr = process.communicate(
                timeout=_remaining(deadline)
            )
            return (
                merge_output_fragments(stdout, drained_stdout),
                merge_output_fragments(stderr, drained_stderr),
                errors,
            )
        except (subprocess.TimeoutExpired, OSError) as error:
            partial_stdout, partial_stderr = exception_output(error)
            stdout = merge_output_fragments(stdout, partial_stdout)
            stderr = merge_output_fragments(stderr, partial_stderr)
            errors.append(error)
            if attempt:
                break
            try:
                process.kill()
            except ProcessLookupError:
                pass
            except OSError as kill_error:
                errors.append(kill_error)

    # If a descendant escaped the boundary and still owns a pipe, stop waiting
    # at the finite deadline. Preserve every byte already read on the original
    # timeout exception and report that collection could not finish.
    if os.name == "nt":
        # Windows communicate() reads pipes on background threads. Closing a
        # buffered reader while one of those threads is blocked can wait on
        # its lock forever, so return with the partial capture and report the
        # inherited pipe that prevented complete collection.
        errors.append(
            ProcessCleanupError(
                "Windows subprocess output readers remained blocked after cleanup"
            )
        )
    else:
        errors.append(
            ProcessCleanupError("subprocess output pipes remained open after cleanup")
        )
        for name in ("stdout", "stderr"):
            stream = getattr(process, name, None)
            if stream is not None:
                try:
                    stream.close()
                except (OSError, ValueError) as close_error:
                    errors.append(close_error)
    return stdout, stderr, errors


def run_finite_capture(
    command: Sequence[str],
    *,
    timeout_seconds: float,
    cwd: Path | None = None,
    env: Mapping[str, str] | None = None,
    input_bytes: bytes | None = None,
    popen_kwargs: Mapping[str, Any] | None = None,
    popen_factory: Callable[..., subprocess.Popen[bytes]] | None = None,
    terminate: Callable[[subprocess.Popen[bytes], float], object] | None = None,
    close_boundary: Callable[[subprocess.Popen[bytes], float], object] | None = None,
    detached: bool = False,
    termination_grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
    cleanup_timeout_seconds: float = 2 * PROCESS_CLEANUP_GRACE_SECONDS,
) -> subprocess.CompletedProcess[bytes]:
    """Capture a finite command and clean its boundary before draining pipes.

    Callers may supply a platform process boundary. ``terminate`` receives the
    process and a finite cleanup deadline; ``close_boundary`` runs after
    termination and after a successful command. Detached service launchers
    deliberately skip boundary closure so their successful descendants live.
    """
    if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
        raise ValueError("subprocess timeout must be positive")
    if (
        not math.isfinite(termination_grace_seconds)
        or termination_grace_seconds < 0
        or not math.isfinite(cleanup_timeout_seconds)
        or cleanup_timeout_seconds < 0
    ):
        raise ValueError("subprocess cleanup timeouts must be finite and non-negative")
    if input_bytes is not None and not isinstance(input_bytes, bytes):
        raise TypeError("subprocess input must be bytes")

    argv = tuple(command)
    operation_deadline = time.monotonic() + timeout_seconds
    arguments: dict[str, Any] = {
        "stdout": subprocess.PIPE,
        "stderr": subprocess.PIPE,
        "text": False,
        **process_group_options(),
    }
    if cwd is not None:
        arguments["cwd"] = str(cwd)
    if env is not None:
        arguments["env"] = dict(env)
    if input_bytes is not None:
        arguments["stdin"] = subprocess.PIPE
    if popen_kwargs is not None:
        arguments.update(popen_kwargs)
    arguments.update(
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=False,
    )

    process = (popen_factory or subprocess.Popen)(list(argv), **arguments)
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    try:
        if input_bytes is None:
            stdout, stderr = process.communicate(timeout=_remaining(operation_deadline))
        else:
            stdout, stderr = process.communicate(
                input=input_bytes,
                timeout=_remaining(operation_deadline),
            )
    except BaseException as original_error:
        stdout, stderr = exception_output(original_error)
        cleanup_started = time.monotonic()
        cleanup_deadline = cleanup_started + max(cleanup_timeout_seconds, 0)
        termination_deadline = min(
            cleanup_deadline,
            cleanup_started + max(termination_grace_seconds, 0),
        )
        cleanup_errors: list[BaseException] = []
        try:
            if terminate is None:
                terminate_process_group(
                    process,
                    grace_seconds=max(termination_deadline - cleanup_started, 0),
                )
            else:
                terminate(process, termination_deadline)
        except BaseException as cleanup_error:
            cleanup_errors.append(cleanup_error)
            cleanup_stdout, cleanup_stderr = exception_output(cleanup_error)
            stdout = merge_output_fragments(stdout, cleanup_stdout)
            stderr = merge_output_fragments(stderr, cleanup_stderr)
        if close_boundary is not None and not detached:
            try:
                close_boundary(process, cleanup_deadline)
            except BaseException as cleanup_error:
                cleanup_errors.append(cleanup_error)
                cleanup_stdout, cleanup_stderr = exception_output(cleanup_error)
                stdout = merge_output_fragments(stdout, cleanup_stdout)
                stderr = merge_output_fragments(stderr, cleanup_stderr)
        stdout, stderr, drain_errors = _drain_after_cleanup(
            process,
            stdout,
            stderr,
            deadline=cleanup_deadline,
        )
        cleanup_errors.extend(drain_errors)
        set_exception_output(original_error, stdout, stderr)
        if cleanup_errors:
            for error in cleanup_errors:
                original_error.add_note(
                    f"process cleanup failed: {type(error).__name__}: {error}"
                )
                for note in getattr(error, "__notes__", ()):
                    original_error.add_note(f"process cleanup: {note}")
        raise

    stdout = output_bytes(stdout)
    stderr = output_bytes(stderr)
    if close_boundary is not None and not detached:
        try:
            close_boundary(process, operation_deadline)
        except BaseException as boundary_error:
            set_exception_output(boundary_error, stdout, stderr)
            try:
                boundary_error.returncode = process.returncode  # type: ignore[attr-defined]
            except (AttributeError, TypeError) as attachment_error:
                boundary_error.add_note(
                    f"subprocess return code could not be attached: {attachment_error}"
                )
            raise
    return subprocess.CompletedProcess(argv, process.returncode, stdout, stderr)


def run_bounded_capture(
    command: list[str],
    *,
    timeout_seconds: float,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    cleanup_grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
) -> subprocess.CompletedProcess[str]:
    """Text-compatible build-script wrapper around finite byte capture."""
    result = run_finite_capture(
        command,
        timeout_seconds=timeout_seconds,
        cwd=cwd,
        env=env,
        termination_grace_seconds=cleanup_grace_seconds,
        cleanup_timeout_seconds=2 * cleanup_grace_seconds,
    )
    return subprocess.CompletedProcess(
        result.args,
        result.returncode,
        output_text(result.stdout),
        output_text(result.stderr),
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
