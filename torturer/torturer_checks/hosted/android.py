"""Hosted Android adapter for the canonical profile-session seam.

DobbyVPN owns Android session state and cleanup; Torturer owns the
test set, assertions, result values, and runner-local evidence. Most scenarios
use one instrumentation invocation; process loss uses two so the app process is
actually absent between the initial connection and the recovery session.
"""

from __future__ import annotations

import ipaddress
import json
import math
from pathlib import Path
import re
import shlex
import threading
import time
import traceback
from typing import Callable, Mapping
import uuid

from torturer_contract.functional.android_observation import (
    AndroidObservationError,
    AndroidProfileObservation,
)
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import (
    ScenarioDefinition,
    ScenarioStep,
    get_scenario,
)

from .cli import (
    CommandResult,
    CommandRunner,
    HostedAdapterError,
    _append_command_result_notes,
    _ensure_directory,
    _executable_file,
    _https_endpoint,
    _parse_external_ip,
    _profile_file,
)


_PACKAGE_NAME = "com.dobby.vpn"
_MAIN_ACTIVITY = "com.dobby.vpn/com.dobby.feature.main.ui.MainActivity"
_APP_DATA = "/data/user/0/com.dobby.vpn"
_APP_FILES = "/data/user/0/com.dobby.vpn/files"
_INSTRUMENTATION_COMPONENT = "com.dobby.vpn.test/com.dobby.TestApplicationRunner"
_INSTRUMENTATION_CLASS = (
    "com.dobby.feature.vpn_service.AndroidHostedProfileInstrumentationTest"
)
_SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
_ALLOWED_OPERATIONS = {
    "configure",
    "connect",
    "observe_tunnel",
    "observe_routing_identity",
    "measure_stability",
    "measure_throughput",
    "disconnect",
    "reconnect",
    "inspect_cleanup",
    "network_transition",
}
_EXTERNAL_OPERATIONS = frozenset({
    "network_transition",
    "observe_routing_identity",
})
_CORE_CAPABILITIES = frozenset(
    {
        Capability.CONFIGURE,
        Capability.CONNECT,
        Capability.TUNNEL_INTERFACE,
        Capability.ROUTING_IDENTITY,
        Capability.TRAFFIC_MEASUREMENT,
        Capability.DISCONNECT,
        Capability.RECONNECT,
        Capability.RESOURCE_CLEANUP,
    }
)
_CLEANUP_RESERVE_FRACTION = 0.125
_MIN_CLEANUP_RESERVE_SECONDS = 10.0
_MAX_CLEANUP_RESERVE_SECONDS = 30.0
_CLEANUP_COMMAND_MAX_SECONDS = 15.0
_ROUTING_CLEANUP_SECONDS = 5.0
_ANDROID_INTERFACE = re.compile(r"^[A-Za-z0-9_.:-]{1,32}$")
_JUNIT_SUCCESS = re.compile(rb"(?m)^[ \t]*OK \(1 test[ \t]*\)$")
_JUNIT_FAILURES = re.compile(rb"(?m)^[ \t]*FAILURES!!![ \t]*$")


def _instrumentation_succeeded(result: CommandResult) -> bool:
    """Require both Android instrumentation completion and JUnit success."""

    return (
        result.returncode == 0
        and not result.timed_out
        and result.stdout.rstrip().endswith(b"INSTRUMENTATION_CODE: -1")
        and _JUNIT_SUCCESS.search(result.stdout) is not None
        and _JUNIT_FAILURES.search(result.stdout) is None
    )


def _instrumentation_failure(result: CommandResult) -> ScenarioExecutionError:
    success_marker_present = result.stdout.rstrip().endswith(
        b"INSTRUMENTATION_CODE: -1"
    )
    junit_summary_present = _JUNIT_SUCCESS.search(result.stdout) is not None
    failures_marker_present = _JUNIT_FAILURES.search(result.stdout) is not None
    failure = ScenarioExecutionError(
        "Android instrumentation failed: "
        f"returncode={result.returncode}, timed_out={result.timed_out}, "
        f"success_marker_present={success_marker_present}, "
        f"junit_summary_present={junit_summary_present}, "
        f"failures_marker_present={failures_marker_present}"
    )
    _append_command_result_notes(failure, result)
    return failure


def _remaining(deadline: float, code: str) -> float:
    value = deadline - time.monotonic()
    if value <= 0:
        raise ScenarioExecutionError(code)
    return value


def _scenario_deadlines(
    started: float, scenario_seconds: float
) -> tuple[float, float]:
    """Return work and cleanup deadlines within one lane.

    The canonical engine measures the complete adapter call against the
    scenario bound, so cleanup is reserved inside that bound. Torturer retains
    the required VPN logs separately; unrelated device diagnostics are not a
    qualification requirement.
    """
    if scenario_seconds <= 0:
        raise ScenarioExecutionError("SCENARIO_TIMEOUT_INVALID")
    cleanup_reserve = min(
        _MAX_CLEANUP_RESERVE_SECONDS,
        max(
            _MIN_CLEANUP_RESERVE_SECONDS,
            scenario_seconds * _CLEANUP_RESERVE_FRACTION,
        ),
    )
    if cleanup_reserve >= scenario_seconds:
        # There must still be a positive work window. This only applies to a
        # malformed future scenario whose bound is too short to qualify.
        raise ScenarioExecutionError("SCENARIO_TIMEOUT_INVALID")
    work_deadline = started + scenario_seconds - cleanup_reserve
    cleanup_deadline = work_deadline + cleanup_reserve
    return work_deadline, cleanup_deadline


def _cleanup_timeout(deadline: float) -> float:
    """Bound each cleanup command while retaining the verification attempt."""
    return min(
        _remaining(deadline, "ANDROID_CLEANUP_TIMEOUT"),
        _CLEANUP_COMMAND_MAX_SECONDS,
    )


def _observation_error_code(error: AndroidObservationError) -> str:
    detail = str(error)
    if "source_sha" in detail:
        return "ANDROID_OBSERVATION_SOURCE_INVALID"
    if "connection" in detail:
        return "ANDROID_OBSERVATION_CONNECTIONS_INVALID"
    if "unexpected shape" in detail:
        return "ANDROID_OBSERVATION_SHAPE_INVALID"
    if "identity" in detail or "platform" in detail:
        return "ANDROID_OBSERVATION_IDENTITY_INVALID"
    return "ANDROID_OBSERVATION_VALUES_INVALID"


