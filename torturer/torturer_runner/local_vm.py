"""Prepare and run one bounded DobbyVPN qualification candidate in a VM.

The private Harness owns the VM, SSH session, timeout supervisor, and process
tree kill. ``prepare`` builds the candidate and records its artifact/runtime
descriptors. ``run`` consumes those descriptors, starts the candidate, and
runs the canonical functional suite. Focused runs use ``local_candidate.py``
where package qualification is unnecessary.

``run`` intentionally leaves the candidate running.  A supervisor invokes
``cleanup`` in a separate step, which also makes a failed setup inspectable.
The run directory is the only state boundary:

    source/       complete DobbyVPN checkout
    profile       private test profile
    logs/          command, product, and functional output
    platform.json platform-owned process/install state
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import errno
import hashlib
import json
import os
from pathlib import Path
import platform as host_platform
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import time
import traceback
from typing import Any

from .diagnostics import collect_installed_backend_logs, output_text

PLATFORMS = ("linux", "windows", "macos", "android", "ios-simulator")
SUITES = ("mini", "full")
DESKTOP_PLATFORMS = frozenset(("linux", "windows", "macos"))
ROUTING_HELPERS = Path(__file__).resolve().parent / "routing"
_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[A-Za-z0-9._-]+$")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
_RELEASE_REPOSITORY = "DobbyVPN/DobbyVPN"
_RELEASE_WORKFLOW = ".github/workflows/release.yml"

# A desktop full lane is supervised by the VM worker's task deadline.  Give
# the hosted journey an inner deadline so it can emit its failure JSON and
# leave enough time for the exact-process/task cleanup boundary to run.  The
# hosted journey has its own reserve before it waits on the real-window smoke
# process (see ui/journey.py).
_NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS = 90.0
_NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS = 900.0
_SCREENSHOT_DECODER_INSTALL_TIMEOUT_SECONDS = 180.0

# The native desktop journey runs in an interactive user session rather than
# the SSH worker's process environment.  Keep only values with a concrete
# runtime purpose here.  In particular, do not let CI credentials, endpoints,
# or arbitrary tool configuration cross the desktop boundary.
_NATIVE_UI_HOST_ENVIRONMENT = frozenset({
    # The native UI smoke driver resolves macOS helpers (and Windows PowerShell) by
    # name, while the Go backend and CLI use HOME for their user-owned stores.
    "PATH",
    "HOME",
})
_NATIVE_UI_RUNTIME_ENVIRONMENT = {
    "windows": frozenset({
        "HOME",
        "PROGRAMDATA",
        "DOBBYVPN_CONTROL_PIPE_USER",
        "DOBBY_LOG_PATH",
        "DOBBY_LOG_ROOT",
        "DOBBY_LOG_PRECREATED",
        "GODEBUG",
    }),
    "macos": frozenset({
        "HOME",
        "DOBBY_LOG_PATH",
        "DOBBYVPN_CONTROL_SOCKET",
    }),
}


class LocalVMError(RuntimeError):
    """A bounded local candidate operation failed."""


def _native_ui_environment(platform: str, runtime: dict[str, Any]) -> dict[str, str]:
    """Build the bounded environment for a native desktop journey.

    ``runtime["environment"]`` is already platform-owned state, but still
    filter it here so a malformed or test-supplied state record cannot turn
    this boundary back into an environment pass-through.  Host values are
    deliberately selected by name; the user's complete SSH/CI environment is
    never copied into the interactive desktop session.
    """

    runtime_names = _NATIVE_UI_RUNTIME_ENVIRONMENT.get(platform)
    if runtime_names is None:
        raise LocalVMError(f"native GUI qualification is unsupported on {platform}")
    environment = {
        name: value
        for name in _NATIVE_UI_HOST_ENVIRONMENT
        if isinstance((value := os.environ.get(name)), str)
    }
    runtime_environment = runtime.get("environment")
    if isinstance(runtime_environment, dict):
        environment.update({
            name: runtime_environment[name]
            for name in runtime_names
            if isinstance(runtime_environment.get(name), str)
        })
    return environment


def _prepare_desktop_ui_home(run_dir: Path) -> str:
    """Give each desktop UI phase an isolated user store and log history."""

    home = run_dir / "ui-home"
    home.mkdir(mode=0o700, parents=True, exist_ok=True)
    home.chmod(0o700)
    return str(home)


def _install_screenshot_decoder(
    run_dir: Path,
    logs: Path,
    timeout: float,
) -> Path:
    """Install the pinned screenshot decoder into this disposable run."""

    source = run_dir / "source"
    requirements = source / ".github" / "scripts" / "requirements-native-ui.txt"
    if requirements.is_symlink() or not requirements.is_file():
        raise LocalVMError("pinned native UI Python requirements are missing")
    target = run_dir / "screenshot-python"
    if target.exists() or target.is_symlink():
        raise LocalVMError("screenshot Python package directory already exists")
    target.mkdir(mode=0o700)
    _run_logged(
        [
            sys.executable,
            "-m",
            "pip",
            "install",
            "--disable-pip-version-check",
            "--only-binary=:all:",
            "--target",
            str(target),
            "--requirement",
            str(requirements),
        ],
        cwd=source,
        logs=logs,
        label="install-screenshot-decoder",
        timeout=min(timeout, _SCREENSHOT_DECODER_INSTALL_TIMEOUT_SECONDS),
    )
    return target


def _positive_timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a number") from error
    if not 0 < result < float("inf"):
        raise argparse.ArgumentTypeError("timeout must be positive and finite")
    return result


def _native_ui_driver_timeout(task_timeout: float) -> float:
    """Return the inner hosted-journey timeout below the task deadline.

    Keep a proportional reserve for short diagnostic invocations while using
    a fixed 90-second reserve for the normal desktop lane.  The result is
    always positive and strictly smaller than the task timeout, so the task
    wrapper can observe the driver's diagnostic result before it terminates
    an unresponsive full run.
    """

    if task_timeout <= 0 or task_timeout == float("inf"):
        raise ValueError("native UI task timeout must be positive and finite")
    reserve = (
        _NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS
        if task_timeout > _NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS
        else task_timeout / 3.0
    )
    return min(_NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS, task_timeout - reserve)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    for action in ("prepare", "run", "cleanup"):
        command = commands.add_parser(action)
        command.add_argument("--platform", choices=PLATFORMS, required=True)
        command.add_argument(
            "--run-dir", type=_absolute_path, required=True,
            help="absolute disposable candidate directory",
        )
        command.add_argument("--timeout", type=_positive_timeout, required=True)
        if action in {"prepare", "run"}:
            command.add_argument("--suite", choices=SUITES, default="mini")
        if action == "run":
            command.add_argument("--scenario", action="append", dest="scenarios")
        if action == "prepare":
            command.add_argument("--architecture")
            command.add_argument("--skip-deps", action="store_true")
            command.add_argument("--source-checks", action="store_true")
            command.add_argument("--source-sha")
            command.add_argument("--source-tree")
            command.add_argument("--network-interface")
            command.add_argument(
                "--release-manifest", type=_absolute_path,
                help="owner-verified exact Release package manifest",
            )
    return parser


def _absolute_path(value: str) -> Path:
    path = Path(value)
    if not path.is_absolute():
        raise argparse.ArgumentTypeError("run-dir must be an absolute path")
    return path


def _run_dir(path: Path) -> Path:
    path = path.resolve()
    if not path.is_absolute() or not path.is_dir():
        raise LocalVMError("run-dir must be an existing directory")
    return path


def _inside(path: Path, root: Path) -> Path:
    path = path.resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise LocalVMError(f"path is outside run-dir: {path}") from error
    return path


def _required_input(run_dir: Path, name: str, *, directory: bool = False) -> Path:
    raw = run_dir / name
    if raw.is_symlink():
        raise LocalVMError(f"run-dir/{name} must not be a symlink")
    path = _inside(raw, run_dir)
    if not path.is_dir() if directory else not path.is_file():
        kind = "directory" if directory else "file"
        raise LocalVMError(f"run-dir/{name} must be a {kind}")
    return path


def _write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(
        json.dumps(value, sort_keys=True, indent=2) + "\n", encoding="utf-8"
    )
    temporary.replace(path)


def _emit_vm_progress(event: str, fields: dict[str, object]) -> None:
    print(
        json.dumps(
            {
                "kind": "dobbyvpn.local_vm.progress",
                "event": event,
                "timestamp_utc": datetime.now(timezone.utc)
                .isoformat(timespec="milliseconds")
                .replace("+00:00", "Z"),
                **fields,
            },
            sort_keys=True,
            allow_nan=False,
        ),
        flush=True,
    )


def _timed_call(phase: str, operation, **fields: object):
    """Record monotonic phase time while leaving command streams untouched."""

    started = time.monotonic()
    progress = {"phase": phase, **fields}
    _emit_vm_progress("phase-start", progress)
    try:
        result = operation()
    except BaseException as error:
        _emit_vm_progress(
            "phase-finish",
            {
                **progress,
                "duration_seconds": time.monotonic() - started,
                "error_type": type(error).__name__,
                "timed_out": isinstance(
                    getattr(error, "__cause__", None), subprocess.TimeoutExpired
                ),
            },
        )
        raise
    finished = {
        **progress,
        "duration_seconds": time.monotonic() - started,
    }
    returncode = getattr(result, "returncode", None)
    if isinstance(returncode, int):
        finished["returncode"] = returncode
    _emit_vm_progress("phase-finish", finished)
    return result


def _read_state(run_dir: Path) -> dict[str, Any] | None:
    path = run_dir / "platform.json"
    if not path.is_file():
        return None
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise LocalVMError(f"platform state is unreadable: {path}") from error
    if not isinstance(value, dict):
        raise LocalVMError("platform state is not an object")
    return value


def _record_failure(run_dir: Path, state: dict[str, Any], error: Exception) -> None:
    """Print the complete failure and retain the latest cleanup descriptors."""
    traceback.print_exception(type(error), error, error.__traceback__, file=sys.stderr)
    try:
        persisted = _read_state(run_dir)
    except LocalVMError:
        persisted = None
    if persisted is not None:
        for key in ("candidate", "runtime", "release"):
            if key in persisted:
                state[key] = persisted[key]
    state["status"] = "failed"
    state["error"] = f"{type(error).__name__}: {error}"
    _write_json(run_dir / "platform.json", state)


def _save_state(run_dir: Path, platform: str, runtime: dict[str, Any]) -> None:
    state = _read_state(run_dir)
    if state is None:
        state = {"platform": platform}
    state["runtime"] = runtime
    state["status"] = "starting"
    _write_json(run_dir / "platform.json", state)


def _command_log(logs: Path, label: str, stream: str) -> Path:
    if not _IDENTITY.fullmatch(label):
        raise LocalVMError("command label is invalid")
    return logs / f"{label}.{stream}.log"


def _next_command_logs(logs: Path, label: str) -> tuple[Path, Path]:
    """Allocate one retained stdout/stderr pair for this invocation.

    Most labels are intentionally stable because operators recognize them in a
    run directory. Repeated labels must still be lossless, so the first pair
    keeps the historical name and later invocations receive a sequence suffix.
    A stale one-sided file also consumes its sequence to avoid overwriting a
    stream left by an interrupted worker.
    """

    sequence = 1
    while True:
        suffix = "" if sequence == 1 else f".{sequence}"
        stdout_path = logs / f"{label}{suffix}.stdout.log"
        stderr_path = logs / f"{label}{suffix}.stderr.log"
        if not stdout_path.exists() and not stderr_path.exists():
            return stdout_path, stderr_path
        sequence += 1


def _diagnostic_bytes(value: object) -> bytes:
    """Normalize subprocess output for retained logs and exception notes."""

    if isinstance(value, bytes):
        return value
    if isinstance(value, str):
        return value.encode("utf-8", errors="surrogatepass")
    return b""


def _add_command_stream_notes(
    error: BaseException,
    label: str,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
) -> None:
    """Attach complete command streams without replacing the primary error."""

    output = _diagnostic_bytes(stdout)
    failure = _diagnostic_bytes(stderr)
    error.add_note(f"{label}_stdout:\n{output_text(output)}")
    error.add_note(f"{label}_stderr:\n{output_text(failure)}")


def _write_probe_streams(
    stdout_path: Path,
    stderr_path: Path,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
) -> None:
    """Write the complete output of a small direct probe to its run logs."""

    stdout_path.write_bytes(_diagnostic_bytes(stdout))
    stderr_path.write_bytes(_diagnostic_bytes(stderr))


def _run_probe_logged(
    command: list[str],
    *,
    cwd: Path,
    logs: Path,
    label: str,
    timeout: float,
    environment: dict[str, str] | None = None,
    check: bool = False,
) -> subprocess.CompletedProcess[bytes]:
    return _timed_call(
        "command",
        lambda: _run_probe_logged_impl(
            command,
            cwd=cwd,
            logs=logs,
            label=label,
            timeout=timeout,
            environment=environment,
            check=check,
        ),
        command_label=label,
        timeout_seconds=timeout,
    )


def _run_probe_logged_impl(
    command: list[str],
    *,
    cwd: Path,
    logs: Path,
    label: str,
    timeout: float,
    environment: dict[str, str] | None = None,
    check: bool = False,
) -> subprocess.CompletedProcess[bytes]:
    """Run a short direct probe while retaining both streams completely.

    The normal command runner streams to files as the process runs.  These
    probes are intentionally synchronous because their output is consumed as
    one small value, but they still need the same no-loss diagnostic contract.
    The probe output is small enough to capture synchronously, then is written
    to its current run's stdout and stderr logs.
    """

    logs.mkdir(parents=True, exist_ok=True)
    if not _IDENTITY.fullmatch(label):
        raise LocalVMError("command label is invalid")
    stdout_path, stderr_path = _next_command_logs(logs, label)
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as error:
        stdout = getattr(error, "stdout", None) or getattr(error, "output", None)
        stderr = getattr(error, "stderr", None)
        try:
            _write_probe_streams(stdout_path, stderr_path, stdout, stderr)
        except OSError as log_error:
            failure = LocalVMError(f"{label}: command timed out")
            _add_command_stream_notes(failure, label, stdout, stderr)
            failure.add_note(f"{label}_log_write: {type(log_error).__name__}: {log_error}")
            raise failure from error
        failure = LocalVMError(f"{label}: command timed out")
        _add_command_stream_notes(failure, label, stdout, stderr)
        raise failure from error
    except OSError as error:
        try:
            _write_probe_streams(stdout_path, stderr_path, b"", b"")
        except OSError as log_error:
            error.add_note(f"{label}_log_write: {type(log_error).__name__}: {log_error}")
        raise LocalVMError(f"{label}: command could not start: {error}") from error
    try:
        _write_probe_streams(stdout_path, stderr_path, result.stdout, result.stderr)
    except OSError as error:
        failure = LocalVMError(f"{label}: command output could not be retained")
        failure.add_note(f"{label}_log_write: {type(error).__name__}: {error}")
        _add_command_stream_notes(failure, label, result.stdout, result.stderr)
        raise failure from error

    if check and result.returncode != 0:
        failure = LocalVMError(f"{label}: command exited {result.returncode}")
        _add_command_stream_notes(failure, label, result.stdout, result.stderr)
        raise failure
    return result


def _terminate_logged_process(
    process: subprocess.Popen[bytes],
    label: str,
    *,
    cwd: Path,
    logs: Path,
) -> None:
    """Stop a timed-out command group and retain cleanup failures."""

    failures: list[str] = []
    if os.name == "nt":
        try:
            result = _run_probe_logged(
                ["taskkill", "/T", "/F", "/PID", str(process.pid)],
                cwd=cwd,
                logs=logs,
                label=f"{label}-taskkill",
                timeout=5,
            )
        except Exception as error:
            failures.append(f"taskkill failed: {type(error).__name__}: {error}")
            failures.extend(getattr(error, "__notes__", ()))
        else:
            if result.returncode != 0:
                failures.append(
                    "taskkill exited "
                    f"{result.returncode}: stdout={output_text(result.stdout)} "
                    f"stderr={output_text(result.stderr)}"
                )
    else:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError as error:
            failures.append(f"SIGTERM failed: {type(error).__name__}: {error}")
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            except OSError as error:
                failures.append(f"SIGKILL failed: {type(error).__name__}: {error}")
            try:
                process.wait(timeout=5)
            except (OSError, subprocess.TimeoutExpired) as error:
                failures.append(f"process did not exit after SIGKILL: {type(error).__name__}: {error}")
        except OSError as error:
            failures.append(f"wait after SIGTERM failed: {type(error).__name__}: {error}")
    if failures:
        raise LocalVMError(f"{label}: process cleanup failed: {'; '.join(failures)}")


def _run_logged(
    command: list[str],
    *,
    cwd: Path,
    logs: Path,
    label: str,
    timeout: float,
    environment: dict[str, str] | None = None,
    input_data: bytes | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[bytes]:
    return _timed_call(
        "command",
        lambda: _run_logged_impl(
            command,
            cwd=cwd,
            logs=logs,
            label=label,
            timeout=timeout,
            environment=environment,
            input_data=input_data,
            check=check,
        ),
        command_label=label,
        timeout_seconds=timeout,
    )


def _run_logged_impl(
    command: list[str],
    *,
    cwd: Path,
    logs: Path,
    label: str,
    timeout: float,
    environment: dict[str, str] | None = None,
    input_data: bytes | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[bytes]:
    if not command or any(not isinstance(item, str) or not item for item in command):
        raise LocalVMError(f"{label}: invalid command")
    logs.mkdir(parents=True, exist_ok=True)
    if not _IDENTITY.fullmatch(label):
        raise LocalVMError("command label is invalid")
    stdout_path, stderr_path = _next_command_logs(logs, label)
    process: subprocess.Popen[bytes] | None = None
    timeout_error: subprocess.TimeoutExpired | None = None
    cleanup_error: BaseException | None = None
    launch_error: BaseException | None = None
    returncode: int | None = None
    try:
        # Stream directly to disk so output is not bounded by an in-memory
        # capture buffer and a timeout still retains bytes emitted before
        # termination. Reconstruct CompletedProcess from those same files.
        with stdout_path.open("wb") as stdout_stream, stderr_path.open("wb") as stderr_stream:
            popen_kwargs: dict[str, Any] = {
                "cwd": str(cwd),
                "env": environment,
                "stdout": stdout_stream,
                "stderr": stderr_stream,
            }
            if input_data is None:
                popen_kwargs["stdin"] = subprocess.DEVNULL
            else:
                popen_kwargs["stdin"] = subprocess.PIPE
            if os.name == "nt":
                popen_kwargs["creationflags"] = getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)
            else:
                popen_kwargs["start_new_session"] = True
            try:
                process = subprocess.Popen(command, **popen_kwargs)
                try:
                    process.communicate(input=input_data, timeout=timeout)
                except subprocess.TimeoutExpired as error:
                    timeout_error = error
                    try:
                        _terminate_logged_process(
                            process,
                            label,
                            cwd=Path(cwd),
                            logs=logs,
                        )
                    except BaseException as error:
                        cleanup_error = error
                    # Ensure the leader is reaped even if group cleanup
                    # reported a secondary failure.
                    try:
                        process.wait(timeout=1)
                    except (OSError, subprocess.TimeoutExpired) as error:
                        if cleanup_error is None:
                            cleanup_error = error
                returncode = process.returncode
            except BaseException as error:
                launch_error = error
    except OSError as error:
        launch_error = error

    try:
        stdout = stdout_path.read_bytes()
    except OSError as error:
        raise LocalVMError(f"{label}: stdout log could not be read: {error}") from error
    try:
        stderr = stderr_path.read_bytes()
    except OSError as error:
        raise LocalVMError(f"{label}: stderr log could not be read: {error}") from error

    if launch_error is not None and timeout_error is None:
        if isinstance(launch_error, LocalVMError):
            raise launch_error
        raise LocalVMError(f"{label}: command could not start: {launch_error}") from launch_error
    if timeout_error is not None:
        failure = LocalVMError(f"{label}: command timed out")
        failure.add_note(f"{label}_stdout:\n{output_text(stdout)}")
        failure.add_note(f"{label}_stderr:\n{output_text(stderr)}")
        if cleanup_error is not None:
            failure.add_note(f"{label}_cleanup: {type(cleanup_error).__name__}: {cleanup_error}")
        raise failure from timeout_error

    if returncode is None:
        returncode = -1
    completed = subprocess.CompletedProcess(
        command, returncode, stdout, stderr
    )
    if check and completed.returncode != 0:
        failure = LocalVMError(f"{label}: command exited {completed.returncode}")
        failure.add_note(f"{label}_stdout:\n{output_text(stdout)}")
        failure.add_note(f"{label}_stderr:\n{output_text(stderr)}")
        raise failure
    return completed


def _candidate_path(
    descriptor: dict[str, Any], name: str, *, required: bool = True
) -> Path | None:
    value = descriptor.get(name)
    if value is None:
        if required:
            raise LocalVMError(f"candidate path is missing: {name}")
        return None
    if not isinstance(value, str):
        raise LocalVMError(f"candidate path is invalid: {name}")
    return Path(value)


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as error:
        raise LocalVMError(f"cannot read Release artifact: {path}") from error
    return digest.hexdigest()


def _release_document(path: Path, *, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        raise LocalVMError(f"{label} is unreadable") from error
    if not isinstance(value, dict):
        raise LocalVMError(f"{label} is not an object")
    return value


def _release_artifact_map(run_dir: Path, manifest: dict[str, Any], platform: str) -> dict[tuple[str, str], Path]:
    """Validate the owner manifest and every staged artifact before install."""
    if manifest.get("schema") != 1 or manifest.get("mode") != "release-package":
        raise LocalVMError("Release manifest schema or mode is invalid")
    if manifest.get("repository") != _RELEASE_REPOSITORY:
        raise LocalVMError("Release manifest repository is invalid")
    if manifest.get("workflow") != "Release" or manifest.get("workflow_path") != _RELEASE_WORKFLOW:
        raise LocalVMError("Release manifest workflow is invalid")
    if manifest.get("run_attempt") != 1 or manifest.get("branch") != "main":
        raise LocalVMError("Release manifest is not a first-attempt main-branch run")
    run_id = manifest.get("run_id")
    source_sha = manifest.get("source_sha")
    if not isinstance(run_id, int) or isinstance(run_id, bool) or run_id <= 0:
        raise LocalVMError("Release manifest run ID is invalid")
    if not isinstance(source_sha, str) or _SOURCE_SHA.fullmatch(source_sha) is None:
        raise LocalVMError("Release manifest source SHA is invalid")
    if manifest.get("platform") != platform:
        raise LocalVMError("Release manifest platform does not match this guest")
    raw_artifacts = manifest.get("artifacts")
    if not isinstance(raw_artifacts, list) or not raw_artifacts:
        raise LocalVMError("Release manifest artifact list is invalid")
    expected: dict[tuple[str, str], tuple[str, str, str]]
    if platform == "windows":
        expected = {
            ("package", "amd64"): (
                "dobbyVPN-windows-amd64.msi",
                "dobbyVPN-windows-amd64.msi",
                "release/windows/dobbyVPN-windows-amd64.msi",
            ),
        }
    elif platform == "macos":
        expected = {
            ("package", "arm64"): (
                "dobbyVPN-macos-aarch64.pkg",
                "dobbyVPN-macos-aarch64.pkg",
                "release/arm64/dobbyVPN-macos-aarch64.pkg",
            ),
            ("package", "amd64"): (
                "dobbyVPN-macos-amd64.pkg",
                "dobbyVPN-macos-amd64.pkg",
                "release/amd64/dobbyVPN-macos-amd64.pkg",
            ),
        }
    else:
        raise LocalVMError("exact Release packages are supported only on Windows/macOS")
    observed: dict[tuple[str, str], Path] = {}
    for item in raw_artifacts:
        if not isinstance(item, dict):
            raise LocalVMError("Release manifest artifact entry is invalid")
        role = item.get("role")
        architecture = item.get("architecture")
        key = (role, architecture)
        if key not in expected or key in observed:
            raise LocalVMError("Release manifest artifact set is missing, extra, or ambiguous")
        artifact_name, file_name, expected_relative = expected[key]
        if (
            item.get("artifact_name") != artifact_name
            or item.get("file_name") != file_name
        ):
            raise LocalVMError("Release manifest artifact identity is invalid")
        relative = item.get("relative_path")
        digest = item.get("sha256")
        if relative != expected_relative:
            raise LocalVMError("Release artifact staging path is invalid")
        if not isinstance(digest, str) or _SHA256.fullmatch(digest) is None:
            raise LocalVMError("Release artifact hash is invalid")
        staged = run_dir / relative
        if staged.is_symlink():
            raise LocalVMError(f"staged Release artifact is a symlink: {relative}")
        path = _inside(staged, run_dir)
        if path.name != file_name or not path.is_file():
            raise LocalVMError(f"staged Release artifact is invalid: {relative}")
        if _file_sha256(path) != digest:
            raise LocalVMError(f"Release artifact hash mismatch: {file_name}")
        observed[key] = path
    if set(observed) != set(expected):
        raise LocalVMError("Release manifest artifact set is incomplete")
    return observed


def _validate_release_inputs(
    run_dir: Path,
    source: Path,
    manifest_path: Path,
    *,
    logs: Path,
) -> tuple[dict[str, Any], dict[tuple[str, str], Path]]:
    if manifest_path.is_symlink():
        raise LocalVMError("Release manifest must be a regular file")
    manifest_path = _inside(manifest_path, run_dir)
    manifest = _release_document(manifest_path, label="Release manifest")
    identity = _release_document(_required_input(run_dir, "source-identity.json"), label="source identity")
    if identity.get("schema") != 1 or identity.get("repository") != _RELEASE_REPOSITORY:
        raise LocalVMError("source identity is invalid")
    if identity.get("source_sha") != manifest.get("source_sha"):
        raise LocalVMError("source identity and Release manifest source SHA differ")
    if identity.get("release_run_id") != manifest.get("run_id"):
        raise LocalVMError("source identity and Release manifest run ID differ")
    if identity.get("platform") != manifest.get("platform"):
        raise LocalVMError("source identity and Release manifest platform differ")
    git_dir = source / ".git"
    if git_dir.exists():
        try:
            result = _run_probe_logged(
                ["git", "-C", str(source), "rev-parse", "--verify", "HEAD"],
                cwd=source,
                logs=logs,
                label="release-source-revision",
                timeout=30,
            )
        except LocalVMError as error:
            failure = LocalVMError("could not verify staged source revision")
            for note in getattr(error, "__notes__", ()):
                failure.add_note(note)
            raise failure from error
        observed = (
            result.stdout.decode("utf-8", errors="replace").strip()
            if isinstance(result.stdout, bytes)
            else str(result.stdout).strip()
        )
        if result.returncode:
            failure = LocalVMError("could not verify staged source revision")
            _add_command_stream_notes(
                failure,
                "release-source-revision",
                result.stdout,
                result.stderr,
            )
            raise failure
        if observed != manifest["source_sha"]:
            failure = LocalVMError("staged source revision does not match Release manifest")
            _add_command_stream_notes(
                failure,
                "release-source-revision",
                result.stdout,
                result.stderr,
            )
            raise failure
    return manifest, _release_artifact_map(run_dir, manifest, str(manifest.get("platform")))


def _prepare_candidate(
    run_dir: Path,
    platform: str,
    *,
    architecture: str | None,
    skip_deps: bool,
    source_sha: str | None = None,
    source_tree: str | None = None,
) -> dict[str, str]:
    """Call the candidate builder directly and render paths into platform state."""
    source = run_dir / "source"
    scripts = str(source / ".github" / "scripts")
    inserted = scripts not in sys.path
    if inserted:
        sys.path.insert(0, scripts)
    try:
        from local_candidate import prepare_candidate

        candidate = prepare_candidate(
            request_root=run_dir,
            source_root=source,
            platform=platform,
            architecture=architecture,
            skip_deps=skip_deps,
            source_sha=source_sha,
            source_tree=source_tree,
        )
    finally:
        if inserted:
            sys.path.remove(scripts)
    return candidate.to_dict()


def _candidate_mode(descriptor: dict[str, Any]) -> str:
    mode = descriptor.get("mode")
    if not isinstance(mode, str) or mode not in {
        "local-build", "installed-package", "release-package", "ios-simulator",
    }:
        raise LocalVMError("prepared candidate mode is invalid")
    return mode


def _run_platform_source_checks(run_dir: Path, platform: str, logs: Path, timeout: float) -> None:
    """Run the same platform source commands used by CI on the selected source."""
    commands = {
        "linux": ("go-tests", "go-native-runtime", "lint-go", "python-tests"),
        "android": ("lint-android",),
        "windows": ("go-native-runtime",),
        "macos": ("go-native-runtime", "swift-unit", "lint-swift"),
    }.get(platform, ())
    source = run_dir / "source"
    script = source / ".github" / "scripts" / "source_checks.py"
    for check in commands:
        _timed_call(
            f"source-{check}",
            lambda check=check: _run_logged(
                [sys.executable, str(script), check], cwd=source, logs=logs,
                label=f"source-{check}", timeout=timeout,
            ),
            platform=platform,
        )


def _prepare_desktop_package(
    run_dir: Path, platform: str, source_sha: str, logs: Path,
    timeout: float, *, architecture: str | None, skip_deps: bool,
) -> dict[str, Any]:
    """Build and install the same native package used by hosted Release."""
    source = run_dir / "source"
    script = source / ".github" / "scripts" / "desktop" / "desktop_package.py"
    output = run_dir / "output" / "desktop-package"
    version = (source / "VERSION").read_text(encoding="utf-8").strip()
    control_pipe_sid = (
        _windows_control_pipe_sid(run_dir, logs, timeout)
        if platform == "windows" else None
    )
    build = [
        sys.executable, str(script), "build", "--platform", platform,
        "--version", version, "--source-sha", source_sha,
        "--output-dir", str(output),
    ]
    if architecture:
        build.extend(("--arch", architecture))
    if skip_deps:
        build.append("--skip-deps")
    _run_logged(build, cwd=source, logs=logs, label="desktop-package-build", timeout=timeout)
    if platform == "windows":
        _run_logged(
            [
                sys.executable,
                str(source / ".github" / "scripts" / "source_checks.py"),
                "cache-clean",
            ],
            cwd=source,
            logs=logs,
            label="windows-pre-installer-cache-clean",
            timeout=timeout,
        )
    if platform in {"windows", "macos"}:
        package_name = "dobbyVPN-windows-amd64.msi" if platform == "windows" else (
            "dobbyVPN-macos-aarch64.pkg" if host_platform.machine().lower() in {"arm64", "aarch64"}
            else "dobbyVPN-macos-amd64.pkg"
        )
        migration = [
            sys.executable, str(source / ".github" / "scripts" / "desktop" / "installer_migration.py"),
            "--platform", platform, "--package", str(output / package_name),
            "--current-version", version,
            "--log-dir", str(logs / "installer-migration"),
        ]
        if control_pipe_sid is not None:
            migration.extend(("--control-pipe-sid", control_pipe_sid))
        if platform == "windows":
            # Keep the downloaded rollback MSI inside this disposable run even
            # if the supervisor has to stop the interactive task on timeout.
            migration.extend(("--temp-parent", str(run_dir)))
            from .local_vm_windows import run_interactive_task

            if control_pipe_sid is None:
                raise LocalVMError("Windows installer migration requires the configured account SID")
            migration_environment = os.environ.copy()
            print(
                f"CFreeBytes={shutil.disk_usage(run_dir).free}; "
                f"CurrentMSIBytes={(output / package_name).stat().st_size}",
                flush=True,
            )
            result = run_interactive_task(
                migration,
                run_dir=run_dir,
                cwd=source,
                logs=logs,
                timeout=timeout,
                environment=migration_environment,
                task_label="installer-migration",
            )
            if result.returncode != 0:
                failure = LocalVMError(
                    f"desktop-installer-migration: command exited {result.returncode}"
                )
                _add_command_stream_notes(
                    failure,
                    "desktop-installer-migration",
                    result.stdout,
                    result.stderr,
                )
                raise failure
        else:
            _run_logged(
                migration,
                cwd=source, logs=logs, label="desktop-installer-migration", timeout=timeout,
            )
    install = [
        sys.executable, str(script), "install", "--build-descriptor",
        str(output / "desktop-package.json"), "--run-dir", str(run_dir),
        "--output", str(run_dir / "installed.json"),
    ]
    if control_pipe_sid is not None:
        install.extend(("--control-pipe-sid", control_pipe_sid))
    _run_logged(install, cwd=source, logs=logs, label="desktop-package-install", timeout=timeout)
    descriptor = _release_document(run_dir / "installed.json", label="installed package")
    return descriptor


def _discover_network_interface(
    run_dir: Path, logs: Path, timeout: float, platform: str,
    configured: str | None,
) -> str:
    pattern = r"^[A-Za-z0-9_.:-]+$" if platform == "linux" else r"^[A-Za-z0-9._-]+$"
    if configured:
        if re.fullmatch(pattern, configured) is None:
            raise LocalVMError("network interface is invalid")
        return configured
    if platform == "linux":
        result = _run_logged(
            ["ip", "-o", "route", "show", "default"], cwd=run_dir,
            logs=logs, label="network-interface", timeout=timeout,
        )
        match = re.search(r"(?:^|\s)dev\s+([^\s]+)", result.stdout.decode(errors="replace"))
    elif platform == "macos":
        result = _run_logged(
            ["route", "-n", "get", "default"], cwd=run_dir,
            logs=logs, label="network-interface", timeout=timeout,
        )
        match = re.search(r"(?m)^[ \t]*interface:\s*([^\s]+)", result.stdout.decode(errors="replace"))
    else:
        raise LocalVMError(f"network interface discovery is unsupported on {platform}")
    if match is None or re.fullmatch(pattern, match.group(1)) is None:
        raise LocalVMError("network interface could not be discovered")
    return match.group(1)


def _wait_linux_service(
    pid: int,
    service: Path,
    control_socket: Path,
    timeout: float,
    *,
    logs: Path,
) -> None:
    deadline = time.monotonic() + min(timeout, 30)
    last_probe_error: OSError | None = None
    while time.monotonic() < deadline:
        if _pid_matches(
            pid, str(service.resolve()), logs=logs, cwd=service.parent
        ) and control_socket.is_socket():
            try:
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as probe:
                    probe.settimeout(min(0.2, max(0.01, deadline - time.monotonic())))
                    probe.connect(str(control_socket))
                return
            except OSError as error:
                # A service socket can exist a little before it starts
                # accepting connections.  Retain those expected probe races,
                # but fail immediately for a real local socket failure.
                if error.errno not in {
                    errno.ECONNREFUSED,
                    errno.ENOENT,
                    errno.ENOTSOCK,
                    errno.EAGAIN,
                    errno.EWOULDBLOCK,
                    errno.ETIMEDOUT,
                }:
                    raise LocalVMError(
                        "Linux service readiness probe failed: "
                        f"{type(error).__name__}: {error}"
                    ) from error
                last_probe_error = error
        time.sleep(min(0.1, max(0.01, deadline - time.monotonic())))
    detail = "Linux service did not become ready"
    if last_probe_error is not None:
        detail += (
            "; last expected socket probe: "
            f"{type(last_probe_error).__name__}: {last_probe_error}"
        )
    raise LocalVMError(detail)


def _start_linux(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    service = _candidate_path(descriptor, "service")
    network = _candidate_path(descriptor, "network")
    if service is None or network is None:
        raise LocalVMError("Linux candidate service paths are incomplete")
    library_path = descriptor.get("library_path")
    library = str(library_path) if isinstance(library_path, str) and library_path else str(service.parent)
    logs.mkdir(parents=True, exist_ok=True)
    service_log = logs / "service.log"
    service_log.touch(exist_ok=True)
    pid_file = run_dir / "service.pid"
    pid_file.touch(exist_ok=True)
    # The service runs as root through the guest's existing noninteractive
    # sudo rule.  Keep the SSH account's UID in the control-socket policy so
    # the local functional client remains the authorized peer.
    launch_script = (
        'binary=$1; socket=$2; log=$3; request_root=$4; library=$5; pid_file=$6; peer_uid=$7; log_root=$8; '
        'stdout_path=$9; stderr_path=${10}; '
        'export DOBBYVPN_CONTROL_SOCKET="$socket" DOBBYVPN_CONTROL_PEER_UID="$peer_uid" '
        'DOBBYVPN_SUPERVISED_REQUEST=1 DOBBYVPN_REQUEST_ROOT="$request_root" '
        'DOBBYVPN_SSH_RUN="${request_root##*/}" DOBBY_LOG_PRECREATED=1 DOBBY_LOG_PATH="$log" '
        'DOBBY_LOG_ROOT="$log_root"; '
        'if [ -n "$library" ]; then export LD_LIBRARY_PATH="$library"; fi; '
        'setsid "$binary" >"$stdout_path" 2>"$stderr_path" < /dev/null & '
        'pid=$!; printf "%s\\n" "$pid" > "$pid_file"; printf "%s\\n" "$pid"'
    )
    command = [
        "sudo", "-n", "sh", "-c", launch_script,
        "dobbyvpn-service", str(service), str(network), str(service_log),
        str(run_dir), library, str(pid_file), str(os.getuid()), str(logs),
        str(logs / "service.log.stdout"), str(logs / "service.log.stderr"),
    ]
    result = _run_logged(command, cwd=service.parent, logs=logs, label="service-start", timeout=timeout)
    values = result.stdout.decode("ascii", errors="replace").split()
    if not values or not _PID.fullmatch(values[-1]):
        raise LocalVMError("Linux service start returned no service PID")
    pid = int(values[-1])
    (run_dir / "service.pid").write_text(f"{pid}\n", encoding="ascii")
    _wait_linux_service(pid, service, network, timeout, logs=logs)
    return {
        "pid": pid,
        "binary": str(service.resolve()),
        "pid_file": str(run_dir / "service.pid"),
        "socket": str(network),
        "environment": {"DOBBYVPN_CONTROL_SOCKET": str(network)},
        "library_path": library,
        "process_group": pid,
        "network_interface": network_interface,
    }


def _macos_launchd_pid(
    result: subprocess.CompletedProcess[bytes], *, label: str, subject: str,
) -> int:
    pids = re.findall(
        r"(?m)^\s*pid\s*=\s*([1-9][0-9]*)\s*$",
        result.stdout.decode(errors="replace"),
    )
    if len(pids) != 1:
        failure = LocalVMError(f"{subject} PID is missing or ambiguous")
        _add_command_stream_notes(failure, label, result.stdout, result.stderr)
        raise failure
    return int(pids[0])


def _wait_macos_service(
    pid: int,
    control_socket: Path,
    run_dir: Path,
    logs: Path,
    timeout: float,
) -> None:
    deadline = time.monotonic() + min(timeout, 30.0)
    last_probe_error: OSError | None = None
    while time.monotonic() < deadline:
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as probe:
                probe.settimeout(min(0.2, max(0.01, deadline - time.monotonic())))
                # Connect directly instead of lstat: launchd's execute-only
                # control-socket directory may reject attribute lookups while
                # still allowing the authorized peer to connect.
                probe.connect(str(control_socket))
        except OSError as error:
            if error.errno not in {
                errno.ECONNREFUSED,
                errno.ENOENT,
                errno.ENOTSOCK,
                errno.EAGAIN,
                errno.EWOULDBLOCK,
                errno.ETIMEDOUT,
                # The daemon publishes the socket before changing its owner.
                # The authorized user can briefly see EACCES during startup.
                errno.EACCES,
            }:
                raise LocalVMError(
                    "macOS service readiness probe failed: "
                    f"{type(error).__name__}: {error}"
                ) from error
            last_probe_error = error
        else:
            remaining = max(0.01, deadline - time.monotonic())
            result = _run_logged(
                ["launchctl", "print", "system/com.dobby.vpnservice"],
                cwd=run_dir, logs=logs, label="service-ready", timeout=min(5.0, remaining),
            )
            observed_pid = _macos_launchd_pid(
                result, label="service-ready", subject="ready macOS launchd service",
            )
            if observed_pid != pid:
                failure = LocalVMError(
                    "macOS launchd service PID changed during startup "
                    f"(expected {pid}, observed {observed_pid})"
                )
                _add_command_stream_notes(
                    failure, "service-ready", result.stdout, result.stderr,
                )
                raise failure
            return
        time.sleep(min(0.1, max(0.01, deadline - time.monotonic())))
    detail = f"macOS service did not become ready on {control_socket}"
    if last_probe_error is not None:
        detail += (
            "; last expected socket probe: "
            f"{type(last_probe_error).__name__}: {last_probe_error}"
        )
    raise LocalVMError(detail)


def _start_macos(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    service = _candidate_path(descriptor, "service")
    network = _candidate_path(descriptor, "network")
    if service is None or network is None:
        raise LocalVMError("macOS candidate service paths are incomplete")
    source = run_dir / "source"
    resources = service.parent
    plist = source / "ui/apple/macos/installer/vpnservice.plist"
    if not plist.is_file():
        raise LocalVMError("macOS service plist is missing")
    logs.mkdir(parents=True, exist_ok=True)
    (logs / "service.log").touch(exist_ok=True)
    # Reinstall the launchd job from this candidate's binary.  This uses the
    # product installer hook and avoids accidentally qualifying an older app
    # left on the VM.
    _run_logged(
        [
            "sudo", "-n", "env",
            f"DOBBYVPN_SERVICE_RESOURCES={resources}",
            f"DOBBYVPN_SERVICE_PLIST={plist}",
            f"DOBBYVPN_CONTROL_PEER_UID={os.getuid()}",
            f"DOBBY_LOG_PATH={logs / 'service.log'}",
            f"DOBBY_SERVICE_STDOUT_PATH={logs / 'service.stdout.log'}",
            f"DOBBY_SERVICE_STDERR_PATH={logs / 'service.stderr.log'}",
            "/bin/bash", str(source / "ui/apple/macos/installer/postinstall.sh"),
        ],
        cwd=run_dir, logs=logs, label="service-start", timeout=timeout,
    )
    result = _run_logged(
        ["launchctl", "print", "system/com.dobby.vpnservice"],
        cwd=run_dir, logs=logs, label="service-probe", timeout=timeout,
    )
    pid = _macos_launchd_pid(
        result, label="service-probe", subject="launchd service",
    )
    pid_file = run_dir / "service.pid"
    pid_file.write_text(f"{pid}\n", encoding="ascii")
    _wait_macos_service(pid, Path("/var/run/dobbyvpn/control.sock"), run_dir, logs, timeout)
    return {
        "pid": pid,
        "pid_file": str(pid_file),
        "binary": str(service.resolve()),
        "socket": "/var/run/dobbyvpn/control.sock",
        "environment": {"DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock", "DOBBY_LOG_PATH": str(logs / "service.log")},
        "launchd_label": "system/com.dobby.vpnservice",
        "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
        "network_interface": network_interface,
    }


def _start_macos_release(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    """Observe the launchd service installed by the exact Release package."""
    service = _candidate_path(descriptor, "service")
    if service is None or not service.is_file():
        raise LocalVMError("installed macOS Release service is missing")
    result = _run_logged(
        ["launchctl", "print", "system/com.dobby.vpnservice"],
        cwd=run_dir, logs=logs, label="service-probe", timeout=timeout,
    )
    pid = _macos_launchd_pid(
        result,
        label="service-probe",
        subject="installed macOS launchd service",
    )
    pid_file = run_dir / "service.pid"
    pid_file.write_text(f"{pid}\n", encoding="ascii")
    _wait_macos_service(pid, Path("/var/run/dobbyvpn/control.sock"), run_dir, logs, timeout)
    return {
        "pid": pid,
        "pid_file": str(pid_file),
        "binary": str(service.resolve()),
        "socket": "/var/run/dobbyvpn/control.sock",
        "environment": {"DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock", "DOBBY_LOG_PATH": str(logs / "service.log")},
        "launchd_label": "system/com.dobby.vpnservice",
        "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
        "network_interface": network_interface,
    }


def _release_state(run_dir: Path, manifest: dict[str, Any], package: Path, architecture: str) -> dict[str, Any]:
    state = _read_state(run_dir) or {"platform": manifest.get("platform"), "suite": "full"}
    release = {
        "mode": "release-package",
        "repository": manifest["repository"],
        "workflow": manifest["workflow"],
        "run_id": manifest["run_id"],
        "source_sha": manifest["source_sha"],
        "platform": manifest["platform"],
        "architecture": architecture,
        "package_path": str(package),
        "package_sha256": _file_sha256(package),
        "install_attempted": False,
        "installed": False,
    }
    state["release"] = release
    state["status"] = "install-pending"
    _write_json(run_dir / "platform.json", state)
    return release


def _windows_control_pipe_sid(run_dir: Path, logs: Path, timeout: float) -> str:
    """Resolve the configured interactive user SID for an MSI run as SYSTEM."""

    user = os.environ.get("DOBBYVPN_CONTROL_PIPE_USER", "").strip()
    if not user:
        raise LocalVMError("configured Windows desktop user is unavailable")
    environment = os.environ.copy()
    environment["DOBBYVPN_CONTROL_PIPE_USER"] = user
    script = r'''$ErrorActionPreference = "Stop"
$account = [string]$env:DOBBYVPN_CONTROL_PIPE_USER
try {
  $sid = (New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $account).Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  [Console]::Error.WriteLine("configured Windows desktop user could not be resolved")
  exit 1
}
[Console]::Out.WriteLine($sid)
'''
    result = _run_logged(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script],
        cwd=run_dir,
        logs=logs,
        label="release-control-user-sid",
        timeout=timeout,
        environment=environment,
        check=False,
    )
    if result.returncode != 0:
        raise LocalVMError(
            f"configured Windows desktop user SID lookup exited {result.returncode}"
        )
    sid = result.stdout.decode("ascii", errors="strict").strip()
    if not re.fullmatch(r"S-1-(?:[0-9]+-)*[0-9]+", sid) or sid == "S-1-5-18":
        raise LocalVMError("configured Windows desktop user SID is invalid")
    return sid


def _install_windows_release(
    run_dir: Path, manifest: dict[str, Any], artifacts: dict[tuple[str, str], Path],
    logs: Path, timeout: float,
) -> tuple[dict[str, Any], dict[str, Any]]:
    package = artifacts[("package", "amd64")]
    release = _release_state(run_dir, manifest, package, "amd64")
    state = _read_state(run_dir) or {}
    release["install_attempted"] = True
    state["release"] = release
    state["status"] = "installing"
    _write_json(run_dir / "platform.json", state)
    package_log = run_dir / "logs" / "windows-install.log"
    try:
        control_pipe_sid = _windows_control_pipe_sid(run_dir, logs, timeout)
        result = _run_logged(
            [
                "msiexec.exe", "/i", str(package), "/qn", "/norestart",
                f"DOBBYVPN_CONTROL_PIPE_SID={control_pipe_sid}",
                "/L*v", str(package_log),
            ],
            cwd=run_dir, logs=logs, label="release-install", timeout=timeout,
            check=False,
        )
        release["install_returncode"] = result.returncode
        if result.returncode != 0:
            state["release"] = release
            _write_json(run_dir / "platform.json", state)
            raise LocalVMError(f"exact Windows MSI install exited {result.returncode}")
        release["installed"] = True
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        query = r'''$ErrorActionPreference = "Stop"
$root = Join-Path $env:ProgramFiles "DobbyVPN"
$cli = @(Get-ChildItem $root -Filter "dobby-cli.exe" -Recurse -File)
$service = @(Get-ChildItem $root -Filter "dobbyvpn-backend.exe" -Recurse -File)
$ui = Join-Path $root "bin\DobbyVPN.exe"
if ($cli.Count -ne 1 -or $service.Count -ne 1 -or -not (Test-Path -LiteralPath $ui -PathType Leaf)) {
  throw "installed Release closure is ambiguous"
}
Write-Output ("$($cli[0].FullName)|$($service[0].FullName)|$ui")
'''
        paths = _run_logged(
            ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", query],
            cwd=run_dir, logs=logs, label="release-installed-paths", timeout=timeout,
        ).stdout.decode("utf-8", errors="strict").strip().split("|")
        if len(paths) != 3 or any(not value for value in paths):
            raise LocalVMError("installed Windows Release paths are invalid")
        descriptor = {
            "service": paths[1], "cli": paths[0], "ui": paths[2],
            "network": str(run_dir / ".dobbyvpn-run" / "s"),
        }
        release["installed_paths"] = {
            "service": paths[1], "cli": paths[0], "ui": paths[2],
        }
        state["release"] = release
        state["candidate"] = descriptor
        state["status"] = "candidate-prepared"
        _write_json(run_dir / "platform.json", state)
        return descriptor, release
    except Exception as error:
        release["install_error"] = f"{type(error).__name__}: {error}"
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        raise


def _install_macos_release(
    run_dir: Path, manifest: dict[str, Any], artifacts: dict[tuple[str, str], Path],
    logs: Path, timeout: float,
) -> tuple[dict[str, Any], dict[str, Any]]:
    machine = host_platform.machine().lower()
    architecture = "arm64" if machine in {"arm64", "aarch64"} else "amd64" if machine in {"x86_64", "amd64"} else ""
    if not architecture:
        raise LocalVMError(f"unsupported macOS guest architecture: {machine}")
    package = artifacts[("package", architecture)]
    release = _release_state(run_dir, manifest, package, architecture)
    state = _read_state(run_dir) or {}
    release["install_attempted"] = True
    state["release"] = release
    state["status"] = "installing"
    _write_json(run_dir / "platform.json", state)
    try:
        result = _run_logged(
            [
                "sudo", "-n", "env",
                f"DOBBYVPN_CONTROL_PEER_UID={os.getuid()}",
                f"DOBBY_LOG_PATH={run_dir / 'logs' / 'service.log'}",
                "installer", "-pkg", str(package), "-target", "/",
            ],
            cwd=run_dir, logs=logs, label="release-install", timeout=timeout,
            check=False,
        )
        release["install_returncode"] = result.returncode
        if result.returncode != 0:
            state["release"] = release
            _write_json(run_dir / "platform.json", state)
            raise LocalVMError(f"exact macOS package install exited {result.returncode}")
        release["installed"] = True
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        paths = {
            "service": Path("/Applications/Dobby VPN.app/Contents/Resources/dobbyvpn-backend"),
            "cli": Path("/Applications/Dobby VPN.app/Contents/Resources/dobby-cli"),
            "ui": Path("/Applications/Dobby VPN.app"),
        }
        if any(not paths[name].is_file() for name in ("service", "cli")) or not paths["ui"].is_dir():
            raise LocalVMError("installed macOS Release closure is incomplete")
        descriptor = {
            "service": str(paths["service"]), "cli": str(paths["cli"]),
            "ui": str(paths["ui"]),
            "network": str(run_dir / ".dobbyvpn-run" / "s"),
        }
        release["installed_paths"] = {key: str(value) for key, value in paths.items()}
        state["release"] = release
        state["candidate"] = descriptor
        state["status"] = "candidate-prepared"
        _write_json(run_dir / "platform.json", state)
        return descriptor, release
    except Exception as error:
        release["install_error"] = f"{type(error).__name__}: {error}"
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        raise


def _prepare_release_candidate(
    run_dir: Path, platform: str, manifest_path: Path, logs: Path, timeout: float,
) -> dict[str, Any]:
    source = _required_input(run_dir, "source", directory=True)
    manifest, artifacts = _validate_release_inputs(
        run_dir,
        source,
        manifest_path,
        logs=logs,
    )
    if platform == "windows":
        descriptor, _ = _install_windows_release(run_dir, manifest, artifacts, logs, timeout)
    elif platform == "macos":
        descriptor, _ = _install_macos_release(run_dir, manifest, artifacts, logs, timeout)
    else:
        raise LocalVMError("exact Release packages are supported only on Windows/macOS")
    descriptor["mode"] = "release-package"
    return descriptor


def _start_windows(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    from .local_vm_windows import start
    return start(run_dir, descriptor, logs, timeout)


def _start_android(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    from .local_vm_android import start
    return start(run_dir, descriptor, logs, timeout)


def _start_ios(
    run_dir: Path,
    descriptor: dict[str, Any],
    logs: Path,
    timeout: float,
) -> dict[str, Any]:
    from .local_vm_ios import run
    return run(run_dir, descriptor, logs, timeout)


def _prepare_ios(
    run_dir: Path,
    logs: Path,
    timeout: float,
    architecture: str | None,
    source_sha: str | None = None,
) -> dict[str, Any]:
    from .local_vm_ios import prepare
    return prepare(run_dir, logs, timeout, architecture, source_sha)


def _functional_command(
    run_dir: Path,
    descriptor: dict[str, Any],
    platform: str,
    timeout: float,
    scenarios: list[str] | None,
    suite: str,
) -> list[str]:
    logs = run_dir / "logs"
    if descriptor.get("mode") == "installed-package":
        runtime = descriptor.get("runtime", {})
        if not isinstance(runtime, dict) or not isinstance(runtime.get("pid"), int):
            raise LocalVMError("installed desktop service runtime PID is unavailable")
        command = [
            sys.executable, str(run_dir / "source" / ".github" / "scripts" / "desktop" / "desktop_package.py"),
            "test", "--mode", "local", "--platform", platform,
            "--installed-descriptor", str(run_dir / "installed.json"),
            "--profile", str(run_dir / "profile"), "--suite", suite,
            "--output", str(logs / "functional.json"), "--raw-log-dir", str(logs),
            "--platform-version", f"local-{platform}",
            "--lane-timeout-seconds", str(timeout),
            "--service-pid", str(runtime["pid"]),
        ]
        endpoint = ("pipe", "--service-pipe") if platform == "windows" else ("socket", "--service-socket")
        if endpoint[0] in runtime:
            command.extend((endpoint[1], str(runtime[endpoint[0]])))
        for name, flag in (("pid_file", "--service-pid-file"), ("identity_file", "--service-identity-file"), ("network_interface", "--network-interface")):
            if isinstance(runtime.get(name), str) and runtime[name]:
                command.extend((flag, str(runtime[name])))
        if scenarios:
            raise LocalVMError("focused scenarios are unavailable for installed desktop packages")
        return command
    module = (
        "torturer_runner.hosted"
        if platform == "android" and not scenarios
        else "torturer_runner.functional"
    )
    command = [
        sys.executable, "-m", module, "--platform", platform,
        "--profile", str(run_dir / "profile"), "--output", str(logs / "functional.json"),
        "--raw-log-dir", str(logs), "--platform-version", f"local-{platform}",
        "--lane-timeout-seconds", str(timeout),
        "--suite", suite,
    ]
    if platform == "android":
        command.extend(("--adb", str(descriptor["runtime"]["adb"])))
        source_sha = descriptor.get("source_sha")
        if source_sha is not None:
            if not isinstance(source_sha, str) or _SOURCE_SHA.fullmatch(source_sha) is None:
                raise LocalVMError("prepared Android candidate source SHA is invalid")
            command.extend(("--source-sha", source_sha))
    else:
        command.extend(("--cli", str(_candidate_path(descriptor, "cli")),))
        runtime = descriptor.get("runtime", {})
        endpoint = ("pipe", "--service-pipe") if platform == "windows" else ("socket", "--service-socket")
        for name, flag in (("pid", "--service-pid"), ("binary", "--service-binary"), endpoint, ("library_path", "--service-library-path"), ("pid_file", "--service-pid-file"), ("identity_file", "--service-identity-file")):
            if name in runtime:
                command.extend((flag, str(runtime[name])))
        if "network_interface" in runtime:
            command.extend(("--network-interface", str(runtime["network_interface"])))
        if platform == "linux":
            command.extend(("--routing-firewall-helper", str(ROUTING_HELPERS / "linux.sh"),))
        elif platform == "macos":
            helper = ROUTING_HELPERS / "macos.sh"
            command.extend(("--routing-firewall-helper", str(helper)))
    for scenario in scenarios or []:
        command.extend(("--scenario", scenario))
    return command


def _native_ui_command(
    run_dir: Path,
    descriptor: dict[str, Any],
    runtime: dict[str, Any],
    platform: str,
    timeout: float,
    ui_helper: Path,
) -> list[str]:
    """Build the real-window journey command for desktop full guests."""
    if platform not in {"windows", "macos"}:
        raise LocalVMError(f"native GUI qualification is unsupported on {platform}")
    smoke = run_dir / "source" / "torturer" / "torturer_runner" / "ui" / "smoke.py"
    module = run_dir / "source" / "torturer" / "torturer_runner" / "ui" / "journey.py"
    if not smoke.is_file():
        raise LocalVMError("native desktop UI qualification script is missing")
    if not module.is_file():
        raise LocalVMError("native desktop UI journey module is missing")
    if not ui_helper.is_file():
        raise LocalVMError("prepared native UI helper is missing")
    for name in ("cli", "ui"):
        if not isinstance(descriptor.get(name), str):
            raise LocalVMError(f"native desktop UI candidate path is missing: {name}")
    endpoint = "pipe" if platform == "windows" else "socket"
    for name in ("pid", "binary", endpoint):
        if name not in runtime:
            raise LocalVMError(f"native desktop UI runtime value is missing: {name}")
    task_timeout = min(timeout, _NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS)
    command = [
        sys.executable,
        "-m",
        "torturer_runner.ui.journey",
        "--platform", platform,
        "--cli", str(descriptor["cli"]),
        "--ui", str(descriptor["ui"]),
        "--profile", str(run_dir / "profile"),
        "--raw-log-dir", str(run_dir / "logs"),
        "--output", str(run_dir / "logs" / "native-ui.json"),
        "--timeout", str(_native_ui_driver_timeout(task_timeout)),
        "--ui-helper", str(ui_helper),
        "--service-pid", str(runtime["pid"]),
        "--service-binary", str(runtime["binary"]),
    ]
    command.extend(("--service-pipe" if platform == "windows" else "--service-socket", str(runtime[endpoint])))
    for name, flag in (
        ("library_path", "--service-library-path"),
        ("pid_file", "--service-pid-file"),
        ("identity_file", "--service-identity-file"),
    ):
        if name in runtime and runtime[name] is not None:
            command.extend((flag, str(runtime[name])))
    if runtime.get("network_interface") is not None:
        command.extend(("--network-interface", str(runtime["network_interface"])))
    if platform == "macos":
        helper = ROUTING_HELPERS / "macos.sh"
        command.extend(("--routing-firewall-helper", str(helper)))
    return command


def _run_native_ui(
    command: list[str],
    *,
    platform: str,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> subprocess.CompletedProcess[bytes]:
    """Run desktop UI smoke in the platform's actual interactive context."""

    if platform == "windows":
        from .local_vm_windows import run_interactive_ui

        return run_interactive_ui(
            command,
            run_dir=run_dir,
            cwd=cwd,
            logs=logs,
            timeout=timeout,
            environment=environment,
        )
    if platform == "macos":
        from .local_vm_macos import run_interactive_ui

        return run_interactive_ui(
            command,
            run_dir=run_dir,
            cwd=cwd,
            logs=logs,
            timeout=timeout,
            environment=environment,
        )
    return _run_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="native-ui",
        timeout=timeout,
        environment=environment,
        check=False,
    )


