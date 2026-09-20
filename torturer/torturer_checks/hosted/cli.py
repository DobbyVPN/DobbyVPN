"""A narrow adapter around DobbyVPN's public CLI.

The adapter executes validated command vectors and keeps their output in
memory for parsing and assertions.  Functional runs do not retain command
lines or command streams on disk; the result document is the only persisted
run output.
"""

from __future__ import annotations

from dataclasses import dataclass
import ipaddress
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
from collections.abc import Mapping
from typing import Callable, Protocol, Sequence
from urllib.parse import urlparse

from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.assertions import (
    STABILITY_SAMPLE_COUNT,
    STABILITY_SAMPLE_INTERVAL_SECONDS,
)
from torturer_contract.functional.engine import CapabilityUnavailable, ScenarioExecutionError
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import ScenarioStep
from torturer_checks.windows_job import (
    WindowsJobError,
    close_for as close_windows_job,
    job_for as windows_job_for,
    popen_with_windows_job,
    terminate_and_prove_empty as terminate_windows_job,
)

_ROUTE_CONVERGENCE_POLL_SECONDS = 0.5
_ROUTING_PROBE_CONNECT_TIMEOUT_SECONDS = 5
_PROCESS_CLEANUP_SECONDS = 5.0
_TRANSIENT_ROUTING_FAILURES = frozenset(
    {
        "COMMAND_TIMEOUT",
        "COMMAND_DEADLINE_EXCEEDED",
        "EXTERNAL_IDENTITY_FAILED",
        "ROUTING_INTERFACE_UNAVAILABLE",
        "ROUTING_PROBE_FAILED",
    }
)


def _call_with_deadline(method, timeout: float, deadline: float | None):
    return method(timeout, deadline=deadline)


def _remaining_until(deadline: float, *, cap: float | None = None) -> float:
    remaining = max(0.0, deadline - time.monotonic())
    return remaining if cap is None else min(remaining, max(0.0, cap))


class HostedAdapterError(RuntimeError):
    """An adapter boundary failure with a stable caller-facing code."""

    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


@dataclass(frozen=True)
class CommandResult:
    command: tuple[str, ...]
    returncode: int
    stdout: bytes = b""
    stderr: bytes = b""
    timed_out: bool = False

    @property
    def stdout_text(self) -> str:
        return self.stdout.decode("utf-8", errors="replace")


def _parse_external_ip(raw: str) -> str:
    """Parse the plain or JSON-string identity response used by all hosts."""

    candidate = raw.strip()
    if candidate.startswith('"'):
        try:
            decoded = json.loads(candidate)
        except (TypeError, ValueError) as error:
            raise ScenarioExecutionError("EXTERNAL_IDENTITY_INVALID") from error
        if not isinstance(decoded, str):
            raise ScenarioExecutionError("EXTERNAL_IDENTITY_INVALID")
        candidate = decoded
    if "\n" in candidate or "\r" in candidate:
        raise ScenarioExecutionError("EXTERNAL_IDENTITY_INVALID")
    try:
        return str(ipaddress.ip_address(candidate))
    except ValueError as error:
        raise ScenarioExecutionError("EXTERNAL_IDENTITY_INVALID") from error


def _append_command_result_notes(error: BaseException, result: CommandResult) -> None:
    """Attach bounded command metadata without copying private values.

    ``CommandResult`` remains available to the caller for the one operation
    that needs to parse it.  Exceptions are often serialized by a workflow,
    however, so never put argv (which can contain profile paths/endpoints) or
    stdout/stderr contents into their notes.
    """

    error.add_note(f"command_returncode={result.returncode}")
    error.add_note(f"command_timed_out={result.timed_out}")
    error.add_note(f"command_stdout_bytes={len(result.stdout)}")
    error.add_note(f"command_stderr_bytes={len(result.stderr)}")


class CommandRunner(Protocol):
    def run(
        self,
        command: Sequence[str],
        *,
        timeout_seconds: float,
        input_bytes: bytes | None = None,
    ) -> CommandResult: ...


def _ensure_directory(path: Path) -> None:
    """Prepare a disposable scratch directory."""

    try:
        path.mkdir(parents=True, exist_ok=True)
    except OSError as error:
        raise HostedAdapterError("SCRATCH_DIRECTORY_UNAVAILABLE") from error
    if not path.is_dir():
        raise HostedAdapterError("SCRATCH_DIRECTORY_UNAVAILABLE")


def _process_group_kwargs() -> dict[str, int | bool]:
    if os.name == "nt":
        creation_flag = getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)
        return {"creationflags": creation_flag} if creation_flag else {}
    return {"start_new_session": True}


def _output_bytes(value: bytes | str | None) -> bytes:
    if isinstance(value, bytes):
        return value
    if isinstance(value, str):
        return value.encode("utf-8", errors="replace")
    return b""


def _merge_output(first: bytes, second: bytes) -> bytes:
    if not first:
        return second
    if not second or second.startswith(first) or first.startswith(second):
        return second if len(second) >= len(first) else first
    return first + second


def _append_error_notes(error: BaseException, errors: Sequence[tuple[str, BaseException]]) -> None:
    for label, secondary in errors:
        code = getattr(secondary, "reason_code", None)
        if not isinstance(code, str):
            code = getattr(secondary, "code", None)
        suffix = f" code={code}" if isinstance(code, str) else ""
        error.add_note(f"{label}_error={type(secondary).__name__}{suffix}")


def _terminate_process(
    process: subprocess.Popen[bytes],
    *,
    deadline: float,
    stage: str,
) -> tuple[bytes, ...]:
    """Terminate only the command boundary and return native diagnostics."""

    if os.name == "nt":
        cleanup = terminate_windows_job(process, deadline=deadline, stage=stage)
        if not cleanup.process_tree_proven:
            error = HostedAdapterError("PROCESS_TERMINATION_FAILED")
            if cleanup.diagnostics:
                error.add_note("; ".join(cleanup.diagnostics))
            raise error
        return tuple(cleanup.diagnostics)
    if os.name == "posix":
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        return ()
    process.kill()
    return ()