class AndroidHostedAdapter:
    """Run canonical scenarios through DobbyVPN Android instrumentation."""

    adapter_id = "hosted-android-app"
    adapter_version = "v5"

    def __init__(
        self,
        *,
        runner: CommandRunner,
        profile: Path,
        adb: Path | None = None,
        source_sha: str | None = None,
        identity_url: str | None = None,
        latency_url: str | None = None,
        download_url: str | None = None,
        upload_url: str | None = None,
        **kwargs: object,
    ) -> None:
        if kwargs:
            raise HostedAdapterError("ANDROID_ARGUMENT_UNEXPECTED")
        _profile_file(profile)
        if adb is None:
            raise HostedAdapterError("ANDROID_ADB_UNAVAILABLE")
        _executable_file(adb, "ANDROID_ADB_UNAVAILABLE")
        if source_sha is not None and _SOURCE_SHA.fullmatch(source_sha) is None:
            raise HostedAdapterError("SOURCE_SHA_INVALID")
        endpoint_values = (identity_url, latency_url, download_url, upload_url)
        if not all(value is not None for value in endpoint_values):
            raise HostedAdapterError("ENDPOINTS_REQUIRED")
        self.runner = runner
        self.profile = profile
        self.adb = adb
        self.source_sha = source_sha
        self.identity_url = (
            _https_endpoint(identity_url, "identity_url") if identity_url is not None else None
        )
        self.latency_url = (
            _https_endpoint(latency_url, "latency_url", allow_query=True)
            if latency_url is not None
            else None
        )
        self.download_url = (
            _https_endpoint(download_url, "download_url", allow_query=True)
            if download_url is not None
            else None
        )
        self.upload_url = (
            _https_endpoint(upload_url, "upload_url") if upload_url is not None else None
        )
        self._active_controls: tuple[tuple[str, str, float], ...] = ()
        self._connections: tuple[ConnectionIdentity, ...] = ()
        self._selected_connection: ConnectionIdentity | None = None
        self._validated_physical_interface: str | None = None
        self._last_observation: AndroidProfileObservation | None = None
        self._observed_baseline_ip: str | None = None
        self._observed_tunneled_ips: set[str] = set()
        self._progress_sink: Callable[[str, dict[str, object]], None] | None = None
        self._progress_scenario_id: str | None = None

    def set_progress_sink(
        self, sink: Callable[[str, dict[str, object]], None]
    ) -> None:
        self._progress_sink = sink

    def _emit_progress(self, event: str, **fields: object) -> None:
        if self._progress_sink is not None:
            selected = self._selected_connection
            contextual = dict(fields)
            if self._progress_scenario_id is not None:
                contextual.setdefault("scenario", self._progress_scenario_id)
            if selected is not None:
                contextual.setdefault("connection_index", selected.index)
                contextual.setdefault("protocol", selected.protocol)
            self._progress_sink(event, contextual)

    def discover_connections(
        self, timeout_seconds: float = 30.0
    ) -> tuple[ConnectionIdentity, ...]:
        if timeout_seconds <= 0:
            raise HostedAdapterError("CONNECTION_DISCOVERY_TIMEOUT")
        self._selected_connection = None
        self.execute_scenario(get_scenario("functional.configure"))
        observation = self._last_observation
        if observation is None:
            raise HostedAdapterError("CONNECTION_INVENTORY_INVALID")
        self._connections = observation.connections
        return self._connections

    def select_connection(self, connection: ConnectionIdentity) -> None:
        if connection not in self._connections:
            raise HostedAdapterError("CONNECTION_NOT_DISCOVERED")
        self._selected_connection = connection

    def _validate_observation_identity(
        self, observation: AndroidProfileObservation
    ) -> None:
        if self._connections and observation.connections != self._connections:
            raise ScenarioExecutionError("CONNECTION_INVENTORY_CHANGED")
        if self._selected_connection is not None and (
            observation.connection != self._selected_connection
        ):
            raise ScenarioExecutionError("CONNECTION_IDENTITY_MISMATCH")

    @property
    def capabilities(self) -> frozenset[Capability]:
        return _CORE_CAPABILITIES | frozenset(
            {
                Capability.PROCESS_LOSS,
                Capability.NETWORK_TRANSITION,
            }
        )

    @property
    def capability_unavailable_reasons(self) -> dict[Capability, str]:
        return {}

    def execute_scenario(self, scenario: ScenarioDefinition) -> Mapping[str, object]:
        """Stage one profile and ordered command, then parse observations.

        The exact Release APK is intentionally non-debuggable. Qualification
        uses the root-capable Android ADB owner to stream both payloads into the
        installed app's protected directory. The payloads remain input-only:
        the command vector and retained diagnostics contain no profile bytes.
        """
        self._progress_scenario_id = scenario.id
        self._validated_physical_interface = None
        started = time.monotonic()
        deadline, cleanup_deadline = _scenario_deadlines(
            started, float(scenario.max_duration_seconds)
        )
        device_files: list[str] = []
        execution_error: BaseException | None = None
        try:
            loss_steps = tuple(
                step for step in scenario.steps if step.operation == "process_loss"
            )
            if not loss_steps:
                observation = self._execute_phase(
                    scenario,
                    scenario.steps,
                    deadline,
                    device_files,
                )
                self._validate_observation_identity(observation)
                self._last_observation = observation
                return self._observations(observation)
            if len(loss_steps) != 1:
                raise ScenarioExecutionError("ANDROID_OPERATION_UNSUPPORTED")
            loss_step = loss_steps[0]
            loss_index = scenario.steps.index(loss_step)
            before_loss = scenario.steps[:loss_index]
            after_loss = scenario.steps[loss_index + 1:]
            if not before_loss or not after_loss:
                raise ScenarioExecutionError("ANDROID_OPERATION_UNSUPPORTED")

            initial = self._execute_phase(
                scenario,
                before_loss,
                deadline,
                device_files,
                preserve_active=True,
            )
            self._validate_observation_identity(initial)
            initial_facts = self._observations(initial)
            if not all(
                initial_facts.get(name) is True
                for name in (
                    "configured", "connected", "tunnel_interface",
                    "routing_verified",
                )
            ):
                raise ScenarioExecutionError("ANDROID_PROCESS_LOSS_PRECONDITION")

            recovery_deadline = min(
                deadline,
                time.monotonic() + float(loss_step.timeout_seconds),
            )
            self._stop_product_for_loss(recovery_deadline)
            recovery_steps = tuple(
                ScenarioStep(
                    id=f"recovery-{step.id}",
                    operation=step.operation,
                    timeout_seconds=step.timeout_seconds,
                )
                for step in before_loss
            ) + after_loss
            recovered = self._execute_phase(
                scenario,
                recovery_steps,
                recovery_deadline,
                device_files,
            )
            self._validate_observation_identity(recovered)
            self._last_observation = recovered
            recovered_facts = self._observations(recovered)
            recovery_verified = all(
                recovered_facts.get(name) is True
                for name in (
                    "configured", "connected", "tunnel_interface",
                    "routing_verified",
                )
            )
            return {
                **initial_facts,
                "process_loss_verified": recovery_verified,
                "restart_verified": recovery_verified,
                "reconnect_completed": recovery_verified,
                "second_tunnel_interface": recovered_facts["tunnel_interface"],
                "second_routing_verified": recovered_facts[
                    "routing_verified"
                ],
                "disconnect_clean": recovered_facts["disconnect_clean"],
                "final_disconnect_clean": recovered_facts["final_disconnect_clean"],
                "cleanup_verified": recovered_facts["cleanup_verified"],
            }
        except BaseException as error:
            execution_error = error
            raise
        finally:
            cleanup_error = self._cleanup_device(tuple(device_files), cleanup_deadline)
            self._active_controls = ()
            if cleanup_error is not None:
                if execution_error is not None:
                    execution_error.add_note(
                        "Android cleanup also failed:\n"
                        + "".join(traceback.format_exception(cleanup_error)).rstrip()
                    )
                else:
                    raise cleanup_error

    def _execute_phase(
        self,
        scenario: ScenarioDefinition,
        steps: tuple[ScenarioStep, ...],
        deadline: float,
        device_files: list[str],
        *,
        preserve_active: bool = False,
    ) -> AndroidProfileObservation:
        command_file, profile_name, output_name = self._write_command(
            scenario,
            steps=steps,
            preserve_active=preserve_active,
        )
        device_files.extend(
            (
                profile_name,
                f"{profile_name}.tmp",
                command_file.name,
                f"{command_file.name}.tmp",
                output_name,
            )
        )
        for control_file, _operation, _timeout in self._active_controls:
            device_files.extend(
                (control_file, f"{control_file}.ready", f"{control_file}.tmp")
            )
            if _operation == "network_transition":
                routing_file = f"{control_file}.routing"
                device_files.extend(
                    (routing_file, f"{routing_file}.ready", f"{routing_file}.tmp")
                )
        try:
            profile_bytes = self.profile.read_bytes()
        except OSError as error:
            raise ScenarioExecutionError("ANDROID_PROFILE_STAGE_FAILED") from error
        self._stage_private_file(
            profile_name,
            profile_bytes,
            _remaining(deadline, "ANDROID_PROFILE_STAGE_TIMEOUT"),
            "ANDROID_PROFILE_STAGE_FAILED",
        )
        try:
            command_bytes = command_file.read_bytes()
        except OSError as error:
            raise ScenarioExecutionError("ANDROID_COMMAND_STAGE_FAILED") from error
        self._stage_private_file(
            command_file.name,
            command_bytes,
            _remaining(deadline, "ANDROID_COMMAND_STAGE_TIMEOUT"),
            "ANDROID_COMMAND_STAGE_FAILED",
        )
        instrument = self._run_instrumentation(
            command_file.name, deadline, preserve_active=preserve_active
        )
        if (
            not _instrumentation_succeeded(instrument)
        ):
            raise _instrumentation_failure(instrument)
        output = self._adb(
            ("shell", "-T", "cat", f"{_APP_FILES}/{output_name}"),
            _remaining(deadline, "ANDROID_OBSERVATION_TIMEOUT"),
            "ANDROID_OBSERVATION_UNAVAILABLE",
        )
        try:
            value = json.loads(output.stdout.decode("utf-8"))
            observation = AndroidProfileObservation.from_mapping(
                value, expected_source_sha=self.source_sha
            )
        except UnicodeDecodeError as error:
            raise ScenarioExecutionError("ANDROID_OBSERVATION_ENCODING_INVALID") from error
        except json.JSONDecodeError as error:
            raise ScenarioExecutionError("ANDROID_OBSERVATION_JSON_INVALID") from error
        except AndroidObservationError as error:
            raise ScenarioExecutionError(_observation_error_code(error)) from error
        if observation.error_code is not None:
            raise ScenarioExecutionError(observation.error_code)
        return observation

    @staticmethod
    def _observations(
        observation: AndroidProfileObservation,
    ) -> dict[str, object]:
        try:
            return observation.to_observations()
        except AndroidObservationError as error:
            raise ScenarioExecutionError("ANDROID_OBSERVATION_ERROR") from error

    def _stage_private_file(
        self, name: str, payload: bytes, timeout: float, failure_code: str
    ) -> None:
        destination = f"{_APP_FILES}/{name}"
        temporary = f"{destination}.tmp"
        script = (
            f"test -d {_APP_DATA} && "
            f"owner=$(stat -c %u:%g {_APP_DATA}) && "
            f"mkdir -p {_APP_FILES} && chown \"$owner\" {_APP_FILES} && "
            f"chmod 700 {_APP_FILES} && restorecon {_APP_FILES} && "
            f"cat > {temporary} && chown \"$owner\" {temporary} && "
            f"chmod 600 {temporary} && restorecon {temporary} && "
            f"mv -f {temporary} {destination}"
        )
        self._adb(
            ("shell", "-T", "sh", "-c", shlex.quote(script)),
            timeout,
            failure_code,
            input_bytes=payload,
        )

    def reset(self, timeout_seconds: float = 5.0) -> None:
        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_RESET_TIMEOUT")
        self._adb(
            ("shell", "am", "force-stop", _PACKAGE_NAME),
            timeout_seconds,
            "ANDROID_RESET_FAILED",
        )

    def finalize(
        self, timeout_seconds: float = 30.0, *, deadline: float | None = None
    ) -> None:
        """Satisfy the shared lifecycle contract; no run-scoped process remains."""

        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_FINALIZE_TIMEOUT")
        if deadline is not None and deadline <= time.monotonic():
            raise HostedAdapterError("SERVICE_FINALIZE_TIMEOUT")

    def _run_instrumentation(
        self,
        command_name: str,
        deadline: float,
        *,
        preserve_active: bool = False,
    ) -> CommandResult:
        controls = self._active_controls
        observe_live = bool(controls or self._progress_sink)
        if preserve_active:
            # Android normally force-stops the target when instrumentation
            # finishes; launch production first so --no-restart leaves the
            # intentional process kill to Torturer.
            start = self._adb(
                (
                    "shell", "am", "start", "-W", "-n", _MAIN_ACTIVITY,
                ),
                _remaining(deadline, "ANDROID_APP_START_TIMEOUT"),
                "ANDROID_APP_START_FAILED",
            )
            if b"Status: ok" not in start.stdout or b"Complete" not in start.stdout:
                failure = ScenarioExecutionError("ANDROID_APP_START_FAILED")
                _append_command_result_notes(failure, start)
                raise failure
        arguments = (
            "shell", "am", "instrument", "-w", "-r",
            *(("--no-restart",) if preserve_active else ()),
            "-e", "dobby.real_profile", "1",
            "-e", "dobby.hosted_command_file", command_name, "-e", "class",
            _INSTRUMENTATION_CLASS, _INSTRUMENTATION_COMPONENT,
        )
        if not observe_live:
            return self._adb(
                arguments,
                _remaining(deadline, "ANDROID_INSTRUMENTATION_TIMEOUT"),
                "ANDROID_INSTRUMENTATION_FAILED",
                allow_nonzero=True,
            )

        holder: dict[str, object] = {}

        def invoke() -> None:
            try:
                holder["result"] = self._adb(
                    arguments,
                    _remaining(deadline, "ANDROID_INSTRUMENTATION_TIMEOUT"),
                    "ANDROID_INSTRUMENTATION_FAILED",
                    allow_nonzero=True,
                )
            except Exception as error:  # surfaced on the owner thread below
                holder["error"] = error

        worker = threading.Thread(target=invoke, name="dobbyvpn-android-instrument")
        worker.start()
        self._emit_progress(
            "native-state",
            kind="instrumentation",
            platform="android",
            state="started",
        )
        def check_worker() -> None:
            if worker.is_alive():
                return
            worker_error = holder.get("error")
            worker_result = holder.get("result")
            if isinstance(worker_error, BaseException):
                raise worker_error
            if isinstance(worker_result, CommandResult):
                if _instrumentation_succeeded(worker_result):
                    return
                failure = _instrumentation_failure(worker_result)
                holder["worker_failure"] = failure
                raise failure
            failure = ScenarioExecutionError("ANDROID_CONTROL_WORKER_EXITED")
            holder["worker_failure"] = failure
            raise failure

        primary_error: BaseException | None = None
        for control_file, operation, timeout in controls:
            try:
                initial_ready: Mapping[str, object] | None = None
                if operation == "observe_routing_identity":
                    # The app may spend the preceding configure/consent/
                    # connect steps before publishing this file. That wait
                    # belongs to the instrumentation deadline, not the
                    # operation's own budget.
                    initial_ready = self._routing_ready(
                        control_file, "ready", deadline, abort=check_worker
                    )
                else:
                    self._wait_device_file(
                        control_file + ".ready", deadline, abort=check_worker
                    )
                    initial_ready = {}
                self._complete_external_control(
                    control_file, operation,
                    min(deadline, time.monotonic() + timeout),
                    ready=initial_ready,
                    abort=check_worker,
                )
            except BaseException as error:
                # Keep the external operation failure primary. Drain the
                # worker below so an instrumentation failure can be appended
                # as secondary evidence.
                primary_error = error
                break

        # A control failure may have sent the app's finish message while the
        # instrumentation worker is still returning its command result. Wait
        # for that result through the same deadline passed to _adb. The command
        # runner owns the actual process timeout; once it has expired, this
        # join only drains the already-bounded worker and does not add a new
        # operation timeout or abandon its output.
        while worker.is_alive():
            remaining = deadline - time.monotonic()
            if remaining > 0:
                worker.join(timeout=min(0.5, remaining))
            else:
                worker.join()

        worker_error = holder.get("error")
        worker_result = holder.get("result")
        worker_failure: BaseException | None = None
        if isinstance(worker_error, BaseException):
            worker_failure = worker_error
        elif isinstance(holder.get("worker_failure"), BaseException):
            worker_failure = holder["worker_failure"]
        elif not isinstance(worker_result, CommandResult):
            worker_failure = RuntimeError(
                "Android instrumentation worker returned no command result"
            )
        elif not _instrumentation_succeeded(worker_result):
            worker_failure = _instrumentation_failure(worker_result)
        if worker_failure is not None:
            if primary_error is None:
                primary_error = worker_failure
            elif worker_failure is not primary_error:
                primary_error.add_note(
                    "Android instrumentation worker failure:\n"
                    + "".join(traceback.format_exception(worker_failure)).rstrip()
                )

        if primary_error is not None:
            self._emit_progress(
                "native-state",
                kind="instrumentation",
                platform="android",
                state="failed",
            )
            raise primary_error
        result = worker_result
        assert isinstance(result, CommandResult)
        self._emit_progress(
            "native-state",
            kind="instrumentation",
            platform="android",
            state="completed" if result.returncode == 0 else "failed",
        )
        return result

    def _complete_external_control(
        self,
        control_file: str,
        operation: str,
        deadline: float,
        *,
        ready: Mapping[str, object] | None = None,
        abort: Callable[[], None] | None = None,
    ) -> None:
        if operation == "observe_routing_identity":
            self._routing_proof(
                control_file, deadline, ready=ready, abort=abort
            )
            return
        if ready is None:
            self._wait_device_file(
                control_file + ".ready", deadline, abort=abort
            )
        self._perform_external_control(operation, deadline)
        payload = json.dumps(
            {"operation": operation},
            sort_keys=True,
            separators=(",", ":"),
        ).encode("ascii") + b"\n"
        self._stage_control_payload(control_file, payload, deadline)
        if operation == "network_transition":
            # The app's legacy transition acknowledgement is followed by a
            # separate ready file so it cannot be confused with routing proof.
            self._routing_proof(f"{control_file}.routing", deadline)

    def _stage_control_payload(
        self, control_file: str, payload: bytes, deadline: float
    ) -> None:
        self._stage_private_file(
            control_file,
            payload,
            _remaining(deadline, "ANDROID_CONTROL_STAGE_TIMEOUT"),
            "ANDROID_CONTROL_STAGE_FAILED",
        )

    def _routing_proof(
        self,
        control_file: str,
        deadline: float,
        *,
        ready: Mapping[str, object] | None = None,
        abort: Callable[[], None] | None = None,
    ) -> None:
        """Complete the app-bound Android routing proof through root ADB."""

        primary: BaseException | None = None
        rule: tuple[str, str, int] | None = None
        removed = False
        physical: str | None = None
        vpn: str | None = None

        def record(error: BaseException) -> None:
            nonlocal primary
            if primary is None:
                primary = error
            else:
                primary.add_note(
                    "Android routing proof secondary failure:\n"
                    + "".join(traceback.format_exception(error)).rstrip()
                )

        try:
            ready_value = (
                ready
                if ready is not None
                else self._routing_ready(
                    control_file, "ready", deadline, abort=abort
                )
            )
            physical, vpn, ipv4, port = self._routing_ready_values(ready_value)
            self._emit_progress(
                "native-state",
                kind="routing-proof",
                platform="android",
                phase="ready",
                physical_interface=physical,
                vpn_interface=vpn,
            )
            before_rx, before_tx = self._routing_counters(vpn, deadline)
            self._routing_rule("insert", physical, ipv4, port, deadline)
            rule = (physical, ipv4, port)
            blocked_payload = json.dumps(
                {"operation": "observe_routing_identity", "phase": "blocked"},
                sort_keys=True,
                separators=(",", ":"),
            ).encode("ascii") + b"\n"
            self._stage_control_payload(control_file, blocked_payload, deadline)
            blocked = self._routing_ready(
                control_file, "blocked", deadline, abort=abort
            )
            tunneled_ip = self._assert_routing_blocked(blocked)
            self._observed_tunneled_ips.add(tunneled_ip)
            after_rx, after_tx = self._routing_counters(vpn, deadline)
            packets = self._routing_rule_counter(physical, ipv4, port, deadline)
            if packets <= 0:
                raise ScenarioExecutionError(
                    "ANDROID_ROUTING_RULE_NOT_HIT "
                    f"interface={physical} destination={ipv4} port={port} "
                    f"packets={packets}"
                )
            if after_rx <= before_rx or after_tx <= before_tx:
                raise ScenarioExecutionError(
                    "ANDROID_ROUTING_TRAFFIC_NOT_OBSERVED "
                    f"interface={vpn} before_rx={before_rx} after_rx={after_rx} "
                    f"before_tx={before_tx} after_tx={after_tx}"
                )
            self._emit_progress(
                "native-state",
                kind="routing-proof",
                platform="android",
                phase="blocked",
                physical_interface=physical,
                vpn_interface=vpn,
                rule_packets=packets,
                rx_delta=after_rx - before_rx,
                tx_delta=after_tx - before_tx,
            )
        except BaseException as error:
            record(error)

        # A timed-out proof still gets a bounded cleanup window. No unbounded
        # worker wait or normal operation deadline extension is introduced.
        cleanup_deadline = max(
            deadline, time.monotonic() + _ROUTING_CLEANUP_SECONDS
        )
        if rule is not None:
            try:
                self._routing_rule(
                    "remove", rule[0], rule[1], rule[2], cleanup_deadline
                )
                removed = True
            except BaseException as error:
                record(error)

        if removed:
            try:
                unblocked_payload = json.dumps(
                    {"operation": "observe_routing_identity", "phase": "unblocked"},
                    sort_keys=True,
                    separators=(",", ":"),
                ).encode("ascii") + b"\n"
                self._stage_control_payload(
                    control_file, unblocked_payload, cleanup_deadline
                )
                unblocked = self._routing_ready(
                    control_file, "unblocked", cleanup_deadline, abort=abort
                )
                baseline_ip = self._assert_routing_recovery(unblocked)
                if self._observed_baseline_ip is None:
                    self._observed_baseline_ip = baseline_ip
                self._emit_progress(
                    "native-state",
                    kind="routing-proof",
                    platform="android",
                    phase="unblocked",
                    physical_interface=physical,
                    vpn_interface=vpn,
                )
            except BaseException as error:
                record(error)

        passed = primary is None
        finish_payload: dict[str, object] = {
            "operation": "observe_routing_identity",
            "phase": "finish",
            "passed": passed,
        }
        if primary is not None:
            finish_payload["error"] = "".join(
                traceback.format_exception(primary)
            ).rstrip()
        try:
            self._stage_control_payload(
                control_file,
                (
                    json.dumps(
                        finish_payload, sort_keys=True, separators=(",", ":")
                    ).encode("utf-8")
                    + b"\n"
                ),
                cleanup_deadline,
            )
            self._emit_progress(
                "native-state",
                kind="routing-proof",
                platform="android",
                phase="finish",
                physical_interface=physical,
                vpn_interface=vpn,
                verified=passed,
            )
        except BaseException as error:
            record(error)
        if primary is not None:
            raise primary
        self._validated_physical_interface = physical

    def _routing_ready(
        self,
        control_file: str,
        phase: str,
        deadline: float,
        *,
        abort: Callable[[], None] | None = None,
    ) -> Mapping[str, object]:
        name = f"{control_file}.ready"
        last_value: Mapping[str, object] | None = None
        while True:
            self._wait_device_file(name, deadline, abort=abort)
            result = self._adb(
                ("shell", "-T", "cat", f"{_APP_FILES}/{name}"),
                _remaining(deadline, "ANDROID_ROUTING_READY_READ_FAILED"),
                "ANDROID_ROUTING_READY_READ_FAILED",
            )
            try:
                value = json.loads(result.stdout.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                failure = ScenarioExecutionError("ANDROID_ROUTING_READY_INVALID")
                _append_command_result_notes(failure, result)
                raise failure from error
            if not isinstance(value, Mapping):
                failure = ScenarioExecutionError("ANDROID_ROUTING_READY_INVALID")
                _append_command_result_notes(failure, result)
                raise failure
            last_value = value
            if value.get("phase") == phase:
                return value
            if time.monotonic() >= deadline:
                failure = ScenarioExecutionError("ANDROID_ROUTING_PHASE_TIMEOUT")
                failure.add_note(
                    "Android routing ready response="
                    + json.dumps(last_value, sort_keys=True)
                )
                raise failure
            if abort is not None:
                abort()
            time.sleep(min(0.1, deadline - time.monotonic()))

    @staticmethod
    def _routing_ready_values(
        ready: Mapping[str, object],
    ) -> tuple[str, str, str, int]:
        physical = ready.get("physical_interface")
        vpn = ready.get("vpn_interface")
        raw_ipv4 = ready.get("ipv4")
        raw_port = ready.get("port")
        if (
            not isinstance(physical, str)
            or _ANDROID_INTERFACE.fullmatch(physical) is None
            or not isinstance(vpn, str)
            or _ANDROID_INTERFACE.fullmatch(vpn) is None
            or physical == vpn
            or not isinstance(raw_ipv4, str)
            or not isinstance(raw_port, int)
            or isinstance(raw_port, bool)
            or not 1 <= raw_port <= 65535
        ):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_READY_INVALID", ready
            )
        try:
            address = ipaddress.ip_address(raw_ipv4)
        except ValueError as error:
            failure = AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_READY_INVALID", ready
            )
            raise failure from error
        if address.version != 4:
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_READY_INVALID", ready
            )
        return physical, vpn, str(address), raw_port

    def _routing_counters(self, interface: str, deadline: float) -> tuple[int, int]:
        if _ANDROID_INTERFACE.fullmatch(interface) is None:
            raise ScenarioExecutionError("ANDROID_ROUTING_INTERFACE_INVALID")
        result = self._adb(
            (
                "shell",
                "-T",
                "cat",
                f"/sys/class/net/{interface}/statistics/rx_bytes",
                f"/sys/class/net/{interface}/statistics/tx_bytes",
            ),
            _remaining(deadline, "ANDROID_ROUTING_COUNTERS_FAILED"),
            "ANDROID_ROUTING_COUNTERS_FAILED",
        )
        values = result.stdout.decode("utf-8", errors="replace").split()
        if len(values) != 2 or any(not value.isdigit() for value in values):
            failure = ScenarioExecutionError("ANDROID_ROUTING_COUNTERS_INVALID")
            _append_command_result_notes(failure, result)
            raise failure
        return int(values[0]), int(values[1])

    def _routing_rule(
        self,
        action: str,
        physical: str,
        ipv4: str,
        port: int,
        deadline: float,
    ) -> None:
        if action not in {"insert", "remove"}:
            raise ScenarioExecutionError("ANDROID_ROUTING_RULE_ACTION_INVALID")
        operation = "-I" if action == "insert" else "-D"
        arguments = (
            "shell", "iptables", operation, "OUTPUT",
            *(("1",) if action == "insert" else ()),
            "-o", physical, "-d", ipv4, "-p", "tcp", "--dport", str(port),
            "-j", "REJECT",
        )
        self._adb(
            arguments,
            _remaining(deadline, "ANDROID_ROUTING_RULE_TIMEOUT"),
            "ANDROID_ROUTING_RULE_INSTALL_FAILED"
            if action == "insert" else "ANDROID_ROUTING_RULE_REMOVE_FAILED",
        )

    def _routing_rule_counter(
        self, physical: str, ipv4: str, port: int, deadline: float
    ) -> int:
        result = self._adb(
            (
                "shell", "iptables", "-L", "OUTPUT", "-v", "-n", "-x",
                "--line-numbers",
            ),
            _remaining(deadline, "ANDROID_ROUTING_RULE_COUNTER_FAILED"),
            "ANDROID_ROUTING_RULE_COUNTER_FAILED",
        )
        expected_destination = {ipv4, f"{ipv4}/32"}
        port_marker = f"dpt:{port}"
        for line in result.stdout_text.splitlines():
            fields = line.split()
            for offset in (0, 1):
                if len(fields) <= offset + 8:
                    continue
                try:
                    packets = int(fields[offset])
                    int(fields[offset + 1])
                except ValueError:
                    continue
                if (
                    fields[offset + 2] == "REJECT"
                    and fields[offset + 3] in {"tcp", "6"}
                    and fields[offset + 4] == "--"
                    and fields[offset + 5] == "*"
                    and fields[offset + 6] == physical
                    and fields[offset + 7] == "0.0.0.0/0"
                    and fields[offset + 8] in expected_destination
                    and port_marker in fields[offset + 9:]
                ):
                    return packets
        failure = ScenarioExecutionError("ANDROID_ROUTING_RULE_COUNTER_INVALID")
        _append_command_result_notes(failure, result)
        raise failure

    @staticmethod
    def _routing_observation_failure(
        code: str, value: Mapping[str, object]
    ) -> ScenarioExecutionError:
        failure = ScenarioExecutionError(code)
        failure.add_note(
            "Android routing probe response="
            + json.dumps(value, sort_keys=True)
        )
        return failure

    @staticmethod
    def _assert_routing_blocked(value: Mapping[str, object]) -> str:
        direct = value.get("direct")
        vpn = value.get("vpn")
        if (
            not isinstance(direct, Mapping)
            or "status" in direct
            or not all(
                isinstance(direct.get(key), str)
                for key in ("error_type", "error", "stack")
            )
        ):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_DIRECT_NOT_BLOCKED", value
            )
        if not isinstance(vpn, Mapping):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
        status = vpn.get("status")
        body = vpn.get("body")
        if (
            isinstance(status, bool)
            or not isinstance(status, int)
            or not 200 <= status < 300
            or not isinstance(body, str)
        ):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
        try:
            return _parse_external_ip(body)
        except ScenarioExecutionError as error:
            failure = AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
            raise failure from error

    @staticmethod
    def _assert_routing_recovery(value: Mapping[str, object]) -> str:
        direct = value.get("direct")
        if not isinstance(direct, Mapping):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_RECOVERY_INVALID", value
            )
        status = direct.get("status")
        body = direct.get("body")
        if (
            isinstance(status, bool)
            or not isinstance(status, int)
            or not 200 <= status < 300
            or not isinstance(body, str)
        ):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_RECOVERY_INVALID", value
            )
        try:
            return _parse_external_ip(body)
        except ScenarioExecutionError as error:
            failure = AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_RECOVERY_INVALID", value
            )
            raise failure from error

    def _wait_device_file(
        self,
        name: str,
        deadline: float,
        *,
        present: bool = True,
        abort: Callable[[], None] | None = None,
    ) -> None:
        expected = b"READY" if present else b"ABSENT"
        while time.monotonic() < deadline:
            if abort is not None:
                abort()
            result = self._adb(
                (
                    "shell", "sh", "-c",
                    shlex.quote(
                        f"if test -f {_APP_FILES}/{name}; then printf READY; else printf ABSENT; fi"
                    ),
                ),
                min(2.0, _remaining(deadline, "ANDROID_CONTROL_TIMEOUT")),
                "ANDROID_CONTROL_PROBE_FAILED",
                allow_nonzero=True,
            )
            if result.returncode == 0 and result.stdout.strip() == expected:
                return
            if abort is not None:
                abort()
            time.sleep(min(0.1, max(0.0, deadline - time.monotonic())))
        raise ScenarioExecutionError("ANDROID_CONTROL_TIMEOUT")

    def _perform_external_control(self, operation: str, deadline: float) -> None:
        if operation == "network_transition":
            interface = self._validated_physical_interface
            if interface is None:
                raise ScenarioExecutionError(
                    "ANDROID_UPLINK_IDENTITY_UNAVAILABLE"
                )
            self._interrupt_uplink(interface, deadline)
            return
        raise ScenarioExecutionError("ANDROID_OPERATION_UNSUPPORTED")

    def _stop_product_for_loss(self, deadline: float) -> None:
        before = self._device_text(
            ("shell", "pidof", _PACKAGE_NAME), deadline,
            "ANDROID_PROCESS_LOSS_PROBE_FAILED",
        )
        if not before:
            raise ScenarioExecutionError("ANDROID_PROCESS_LOSS_PRECONDITION")
        self._emit_progress(
            "native-state",
            kind="app-process",
            platform="android",
            state="present",
        )
        self._adb(
            ("shell", "am", "force-stop", _PACKAGE_NAME),
            _remaining(deadline, "ANDROID_PROCESS_LOSS_STOP_FAILED"),
            "ANDROID_PROCESS_LOSS_STOP_FAILED",
        )
        absent_probes = 0
        while time.monotonic() < deadline:
            current = self._adb(
                ("shell", "pidof", _PACKAGE_NAME),
                min(2.0, _remaining(deadline, "ANDROID_PROCESS_LOSS_TIMEOUT")),
                "ANDROID_PROCESS_LOSS_PROBE_FAILED",
                allow_nonzero=True,
            )
            if current.returncode in (0, 1) and not current.stdout.strip():
                absent_probes += 1
                if absent_probes >= 2:
                    self._emit_progress(
                        "native-state",
                        kind="app-process",
                        platform="android",
                        state="absent",
                    )
                    return
            else:
                absent_probes = 0
            time.sleep(min(0.1, max(0.0, deadline - time.monotonic())))
        raise ScenarioExecutionError("ANDROID_PROCESS_LOSS_NOT_ABSENT")

    def _interrupt_uplink(self, interface: str, deadline: float) -> None:
        """Drop and restore Android's real non-VPN default-route link.

        ADB remains on an independent Android control transport. The guest-side
        transaction uses the physical interface already validated by the
        app-bound routing proof and restores it from an EXIT/signal trap.
        """

        remaining = _remaining(deadline, "ANDROID_UPLINK_TIMEOUT")
        if _ANDROID_INTERFACE.fullmatch(interface) is None:
            raise ScenarioExecutionError("ANDROID_UPLINK_IDENTITY_UNAVAILABLE")
        restore_reserve = min(10.0, max(2.0, remaining / 3.0))
        down_window = remaining - restore_reserve
        if down_window <= 0:
            raise ScenarioExecutionError("ANDROID_UPLINK_TIMEOUT")
        down_budget_seconds = max(1, math.ceil(down_window))
        overall_budget_seconds = max(down_budget_seconds + 1, math.ceil(remaining))
        script = r'''
set +e
script_start=$(date +%s)
down_deadline=$((script_start + $1))
overall_deadline=$((script_start + $2))
interface=$3
lowered=0
restore_status=0
restore_detail=

link_is_up() {
    flags=$(printf '%s\n' "$1" | sed -n 's/^[0-9][0-9]*:[^:]*: <\([^>]*\)>.*/\1/p')
    case ",$flags," in
        *,UP,*) return 0 ;;
    esac
    return 1
}

route_is_usable() {
    selected=0
    usable=0
    while IFS= read -r line; do
        case "$line" in
            default*)
                case " $line " in
                    *" dev $interface "*)
                        selected=1
                        case " $line " in
                            *" linkdown "*) ;;
                            *) usable=1 ;;
                        esac
                        ;;
                esac
                ;;
        esac
    done <<EOF
$1
EOF
    [ "$selected" -eq 1 ] && [ "$usable" -eq 1 ]
}

restore_uplink() {
    [ "$lowered" -eq 1 ] || return 0
    restore_output=$(ip link set dev "$interface" up 2>&1)
    restore_rc=$?
    lowered=0
    if [ "$restore_rc" -ne 0 ]; then
        restore_status=1
        restore_detail="ip link set up rc=$restore_rc output=$restore_output"
        return 0
    fi
    restore_ready=0
    while [ "$(date +%s)" -lt "$overall_deadline" ]; do
        restore_link=$(ip -o link show dev "$interface" 2>&1)
        restore_link_rc=$?
        restore_routes=$(ip -4 route show table all default 2>&1)
        restore_routes_rc=$?
        if [ "$restore_link_rc" -ne 0 ] || [ "$restore_routes_rc" -ne 0 ]; then
            restore_status=1
            restore_detail="restore link rc=$restore_link_rc output=$restore_link; restore routes rc=$restore_routes_rc output=$restore_routes"
            return 0
        fi
        if link_is_up "$restore_link" && route_is_usable "$restore_routes"; then
            restore_ready=$((restore_ready + 1))
        else
            restore_ready=0
        fi
        if [ "$restore_ready" -ge 2 ]; then
            break
        fi
        sleep 0.1
    done
    if [ "$restore_ready" -lt 2 ]; then
        restore_status=1
        restore_detail="restore link=$restore_link; restore routes=$restore_routes"
        return 0
    fi
    printf '%s\n' "DobbyVPN uplink interface=$interface state=restored"
}

finish() {
    exit_status=$1
    trap - 0 1 2 3 15
    restore_uplink
    if [ "$restore_status" -ne 0 ]; then
        printf '%s\n' "DobbyVPN uplink secondary code=ANDROID_UPLINK_RESTORE_FAILED detail=$restore_detail" >&2
        [ "$exit_status" -eq 0 ] && exit_status=1
    fi
    exit "$exit_status"
}
on_exit() {
    finish "$1"
}
on_signal() {
    finish "$1"
}
trap 'on_exit "$?"' 0
trap 'on_signal 129' 1
trap 'on_signal 130' 2
trap 'on_signal 131' 3
trap 'on_signal 143' 15

uid_output=$(id -u 2>&1)
uid_rc=$?
if [ "$uid_rc" -ne 0 ] || [ "$uid_output" != "0" ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_ROOT_REQUIRED detail=id -u rc=$uid_rc output=$uid_output" >&2
    exit 10
fi

route_output=$(ip -4 route show table all default 2>&1)
route_rc=$?
if [ "$route_rc" -ne 0 ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_ROUTE_PROBE_FAILED detail=ip route rc=$route_rc output=$route_output" >&2
    exit 11
fi
before_link=$(ip -o link show dev "$interface" 2>&1)
before_link_rc=$?
if [ "$before_link_rc" -ne 0 ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_LINK_PROBE_FAILED detail=link rc=$before_link_rc output=$before_link" >&2
    exit 13
fi
if ! link_is_up "$before_link" || ! route_is_usable "$route_output"; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_PRECONDITION detail=link=$before_link; routes=$route_output" >&2
    exit 14
fi
printf '%s\n' "DobbyVPN uplink interface=$interface state=present"

# Mark the link for restoration before attempting down, including a partial
# or failed down command.
lowered=1
down_output=$(ip link set dev "$interface" down 2>&1)
down_rc=$?
if [ "$down_rc" -ne 0 ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_DOWN_FAILED detail=ip link set down rc=$down_rc output=$down_output" >&2
    exit 15
fi

absence=0
while [ "$(date +%s)" -lt "$down_deadline" ]; do
    down_link=$(ip -o link show dev "$interface" 2>&1)
    down_link_rc=$?
    down_routes=$(ip -4 route show table all default 2>&1)
    down_routes_rc=$?
    if [ "$down_link_rc" -ne 0 ] || [ "$down_routes_rc" -ne 0 ]; then
        printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_PROBE_FAILED detail=link rc=$down_link_rc output=$down_link; routes rc=$down_routes_rc output=$down_routes" >&2
        exit 16
    fi
    if ! link_is_up "$down_link" && ! route_is_usable "$down_routes"; then
        absence=$((absence + 1))
    else
        absence=0
    fi
    if [ "$absence" -ge 2 ]; then
        break
    fi
    sleep 0.1
done
if [ "$absence" -lt 2 ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_LOSS_NOT_OBSERVED detail=link=$down_link; routes=$down_routes" >&2
    exit 17
fi
printf '%s\n' "DobbyVPN uplink interface=$interface state=absent"
exit 0
'''
        result = self._adb(
            (
                "shell",
                "sh",
                "-c",
                shlex.quote(script),
                "dobbyvpn-uplink",
                str(down_budget_seconds),
                str(overall_budget_seconds),
                interface,
            ),
            remaining,
            "ANDROID_UPLINK_TRANSITION_FAILED",
        )
        states: list[str] = []
        reported_interface: str | None = None
        state_pattern = re.compile(
            r"^DobbyVPN uplink interface=([A-Za-z0-9_.:-]+) state=(present|absent|restored)$"
        )
        for line in result.stdout_text.splitlines():
            match = state_pattern.fullmatch(line.strip())
            if match is None:
                continue
            current_interface, state = match.groups()
            if reported_interface is None:
                reported_interface = current_interface
            elif reported_interface != current_interface:
                error = ScenarioExecutionError("ANDROID_UPLINK_OUTPUT_INVALID")
                _append_command_result_notes(error, result)
                raise error
            if current_interface != interface:
                error = ScenarioExecutionError("ANDROID_UPLINK_OUTPUT_INVALID")
                _append_command_result_notes(error, result)
                raise error
            states.append(state)
        if (
            reported_interface is None
            or states != ["present", "absent", "restored"]
        ):
            error = ScenarioExecutionError("ANDROID_UPLINK_OUTPUT_INVALID")
            _append_command_result_notes(error, result)
            raise error
        for state in states:
            self._emit_progress(
                "native-state",
                interface=reported_interface,
                kind="uplink",
                platform="android",
                state=state,
            )

    def _device_text(self, arguments: tuple[str, ...], deadline: float, failure: str) -> str:
        result = self._adb(arguments, _remaining(deadline, failure), failure, allow_nonzero=True)
        if result.returncode != 0:
            error = ScenarioExecutionError(failure)
            _append_command_result_notes(error, result)
            raise error
        return result.stdout.decode("utf-8", errors="replace").strip()

    def _write_command(
        self,
        scenario: ScenarioDefinition,
        *,
        steps: tuple[ScenarioStep, ...] | None = None,
        preserve_active: bool = False,
    ) -> tuple[Path, str, str]:
        self._active_controls = ()
        raw_directory = getattr(self.runner, "raw_directory", None)
        if not isinstance(raw_directory, Path):
            raise ScenarioExecutionError("ANDROID_EVIDENCE_UNAVAILABLE")
        try:
            _ensure_directory(raw_directory)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        token = uuid.uuid4().hex
        profile_name = f"android-hosted-{token}.profile"
        command_name = f"android-hosted-{token}.command.json"
        output_name = f"android-hosted-{token}.observation.json"
        operations = []
        controls: list[tuple[str, str, float]] = []
        for step in (scenario.steps if steps is None else steps):
            if step.operation not in _ALLOWED_OPERATIONS:
                raise ScenarioExecutionError("ANDROID_OPERATION_UNSUPPORTED")
            item = {
                "id": step.id,
                "operation": step.operation,
                "timeout_seconds": step.timeout_seconds,
            }
            if step.operation in _EXTERNAL_OPERATIONS:
                control_file = f"{token}.external-{len(controls)}.json"
                item["control_file"] = control_file
                controls.append(
                    (control_file, step.operation, float(step.timeout_seconds))
                )
            operations.append(item)
        command = {
            "profile_file": profile_name,
            "output_file": output_name,
            "endpoints": {
                "identity_url": self.identity_url,
                "latency_url": self.latency_url,
                "download_url": self.download_url,
                "upload_url": self.upload_url,
            },
            "operations": operations,
        }
        if self._selected_connection is not None:
            command["profile_index"] = self._selected_connection.index
        if self.source_sha is not None:
            command["source_sha"] = self.source_sha
        if preserve_active:
            command["preserve_active"] = True
        command_file = raw_directory / command_name
        try:
            command_file.write_text(
                json.dumps(command, sort_keys=True, separators=(",", ":")) + "\n",
                encoding="utf-8",
            )
        except OSError as error:
            raise ScenarioExecutionError("ANDROID_COMMAND_WRITE_FAILED") from error
        self._active_controls = tuple(controls)
        return command_file, profile_name, output_name

    def _adb(
        self,
        arguments: tuple[str, ...],
        timeout_seconds: float,
        failure_code: str,
        *,
        allow_nonzero: bool = False,
        input_bytes: bytes | None = None,
    ) -> CommandResult:
        if self.adb is None:
            raise ScenarioExecutionError("ANDROID_ADB_UNAVAILABLE")
        try:
            command = (str(self.adb), *arguments)
            result = self.runner.run(
                command,
                timeout_seconds=timeout_seconds,
                input_bytes=input_bytes,
            )
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        if result.timed_out:
            failure = ScenarioExecutionError("ANDROID_COMMAND_TIMEOUT")
            _append_command_result_notes(failure, result)
            raise failure
        if result.returncode != 0 and not allow_nonzero:
            failure = ScenarioExecutionError(failure_code)
            _append_command_result_notes(failure, result)
            raise failure
        return result

    def _cleanup_device(
        self, names: tuple[str, ...], deadline: float
    ) -> ScenarioExecutionError | None:
        error: ScenarioExecutionError | None = None
        mutation_parts: list[str] = []
        if names:
            mutation_parts.append(
                "rm -f " + " ".join(f"{_APP_FILES}/{name}" for name in names)
            )
        mutation_parts.append(f"am force-stop {_PACKAGE_NAME}")
        try:
            self._adb(
                (
                    "shell",
                    "sh",
                    "-c",
                    shlex.quote(" && ".join(mutation_parts)),
                ),
                _cleanup_timeout(deadline),
                "ANDROID_CLEANUP_FAILED",
            )
        except ScenarioExecutionError as failure:
            error = failure

        verification_parts = [
            *(f"test ! -e {_APP_FILES}/{name}" for name in names),
            f"test -z \"$(pidof {_PACKAGE_NAME})\"",
        ]
        try:
            self._adb(
                (
                    "shell",
                    "sh",
                    "-c",
                    shlex.quote(" && ".join(verification_parts)),
                ),
                _cleanup_timeout(deadline),
                "ANDROID_CLEANUP_FAILED",
            )
        except ScenarioExecutionError as failure:
            if error is None:
                error = failure
            else:
                error.add_note(
                    "Android cleanup verification also failed:\n"
                    + "".join(traceback.format_exception(failure)).rstrip()
                )
        return error