def _record_native_ui_unavailable(run_dir: Path, platform: str, error: Exception) -> None:
    """Retain an explicit unavailable native-window result for collection."""

    reason_code = str(getattr(error, "reason_code", "NATIVE_UI_UNAVAILABLE"))
    _write_json(run_dir / "logs" / "native-ui.json", {
        "suite": "full",
        "action_driver": "native-window",
        "platform": platform,
        "complete": False,
        "status": "unavailable",
        "availability": "unavailable",
        "reason_code": reason_code,
        "error": str(error),
    })


def _refresh_desktop_runtime_after_headless(
    runtime: dict[str, Any],
) -> dict[str, Any]:
    """Use the service PID sidecar after headless process-loss recovery.

    Windows and macOS process-loss adapters deliberately replace the service
    and update ``service.pid``.  A desktop full lane starts after mini, so the
    native journey must bind to that current candidate identity rather than
    the PID recorded before mini began.
    """
    pid_file = runtime.get("pid_file")
    if isinstance(pid_file, str):
        try:
            value = Path(pid_file).read_text(encoding="ascii").strip()
        except (OSError, UnicodeDecodeError) as error:
            raise LocalVMError("desktop service PID sidecar is unavailable after mini") from error
        if _PID.fullmatch(value) is None:
            raise LocalVMError("desktop service PID sidecar is invalid after mini")
        runtime = dict(runtime)
        runtime["pid"] = int(value)
    return runtime