def _close_process_boundary(
    process: subprocess.Popen[bytes],
    *,
    deadline: float,
    stage: str,
) -> tuple[bytes, ...]:
    if os.name != "nt" or windows_job_for(process) is None:
        return ()
    diagnostics = close_windows_job(process, stage=stage, deadline=deadline)
    if bool(diagnostics):
        error = HostedAdapterError("PROCESS_CLEANUP_FAILED")
        if diagnostics:
            error.add_note("; ".join(diagnostics))
        raise error
    return tuple(diagnostics)


def _drain_after_termination(
    process: subprocess.Popen[bytes],
    *,
    deadline: float,
    stdout: bytes,
    stderr: bytes,
    input_bytes: bytes | None = None,
) -> tuple[bytes, bytes, list[tuple[str, BaseException]]]:
    errors: list[tuple[str, BaseException]] = []
    try:
        if input_bytes is None:
            recovered_stdout, recovered_stderr = process.communicate(
                timeout=_remaining_until(deadline)
            )
        else:
            recovered_stdout, recovered_stderr = process.communicate(
                input=input_bytes,
                timeout=_remaining_until(deadline),
            )
        return (
            _merge_output(stdout, recovered_stdout),
            _merge_output(stderr, recovered_stderr),
            errors,
        )
    except subprocess.TimeoutExpired as error:
        errors.append(("output_drain", error))
        return (
            _merge_output(stdout, _output_bytes(error.output)),
            _merge_output(stderr, _output_bytes(error.stderr)),
            errors,
        )
    except OSError as error:
        errors.append(("output_drain", error))
        return (
            _merge_output(stdout, _output_bytes(getattr(error, "stdout", None))),
            _merge_output(stderr, _output_bytes(getattr(error, "stderr", None))),
            errors,
        )


class SubprocessRunner:
    """Run one command with one deadline and ephemeral in-memory output."""

    def __init__(
        self,
        raw_directory: Path,
        *,
        environment: Mapping[str, str] | None = None,
    ) -> None:
        self.raw_directory = raw_directory
        _ensure_directory(raw_directory)
        self.environment = dict(os.environ)
        if environment is not None:
            self.environment.update(environment)

    def run(
        self,
        command: Sequence[str],
        *,
        timeout_seconds: float,
        input_bytes: bytes | None = None,
    ) -> CommandResult:
        if input_bytes is not None and not isinstance(input_bytes, bytes):
            raise HostedAdapterError("INVALID_INPUT_BYTES")
        if timeout_seconds <= 0 or any(
            not isinstance(item, str) or not item for item in command
        ):
            raise HostedAdapterError("INVALID_COMMAND")
        argv = tuple(command)
        deadline = time.monotonic() + timeout_seconds
        process: subprocess.Popen[bytes] | None = None
        try:
            process = popen_with_windows_job(
                subprocess.Popen,
                list(argv),
                stdin=subprocess.PIPE if input_bytes is not None else None,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=self.environment,
                stage="hosted-cli-command",
                deadline=deadline,
                **_process_group_kwargs(),
            )
            try:
                if input_bytes is None:
                    stdout, stderr = process.communicate(
                        timeout=_remaining_until(deadline)
                    )
                else:
                    stdout, stderr = process.communicate(
                        input=input_bytes,
                        timeout=_remaining_until(deadline),
                    )
            except subprocess.TimeoutExpired as timeout_error:
                stdout = _output_bytes(timeout_error.output)
                stderr = _output_bytes(timeout_error.stderr)
                errors: list[tuple[str, BaseException]] = []
                cleanup_deadline = time.monotonic() + _PROCESS_CLEANUP_SECONDS
                try:
                    _terminate_process(
                        process,
                        deadline=cleanup_deadline,
                        stage="hosted-cli-command-timeout",
                    )
                except BaseException as error:
                    errors.append(("termination", error))
                stdout, stderr, drain_errors = _drain_after_termination(
                    process,
                    deadline=cleanup_deadline,
                    stdout=stdout,
                    stderr=stderr,
                )
                errors.extend(drain_errors)
                try:
                    _close_process_boundary(
                        process,
                        deadline=cleanup_deadline,
                        stage="hosted-cli-command-timeout",
                    )
                except BaseException as error:
                    errors.append(("close", error))
                result = CommandResult(argv, 124, stdout, stderr, timed_out=True)
                primary = HostedAdapterError("COMMAND_TIMEOUT")
                _append_error_notes(primary, errors)
                _append_command_result_notes(primary, result)
                raise primary from None
            try:
                _close_process_boundary(
                    process,
                    deadline=deadline,
                    stage="hosted-cli-command",
                )
            except BaseException as error:
                result = CommandResult(argv, process.returncode, stdout, stderr)
                primary = HostedAdapterError("PROCESS_CLEANUP_FAILED")
                _append_error_notes(primary, (("close", error),))
                _append_command_result_notes(primary, result)
                raise primary from None
            result = CommandResult(argv, process.returncode, stdout, stderr)
            return result
        except WindowsJobError as error:
            result = CommandResult(argv, -1, error.stdout, error.stderr)
            primary = HostedAdapterError("PROCESS_CONTAINMENT_UNAVAILABLE")
            _append_command_result_notes(primary, result)
            raise primary from None
        except OSError as error:
            stdout = _output_bytes(getattr(error, "stdout", None))
            stderr = _output_bytes(getattr(error, "stderr", None))
            result = CommandResult(argv, -1, stdout, stderr)
            primary = HostedAdapterError("COMMAND_UNAVAILABLE")
            _append_command_result_notes(primary, result)
            raise primary from None

    def run_detached(
        self, command: Sequence[str], *, timeout_seconds: float
    ) -> CommandResult:
        """Run a service launcher whose child intentionally outlives it."""

        if timeout_seconds <= 0 or any(
            not isinstance(item, str) or not item for item in command
        ):
            raise HostedAdapterError("INVALID_COMMAND")
        argv = tuple(command)
        deadline = time.monotonic() + timeout_seconds
        process: subprocess.Popen[bytes] | None = None
        try:
            process = subprocess.Popen(
                list(argv),
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=self.environment,
                **_process_group_kwargs(),
            )
            try:
                stdout, stderr = process.communicate(
                    timeout=_remaining_until(deadline)
                )
            except subprocess.TimeoutExpired as timeout_error:
                stdout = _output_bytes(timeout_error.output)
                stderr = _output_bytes(timeout_error.stderr)
                errors: list[tuple[str, BaseException]] = []
                cleanup_deadline = time.monotonic() + _PROCESS_CLEANUP_SECONDS
                try:
                    _terminate_process(
                        process,
                        deadline=cleanup_deadline,
                        stage="hosted-detached-command-timeout",
                    )
                except BaseException as error:
                    errors.append(("termination", error))
                stdout, stderr, drain_errors = _drain_after_termination(
                    process,
                    deadline=cleanup_deadline,
                    stdout=stdout,
                    stderr=stderr,
                )
                errors.extend(drain_errors)
                result = CommandResult(argv, 124, stdout, stderr, timed_out=True)
                primary = HostedAdapterError("COMMAND_TIMEOUT")
                _append_error_notes(primary, errors)
                _append_command_result_notes(primary, result)
                raise primary from None
            result = CommandResult(argv, process.returncode, stdout, stderr)
            return result
        except OSError as error:
            stdout = _output_bytes(getattr(error, "stdout", None))
            stderr = _output_bytes(getattr(error, "stderr", None))
            result = CommandResult(argv, -1, stdout, stderr)
            primary = HostedAdapterError("COMMAND_UNAVAILABLE")
            _append_command_result_notes(primary, result)
            raise primary from None

