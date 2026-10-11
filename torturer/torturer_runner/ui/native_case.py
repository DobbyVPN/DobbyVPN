"""Run one desktop behavior case in one native helper process."""

from __future__ import annotations

import json
import math
import os
from pathlib import Path
import subprocess
import sys
import threading
import time
import traceback
from typing import Any, Callable


def run_desktop_case(helper: Path, app: Path, case: str, directory: Path,
                     timeout: float, *, context: dict[str, Any] | None = None,
                     observer: Callable[[dict[str, Any]], dict[str, object]] | None = None
                     ) -> dict[str, Any]:
    from ..local_vm import _run_logged

    directory.mkdir(parents=True, exist_ok=False)
    result_path = directory / "result.json"
    request = {
        "operation": "native-case", "case": case, "executable": str(app),
        "fixture_log": str(directory / "backend.jsonl"),
        "result_path": str(result_path),
        "screenshot_directory": str(directory / "screenshots"),
        "timeout_seconds": timeout,
    }
    request.update(context or {})
    started = time.monotonic()
    cleanup_reserve = 75 if case in {
        "text-size-layout", "connected-layout", "connected-session", "auto-recovery-stop",
    } else 15
    observer_errors: list[str] = []
    if observer is None:
        process = _run_logged([str(helper)], cwd=app.parent, logs=directory,
                              label="native-case", timeout=timeout + cleanup_reserve,
                              input_data=json.dumps(request).encode("utf-8"), check=False)
    else:
        request["observer_enabled"] = True
        process, observer_errors = _run_observed_case(
            helper, app, directory, request, timeout, cleanup_reserve, observer,
        )
    try:
        result = json.loads(result_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        failure = RuntimeError(f"{case}: helper exited {process.returncode} without a readable result")
        _add_log_notes(failure, directory)
        if observer_errors:
            failure.add_note("native observation error: " + observer_errors[0])
        raise failure from error
    if (not isinstance(result, dict) or result.get("case_id") != case
            or result.get("outcome") not in {"passed", "failed", "unavailable", "not-run"}
            or not isinstance(result.get("assertions"), list)
            or not isinstance(result.get("cleanup_errors"), list)):
        raise RuntimeError(f"{case}: native helper result is invalid")
    if result["outcome"] == "passed" and (
            not result["assertions"] or any(not isinstance(assertion, dict) or assertion.get("outcome") != "passed"
                                           for assertion in result["assertions"])):
        raise RuntimeError(f"{case}: passed result has no successful assertions")
    if result["outcome"] == "not-run":
        result.update(outcome="failed", error="native helper did not produce a terminal case result")
        result["cleanup_errors"].append("native helper did not confirm app cleanup")
    if process.returncode != 0 and result["outcome"] == "passed":
        result.update(outcome="failed", error=f"native helper exited {process.returncode}")
    if observer_errors:
        result.update(outcome="failed")
        result.setdefault("error", "native observation request failed: " + observer_errors[0])
    if result["cleanup_errors"] and result["outcome"] == "passed":
        result["outcome"] = "failed"
    result["process_exit_code"] = process.returncode
    result["elapsed_seconds"] = time.monotonic() - started
    result["stdout_path"] = str(next(directory.glob("native-case*.stdout.log")))
    result["stderr_path"] = str(next(directory.glob("native-case*.stderr.log")))
    return result


def _add_log_notes(error: BaseException, directory: Path) -> None:
    from ..diagnostics import output_text

    for stream in ("stdout", "stderr"):
        paths = sorted(directory.glob(f"native-case*.{stream}.log"))
        if not paths:
            continue
        for path in paths:
            try:
                data = path.read_bytes()
            except OSError as log_error:
                error.add_note(f"{path.name} could not be read: {type(log_error).__name__}: {log_error}")
            else:
                error.add_note(f"{path.name}:\n{output_text(data)}")


def _run_observed_case(
    helper: Path,
    app: Path,
    directory: Path,
    request: dict[str, Any],
    timeout: float,
    cleanup_reserve: float,
    observer: Callable[[dict[str, Any]], dict[str, object]],
) -> tuple[subprocess.CompletedProcess[bytes], list[str]]:
    """Run the helper duplex while retaining and forwarding its exact streams."""
    from ..diagnostics import output_text
    from ..local_vm import _next_command_logs, _terminate_logged_process

    label = "native-case"
    directory.mkdir(parents=True, exist_ok=True)
    stdout_path, stderr_path = _next_command_logs(directory, label)
    process: subprocess.Popen[bytes] | None = None
    case_deadline = time.monotonic() + timeout
    process_deadline = case_deadline + cleanup_reserve
    observer_errors: list[str] = []
    stderr_lock = threading.Lock()
    reader_failures: list[BaseException] = []
    stdout_stream = None
    stderr_stream = None
    stdout_done = threading.Event()
    stdout_thread: threading.Thread | None = None
    stderr_thread: threading.Thread | None = None
    timeout_error: subprocess.TimeoutExpired | None = None
    process_cleanup_error: BaseException | None = None
    readers_quiescent = True

    def forward(payload: bytes, destination: Any) -> None:
        binary = getattr(destination, "buffer", None)
        if binary is None:
            return
        binary.write(payload)
        binary.flush()

    def write_stderr(payload: bytes, *, forward_output: bool = True) -> None:
        with stderr_lock:
            assert stderr_stream is not None
            stderr_stream.write(payload)
            stderr_stream.flush()
            if forward_output:
                forward(payload, sys.stderr)

    def observer_error(error: BaseException) -> str:
        message = f"{type(error).__name__}: {error}"
        observer_errors.append(message)
        write_stderr(traceback.format_exc().encode("utf-8", errors="backslashreplace"))
        return message

    def retain_observer_quiescence(error: BaseException) -> None:
        # The stdout reader executes every observation callback synchronously.
        # If it is still alive, it may still be using the adapter or receive a
        # later request while the helper's process cleanup is uncertain.
        quiescent = stdout_thread is None or not stdout_thread.is_alive()
        error.observer_quiescent = quiescent
        if not quiescent:
            error.add_note("native observation reader is still active; backend cleanup is unconfirmed")

    def respond(value: dict[str, Any], *, remaining: float) -> None:
        assert process is not None and process.stdin is not None
        prefix = b"DOBBY_NATIVE_OBSERVE "
        line = value[len(prefix):].rstrip(b"\r\n")
        response: dict[str, object]
        try:
            decoded = json.loads(line.decode("utf-8"))
            if not isinstance(decoded, dict):
                raise ValueError("native observation request must be an object")
            requested_timeout = decoded.get("timeout_seconds")
            if (isinstance(requested_timeout, bool)
                    or not isinstance(requested_timeout, (int, float))
                    or not math.isfinite(requested_timeout)
                    or requested_timeout <= 0):
                raise ValueError("native observation request has an invalid timeout")
            available = min(float(requested_timeout), remaining)
            if available <= 0:
                raise TimeoutError("native case deadline expired before observation")
            decoded["timeout_seconds"] = available
            result = observer(decoded)
            if not isinstance(result, dict) or any(not isinstance(key, str) for key in result):
                raise TypeError("native observation callback returned a non-object result")
            json.dumps(result, allow_nan=False)
            response = {"ok": True, "value": result}
        except BaseException as error:
            message = observer_error(error)
            response = {"ok": False, "error": message}
        encoded = (json.dumps(response, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        try:
            process.stdin.write(encoded)
            process.stdin.flush()
        except (BrokenPipeError, OSError, ValueError) as error:
            observer_errors.append(f"{type(error).__name__}: {error}")
            write_stderr(
                (f"native observation response could not be delivered: {type(error).__name__}: {error}\n")
                .encode("utf-8", errors="backslashreplace")
            )

    def read_stdout() -> None:
        assert process is not None and process.stdout is not None
        pending = bytearray()
        prefix = b"DOBBY_NATIVE_OBSERVE "
        try:
            while True:
                chunk = process.stdout.read(4096)
                if not chunk:
                    break
                stdout_stream.write(chunk)
                stdout_stream.flush()
                forward(chunk, sys.stdout)
                pending.extend(chunk)
                while True:
                    newline = pending.find(b"\n")
                    if newline < 0:
                        break
                    line = bytes(pending[:newline + 1])
                    del pending[:newline + 1]
                    if line.startswith(prefix):
                        respond(line, remaining=case_deadline - time.monotonic())
            if pending:
                line = bytes(pending)
                if line.startswith(prefix):
                    respond(line, remaining=case_deadline - time.monotonic())
        except BaseException as error:
            reader_failures.append(error)
        finally:
            for stream in (stdout_stream, process.stdout):
                if stream is None:
                    continue
                try:
                    stream.close()
                except ValueError:
                    pass
                except OSError as error:
                    reader_failures.append(error)
            stdout_done.set()

    def read_stderr() -> None:
        assert process is not None and process.stderr is not None
        try:
            while True:
                chunk = process.stderr.read(4096)
                if not chunk:
                    return
                write_stderr(chunk)
        except BaseException as error:
            reader_failures.append(error)
        finally:
            # The stdout reader owns observer callbacks, which may write their
            # traceback to this log after the child's stderr pipe has closed.
            # Keep this writer open until those callbacks have returned.
            stdout_done.wait()
            with stderr_lock:
                try:
                    if stderr_stream is not None:
                        stderr_stream.close()
                except ValueError:
                    pass
                except OSError as error:
                    reader_failures.append(error)
            try:
                process.stderr.close()
            except ValueError:
                pass
            except OSError as error:
                reader_failures.append(error)

    try:
        # Open before starting the child, then remove these empty files if the
        # executable could not be launched; an empty log is not stage evidence.
        stdout_stream = stdout_path.open("wb")
        stderr_stream = stderr_path.open("wb")
        popen_kwargs: dict[str, Any] = {
            "cwd": str(app.parent),
            "stdin": subprocess.PIPE,
            "stdout": subprocess.PIPE,
            "stderr": subprocess.PIPE,
            "bufsize": 0,
        }
        if os.name == "nt":
            popen_kwargs["creationflags"] = getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)
        else:
            popen_kwargs["start_new_session"] = True
        process = subprocess.Popen([str(helper)], **popen_kwargs)
        stdout_thread = threading.Thread(target=read_stdout, name="native-case-stdout", daemon=True)
        stderr_thread = threading.Thread(target=read_stderr, name="native-case-stderr", daemon=True)
        stderr_thread.start()
        stdout_thread.start()
        assert process.stdin is not None
        initial_request = (json.dumps(request, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        process.stdin.write(initial_request)
        process.stdin.flush()
        try:
            process.wait(timeout=max(0.0, process_deadline - time.monotonic()))
        except subprocess.TimeoutExpired as error:
            timeout_error = error
            try:
                _terminate_logged_process(process, label, cwd=app.parent, logs=directory)
            except BaseException as cleanup_error:
                process_cleanup_error = cleanup_error
            try:
                process.wait(timeout=1)
            except (OSError, subprocess.TimeoutExpired) as cleanup_error:
                if process_cleanup_error is None:
                    process_cleanup_error = cleanup_error
        try:
            process.stdin.close()
        except (BrokenPipeError, OSError, ValueError):
            pass
        for thread in (stdout_thread, stderr_thread):
            if thread is not None:
                thread.join(timeout=5)
        readers_quiescent = not any(
            thread is not None and thread.is_alive() for thread in (stdout_thread, stderr_thread)
        )
        if not readers_quiescent:
            reader_failures.append(TimeoutError("native helper output reader did not finish"))
    except BaseException as error:
        if stdout_thread is None or stdout_thread.ident is None:
            # stderr waits for stdout callbacks to finish before closing its
            # log. If stdout never started, there can be no later callback.
            stdout_done.set()
        if process is not None and process.poll() is None:
            try:
                _terminate_logged_process(process, label, cwd=app.parent, logs=directory)
            except BaseException as cleanup_error:
                error.add_note(f"native helper cleanup failed: {type(cleanup_error).__name__}: {cleanup_error}")
            try:
                process.wait(timeout=1)
            except (OSError, subprocess.TimeoutExpired) as cleanup_error:
                error.add_note(f"native helper wait failed: {type(cleanup_error).__name__}: {cleanup_error}")
        if process is not None:
            if process.stdin is not None:
                try:
                    process.stdin.close()
                except (BrokenPipeError, OSError, ValueError):
                    pass
            for thread in (stdout_thread, stderr_thread):
                if thread is not None and thread.ident is not None:
                    thread.join(timeout=5)
                    if thread.is_alive():
                        readers_quiescent = False
                        error.add_note(f"native helper reader did not finish: {thread.name}")
        if readers_quiescent:
            _add_log_notes(error, directory)
        else:
            error.add_note("native helper output readers remain active; complete logs are retained at:")
            error.add_note(f"stdout: {stdout_path}")
            error.add_note(f"stderr: {stderr_path}")
        retain_observer_quiescence(error)
        raise
    finally:
        if process is None:
            if stdout_stream is not None:
                stdout_stream.close()
            if stderr_stream is not None:
                stderr_stream.close()
            stdout_path.unlink(missing_ok=True)
            stderr_path.unlink(missing_ok=True)
        else:
            streams = [process.stdin]
            if readers_quiescent:
                streams.extend((process.stdout, process.stderr))
            for stream in streams:
                if stream is not None:
                    try:
                        stream.close()
                    except ValueError:
                        pass
                    except OSError as error:
                        reader_failures.append(error)
            if readers_quiescent:
                for stream in (stdout_stream, stderr_stream):
                    if stream is not None:
                        try:
                            stream.close()
                        except ValueError:
                            pass
                        except OSError as error:
                            reader_failures.append(error)

    if readers_quiescent:
        stdout = stdout_path.read_bytes()
        stderr = stderr_path.read_bytes()
    else:
        stdout = stderr = b""
    if timeout_error is not None:
        failure = RuntimeError(f"{label}: native helper timed out")
        retain_observer_quiescence(failure)
        if readers_quiescent:
            failure.add_note(f"{label}_stdout:\n{output_text(stdout)}")
            failure.add_note(f"{label}_stderr:\n{output_text(stderr)}")
        else:
            failure.add_note("native helper output readers remain active; cleanup and complete logs are unconfirmed")
            failure.add_note(f"stdout retained at: {stdout_path}")
            failure.add_note(f"stderr retained at: {stderr_path}")
        if process_cleanup_error is not None:
            failure.add_note(
                f"{label}_cleanup: {type(process_cleanup_error).__name__}: {process_cleanup_error}"
            )
        raise failure from timeout_error
    if reader_failures:
        failure = RuntimeError(f"{label}: native helper output handling failed")
        retain_observer_quiescence(failure)
        if readers_quiescent:
            failure.add_note(f"{label}_stdout:\n{output_text(stdout)}")
            failure.add_note(f"{label}_stderr:\n{output_text(stderr)}")
        else:
            failure.add_note("native helper output readers remain active; cleanup and complete logs are unconfirmed")
            failure.add_note(f"stdout retained at: {stdout_path}")
            failure.add_note(f"stderr retained at: {stderr_path}")
        for error in reader_failures:
            failure.add_note(f"{label}_reader: {type(error).__name__}: {error}")
        raise failure from reader_failures[0]
    completed = subprocess.CompletedProcess([str(helper)], process.returncode, stdout, stderr)
    return completed, observer_errors