def _validate_suite(platform: str, suite: str, scenarios: list[str] | None) -> None:
    if suite == "full" and platform not in {"windows", "macos"}:
        raise LocalVMError(
            f"{platform} full is unsupported: local full adds a native desktop window only"
        )
    if suite == "full" and scenarios:
        raise LocalVMError(
            "full qualification cannot select focused scenarios; run diagnostics with --suite mini"
        )


def prepare(args: argparse.Namespace) -> int:
    """Build a candidate and persist its artifact and cleanup descriptors."""
    run_dir = _run_dir(args.run_dir)
    if _read_state(run_dir) is not None:
        raise LocalVMError("run-dir already has platform state; clean it before preparing a new candidate")
    _required_input(run_dir, "source", directory=True)
    if args.platform == "android" and (args.source_sha is None) != (args.source_tree is None):
        raise LocalVMError("complete Android build requires both source commit and source tree")
    # Focused scenarios belong to the execution request.  Preparation only
    # validates the platform and suite selected for the eventual run.
    _validate_suite(args.platform, args.suite, None)
    if args.release_manifest is not None and (
        args.suite != "full" or args.platform not in {"windows", "macos"}
    ):
        raise LocalVMError("exact Release packages require full Windows/macOS coverage")
    if args.platform != "ios-simulator":
        _required_input(run_dir, "profile")

    logs = run_dir / "logs"
    (run_dir / "output").mkdir(parents=True, exist_ok=True)
    state: dict[str, Any] = {
        "platform": args.platform,
        "suite": args.suite,
        "status": "preparing",
    }
    _write_json(run_dir / "platform.json", state)
    try:
        if args.source_checks:
            state["source_checks_attempted"] = True
            state["status"] = "source-checks"
            _write_json(run_dir / "platform.json", state)
            _run_platform_source_checks(run_dir, args.platform, logs, args.timeout)
            state["source_checks"] = "passed"
            state["status"] = "preparing"
            _write_json(run_dir / "platform.json", state)

        if args.platform == "macos" and args.suite == "full":
            from .local_vm_macos import preflight_interactive_desktop

            state["status"] = "desktop-preflight"
            _write_json(run_dir / "platform.json", state)
            preflight_interactive_desktop(
                run_dir=run_dir,
                logs=logs,
                timeout=min(args.timeout, 30.0),
            )
            state["macos_desktop_preflight"] = "passed"
            state["status"] = "preparing"
            _write_json(run_dir / "platform.json", state)

        if args.platform == "ios-simulator":
            candidate = _timed_call(
                "ios-simulator-build",
                lambda: _prepare_ios(
                    run_dir, logs, args.timeout, args.architecture, args.source_sha
                ),
                platform=args.platform,
            )
        elif args.release_manifest is not None:
            candidate = _timed_call(
                "release-package-prepare-install",
                lambda: _prepare_release_candidate(
                    run_dir, args.platform, args.release_manifest, logs, args.timeout,
                ),
                platform=args.platform,
            )
            persisted = _read_state(run_dir)
            if persisted is not None:
                state.update(persisted)
            candidate["mode"] = "release-package"
        elif args.source_sha and args.platform in {"linux", "windows", "macos"}:
            candidate = _timed_call(
                "desktop-package-build-install",
                lambda: _prepare_desktop_package(
                    run_dir, args.platform, args.source_sha, logs, args.timeout,
                    architecture=args.architecture, skip_deps=args.skip_deps,
                ),
                platform=args.platform,
            )
        else:
            candidate = _timed_call(
                "candidate-build",
                lambda: _prepare_candidate(
                    run_dir,
                    args.platform,
                    architecture=args.architecture,
                    skip_deps=args.skip_deps,
                    source_sha=args.source_sha,
                    source_tree=args.source_tree,
                ),
                platform=args.platform,
            )

        if args.platform == "android" and args.source_sha is not None:
            candidate["source_sha"] = args.source_sha
        state["candidate"] = candidate
        if args.platform == "linux":
            service = _candidate_path(candidate, "service")
            network = _candidate_path(candidate, "network")
            if service is not None and network is not None:
                state["runtime"] = {
                    "binary": str(service.resolve()), "socket": str(network),
                    "pid_file": str(run_dir / "service.pid"),
                }
        elif args.platform == "macos":
            service = _candidate_path(candidate, "service")
            network = _candidate_path(candidate, "network")
            if service is not None and network is not None:
                state["runtime"] = {
                    "binary": str(service.resolve()),
                    "socket": "/var/run/dobbyvpn/control.sock",
                    "launchd_label": "system/com.dobby.vpnservice",
                    "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
                }
        _write_json(run_dir / "platform.json", state)
        if args.platform == "ios-simulator" and args.source_sha is not None:
            from .local_vm_ios import check_production

            _timed_call(
                "ios-production-analysis-and-archive",
                lambda: check_production(run_dir, logs, args.timeout, args.source_sha),
                platform=args.platform,
            )

        screenshot_python: Path | None = None
        if args.platform == "ios-simulator" or (
            args.suite == "full" and args.platform in {"windows", "macos"}
        ):
            screenshot_python = _install_screenshot_decoder(run_dir, logs, args.timeout)
        if screenshot_python is not None:
            candidate["screenshot_python"] = str(screenshot_python)
            state["candidate"] = candidate
            _write_json(run_dir / "platform.json", state)
        if args.suite == "full" and args.platform in {"windows", "macos"}:
            from .ui.helper import prepare_helper

            candidate["ui_helper"] = str(
                _timed_call(
                    "native-ui-helper-build",
                    lambda: prepare_helper(args.platform, run_dir, logs, args.timeout),
                    platform=args.platform,
                ).resolve()
            )
            state["candidate"] = candidate
            _write_json(run_dir / "platform.json", state)

        # Persist network ownership before interface discovery and execution.
        if args.platform in {"linux", "macos"}:
            interface = _discover_network_interface(
                run_dir, logs, args.timeout, args.platform, args.network_interface
            )
            state.setdefault("runtime", {})["network_interface"] = interface
            _write_json(run_dir / "platform.json", state)

        state["status"] = "candidate-prepared"
        _write_json(run_dir / "platform.json", state)
        return 0
    except Exception as error:
        _record_failure(run_dir, state, error)
        return 1


