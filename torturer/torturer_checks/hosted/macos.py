"""macOS hosted adapter using DobbyVPN's public CLI."""

from __future__ import annotations

import ipaddress
import os
from pathlib import Path
import re
import time
import traceback
from typing import NamedTuple
from urllib.parse import urlsplit

from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.engine import CapabilityUnavailable, ScenarioExecutionError
from torturer_contract.functional.scenarios import ScenarioStep

from .cli import (
    CommandRunner,
    HostedAdapterError,
    HostedCLIAdapter,
    HostedServiceProcessController,
    RoutingProofMixin,
    _allocate_evidence_path,
    _append_command_result_notes,
    _call_with_deadline,
)


_MACOS_LAUNCHD_LABEL = "system/com.dobby.vpnservice"
_MACOS_LAUNCHD_PRINT = (
    "launchctl", "print", _MACOS_LAUNCHD_LABEL
)
_MACOS_LAUNCHD_KILL = (
    "sudo", "-n", "launchctl", "kill", "SIGKILL", _MACOS_LAUNCHD_LABEL
)
_MACOS_INTERFACE = re.compile(r"^[A-Za-z0-9._-]+$")
_MACOS_SOCKET_PROBE = """import socket
import sys

def _emit_probe_error(error):
    exception_type = type(error).__name__
    if not exception_type.isidentifier():
        exception_type = "Exception"
    errno = getattr(error, "errno", 0)
    if isinstance(errno, bool) or not isinstance(errno, int):
        errno = 0
    print("service_control_probe_error")
    print(
        "service_control_probe_exception type=%s detail=socket-connect errno=%d exception=%r" %
        (exception_type, errno, error),
        file=sys.stderr,
    )

try:
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(0.5)
        connection.connect(sys.argv[1])
except Exception as error:
    _emit_probe_error(error)
    raise SystemExit(1)
"""


class _MacOSLaunchdJob(NamedTuple):
    program: str
    pid: int | None
    runs: int


def _parse_macos_launchd_job(stdout: str) -> _MacOSLaunchdJob:
    """Parse launchd's complete job record used for ownership and restarts."""

    values: dict[str, str] = {}
    for line in stdout.splitlines():
        key, separator, value = line.strip().partition("=")
        key = key.strip()
        if not separator or key not in {"program", "pid", "runs"}:
            continue
        if key in values:
            raise ValueError(f"launchd record contains duplicate {key}")
        values[key] = value.strip()
    program = values.get("program")
    runs_text = values.get("runs")
    if not program or runs_text is None:
        raise ValueError("launchd record is missing program or runs")
    if not re.fullmatch(r"[1-9][0-9]*", runs_text):
        raise ValueError("launchd record has invalid runs")
    pid_text = values.get("pid")
    if pid_text is not None and not re.fullmatch(r"[1-9][0-9]*", pid_text):
        raise ValueError("launchd record has invalid pid")
    return _MacOSLaunchdJob(
        program=program,
        pid=int(pid_text) if pid_text is not None else None,
        runs=int(runs_text),
    )


def _default_control_socket() -> Path:
    configured = os.environ.get("DOBBYVPN_CONTROL_SOCKET")
    if configured:
        return Path(configured)
    return Path("/var/run/dobbyvpn/control.sock")