def _require_scratch_file(path: Path) -> None:
    if not path.is_file():
        raise HostedAdapterError("SCRATCH_FILE_UNAVAILABLE")


def _discard_scratch_file(path: Path) -> None:
    """Delete a temporary helper stream or script after its operation."""
    _require_scratch_file(path)
    try:
        path.unlink()
    except OSError:
        raise HostedAdapterError("SCRATCH_CLEANUP_FAILED") from None


def _allocate_scratch_path(
    directory: Path,
    stem: str,
    suffix: str,
) -> Path:
    _ensure_directory(directory)
    path = directory / f"{stem}{suffix}"
    sequence = 2
    while path.exists():
        path = directory / f"{stem}-{sequence}{suffix}"
        sequence += 1
    path.write_bytes(b"")
    return path


def _https_endpoint(value: str | None, name: str, *, allow_query: bool = False) -> str:
    if not isinstance(value, str) or not value:
        raise HostedAdapterError(f"{name.upper()}_INVALID")
    try:
        parsed = urlparse(value)
        hostname = parsed.hostname
        parsed.port
    except ValueError as error:
        raise HostedAdapterError(f"{name.upper()}_INVALID") from error
    if (
        parsed.scheme != "https"
        or not hostname
        or parsed.username is not None
        or parsed.password is not None
        or (parsed.query and not allow_query)
        or parsed.fragment
        or ("?" in value and not allow_query)
        or "#" in value
        or any(character.isspace() for character in value)
    ):
        raise HostedAdapterError(f"{name.upper()}_INVALID")
    return value


_SERVICE_PID = re.compile(r"^[1-9][0-9]*$")