def run(args: argparse.Namespace) -> int:
    """Execute the prepared candidate described by ``platform.json``."""
    run_dir = _run_dir(args.run_dir)
    source = _required_input(run_dir, "source", directory=True)
    if args.platform != "ios-simulator":
        _required_input(run_dir, "profile")
    _validate_suite(args.platform, args.suite, args.scenarios)
    state = _read_state(run_dir)
    if state is None or state.get("status") != "candidate-prepared":
        raise LocalVMError("run requires a successfully prepared candidate")
    if state.get("platform") != args.platform or state.get("suite") != args.suite:
        raise LocalVMError("run request does not match the prepared platform and suite")
    descriptor = state.get("candidate")
    if not isinstance(descriptor, dict):
        raise LocalVMError("prepared candidate descriptor is missing")
    _candidate_mode(descriptor)
    logs = run_dir / "logs"
    (run_dir / "output").mkdir(parents=True, exist_ok=True)
    try:
        if args.platform == "ios-simulator":
            state["status"] = "running"
            _write_json(run_dir / "platform.json", state)
            runtime = _timed_call(
                "ios-simulator-app-contract",
                lambda: _start_ios(run_dir, descriptor, logs, args.timeout),
                platform=args.platform,
            )
            state["runtime"] = runtime
            state["status"] = "functional-complete"
            state["functional_exit_code"] = 0
            _write_json(run_dir / "platform.json", state)
            return 0

        runtime_plan = state.get("runtime")
        runtime_plan = dict(runtime_plan) if isinstance(runtime_plan, dict) else {}
        state["status"] = "starting"
        _write_json(run_dir / "platform.json", state)
        mode = _candidate_mode(descriptor)
        if args.platform == "linux":
            runtime = _timed_call(
                "service-start",
                lambda: _start_linux(
                    run_dir, descriptor, logs, args.timeout,
                    runtime_plan.get("network_interface"),
                ),
                platform=args.platform,
            )
        elif args.platform == "macos":
            start_macos = (
                _start_macos_release
                if mode in {"release-package", "installed-package"}
                else _start_macos
            )
            runtime = _timed_call(
                "service-start",
                lambda: start_macos(
                    run_dir, descriptor, logs, args.timeout,
                    runtime_plan.get("network_interface"),
                ),
                platform=args.platform,
            )
        elif args.platform == "windows":
            if mode in {"release-package", "installed-package"}:
                from .local_vm_windows import stop_installed_service

                release_state = state.get("release")
                if mode == "release-package" and not isinstance(release_state, dict):
                    raise LocalVMError("exact Windows Release install state is missing")
                if isinstance(release_state, dict):
                    release_state["msi_service_stop_attempted"] = True
                    state["release"] = release_state
                _write_json(run_dir / "platform.json", state)
                try:
                    stop_installed_service(run_dir, logs, args.timeout)
                except Exception as error:
                    if isinstance(release_state, dict):
                        release_state["msi_service_stop_error"] = f"{type(error).__name__}: {error}"
                        state["release"] = release_state
                        _write_json(run_dir / "platform.json", state)
                    raise
                if isinstance(release_state, dict):
                    release_state["msi_service_stopped"] = True
                    state["release"] = release_state
                    _write_json(run_dir / "platform.json", state)
            runtime = _timed_call(
                "service-start",
                lambda: _start_windows(run_dir, descriptor, logs, args.timeout),
                platform=args.platform,
            )
        elif args.platform == "android":
            runtime = _timed_call(
                "android-package-install",
                lambda: _start_android(run_dir, descriptor, logs, args.timeout),
                platform=args.platform,
            )

        if args.platform in {"windows", "macos"}:
            runtime_environment = runtime.get("environment")
            runtime_environment = dict(runtime_environment) if isinstance(runtime_environment, dict) else {}
            runtime_environment["HOME"] = _prepare_desktop_ui_home(run_dir)
            runtime["environment"] = runtime_environment
        state["runtime"] = runtime
        state["status"] = "running"
        _write_json(run_dir / "platform.json", state)

        if args.platform == "android":
            from .local_vm_android import run_ui as run_android_ui

            native_result = _timed_call(
                "android-native-ui",
                lambda: run_android_ui(run_dir, runtime, logs, args.timeout),
                platform=args.platform,
            )
            state["native_ui_exit_code"] = native_result.returncode
            if native_result.returncode != 0:
                state["status"] = "native-ui-failed"
                _write_json(run_dir / "platform.json", state)
                return native_result.returncode
            state["native_ui_status"] = "passed"
            _write_json(run_dir / "platform.json", state)

        # Full desktop qualification keeps the canonical mini suite, then runs
        # the real-window journey with the helper built during preparation.
        functional_suite = "mini" if args.suite == "full" else args.suite
        command = _functional_command(
            run_dir, {**descriptor, "runtime": runtime}, args.platform,
            args.timeout, args.scenarios, functional_suite,
        )
        functional_environment = {
            **os.environ,
            "PYTHONPATH": str(source / "torturer"),
            "DOBBYVPN_SSH_RUN": run_dir.name,
        }
        if args.platform == "linux":
            functional_environment.update({
                "DOBBYVPN_SUPERVISED_REQUEST": "1",
                "DOBBYVPN_REQUEST_ROOT": str(run_dir),
            })
        runtime_environment = runtime.get("environment")
        if isinstance(runtime_environment, dict):
            functional_environment.update({
                str(key): str(value)
                for key, value in runtime_environment.items()
                if isinstance(key, str) and isinstance(value, str)
            })
        result = _timed_call(
            "functional-suite",
            lambda: _run_logged(
                command, cwd=source / "torturer", logs=logs,
                label="functional", timeout=args.timeout,
                environment=functional_environment, check=False,
            ),
            platform=args.platform,
            suite=functional_suite,
        )
        state["functional_exit_code"] = result.returncode
        state["status"] = "functional-complete" if result.returncode == 0 else "functional-failed"
        _write_json(run_dir / "platform.json", state)
        if result.returncode != 0:
            return result.returncode

        if args.suite == "full" and args.platform in {"windows", "macos"}:
            if args.platform == "windows":
                runtime = _start_windows(run_dir, descriptor, logs, args.timeout)
            else:
                runtime = _refresh_desktop_runtime_after_headless(runtime)
            runtime_environment = runtime.get("environment")
            runtime_environment = dict(runtime_environment) if isinstance(runtime_environment, dict) else {}
            runtime_environment["HOME"] = _prepare_desktop_ui_home(run_dir)
            runtime["environment"] = runtime_environment
            native_descriptor = descriptor
            if args.platform == "macos":
                from .local_vm_macos import stage_native_ui_bundle

                native_descriptor = {
                    **descriptor,
                    "ui": str(stage_native_ui_bundle(descriptor["ui"])),
                }
            ui_helper = _candidate_path(descriptor, "ui_helper")
            state["runtime"] = runtime
            _write_json(run_dir / "platform.json", state)
            native_environment = _native_ui_environment(args.platform, runtime)
            screenshot_python = descriptor.get("screenshot_python")
            if isinstance(screenshot_python, str):
                native_environment["PYTHONPATH"] = screenshot_python
            native_task_timeout = min(args.timeout, _NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS)
            try:
                native_result = _timed_call(
                    "desktop-native-ui",
                    lambda: _run_native_ui(
                        _native_ui_command(
                            run_dir, native_descriptor, runtime, args.platform,
                            native_task_timeout, ui_helper,
                        ),
                        platform=args.platform,
                        run_dir=run_dir,
                        cwd=source / "torturer",
                        logs=logs,
                        timeout=native_task_timeout,
                        environment=native_environment,
                    ),
                    platform=args.platform,
                )
            except Exception as error:
                from .local_vm_windows import WindowsInteractiveDesktopUnavailable
                from .local_vm_macos import MacOSInteractiveDesktopUnavailable

                if isinstance(error, (WindowsInteractiveDesktopUnavailable, MacOSInteractiveDesktopUnavailable)):
                    _record_native_ui_unavailable(run_dir, args.platform, error)
                    state["native_ui_exit_code"] = 1
                    state["native_ui_status"] = "unavailable"
                    state["native_ui_reason"] = str(error)
                    state["status"] = "native-ui-unavailable"
                    _write_json(run_dir / "platform.json", state)
                    return 1
                raise
            state["native_ui_exit_code"] = native_result.returncode
            if native_result.returncode != 0:
                state["status"] = "native-ui-failed"
                _write_json(run_dir / "platform.json", state)
                return native_result.returncode
            state["native_ui_status"] = "passed"
            _write_json(run_dir / "platform.json", state)

        state["status"] = "functional-complete"
        _write_json(run_dir / "platform.json", state)
        return 0
    except Exception as error:
        _record_failure(run_dir, state, error)
        return 1