class MacOSServiceProcessController(HostedServiceProcessController):
    """Control the installed launchd service and prove its Unix socket."""

    def __init__(
        self,
        *,
        pid: int,
        binary: Path,
        pid_file: Path | None,
        runner: CommandRunner,
        raw_directory: Path,
        control_socket: Path,
    ) -> None:
        super().__init__(
            pid=pid,
            binary=binary,
            pid_file=pid_file,
            identity_file=None,
            runner=runner,
            raw_directory=raw_directory,
        )
        self.control_socket = control_socket
        self._initial_job: _MacOSLaunchdJob | None = None
        self._replacement_job: _MacOSLaunchdJob | None = None
        self._write_pid(pid)
        try:
            identity_deadline = time.monotonic() + 5.0
            job = self._launchd_job(
                self._remaining(identity_deadline, "SERVICE_LAUNCHD_PROBE_FAILED")
            )
            self._validate_job(job, pid)
            if job.pid != pid:
                raise ScenarioExecutionError("SERVICE_LAUNCHD_PID_MISMATCH")
            self._initial_job = job
        except ScenarioExecutionError as error:
            raise HostedAdapterError(error.reason_code) from error

    def _alive(self, timeout: float) -> bool:
        job = self._launchd_job(timeout)
        self._validate_job(job, self.pid)
        expected = self._replacement_job or self._initial_job
        return job.pid == self.pid and (expected is None or job.runs == expected.runs)

    def _launchd_job(self, timeout: float) -> _MacOSLaunchdJob:
        try:
            result = self.runner.run(_MACOS_LAUNCHD_PRINT, timeout_seconds=timeout)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("SERVICE_LAUNCHD_PROBE_FAILED")
            _append_command_result_notes(failure, result)
            raise failure
        try:
            return _parse_macos_launchd_job(result.stdout_text)
        except ValueError as error:
            failure = ScenarioExecutionError("SERVICE_LAUNCHD_PROBE_FAILED")
            failure.add_note(f"launchd_parse_error={error}")
            _append_command_result_notes(failure, result)
            raise failure from error

    def _validate_job(self, job: _MacOSLaunchdJob, pid: int) -> None:
        self._validate_program(job)
        if job.pid is not None and job.pid != pid:
            raise ScenarioExecutionError("SERVICE_PID_NOT_CANDIDATE")

    def _validate_program(self, job: _MacOSLaunchdJob) -> None:
        expected = os.path.normcase(str(self.binary.resolve()))
        if os.path.normcase(job.program) != expected:
            raise ScenarioExecutionError("SERVICE_PID_NOT_CANDIDATE")

    def _verify_candidate_pid(self, timeout: float) -> _MacOSLaunchdJob:
        job = self._launchd_job(timeout)
        expected = self._replacement_job or self._initial_job
        if expected is None or job.pid != self.pid or job.runs != expected.runs:
            raise ScenarioExecutionError("SERVICE_PID_NOT_CANDIDATE")
        self._validate_job(job, self.pid)
        return job

    def _terminate(self, timeout: float, *, deadline: float | None = None) -> None:
        if deadline is None:
            deadline = time.monotonic() + timeout
        expected = self._replacement_job or self._initial_job
        if expected is None:
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        self._verify_candidate_pid(
            self._remaining(deadline, "SERVICE_PID_PROBE_FAILED")
        )
        try:
            result = self.runner.run(
                _MACOS_LAUNCHD_KILL,
                timeout_seconds=self._remaining(deadline, "SERVICE_KILL_FAILED"),
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("SERVICE_KILL_FAILED")
            _append_command_result_notes(failure, result)
            raise failure

    def _wait_dead(self, deadline: float) -> None:
        expected = self._replacement_job or self._initial_job
        if expected is None:
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        while time.monotonic() < deadline:
            current = self._launchd_job(
                self._remaining(deadline, "SERVICE_LOSS_TIMEOUT")
            )
            self._validate_program(current)
            if current.pid != expected.pid or current.runs != expected.runs:
                return
            time.sleep(min(0.05, max(0.0, deadline - time.monotonic())))
        raise ScenarioExecutionError("SERVICE_DID_NOT_EXIT")

    def _control_ready(self, timeout: float) -> bool:
        result = self._probe(
            (
                "python3",
                "-c",
                _MACOS_SOCKET_PROBE,
                str(self.control_socket),
            ),
            timeout,
            "SERVICE_CONTROL_PROBE_FAILED",
        )
        return result.returncode == 0

    def _start(self, timeout: float) -> None:
        self._restart_number += 1
        deadline = time.monotonic() + timeout
        predecessor = self._replacement_job or self._initial_job
        if predecessor is None:
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        self._replacement_job = None
        while time.monotonic() < deadline:
            job = self._launchd_job(
                self._remaining(deadline, "SERVICE_RESTART_TIMEOUT")
            )
            self._validate_program(job)
            if job.pid is not None and job.runs > predecessor.runs:
                self.pid = job.pid
                self._replacement_job = job
                self._write_pid(self.pid)
                if self._control_ready(
                    self._remaining(deadline, "SERVICE_RESTART_TIMEOUT")
                ):
                    return
            time.sleep(min(0.5, max(0.0, deadline - time.monotonic())))
        raise ScenarioExecutionError("SERVICE_RESTART_NOT_READY")

    def finalize_restarted_service(
        self, timeout_seconds: float, *, deadline: float | None = None
    ) -> None:
        """Verify the launchd-owned replacement without stopping it."""

        if timeout_seconds <= 0:
            raise ScenarioExecutionError("INVALID_FINALIZE_TIMEOUT")
        if self._restart_number == 0:
            return
        if self._replacement_job is None:
            raise ScenarioExecutionError("SERVICE_PID_PROBE_FAILED")
        if deadline is None:
            deadline = time.monotonic() + timeout_seconds
        else:
            deadline = min(deadline, time.monotonic() + timeout_seconds)
        current = self._launchd_job(
            self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT")
        )
        self._validate_job(current, self.pid)
        if current != self._replacement_job:
            raise ScenarioExecutionError("SERVICE_PID_NOT_CANDIDATE")
        if not self._control_ready(
            self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT")
        ):
            raise ScenarioExecutionError("SERVICE_CONTROL_PROBE_FAILED")


class MacOSHostedAdapter(RoutingProofMixin, HostedCLIAdapter):
    adapter_id = "hosted-macos-cli"
    adapter_version = "v3"

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
        service_pid: int | None = None,
        service_binary: Path | None = None,
        service_pid_file: Path | None = None,
        service_identity_file: Path | None = None,
        service_socket: Path | None = None,
        network_interface: str | None = None,
        routing_firewall_helper: Path | None = None,
        network_transition_helper: Path | None = None,
    ) -> None:
        super().__init__(
            cli=cli,
            profile=profile,
            runner=runner,
            identity_url=identity_url,
            download_url=download_url,
            upload_url=upload_url,
            local_mode=local_mode,
        )
        if any(value is not None for value in (service_pid, service_binary)):
            if service_pid is None or service_binary is None:
                raise HostedAdapterError("SERVICE_CONTROL_INCOMPLETE")
            raw_directory = getattr(runner, "raw_directory", None)
            if not isinstance(raw_directory, Path):
                raise HostedAdapterError("SERVICE_EVIDENCE_UNAVAILABLE")
            control_socket = service_socket or _default_control_socket()
            self.service: MacOSServiceProcessController | None = MacOSServiceProcessController(
                pid=service_pid,
                binary=service_binary,
                pid_file=service_pid_file if local_mode else None,
                runner=runner,
                raw_directory=raw_directory,
                control_socket=control_socket,
            )
        else:
            self.service = None
        if network_interface is not None and _MACOS_INTERFACE.fullmatch(network_interface) is None:
            raise HostedAdapterError("NETWORK_INTERFACE_INVALID")
        self.routing_firewall_helper = routing_firewall_helper
        self._initialize_routing_proof(
            enabled=routing_firewall_helper is not None,
            network_interface=network_interface,
        )
        self.network_transition_helper = network_transition_helper
        raw_directory = getattr(runner, "raw_directory", None)
        if network_transition_helper is not None and not isinstance(raw_directory, Path):
            raise HostedAdapterError("NETWORK_TRANSITION_EVIDENCE_UNAVAILABLE")
        self.raw_directory = raw_directory

    @property
    def capabilities(self) -> frozenset[Capability]:
        result = set(super().capabilities)
        if self.service is not None:
            result.add(Capability.PROCESS_LOSS)
        if (
            self.local_mode
            and self.network_interface is not None
            and self.network_transition_helper is not None
        ):
            result.add(Capability.NETWORK_TRANSITION)
        return frozenset(result)

    @property
    def capability_unavailable_reasons(self) -> dict[Capability, str]:
        reasons = dict(super().capability_unavailable_reasons)
        if Capability.NETWORK_TRANSITION in self.capabilities:
            reasons.pop(Capability.NETWORK_TRANSITION, None)
        else:
            reasons[Capability.NETWORK_TRANSITION] = "HOSTED_MACOS_UPLINK_TOGGLE_UNSUPPORTED"
        return reasons

    def finalize(
        self, timeout_seconds: float = 30.0, *, deadline: float | None = None
    ) -> None:
        super().finalize(timeout_seconds, deadline=deadline)
        if self.service is not None:
            now = time.monotonic()
            if deadline is None:
                deadline = now + timeout_seconds
            else:
                if deadline <= now:
                    raise ScenarioExecutionError("SERVICE_FINALIZE_TIMEOUT")
                # Keep the adapter's own finalization budget independent of
                # the larger outer hosted-lane deadline.
                deadline = min(deadline, now + timeout_seconds)
            _call_with_deadline(
                self.service.finalize_restarted_service,
                self._remaining(deadline, "SERVICE_FINALIZE_TIMEOUT"),
                deadline,
            )

    def execute(self, step: ScenarioStep) -> dict[str, object]:
        if self._routing_proof_enabled and step.operation == "connect":
            self._connect_with_routing_probe(float(step.timeout_seconds))
            return {}
        if self._routing_proof_enabled and step.operation == "reconnect":
            return self._reconnect_with_routing_probe(float(step.timeout_seconds))
        if step.operation == "process_loss":
            self._emit_progress("native-state", kind="vpn-service", state="loss-started")
            result = self._process_loss(float(step.timeout_seconds))
            self._emit_progress("native-state", kind="vpn-service", state="recovered")
            return result
        if step.operation == "network_transition":
            self._emit_progress("native-state", kind="physical-uplink", state="loss-started")
            result = self._network_transition(float(step.timeout_seconds))
            self._emit_progress("native-state", kind="physical-uplink", state="recovered")
            return result
        return super().execute(step)

    def _resolve_routing_probe(self, timeout: float) -> None:
        if self.identity_url is None:
            raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE")
        try:
            endpoint = urlsplit(self.identity_url)
            port = endpoint.port or 443
        except ValueError as error:
            raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE") from error
        if endpoint.scheme != "https" or endpoint.hostname is None or port != 443:
            raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE")
        try:
            result = self.runner.run(
                ("/usr/bin/dscacheutil", "-q", "host", "-a", "name", endpoint.hostname),
                timeout_seconds=timeout,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("ROUTING_PROBE_RESOLUTION_FAILED")
            _append_command_result_notes(failure, result)
            raise failure
        addresses: set[str] = set()
        for line in result.stdout_text.splitlines():
            key, separator, value = line.partition(":")
            if not separator or key.strip() != "ip_address":
                continue
            try:
                candidate = ipaddress.ip_address(value.strip())
            except ValueError:
                continue
            if candidate.version == 4 and candidate.is_global:
                addresses.add(str(candidate))
        if not addresses:
            raise ScenarioExecutionError("ROUTING_PROBE_RESOLUTION_FAILED")
        self._routing_probe_host = endpoint.hostname
        self._routing_probe_port = port
        self._routing_probe_address = sorted(addresses)[0]

    def _firewall(self, action: str, timeout: float) -> None:
        if self.routing_firewall_helper is None:
            raise ScenarioExecutionError("ROUTING_FIREWALL_HELPER_UNAVAILABLE")
        arguments = [
            "sudo", "-n", str(self.routing_firewall_helper), f"routing-{action}"
        ]
        if action == "block":
            if self.network_interface is None or self._routing_probe_address is None:
                raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE")
            arguments.extend((self.network_interface, self._routing_probe_address))
        elif action != "remove":
            raise ScenarioExecutionError("ROUTING_FIREWALL_ACTION_INVALID")
        try:
            result = self.runner.run(arguments, timeout_seconds=timeout)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError(
                f"ROUTING_FIREWALL_{action.upper()}_FAILED"
            )
            _append_command_result_notes(failure, result)
            raise failure

    def _route_interface(self, timeout: float) -> str:
        if self._routing_probe_address is None:
            raise ScenarioExecutionError("ROUTING_PROBE_UNAVAILABLE")
        try:
            result = self.runner.run(
                ("/sbin/route", "-n", "get", self._routing_probe_address),
                timeout_seconds=timeout,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("ROUTING_INTERFACE_UNAVAILABLE")
            _append_command_result_notes(failure, result)
            raise failure
        interfaces = [
            value.strip()
            for line in result.stdout_text.splitlines()
            for key, separator, value in (line.partition(":"),)
            if separator and key.strip() == "interface"
        ]
        if len(interfaces) != 1 or _MACOS_INTERFACE.fullmatch(interfaces[0]) is None:
            raise ScenarioExecutionError("ROUTING_INTERFACE_UNAVAILABLE")
        return interfaces[0]

    def _interface_counters(self, interface: str, timeout: float) -> tuple[int, int]:
        if _MACOS_INTERFACE.fullmatch(interface) is None:
            raise ScenarioExecutionError("ROUTING_INTERFACE_INVALID")
        try:
            result = self.runner.run(
                ("/usr/sbin/netstat", "-bI", interface),
                timeout_seconds=timeout,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            failure = ScenarioExecutionError("ROUTING_COUNTERS_UNAVAILABLE")
            _append_command_result_notes(failure, result)
            raise failure
        link_rows = [
            line.split()
            for line in result.stdout_text.splitlines()
            if line.split()[:1] == [interface]
            and len(line.split()) >= 3
            and line.split()[2].startswith("<Link#")
        ]
        try:
            if len(link_rows) != 1 or len(link_rows[0]) != 10:
                raise ValueError
            received = int(link_rows[0][5])
            sent = int(link_rows[0][8])
        except (IndexError, TypeError, ValueError) as error:
            raise ScenarioExecutionError("ROUTING_COUNTERS_UNAVAILABLE") from error
        if received < 0 or sent < 0:
            raise ScenarioExecutionError("ROUTING_COUNTERS_UNAVAILABLE")
        return received, sent

    def _network_command(
        self, command: tuple[str, ...], timeout: float, failure: str
    ):
        try:
            result = self.runner.run(command, timeout_seconds=timeout)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out or result.returncode != 0:
            error = ScenarioExecutionError(failure)
            _append_command_result_notes(error, result)
            raise error
        return result

    def _interface_is_up(self, timeout: float) -> bool:
        if self.network_interface is None:
            raise CapabilityUnavailable()
        result = self._network_command(
            ("/sbin/ifconfig", self.network_interface),
            timeout,
            "NETWORK_INTERFACE_PROBE_FAILED",
        )
        first_line = result.stdout_text.splitlines()[0] if result.stdout_text.splitlines() else ""
        match = re.search(r"\bflags=[0-9]+<([^>]*)>", first_line)
        if match is None:
            raise ScenarioExecutionError("NETWORK_INTERFACE_STATE_INVALID")
        return "UP" in match.group(1).split(",")

    def _retain_network_repair_evidence(self, paths: tuple[Path, Path]) -> None:
        for path, kind in zip(paths, ("macos-network-repair-stdout", "macos-network-repair-stderr")):
            self.runner.retain_external_evidence(path, evidence_kind=kind)

    def _network_transition(self, timeout: float) -> dict[str, object]:
        if (
            not self.local_mode
            or self.network_interface is None
            or self.network_transition_helper is None
            or not isinstance(self.raw_directory, Path)
        ):
            raise CapabilityUnavailable()
        deadline = time.monotonic() + timeout
        state = _allocate_evidence_path(
            self.raw_directory, "macos-network-repair", ".state"
        )
        stdout = _allocate_evidence_path(
            self.raw_directory, "macos-network-repair", ".stdout.raw.log",
        )
        stderr = _allocate_evidence_path(
            self.raw_directory, "macos-network-repair", ".stderr.raw.log",
        )
        primary: Exception | None = None
        try:
            self._network_command(
                (
                    "sudo", "-n", str(self.network_transition_helper), "arm",
                    self.network_interface, str(state), str(stdout), str(stderr),
                ),
                self._remaining(deadline, "NETWORK_DOWN_FAILED"),
                "NETWORK_DOWN_FAILED",
            )
            if self._interface_is_up(
                self._remaining(deadline, "NETWORK_LOSS_NOT_OBSERVED")
            ):
                raise ScenarioExecutionError("NETWORK_LOSS_NOT_OBSERVED")
        except Exception as error:
            primary = error
        finally:
            if state.stat().st_size:
                try:
                    self._network_command(
                        (
                            "sudo", "-n", str(self.network_transition_helper),
                            "finish", str(state),
                        ),
                        self._remaining(deadline, "NETWORK_UP_FAILED"),
                        "NETWORK_UP_FAILED",
                    )
                except Exception as error:
                    if primary is None:
                        primary = error
                    else:
                        primary.add_note(
                            "Network uplink restoration also failed:\n"
                            + "".join(traceback.format_exception(error)).rstrip()
                        )
            try:
                self._retain_network_repair_evidence((stdout, stderr))
            except Exception as error:
                if primary is None:
                    primary = error
                else:
                    primary.add_note(
                        "Network repair evidence retention also failed:\n"
                        + "".join(traceback.format_exception(error)).rstrip()
                    )
        if primary is not None:
            raise primary
        while not self._interface_is_up(
            self._remaining(deadline, "NETWORK_UP_FAILED")
        ):
            time.sleep(min(0.1, self._remaining(deadline, "NETWORK_UP_FAILED")))
        if not self._connected(self._remaining(deadline, "NETWORK_TUNNEL_NOT_RESTORED")):
            raise ScenarioExecutionError("NETWORK_TUNNEL_NOT_RESTORED")
        if not self._wait_for_routing_verified(
            self._remaining(deadline, "NETWORK_ROUTING_NOT_RESTORED")
        ):
            raise ScenarioExecutionError("NETWORK_ROUTING_NOT_RESTORED")
        return {"network_transition_verified": True}

    def _process_loss(self, timeout: float) -> dict[str, object]:
        if self.service is None:
            raise CapabilityUnavailable()
        deadline = time.monotonic() + timeout
        self.service.restart_after_loss(self._remaining(deadline, "PROCESS_LOSS_TIMEOUT"))
        self._command(
            ("connect-profile", str(self.profile), self._selected_connection_index()),
            self._remaining(deadline, "PROCESS_LOSS_TIMEOUT"),
            "PROCESS_LOSS_CONNECT_FAILED",
        )
        if not self._connected(self._remaining(deadline, "PROCESS_LOSS_TIMEOUT")):
            raise ScenarioExecutionError("PROCESS_LOSS_NOT_RECOVERED")
        if not self._wait_for_routing_verified(
            self._remaining(deadline, "PROCESS_LOSS_TIMEOUT")
        ):
            raise ScenarioExecutionError("PROCESS_LOSS_ROUTING_NOT_RECOVERED")
        return {"process_loss_verified": True}


__all__ = ["MacOSHostedAdapter", "MacOSServiceProcessController"]