class HostedServiceProcessController:
    """Common bounded process-loss lifecycle for hosted desktop adapters.

    Platform adapters provide only the OS command vectors for terminating,
    probing, launching, and checking readiness. The deadline accounting,
    PID-file handling, and complete command-runner diagnostics stay shared so
    every hosted desktop lane has the same failure and cleanup semantics.
    """

    def __init__(
        self,
        *,
        pid: int,
        binary: Path,
        pid_file: Path | None,
        identity_file: Path | None = None,
        runner: CommandRunner,
        raw_directory: Path,
    ) -> None:
        if pid <= 0 or not _SERVICE_PID.fullmatch(str(pid)):
            raise HostedAdapterError("SERVICE_PID_INVALID")
        if not binary.is_file():
            raise HostedAdapterError("SERVICE_BINARY_UNAVAILABLE")
        _ensure_directory(raw_directory)
        self.pid = pid
        self.binary = binary
        self.pid_file = pid_file
        self.runner = runner
        self.raw_directory = raw_directory
        self._restart_number = 0
        # Keep process identity explicit so cleanup proves the same process
        # before signalling it.
        self.identity_file = identity_file
        # Platform controllers fill this with the native start-time token for
        # the host-owned process.  It is deliberately cleared before a
        # replacement launch so a partially launched, unowned process can
        # never be finalized as if it were the original service.
        self._initial_identity: object | None = None

    @staticmethod
    def _remaining(deadline: float, failure: str) -> float:
        value = deadline - time.monotonic()
        if value <= 0:
            raise ScenarioExecutionError(failure)
        return value

    def _probe(
        self,
        command: Sequence[str],
        timeout: float,
        failure: str,
    ) -> CommandResult:
        try:
            result = self.runner.run(command, timeout_seconds=timeout)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out:
            raise ScenarioExecutionError(failure)
        return result

    def _checked(
        self,
        command: Sequence[str],
        timeout: float,
        failure: str,
    ) -> CommandResult:
        result = self._probe(command, timeout, failure)
        if result.returncode != 0:
            raise ScenarioExecutionError(failure)
        return result

    def _write_pid(self, pid: int) -> None:
        if self.pid_file is None:
            return
        if _SERVICE_PID.fullmatch(str(pid)) is None:
            raise ScenarioExecutionError("SERVICE_RESTART_PID_INVALID")
        self.pid_file.parent.mkdir(parents=True, exist_ok=True)
        self.pid_file.write_text(f"{pid}\n", encoding="ascii")

    def _invalidate_identity_file(self) -> None:
        """Remove predecessor authority before launching a replacement.

        A replacement may fail after launch but before its native identity is
        persisted.  Publishing an invalid marker first prevents an outer
        always-run cleanup from treating the predecessor's sidecar as the
        replacement authority; platform workflows then rediscover or fail
        closed.
        """

        if self.identity_file is None:
            return
        try:
            self.identity_file.parent.mkdir(parents=True, exist_ok=True)
            self.identity_file.write_text("pending\n", encoding="ascii")
        except OSError as error:
            raise ScenarioExecutionError("SERVICE_IDENTITY_PERSIST_FAILED") from error

    def _wait_dead(self, deadline: float) -> None:
        while time.monotonic() < deadline:
            if not self._alive(self._remaining(deadline, "SERVICE_LOSS_TIMEOUT")):
                return
            time.sleep(min(0.5, max(0.0, deadline - time.monotonic())))
        raise ScenarioExecutionError("SERVICE_DID_NOT_EXIT")

    def restart_after_loss(self, timeout: float) -> None:
        """Kill the recorded candidate and restart that exact binary."""
        if timeout <= 0:
            raise ScenarioExecutionError("PROCESS_LOSS_TIMEOUT")
        deadline = time.monotonic() + timeout
        verify = getattr(self, "_verify_candidate_pid", None)
        if not callable(verify):
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        # Process-loss termination is destructive.  Re-prove the original
        # PID's identity immediately before the platform-specific kill so a
        # reused PID cannot terminate an unrelated host process.
        verify(self._remaining(deadline, "SERVICE_PID_PROBE_FAILED"))
        self._terminate(
            self._remaining(deadline, "SERVICE_KILL_FAILED"),
            deadline=deadline,
        )
        self._wait_dead(deadline)
        self._start(self._remaining(deadline, "SERVICE_RESTART_TIMEOUT"))

    def finalize_restarted_service(
        self, timeout_seconds: float, *, deadline: float | None = None
    ) -> None:
        """Stop only the replacement service owned by this controller.

        The initial service belongs to the host/workflow and is intentionally
        left alone.  Once process-loss has created a replacement, subclasses'
        platform-specific ``_terminate`` implementations provide the exact
        identity/tree proof required before anything is killed.
        """

        if timeout_seconds <= 0:
            raise ScenarioExecutionError("INVALID_FINALIZE_TIMEOUT")
        if self._restart_number == 0:
            return
        # A launcher may have produced a PID but failed before readiness and
        # native identity validation.  That process is not controller-owned;
        # never finalize it as a replacement based on a path or PID alone.
        if getattr(self, "_replacement_identity", None) is None:
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        if deadline is None:
            deadline = time.monotonic() + timeout_seconds
        elif deadline <= time.monotonic():
            raise ScenarioExecutionError("SERVICE_FINALIZE_TIMEOUT")
        alive = self._alive(self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT"))
        if alive:
            verify = getattr(self, "_verify_candidate_pid", None)
            if not callable(verify):
                raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
            verify(self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT"))
        # A missing root is not proof that its descendants are gone.  Let the
        # platform tree finalizer establish that fact (or fail closed) before
        # returning success.
        self._terminate(
            self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT"),
            deadline=deadline,
        )

    def _alive(self, timeout: float) -> bool:
        raise NotImplementedError

    def _terminate(self, timeout: float, *, deadline: float | None = None) -> None:
        raise NotImplementedError

    def _start(self, timeout: float) -> None:
        raise NotImplementedError


def _profile_file(path: Path) -> None:
    if not path.is_file():
        raise HostedAdapterError("PROFILE_INVALID")


def _executable_file(path: Path, code: str) -> None:
    if not isinstance(path, Path):
        raise HostedAdapterError(code)
    if not path.is_file() or not os.access(path, os.X_OK):
        raise HostedAdapterError(code)


class RoutingProofMixin:
    """Share routing-proof meaning while platforms provide native commands."""

    def _initialize_routing_proof(
        self, *, enabled: bool, network_interface: str | None
    ) -> None:
        self._routing_proof_enabled = enabled
        self._routing_firewall_active = False
        self._routing_probe_address: str | None = None
        self._routing_probe_host: str | None = None
        self._routing_probe_port = 443
        self._routing_probe_interface: str | None = None
        self.network_interface = network_interface

    def prepare_native_connect(self, timeout: float) -> None:
        """Prepare observations before a native UI Connect action.

        The UI adapter cannot use ``execute(connect)`` because that would
        issue a second, CLI-driven connection.  Routing-enabled hosted
        adapters therefore prepare the same firewall/baseline proof used by
        their CLI connect path, while ordinary adapters retain the simple
        external-IP baseline behavior from ``HostedCLIAdapter``.
        """
        if self._routing_proof_enabled:
            self._prepare_routing_probe(timeout)
            return
        super().prepare_native_connect(timeout)

    def _probe_command(self, timeout: float):
        if (
            self.identity_url is None
            or self._routing_probe_host is None
            or self._routing_probe_address is None
        ):
            raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE")
        try:
            return self.runner.run(
                (
                    "curl", "--fail", "--location", "--show-error",
                    "--connect-timeout", str(
                        max(
                            1,
                            min(
                                _ROUTING_PROBE_CONNECT_TIMEOUT_SECONDS,
                                int(timeout),
                            ),
                        )
                    ),
                    "--max-time", str(max(1, int(timeout))),
                    "--resolve",
                    f"{self._routing_probe_host}:{self._routing_probe_port}:"
                    f"{self._routing_probe_address}",
                    self.identity_url,
                ),
                timeout_seconds=timeout,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error

    def _probe_external_ip(self, timeout: float) -> str:
        result = self._probe_command(timeout)
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("ROUTING_PROBE_FAILED")
            _append_command_result_notes(failure, result)
            raise failure
        return _parse_external_ip(result.stdout_text)

    def _remove_routing_firewall(self, timeout: float) -> None:
        if not self._routing_proof_enabled:
            return
        self._firewall("remove", timeout)
        self._routing_firewall_active = False

    def _prepare_routing_probe(self, timeout: float) -> None:
        deadline = time.monotonic() + timeout
        # One idempotent removal also recovers a rule left by an interrupted
        # prior diagnostic before this adapter instance existed.
        self._remove_routing_firewall(
            self._remaining(deadline, "ROUTING_FIREWALL_REMOVE_FAILED")
        )
        self._resolve_routing_probe(
            self._remaining(deadline, "ROUTING_PROBE_RESOLUTION_FAILED")
        )
        baseline = self._probe_external_ip(
            self._remaining(deadline, "ROUTING_PROBE_DIRECT_UNAVAILABLE")
        )
        self._baseline_ip = baseline
        self._tunneled_ips.clear()
        self._firewall(
            "block", self._remaining(deadline, "ROUTING_FIREWALL_BLOCK_FAILED")
        )
        self._routing_firewall_active = True
        blocked = self._probe_command(
            self._remaining(deadline, "ROUTING_FIREWALL_PROBE_TIMEOUT")
        )
        if not blocked.timed_out and blocked.returncode == 0:
            self._remove_routing_firewall(
                self._remaining(deadline, "ROUTING_FIREWALL_REMOVE_FAILED")
            )
            raise ScenarioExecutionError("ROUTING_FIREWALL_INEFFECTIVE")

    def _connect_with_routing_probe(self, timeout: float) -> None:
        deadline = time.monotonic() + timeout
        self._prepare_routing_probe(
            self._remaining(deadline, "CONNECT_TIMEOUT")
        )
        try:
            self._command(
                ("connect-profile", str(self.profile), self._selected_connection_index()),
                self._remaining(deadline, "CONNECT_TIMEOUT"),
                "CONNECT_FAILED",
            )
            self._emit_progress(
                "native-state", kind="vpn-session", state="connect-issued"
            )
        except Exception as error:
            try:
                self._remove_routing_firewall(
                    self._remaining(deadline, "ROUTING_FIREWALL_REMOVE_FAILED")
                )
            except (HostedAdapterError, ScenarioExecutionError) as cleanup_error:
                error.add_note(
                    f"routing_firewall_cleanup_error={type(cleanup_error).__name__}"
                )
            raise

    def _reconnect_with_routing_probe(self, timeout: float) -> dict[str, object]:
        deadline = time.monotonic() + timeout
        self._prepare_routing_probe(
            self._remaining(deadline, "RECONNECT_TIMEOUT")
        )
        try:
            self._command(
                ("connect-profile", str(self.profile), self._selected_connection_index()),
                self._remaining(deadline, "RECONNECT_TIMEOUT"),
                "RECONNECT_CONNECT_FAILED",
            )
            if not self._connected(self._remaining(deadline, "RECONNECT_TIMEOUT")):
                raise ScenarioExecutionError("RECONNECT_NOT_ESTABLISHED")
        except Exception as error:
            try:
                self._remove_routing_firewall(
                    self._remaining(deadline, "ROUTING_FIREWALL_REMOVE_FAILED")
                )
            except (HostedAdapterError, ScenarioExecutionError) as cleanup_error:
                error.add_note(
                    f"routing_firewall_cleanup_error={type(cleanup_error).__name__}"
                )
            raise
        return {
            "restart_verified": True,
            "reconnect_completed": time.monotonic() <= deadline,
        }

    def _routing_verified(self, timeout: float) -> bool:
        if not self._routing_proof_enabled:
            return super()._routing_verified(timeout)
        if not self._routing_firewall_active or self.network_interface is None:
            return False
        deadline = time.monotonic() + timeout
        interface = self._route_interface(
            self._remaining(deadline, "ROUTING_INTERFACE_UNAVAILABLE")
        )
        if interface == self.network_interface:
            return False
        before = self._interface_counters(
            interface, self._remaining(deadline, "ROUTING_COUNTERS_UNAVAILABLE")
        )
        current = self._probe_external_ip(
            self._remaining(deadline, "ROUTING_PROBE_FAILED")
        )
        after = self._interface_counters(
            interface, self._remaining(deadline, "ROUTING_COUNTERS_UNAVAILABLE")
        )
        self._tunneled_ips.add(current)
        self._routing_probe_interface = interface
        verified = after[0] > before[0] and after[1] > before[1]
        self._emit_progress(
            "native-state",
            kind="vpn-route",
            interface=interface,
            uplink=self.network_interface,
            rx_delta=after[0] - before[0],
            tx_delta=after[1] - before[1],
            verified=verified,
        )
        return verified

    def _cleanup_verified(self, timeout: float) -> bool:
        if not self._routing_proof_enabled:
            return super()._cleanup_verified(timeout)
        deadline = time.monotonic() + timeout
        result = self._command(
            ("status", "--json"),
            self._remaining(deadline, "CLEANUP_STATUS_FAILED"),
            "CLEANUP_STATUS_FAILED",
        )
        try:
            disconnected = json.loads(result.stdout_text).get("state") == "Disconnected"
        except (AttributeError, TypeError, ValueError) as error:
            raise ScenarioExecutionError("CLEANUP_STATUS_INVALID") from error
        if not disconnected:
            return False
        if self._routing_firewall_active:
            self._remove_routing_firewall(
                self._remaining(deadline, "ROUTING_FIREWALL_REMOVE_FAILED")
            )
        if self._routing_probe_address is None:
            return True
        self._probe_external_ip(
            self._remaining(deadline, "ROUTING_PROBE_RESTORE_FAILED")
        )
        return self._route_interface(
            self._remaining(deadline, "ROUTING_INTERFACE_UNAVAILABLE")
        ) == self.network_interface

    def reset(self, timeout_seconds: float = 30.0) -> None:
        try:
            super().reset(timeout_seconds)
        finally:
            if self._routing_firewall_active:
                self._remove_routing_firewall(max(0.1, timeout_seconds))

    def finalize(
        self, timeout_seconds: float = 30.0, *, deadline: float | None = None
    ) -> None:
        super().finalize(timeout_seconds, deadline=deadline)
        if self._routing_firewall_active:
            available = timeout_seconds
            if deadline is not None:
                available = min(available, max(0.1, deadline - time.monotonic()))
            self._remove_routing_firewall(available)


class HostedCLIAdapter:
    """Drive one installed DobbyVPN CLI through semantic adapter operations."""

    adapter_id = "hosted-cli"
    adapter_version = "v1"

    def __init__(
        self,
        *,
        cli: Path,
        profile: Path,
        runner: CommandRunner,
        identity_url: str | None = None,
        download_url: str | None = None,
        upload_url: str | None = None,
        local_mode: bool = False,
    ) -> None:
        if not cli.is_file():
            raise HostedAdapterError("CLI_UNAVAILABLE")
        _profile_file(profile)
        if (download_url is None) != (upload_url is None):
            raise HostedAdapterError("THROUGHPUT_URL_PAIR_REQUIRED")
        if download_url is not None:
            _https_endpoint(download_url, "download_url", allow_query=True)
        if upload_url is not None:
            _https_endpoint(upload_url, "upload_url", allow_query=True)
        self.cli = cli
        self.profile = profile
        self.runner = runner
        self.identity_url = (
            _https_endpoint(identity_url, "identity_url")
            if identity_url is not None
            else None
        )
        self.download_url = download_url
        self.upload_url = upload_url
        self.local_mode = local_mode
        self._baseline_ip: str | None = None
        self._tunneled_ips: set[str] = set()
        self._connections: tuple[ConnectionIdentity, ...] = ()
        self._selected_connection: ConnectionIdentity | None = None
        self._progress_sink: Callable[[str, dict[str, object]], None] | None = None

    def set_progress_sink(
        self, sink: Callable[[str, dict[str, object]], None]
    ) -> None:
        self._progress_sink = sink

    def _emit_progress(self, event: str, **fields: object) -> None:
        if self._progress_sink is not None:
            self._progress_sink(event, fields)

    def _operation_progress_fields(
        self, scenario_id: str, step: ScenarioStep
    ) -> dict[str, object]:
        selected = self._selected_connection
        return {
            "connection_index": selected.index if selected is not None else -1,
            "operation": step.operation,
            "operation_id": step.id,
            "protocol": selected.protocol if selected is not None else "NONE",
            "scenario": scenario_id,
        }

    def discover_connections(self, timeout_seconds: float = 30.0) -> tuple[ConnectionIdentity, ...]:
        """Return the complete profile inventory reported by DobbyVPN."""

        connections = self._read_connections(timeout_seconds)
        self._connections = connections
        self._selected_connection = None
        return connections

    def select_connection(self, connection: ConnectionIdentity) -> None:
        if connection not in self._connections:
            raise HostedAdapterError("CONNECTION_NOT_DISCOVERED")
        self._selected_connection = connection

    def _selected_connection_index(self) -> str:
        if self._selected_connection is None:
            raise ScenarioExecutionError("CONNECTION_NOT_SELECTED")
        return str(self._selected_connection.index)

    def _read_connections(self, timeout: float) -> tuple[ConnectionIdentity, ...]:
        result = self._command(
            ("profile-inventory", str(self.profile)),
            timeout,
            "CONNECTION_INVENTORY_REJECTED",
        )
        try:
            value = json.loads(result.stdout_text)
            if not isinstance(value, dict) or "profiles" not in value:
                raise ValueError
            profiles = value["profiles"]
            if not isinstance(profiles, list) or not profiles:
                raise ValueError
            connections = tuple(
                ConnectionIdentity(index=item["index"], protocol=item["protocol"])
                for item in profiles
                if isinstance(item, dict) and {"index", "protocol"}.issubset(item)
            )
            if len(connections) != len(profiles):
                raise ValueError
            if [connection.index for connection in connections] != list(range(len(connections))):
                raise ValueError
        except (KeyError, TypeError, ValueError) as error:
            raise ScenarioExecutionError("CONNECTION_INVENTORY_INVALID") from error
        return connections

    @property
    def capabilities(self) -> frozenset[Capability]:
        result = {
            Capability.CONFIGURE,
            Capability.CONNECT,
            Capability.TUNNEL_INTERFACE,
            Capability.ROUTING_IDENTITY,
            Capability.DISCONNECT,
            Capability.RECONNECT,
            Capability.RESOURCE_CLEANUP,
        }
        if self.download_url is not None and self.upload_url is not None:
            result.add(Capability.TRAFFIC_MEASUREMENT)
        return frozenset(result)

    @property
    def capability_unavailable_reasons(self) -> dict[Capability, str]:
        """Explain capabilities that this hosted shell cannot safely provide."""

        return {
            Capability.NETWORK_TRANSITION: "HOSTED_RUNNER_UPLINK_TOGGLE_UNSUPPORTED",
            Capability.PROCESS_LOSS: "HOSTED_SERVICE_CONTROL_UNAVAILABLE",
        }

    def execute(self, step: ScenarioStep) -> dict[str, object]:
        operation = step.operation
        timeout = float(step.timeout_seconds)
        if operation == "configure":
            return {"configured": self._configure(timeout)}
        if operation == "connect":
            deadline = time.monotonic() + timeout
            self._capture_baseline(self._remaining(deadline, "CONNECT_TIMEOUT"))
            self._command(("connect-profile", str(self.profile), self._selected_connection_index()),
                          self._remaining(deadline, "CONNECT_TIMEOUT"), "CONNECT_FAILED")
            self._emit_progress(
                "native-state",
                kind="vpn-session",
                state="connect-issued",
            )
            return {}
        if operation == "observe_tunnel":
            key = "second_tunnel_interface" if step.id == "second-tunnel" else "tunnel_interface"
            connected = self._connected(timeout)
            self._emit_progress(
                "native-state",
                kind="vpn-interface",
                state="present" if connected else "absent",
            )
            return {key: connected}
        if operation == "observe_routing_identity":
            verified = self._wait_for_routing_verified(timeout)
            key = "second_routing_verified" if step.id == "second-routing" else "routing_verified"
            return {key: verified}
        if operation == "measure_stability":
            return self._stability(timeout)
        if operation == "measure_throughput":
            return self._throughput(timeout)
        if operation == "disconnect":
            clean = self._disconnect_clean(timeout)
            self._emit_progress(
                "native-state",
                kind="vpn-session",
                state="disconnected" if clean else "disconnect-unclean",
            )
            key = "final_disconnect_clean" if step.id == "final-disconnect" else "disconnect_clean"
            return {key: clean}
        if operation == "reconnect":
            return self._reconnect(timeout)
        if operation == "inspect_cleanup":
            verified = self._cleanup_verified(timeout)
            self._emit_progress(
                "native-state",
                kind="resource-cleanup",
                state="verified" if verified else "unverified",
            )
            return {"cleanup_verified": verified}
        raise ScenarioExecutionError("UNSUPPORTED_OPERATION")

    def execute_scenario(self, scenario) -> dict[str, object]:
        """Run the canonical scenario boundary shared by every adapter.

        Platform subclasses override ``execute`` only for operations that need
        platform-specific control.  Keeping the scenario loop here means the
        functional engine has one adapter API while preserving those narrow
        platform seams.
        """
        observations: dict[str, object] = {}
        for step in scenario.steps:
            fields = self._operation_progress_fields(scenario.id, step)
            self._emit_progress("operation-start", **fields)
            try:
                result = self.execute(step)
            except Exception as error:
                code = getattr(error, "reason_code", getattr(error, "code", None))
                self._emit_progress(
                    "operation-error",
                    **fields,
                    code=code if isinstance(code, str) else type(error).__name__,
                )
                raise
            if not isinstance(result, dict):
                raise ScenarioExecutionError("ADAPTER_RESULT_INVALID")
            observations.update(result)
            self._emit_progress("operation-finish", **fields, observations=result)
        return observations

    @staticmethod
    def _remaining(deadline: float, failure: str) -> float:
        value = deadline - time.monotonic()
        if value <= 0:
            raise ScenarioExecutionError(failure)
        return value

    def reset(self, timeout_seconds: float = 30.0) -> None:
        """Stop a scenario's session within one total timeout window."""
        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_RESET_TIMEOUT")
        deadline = time.monotonic() + timeout_seconds

        def remaining() -> float:
            value = deadline - time.monotonic()
            if value <= 0:
                raise HostedAdapterError("RESET_TIMEOUT")
            return value

        try:
            if self._connected(remaining()):
                self._command(("disconnect",), remaining(), "RESET_FAILED")
            if self._baseline_ip is not None and not self._cleanup_verified(remaining()):
                raise HostedAdapterError("RESET_CLEANUP_UNVERIFIED")
        finally:
            self._baseline_ip = None
            self._tunneled_ips.clear()

    def finalize(
        self, timeout_seconds: float = 30.0, *, deadline: float | None = None
    ) -> None:
        """Release adapter-owned run resources inside the canonical lane."""

        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_FINALIZE_TIMEOUT")
        if deadline is not None and deadline <= time.monotonic():
            raise HostedAdapterError("SERVICE_FINALIZE_TIMEOUT")

    def _command(self, arguments: Sequence[str], timeout: float, failure: str) -> CommandResult:
        command = (str(self.cli), *arguments)
        try:
            result = self.runner.run(command, timeout_seconds=timeout)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out:
            failure_error = ScenarioExecutionError("COMMAND_TIMEOUT")
            _append_command_result_notes(failure_error, result)
            raise failure_error
        if result.returncode != 0:
            failure_error = ScenarioExecutionError(failure)
            _append_command_result_notes(failure_error, result)
            raise failure_error
        return result

    def _configure(self, timeout: float) -> bool:
        selected = self._selected_connection
        if selected is None or selected not in self._connections:
            return False
        result = self._command(
            ("check-config", str(self.profile)), timeout, "CONFIGURE_REJECTED"
        )
        match = re.search(r"(?:^|\s)profiles=([1-9][0-9]*)\s", result.stdout_text)
        return match is not None and int(match.group(1)) == len(self._connections)

    def _capture_baseline(self, timeout: float) -> None:
        self._tunneled_ips.clear()
        self._baseline_ip = self._external_ip(timeout)

    def prepare_native_connect(self, timeout: float) -> None:
        """Capture the observation baseline before a native UI Connect."""
        self._capture_baseline(timeout)

    def restart_service_for_native_ui(self, timeout: float) -> dict[str, object]:
        """Restart a desktop service without issuing a CLI session command.

        The native-window full lane must prove that its visible recovery action
        reconnects the VPN.  Calling ``_process_loss`` here would restart the
        service and then use ``connect-profile`` as a hidden recovery shortcut.
        Keep the destructive service operation separate; the native UI owns
        configuration and Connect, while the caller performs the independent
        tunnel, routing, stability, and throughput observations afterward.
        """
        service = getattr(self, "service", None)
        if service is None:
            raise CapabilityUnavailable()
        deadline = time.monotonic() + timeout
        self._emit_progress("native-state", kind="vpn-service", state="loss-started")
        service.restart_after_loss(self._remaining(deadline, "PROCESS_LOSS_TIMEOUT"))
        self._emit_progress("native-state", kind="vpn-service", state="restarted")
        return {"process_loss_verified": True}

    def _routing_identity_changed(self, timeout: float) -> bool:
        current = self._external_ip(timeout)
        changed = self._baseline_ip is not None and current != self._baseline_ip
        if changed:
            self._tunneled_ips.add(current)
        return changed

    def _wait_for_routing_identity_changed(self, timeout: float) -> bool:
        """Observe eventual route convergence within the caller's step bound."""

        deadline = time.monotonic() + timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            if self._routing_identity_changed(remaining):
                return True
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            time.sleep(min(_ROUTE_CONVERGENCE_POLL_SECONDS, remaining))

    def _wait_for_routing_verified(self, timeout: float) -> bool:
        """Prove routed traffic using this platform's available observation."""

        deadline = time.monotonic() + timeout
        last_error: ScenarioExecutionError | None = None
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                if last_error is not None:
                    raise last_error
                return False
            try:
                if self._routing_verified(remaining):
                    return True
                last_error = None
            except ScenarioExecutionError as error:
                if error.reason_code not in _TRANSIENT_ROUTING_FAILURES:
                    raise
                last_error = error
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                if last_error is not None:
                    raise last_error
                return False
            time.sleep(min(_ROUTE_CONVERGENCE_POLL_SECONDS, remaining))

    def _routing_verified(self, timeout: float) -> bool:
        return self._routing_identity_changed(timeout)

    def _connected(self, timeout: float) -> bool:
        result = self._command(("status", "--json"), timeout, "STATUS_FAILED")
        try:
            value = json.loads(result.stdout_text)
        except (TypeError, ValueError) as error:
            raise ScenarioExecutionError("STATUS_INVALID") from error
        return isinstance(value, dict) and value.get("state") == "Connected"

    def _external_ip(self, timeout: float) -> str:
        if self.identity_url is None:
            result = self._command(("external-ip",), timeout, "EXTERNAL_IDENTITY_FAILED")
        else:
            try:
                result = self.runner.run(
                    (
                        "curl", "--fail", "--location", "--show-error",
                        "--max-time", str(max(1, int(timeout))), self.identity_url,
                    ),
                    timeout_seconds=timeout,
                )
            except HostedAdapterError as error:
                raise ScenarioExecutionError(error.code) from error
            if result.timed_out or result.returncode != 0:
                failure = ScenarioExecutionError("EXTERNAL_IDENTITY_FAILED")
                _append_command_result_notes(failure, result)
                raise failure
        return _parse_external_ip(result.stdout_text)

    def _stability(self, timeout: float) -> dict[str, object]:
        deadline = time.monotonic() + timeout
        completed_samples = 0
        if self.download_url is None:
            raise ScenarioExecutionError("STABILITY_ENDPOINT_UNAVAILABLE")
        for index in range(STABILITY_SAMPLE_COUNT):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            connected = self._connected(
                self._remaining(deadline, "STABILITY_TIMEOUT")
            )
            if not connected:
                return {
                    "stability_verified": False,
                    "stability_sample_count": completed_samples,
                    "stability_sample_interval_seconds": STABILITY_SAMPLE_INTERVAL_SECONDS,
                }
            try:
                # The status probe remains a useful service-state observation,
                # but stability is only established by a successful HTTPS
                # request through the candidate's current route.  Reuse the
                # throughput request helper so status, transfer bytes, and
                # bounded request failures have one implementation.
                self._curl_metric(
                    self.download_url,
                    self._remaining(deadline, "STABILITY_TIMEOUT"),
                    upload=False,
                )
            except ScenarioExecutionError as error:
                error.add_note(
                    f"stability_sample={index + 1}/{STABILITY_SAMPLE_COUNT}"
                )
                raise
            completed_samples += 1
            if index + 1 < STABILITY_SAMPLE_COUNT:
                time.sleep(
                    min(
                        STABILITY_SAMPLE_INTERVAL_SECONDS,
                        max(0.0, deadline - time.monotonic()),
                    )
                )
        return {
            "stability_verified": completed_samples == STABILITY_SAMPLE_COUNT
            and time.monotonic() <= deadline,
            "stability_sample_count": completed_samples,
            "stability_sample_interval_seconds": STABILITY_SAMPLE_INTERVAL_SECONDS,
        }

    def _throughput(self, timeout: float) -> dict[str, object]:
        if self.download_url is None or self.upload_url is None:
            raise ScenarioExecutionError("THROUGHPUT_UNAVAILABLE")
        deadline = time.monotonic() + timeout
        download = self._curl_metric(self.download_url, self._remaining(deadline, "THROUGHPUT_TIMEOUT"), upload=False)
        upload = self._curl_metric(self.upload_url, self._remaining(deadline, "THROUGHPUT_TIMEOUT"), upload=True)
        return {
            "latency_ms": download[0],
            "download_mbps": download[1],
            "upload_mbps": upload[1],
        }

    def _reconnect(self, timeout: float) -> dict[str, object]:
        """Establish and verify the next session generation within one bound.

        The scenario owns the explicit disconnect before this operation and
        the final disconnect/cleanup after its independent observations. This
        operation composes only the public connect and status commands; it
        does not hide a second disconnect or cleanup inside the observation.
        """
        deadline = time.monotonic() + timeout

        def remaining() -> float:
            value = deadline - time.monotonic()
            if value <= 0:
                raise ScenarioExecutionError("RECONNECT_TIMEOUT")
            return value

        self._command(
            ("connect-profile", str(self.profile), self._selected_connection_index()),
            remaining(),
            "RECONNECT_CONNECT_FAILED",
        )
        if not self._connected(remaining()):
            raise ScenarioExecutionError("RECONNECT_NOT_ESTABLISHED")
        return {
            "restart_verified": True,
            "reconnect_completed": time.monotonic() <= deadline,
        }

    def _curl_metric(self, url: str, timeout: float, *, upload: bool) -> tuple[float, float]:
        if upload:
            payload = self._upload_payload()
            transfer_args = (
                "--request", "POST", "--upload-file", str(payload),
                "--output", os.devnull,
                "--write-out", "%{time_total}\t%{size_upload}\t%{http_code}",
            )
        else:
            transfer_args = (
                "--output", os.devnull,
                "--write-out", "%{time_total}\t%{size_download}\t%{http_code}",
            )
        try:
            result = self.runner.run(
                (
                    "curl", "--location", "--show-error",
                    "--max-time", str(max(1, int(timeout))), *transfer_args, url,
                ),
                timeout_seconds=timeout,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            raise ScenarioExecutionError("THROUGHPUT_FAILED")
        try:
            seconds_text, bytes_text, status = result.stdout_text.strip().split("\t", 2)
            seconds = float(seconds_text)
            bytes_count = float(bytes_text)
        except (ValueError, TypeError) as error:
            raise ScenarioExecutionError("THROUGHPUT_INVALID") from error
        if not status.startswith("2"):
            raise ScenarioExecutionError("MEASUREMENT_SERVICE_UNAVAILABLE")
        if seconds <= 0 or bytes_count <= 0:
            raise ScenarioExecutionError("THROUGHPUT_INVALID")
        return seconds * 1000.0, bytes_count * 8.0 / seconds / 1_000_000.0

    def _upload_payload(self) -> Path:
        raw_directory = getattr(self.runner, "raw_directory", None)
        if not isinstance(raw_directory, Path):
            raise ScenarioExecutionError("THROUGHPUT_UPLOAD_UNAVAILABLE")
        try:
            _ensure_directory(raw_directory)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        payload = raw_directory / "traffic-upload.bin"
        if not payload.exists():
            with payload.open("wb") as output:
                output.write(b"\0" * (1024 * 1024))
        return payload

    def _disconnect_clean(self, timeout: float) -> bool:
        deadline = time.monotonic() + timeout

        def remaining() -> float:
            value = deadline - time.monotonic()
            if value <= 0:
                raise ScenarioExecutionError("DISCONNECT_TIMEOUT")
            return value

        self._command(("disconnect",), remaining(), "DISCONNECT_FAILED")
        return self._cleanup_verified(remaining())

    def _cleanup_verified(self, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        if self._baseline_ip is None or not self._tunneled_ips:
            return False
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            result = self._command(
                ("status", "--json"), remaining, "CLEANUP_STATUS_FAILED"
            )
            try:
                value = json.loads(result.stdout_text)
            except (TypeError, ValueError) as error:
                raise ScenarioExecutionError("CLEANUP_STATUS_INVALID") from error
            if isinstance(value, dict) and value.get("state") == "Disconnected":
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                if self._external_ip(remaining) not in self._tunneled_ips:
                    return True
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            delay = min(_ROUTE_CONVERGENCE_POLL_SECONDS, remaining)
            time.sleep(delay)


__all__ = [
    "CommandResult",
    "HostedAdapterError",
    "HostedCLIAdapter",
    "HostedServiceProcessController",
    "SubprocessRunner",
]