def _pid_matches(
    pid: int,
    binary: str | None,
    *,
    logs: Path,
    cwd: Path,
) -> bool:
    if pid <= 0 or not binary or os.name == "nt":
        return False
    if not _pid_alive(pid):
        return False
    # A capability-bearing service may be non-dumpable even to its own UID.
    # Use the VM's existing sudo privilege for this specific procfs lookup.
    command = ["sudo", "-n", "readlink", "-f", f"/proc/{pid}/exe"]
    observed = _run_probe_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="service-pid-executable",
        timeout=5,
    )
    if observed.returncode:
        if not _pid_alive(pid):
            return False
        failure = LocalVMError(
            "cannot inspect service executable; noninteractive sudo readlink is required\n"
        )
        _add_command_stream_notes(
            failure,
            "service-pid-executable",
            observed.stdout,
            observed.stderr,
        )
        raise failure
    observed_stdout = (
        observed.stdout.decode("utf-8", errors="replace")
        if isinstance(observed.stdout, bytes)
        else str(observed.stdout)
    )
    return observed_stdout.strip() == str(Path(binary).resolve())


def _pid_alive(pid: int) -> bool:
    try:
        return (Path("/proc") / str(pid) / "stat").read_text().rsplit(")", 1)[1].split()[0] != "Z"
    except FileNotFoundError:
        return False


def _checked_process_group(
    pid: int,
    binary: str | None,
    *,
    logs: Path,
    cwd: Path,
) -> str | None:
    if not _pid_matches(pid, binary, logs=logs, cwd=cwd):
        return None
    command = ["sudo", "-n", "ps", "-o", "pgid=", "-p", str(pid)]
    observed = _run_probe_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="service-pid-group",
        timeout=5,
    )
    if observed.returncode:
        if not _pid_alive(pid):
            return None
        failure = LocalVMError(
            "cannot inspect service process group; noninteractive sudo ps is required"
        )
        _add_command_stream_notes(
            failure,
            "service-pid-group",
            observed.stdout,
            observed.stderr,
        )
        raise failure
    process_group = (
        observed.stdout.decode("utf-8", errors="replace").strip()
        if isinstance(observed.stdout, bytes)
        else str(observed.stdout).strip()
    )
    if _PID.fullmatch(process_group) is None:
        raise LocalVMError("service process group is invalid")
    return process_group


def _signal_process_group(
    process_group: str,
    signal_name: str,
    *,
    logs: Path,
    cwd: Path,
) -> bool:
    command = ["sudo", "-n", "kill", signal_name, "--", f"-{process_group}"]
    result = _run_probe_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="service-pid-signal",
        timeout=5,
    )
    if result.returncode != 0:
        failure = LocalVMError(
            f"could not signal service process group {process_group} with {signal_name} "
            f"(exit={result.returncode})"
        )
        _add_command_stream_notes(
            failure,
            "service-pid-signal",
            result.stdout,
            result.stderr,
        )
        raise failure
    return True


def _stop_pid(
    pid: int,
    binary: str | None,
    timeout: float,
    *,
    logs: Path,
    cwd: Path,
) -> bool:
    if not _pid_alive(pid):
        return True
    process_group = _checked_process_group(pid, binary, logs=logs, cwd=cwd)
    if process_group is None:
        return False
    if not _signal_process_group(
        process_group,
        "-TERM",
        logs=logs,
        cwd=cwd,
    ) and _pid_alive(pid):
        raise LocalVMError("could not terminate service process group")
    deadline = time.monotonic() + min(timeout, 3)
    while time.monotonic() < deadline:
        if not _pid_matches(pid, binary, logs=logs, cwd=cwd):
            return True
        time.sleep(0.05)
    if _pid_matches(pid, binary, logs=logs, cwd=cwd):
        if not _signal_process_group(
            process_group,
            "-KILL",
            logs=logs,
            cwd=cwd,
        ) and _pid_alive(pid):
            raise LocalVMError("could not kill service process group")
    deadline = time.monotonic() + min(timeout, 5)
    while time.monotonic() < deadline:
        if not _pid_matches(pid, binary, logs=logs, cwd=cwd):
            return True
        time.sleep(0.05)
    return False


def _linux_runtime_paths(run_dir: Path, socket: object) -> tuple[Path, Path] | None:
    """Return the one disposable Linux socket and its private parent."""
    if socket is None:
        return None
    if not isinstance(socket, str):
        raise LocalVMError("Linux runtime socket path is invalid")
    socket_path = Path(socket)
    runtime_dir = run_dir.resolve() / ".dobbyvpn-run"
    if (
        not socket_path.is_absolute()
        or socket_path.name != "s"
        or socket_path.parent.resolve() != runtime_dir
    ):
        raise LocalVMError("Linux runtime socket is outside the private run directory")
    return socket_path, runtime_dir


def _cleanup_linux_runtime_state(
    run_dir: Path, socket: object, *, logs: Path, timeout: float, errors: list[str]
) -> None:
    try:
        paths = _linux_runtime_paths(run_dir, socket)
    except LocalVMError as error:
        errors.append(f"cleanup-service: {error}")
        return
    if paths is None:
        return
    socket_path, runtime_dir = paths
    if socket_path.exists():
        _cleanup_logged(
            ["sudo", "-n", "unlink", "--", str(socket_path)],
            cwd=run_dir, logs=logs, label="cleanup-service-socket", timeout=timeout,
            errors=errors,
        )
    # rmdir is deliberately non-recursive: an unexpected file must not be
    # hidden, and no path outside this exact candidate runtime directory is
    # ever passed to sudo.
    if runtime_dir.is_dir():
        _cleanup_logged(
            ["sudo", "-n", "rmdir", "--", str(runtime_dir)],
            cwd=run_dir, logs=logs, label="cleanup-service-runtime", timeout=timeout,
            errors=errors,
        )


def _cleanup_logged(
    command: list[str], *, cwd: Path, logs: Path, label: str, timeout: float,
    errors: list[str], tolerate_returncode: tuple[int, ...] = (),
) -> None:
    try:
        result = _run_logged(
            command, cwd=cwd, logs=logs, label=label, timeout=timeout, check=False
        )
    except Exception as error:
        errors.append(f"{label}: {type(error).__name__}: {error}")
        return
    if result.returncode != 0 and result.returncode not in tolerate_returncode:
        errors.append(f"{label}: command exited {result.returncode}")


def _cleanup_launchd(
    command: list[str], *, cwd: Path, logs: Path, timeout: float,
    errors: list[str],
) -> None:
    """Bootout one exact job, treating only an already-absent job as clean."""
    try:
        result = _run_logged(
            command, cwd=cwd, logs=logs, label="cleanup-service",
            timeout=timeout, check=False,
        )
    except Exception as error:
        errors.append(f"cleanup-service: {type(error).__name__}: {error}")
        return
    if result.returncode != 0:
        output = (result.stdout + result.stderr).decode("utf-8", errors="replace")
        absent = "Could not find service" in output or (
            result.returncode == 3 and "No such process" in output
        )
        if not absent:
            errors.append(f"cleanup-service: command exited {result.returncode}")


def _windows_release_package(run_dir: Path, release: dict[str, Any]) -> Path:
    """Return the one exact staged MSI, rejecting mutable path substitutions."""
    package = release.get("package_path")
    if not isinstance(package, str) or not package:
        raise LocalVMError("recorded MSI path is invalid")
    candidate = Path(package)
    if candidate.is_symlink():
        raise LocalVMError("recorded MSI path must not be a symlink")
    try:
        resolved = _inside(candidate, run_dir)
    except LocalVMError as error:
        raise LocalVMError("recorded MSI path is outside the exact Release staging path") from error
    except OSError as error:
        raise LocalVMError("recorded MSI path is unavailable") from error
    expected = (run_dir / "release" / "windows" / "dobbyVPN-windows-amd64.msi").resolve()
    if resolved != expected or not resolved.is_file():
        raise LocalVMError("recorded MSI path is outside the exact Release staging path")
    return resolved


def cleanup(args: argparse.Namespace) -> int:
    run_dir = _run_dir(args.run_dir)
    state = _read_state(run_dir)
    if state is None:
        return 0
    if state.get("platform") != args.platform:
        raise LocalVMError("cleanup platform does not match platform state")
    logs = run_dir / "logs"
    runtime = state.get("runtime") if isinstance(state.get("runtime"), dict) else {}
    release = state.get("release") if isinstance(state.get("release"), dict) else None
    installed_descriptor = run_dir / "installed.json"
    errors: list[str] = []
    from .subscription_fixture import SubscriptionFixture
    for fixture_name in ("native-subscription-fixture", "android-subscription-fixture"):
        try:
            SubscriptionFixture.cleanup_interrupted(run_dir / fixture_name)
        except Exception as error:
            errors.append("cleanup-subscription-fixture: " + "".join(traceback.format_exception(error)))

    if args.platform == "linux":
        helper = ROUTING_HELPERS / "linux.sh"
        _cleanup_logged(["sudo", "-n", str(helper), "remove"], cwd=run_dir, logs=logs, label="cleanup-routing", timeout=args.timeout, errors=errors)
        interface = runtime.get("network_interface")
        if isinstance(interface, str):
            _cleanup_logged(["sudo", "-n", "ip", "link", "set", "dev", interface, "up"], cwd=run_dir, logs=logs, label="cleanup-uplink", timeout=args.timeout, errors=errors)
        pid: int | None = runtime.get("pid") if isinstance(runtime.get("pid"), int) else None
        pid_file = runtime.get("pid_file")
        if isinstance(pid_file, str):
            try:
                value = Path(pid_file).read_text(encoding="ascii").strip()
            except FileNotFoundError:
                value = ""
            except OSError as error:
                errors.append(f"cleanup-service: cannot read service PID file: {error}")
                value = ""
            if value:
                if not _PID.fullmatch(value):
                    errors.append("cleanup-service: service PID file is invalid")
                else:
                    pid = int(value)
        service_stopped = True
        if isinstance(pid, int):
            try:
                if not _stop_pid(
                    pid,
                    runtime.get("binary"),
                    args.timeout,
                    logs=logs,
                    cwd=run_dir,
                ):
                    errors.append("cleanup-service: recorded PID no longer names the candidate binary")
                    service_stopped = False
            except Exception as error:
                errors.append(f"cleanup-service: {type(error).__name__}: {error}")
                service_stopped = False
        if service_stopped:
            _cleanup_linux_runtime_state(
                run_dir, runtime.get("socket"), logs=logs, timeout=args.timeout,
                errors=errors,
            )
    elif args.platform == "macos":
        helper = ROUTING_HELPERS / "macos.sh"
        _cleanup_logged(["sudo", "-n", str(helper), "routing-remove"], cwd=run_dir, logs=logs, label="cleanup-routing", timeout=args.timeout, errors=errors)
        if release is not None:
            # The package's fixed uninstaller owns launchd, its plist/socket,
            # receipt, app bundle, and uninstaller path.  Do not reproduce its
            # root-side deletion logic or accept paths from test state.
            _cleanup_logged(
                ["sudo", "-n", "/usr/local/libexec/dobbyvpn-uninstall"],
                cwd=run_dir, logs=logs, label="cleanup-package",
                timeout=args.timeout, errors=errors,
            )
        elif not installed_descriptor.is_file():
            label = runtime.get("launchd_label", "system/com.dobby.vpnservice")
            # bootout is required: KeepAlive would immediately restart the
            # service after a mere launchctl kill.
            _cleanup_launchd(["sudo", "-n", "launchctl", "bootout", str(label)], cwd=run_dir, logs=logs, timeout=args.timeout, errors=errors)
            plist = runtime.get("plist", "/Library/LaunchDaemons/com.dobby.vpnservice.plist")
            control_socket = runtime.get("socket", "/var/run/dobbyvpn/control.sock")
            remove_paths = [value for value in (plist, control_socket) if isinstance(value, str) and value.startswith(("/Library/", "/var/run/"))]
            if remove_paths:
                _cleanup_logged(["sudo", "-n", "rm", "-f", *remove_paths], cwd=run_dir, logs=logs, label="cleanup-macos-state", timeout=args.timeout, errors=errors)
    elif args.platform == "windows":
        from .local_vm_windows import cleanup as cleanup_windows
        try:
            cleanup_windows(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-windows: {type(error).__name__}: {error}")
        if release is not None:
            installed = release.get("installed") is True
            try:
                package = _windows_release_package(run_dir, release)
            except Exception as error:
                errors.append(f"cleanup-package: {type(error).__name__}: {error}")
            else:
                _cleanup_logged(
                    [
                        "msiexec.exe", "/x", str(package), "/qn", "/norestart",
                        "/L*v", str(logs / "windows-uninstall.log"),
                    ],
                    cwd=run_dir, logs=logs, label="cleanup-package", timeout=args.timeout,
                    errors=errors, tolerate_returncode=() if installed else (1605,),
                )
    elif args.platform == "android":
        from .local_vm_android import cleanup as cleanup_android
        try:
            cleanup_android(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-android: {type(error).__name__}: {error}")
    elif args.platform == "ios-simulator":
        from .local_vm_ios import cleanup as cleanup_ios
        try:
            cleanup_ios(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-ios: {type(error).__name__}: {error}")
    if installed_descriptor.is_file():
        source = run_dir / "source"
        _cleanup_logged(
            [
                sys.executable, str(source / ".github" / "scripts" / "desktop" / "desktop_package.py"),
                "uninstall", "--installed-descriptor", str(installed_descriptor),
                "--run-dir", str(run_dir),
            ],
            cwd=source, logs=logs, label="cleanup-installed-package",
            timeout=args.timeout, errors=errors,
        )
        # Installed packages use the OS's fixed log directory. The private
        # service used by focused checks already writes inside this run.
        if args.platform in {"macos", "windows"}:
            directory = (
                Path("/Library/Logs/DobbyVPN") if args.platform == "macos"
                else Path(os.environ.get("PROGRAMDATA", r"C:\ProgramData")) / "DobbyVPN" / "Logs"
            )
            collect_installed_backend_logs(directory, logs, errors)
    if state.get("source_checks_attempted") is True:
        source = run_dir / "source"
        _cleanup_logged(
            [sys.executable, str(source / ".github" / "scripts" / "source_checks.py"), "cache-clean"],
            cwd=source, logs=logs, label="cleanup-source-caches",
            timeout=args.timeout, errors=errors,
        )
    state["status"] = "cleaned" if not errors else "cleanup-failed"
    if errors:
        state["cleanup_errors"] = errors
    _write_json(run_dir / "platform.json", state)
    if errors:
        print("; ".join(errors), file=sys.stderr)
        return 1
    return 0


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.action == "prepare":
            return prepare(args)
        return run(args) if args.action == "run" else cleanup(args)
    except LocalVMError as error:
        traceback.print_exception(type(error), error, error.__traceback__, file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
