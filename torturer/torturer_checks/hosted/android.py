"""Hosted Android adapters for binding and rendered Go/Fyne Android lanes.

DobbyVPN owns Android session state and cleanup; Torturer owns the test set,
assertions, result values, and disposable runner scratch. The protocol-matrix lane
uses the native binding to exercise each discovered profile. The gui-auto lane
uses one visible AUTO action and never claims to cover that matrix. Most
scenarios use one instrumentation invocation; process loss uses two so the app
process is actually absent between the initial connection and recovery.
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
from typing import Callable, Mapping
import uuid

from torturer_checks.android_instrumentation import (
    ROUTING_RULE_CHAIN,
    parse_instrumentation_result,
)
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
_MAIN_ACTIVITY = "com.dobby.vpn/org.golang.app.GoNativeActivity"
_APP_DATA = "/data/user/0/com.dobby.vpn"
_APP_FILES = "/data/user/0/com.dobby.vpn/files"
_INSTRUMENTATION_COMPONENT = (
    "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner"
)
_INSTRUMENTATION_CLASS = "com.dobby.GoUiHostedProfileTest"
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
_ANDROID_UI_MODES = frozenset({"protocol-matrix", "gui-auto"})
# Keep this boundary in step with sessionapi's product parser.  The rendered
# lane needs one input small enough for the real Fyne/Android editor, but it
# must still receive an untouched, complete TOML protocol block.  In
# particular, do not re-encode or otherwise normalize private profile bytes in
# the controller.
_GUI_PROFILE_HEADER = re.compile(
    rb"(?m)^[ \t]*\[\[\s*(Outline|Xray|TrustTunnel)\s*\]\]"
    rb"[ \t]*(?:#[^\r\n]*)?(?:\r?\n|$)"
)
_GUI_PROFILE_PROTOCOLS = frozenset({b"Outline", b"Xray"})
# Fyne's Android editor forwards each insertion through a native text bridge.
# Keep the rendered representative comfortably below the product's 1 MiB
# configuration limit; the full bundle remains the binding lane's concern.
_GUI_PROFILE_MAX_BYTES = 64 * 1024
_ANDROID_UI_PROGRESS_VALUE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,63}$")
_ANDROID_UI_CONSENT_DIAGNOSTIC_VALUES = {
    "foreground": frozenset({"VPN_DIALOG", "PRODUCT", "OTHER", "NONE"}),
    "button1": frozenset({"ENABLED", "DISABLED", "ABSENT", "UNAVAILABLE"}),
    "vpn_permission": frozenset({"PENDING", "GRANTED", "UNAVAILABLE"}),
    "launch_state": frozenset(
        {"NOT_REQUESTED", "QUEUED", "STARTED", "RETURNED", "FAILED"}
    ),
    "post_tap_state": frozenset(
        {
            "Connecting",
            "Connected",
            "Error",
            "Failed",
            "Ready",
            "Disconnected",
            "UNKNOWN",
        }
    ),
}


def _instrumentation_succeeded(result: CommandResult) -> bool:
    """Require both Android instrumentation completion and JUnit success."""

    return parse_instrumentation_result(
        returncode=result.returncode,
        stdout=result.stdout,
        stderr=result.stderr,
        timed_out=result.timed_out,
    ).succeeded


def _instrumentation_failure(result: CommandResult) -> ScenarioExecutionError:
    parsed = parse_instrumentation_result(
        returncode=result.returncode,
        stdout=result.stdout,
        stderr=result.stderr,
        timed_out=result.timed_out,
    )
    failure = ScenarioExecutionError(
        "Android instrumentation failed: "
        f"returncode={result.returncode}, timed_out={result.timed_out}, "
        f"success_marker_present={parsed.success_marker_present}, "
        f"junit_summary_present={parsed.junit_summary_present}, "
        f"failures_marker_present={parsed.failures_marker_present}"
    )
    _append_command_result_notes(failure, result)
    return failure


def _remaining(deadline: float, code: str) -> float:
    value = deadline - time.monotonic()
    if value <= 0:
        raise ScenarioExecutionError(code)
    return value


def _select_gui_profile(raw: bytes) -> bytes:
    """Return the first complete emulator-supported protocol block.

    Android's rendered lane intentionally proves one real GUI journey while
    the binding lane retains full profile-matrix coverage.  A large
    multi-profile bundle can overwhelm the native editor before Fyne has
    delivered every inserted span, so the rendered lane stages one
    source-preserving ``Outline`` or ``Xray`` block within the conservative
    native-editor bound. TrustTunnel is excluded because it is not an
    emulator-supported representative for this lane. Oversized candidates
    are skipped so a later bounded protocol block can still represent the
    rendered journey; if none exists, selection fails closed.

    The product's Go parser remains authoritative for syntax and protocol
    validation once the bytes reach the app.  This helper only finds strict
    protocol-header boundaries; it never parses, logs, or reconstructs
    profile content.
    """
    headers = tuple(_GUI_PROFILE_HEADER.finditer(raw))
    for index, header in enumerate(headers):
        if header.group(1) not in _GUI_PROFILE_PROTOCOLS:
            continue
        end = headers[index + 1].start() if index + 1 < len(headers) else len(raw)
        candidate = raw[header.start():end]
        if candidate.strip() and len(candidate) <= _GUI_PROFILE_MAX_BYTES:
            return candidate
    raise ScenarioExecutionError("ANDROID_GUI_PROFILE_UNAVAILABLE")


def _scenario_deadlines(
    started: float, scenario_seconds: float
) -> tuple[float, float]:
    """Return work and cleanup deadlines within one lane.

    The canonical engine measures the complete adapter call against the
    scenario bound, so cleanup is reserved inside that bound. Command output
    remains in memory for parsing and assertions; no VPN or device logs are
    retained by the functional lane.
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
    if "coverage lane" in detail:
        return "ANDROID_OBSERVATION_LANE_INVALID"
    if "connection" in detail:
        return "ANDROID_OBSERVATION_CONNECTIONS_INVALID"
    if "unexpected shape" in detail:
        return "ANDROID_OBSERVATION_SHAPE_INVALID"
    if "identity" in detail or "platform" in detail:
        return "ANDROID_OBSERVATION_IDENTITY_INVALID"
    return "ANDROID_OBSERVATION_VALUES_INVALID"


def _failure_code(error: BaseException) -> str:
    """Return a stable code without serializing exception text."""

    for attribute in ("reason_code", "code"):
        value = getattr(error, attribute, None)
        if isinstance(value, str) and value:
            return value
    return type(error).__name__


class AndroidHostedAdapter:
    """Run canonical scenarios through DobbyVPN Android instrumentation."""

    adapter_id = "hosted-android-app"
    adapter_version = "v7"

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
        ui_mode: str = "protocol-matrix",
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
        if ui_mode not in _ANDROID_UI_MODES:
            raise HostedAdapterError(
                "ANDROID_UI_MODE_INVALID: expected protocol-matrix or gui-auto"
            )
        endpoint_values = (identity_url, latency_url, download_url, upload_url)
        if not all(value is not None for value in endpoint_values):
            raise HostedAdapterError("ENDPOINTS_REQUIRED")
        self.runner = runner
        self.profile = profile
        self.adb = adb
        self.source_sha = source_sha
        self.ui_mode = ui_mode
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
        self._validated_physical_transport: str | None = None
        self._last_observation: AndroidProfileObservation | None = None
        self._observed_baseline_ip: str | None = None
        self._observed_tunneled_ips: set[str] = set()
        self._progress_sink: Callable[[str, dict[str, object]], None] | None = None
        self._progress_scenario_id: str | None = None
        self._scratch_files: set[Path] = set()

    @property
    def coverage_lane(self) -> str:
        """Human-readable lane identity retained in progress and commands."""

        return self.ui_mode

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
            contextual.setdefault("coverage_lane", self.coverage_lane)
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
        if self.ui_mode == "gui-auto":
            # The rendered lane deliberately does not probe the binding to
            # discover per-profile entries.  Its contract is one visible
            # AUTO action; the protocol matrix remains a separate adapter
            # lane using the binding mode below.
            self._connections = (ConnectionIdentity(index=0, protocol="AUTO"),)
            return self._connections
        self.execute_scenario(get_scenario("functional.configure"))
        observation = self._last_observation
        if observation is None:
            raise HostedAdapterError("CONNECTION_INVENTORY_INVALID")
        self._connections = observation.connections
        return self._connections

    def select_connection(self, connection: ConnectionIdentity) -> None:
        if connection not in self._connections:
            raise HostedAdapterError("CONNECTION_NOT_DISCOVERED")
        if self.ui_mode == "gui-auto" and connection != ConnectionIdentity(
            index=0, protocol="AUTO"
        ):
            raise HostedAdapterError("ANDROID_GUI_CONNECTION_INVALID")
        self._selected_connection = connection

    def _validate_observation_identity(
        self, observation: AndroidProfileObservation
    ) -> None:
        if self._connections and observation.connections != self._connections:
            raise ScenarioExecutionError("CONNECTION_INVENTORY_CHANGED")
        if observation.coverage_lane != self.coverage_lane:
            raise ScenarioExecutionError("ANDROID_COVERAGE_LANE_MISMATCH")
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
        the command vector and in-memory result metadata contain no profile bytes.
        """
        self._progress_scenario_id = scenario.id
        self._validated_physical_interface = None
        self._validated_physical_transport = None
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
                self._validate_gui_observation(scenario, observation)
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
            self._validate_gui_observation(scenario, initial, steps=before_loss)
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
            self._validate_gui_observation(scenario, recovered)
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
            scratch_error = self._cleanup_local_scratch()
            self._active_controls = ()
            if cleanup_error is not None:
                if execution_error is not None:
                    execution_error.add_note(
                        f"android_cleanup_error={_failure_code(cleanup_error)}"
                    )
                else:
                    raise cleanup_error
            if scratch_error is not None:
                if execution_error is not None:
                    execution_error.add_note(
                        f"android_scratch_cleanup_error={_failure_code(scratch_error)}"
                    )
                elif cleanup_error is None:
                    raise scratch_error

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
        progress_name = self._progress_name(command_file)
        device_files.extend(
            (
                profile_name,
                f"{profile_name}.tmp",
                command_file.name,
                f"{command_file.name}.tmp",
                output_name,
                progress_name,
                f"{progress_name}.tmp",
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
        if self.ui_mode == "gui-auto":
            profile_bytes = _select_gui_profile(profile_bytes)
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
            command_file.name,
            deadline,
            preserve_active=preserve_active,
            output_name=output_name,
            progress_name=progress_name,
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

    def _validate_gui_observation(
        self,
        scenario: ScenarioDefinition,
        observation: AndroidProfileObservation,
        *,
        steps: tuple[ScenarioStep, ...] | None = None,
    ) -> None:
        if self.ui_mode != "gui-auto":
            return
        if not observation.gui_auto_verified:
            raise ScenarioExecutionError("ANDROID_GUI_AUTO_NOT_VERIFIED")
        executed_steps = scenario.steps if steps is None else steps
        if any(step.operation == "inspect_cleanup" for step in executed_steps) and not observation.ui_reopen_verified:
            raise ScenarioExecutionError("ANDROID_UI_REOPEN_NOT_VERIFIED")

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
        output_name: str | None = None,
        progress_name: str | None = None,
    ) -> CommandResult:
        controls = self._active_controls
        observe_live = bool(controls or self._progress_sink)
        if not preserve_active:
            # A preceding real-renderer invocation can leave Fyne's
            # NativeActivity process alive after Android has torn down the
            # instrumentation session.  Starting the next runner against
            # that process can produce a blank surface and leave
            # GoUiHostedProfileTest waiting until its outer deadline.  The
            # runner has not started yet, so this controller-side stop cannot
            # kill an active instrumentation process.  Preserve the live
            # process deliberately for the first half of process-loss
            # coverage, where --no-restart depends on it.
            self._adb(
                ("shell", "am", "force-stop", _PACKAGE_NAME),
                _remaining(deadline, "ANDROID_COLD_START_TIMEOUT"),
                "ANDROID_COLD_START_FAILED",
            )
        if preserve_active:
            # The rendered process-loss phase must not inherit the previous
            # scenario's Fyne editor/activity state.  A preserved Activity
            # can still contain the prior profile, and appending the next
            # source through its real InputConnection would make the failure
            # look like a profile-entry or Go parse problem.  Reset only the
            # rendered lane; protocol-matrix does not own that UI state.
            if self.ui_mode == "gui-auto":
                self._adb(
                    ("shell", "am", "force-stop", _PACKAGE_NAME),
                    _remaining(deadline, "ANDROID_COLD_START_TIMEOUT"),
                    "ANDROID_COLD_START_FAILED",
                )
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
        observation_checked = False
        last_ui_progress: tuple[object, ...] | None = None

        def poll_ui_progress() -> None:
            """Forward only the driver's redacted phase marker.

            The hosted Java driver never writes profile text, endpoint values,
            or exception details to this file.  Validate the small value
            vocabulary here as a second boundary so a malformed candidate
            record cannot leak arbitrary data into the runner's live output.
            """

            nonlocal last_ui_progress
            if progress_name is None:
                return
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return
            try:
                result = self._adb(
                    ("shell", "-T", "cat", f"{_APP_FILES}/{progress_name}"),
                    min(1.0, remaining),
                    "ANDROID_UI_PROGRESS_UNAVAILABLE",
                    allow_nonzero=True,
                )
            except ScenarioExecutionError:
                # The marker is intentionally best-effort diagnostics.  A
                # missing file must never replace the instrumentation result.
                return
            if result.returncode != 0:
                return
            try:
                value = json.loads(result.stdout.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                return
            if not isinstance(value, Mapping):
                return
            operation = value.get("operation")
            stage = value.get("stage")
            state = value.get("state")
            sequence = value.get("sequence")
            if (
                not all(
                    isinstance(item, str)
                    and _ANDROID_UI_PROGRESS_VALUE.fullmatch(item) is not None
                    for item in (operation, stage, state)
                )
                or not isinstance(sequence, int)
                or isinstance(sequence, bool)
                or sequence < 0
            ):
                return
            consent_diagnostic = value.get("consent_diagnostic")
            diagnostic_values: dict[str, str] | None = None
            if consent_diagnostic is not None:
                if not isinstance(consent_diagnostic, Mapping):
                    return
                if set(consent_diagnostic) != set(
                    _ANDROID_UI_CONSENT_DIAGNOSTIC_VALUES
                ):
                    return
                candidate: dict[str, str] = {}
                for key, allowed in _ANDROID_UI_CONSENT_DIAGNOSTIC_VALUES.items():
                    item = consent_diagnostic.get(key)
                    if not isinstance(item, str) or item not in allowed:
                        return
                    candidate[key] = item
                diagnostic_values = candidate
            marker = (
                operation,
                stage,
                state,
                sequence,
                tuple(
                    diagnostic_values.get(key)
                    for key in _ANDROID_UI_CONSENT_DIAGNOSTIC_VALUES
                )
                if diagnostic_values is not None
                else None,
            )
            if marker == last_ui_progress:
                return
            last_ui_progress = marker
            if diagnostic_values is None:
                self._emit_progress(
                    "native-state",
                    kind="ui-phase",
                    platform="android",
                    operation=operation,
                    phase=stage,
                    state=state,
                    progress_sequence=sequence,
                )
            else:
                self._emit_progress(
                    "native-state",
                    kind="ui-phase",
                    platform="android",
                    operation=operation,
                    phase=stage,
                    state=state,
                    progress_sequence=sequence,
                    consent_diagnostic=diagnostic_values,
                )

        def check_worker() -> None:
            nonlocal observation_checked
            poll_ui_progress()
            if worker.is_alive():
                return
            worker_error = holder.get("error")
            worker_result = holder.get("result")
            if isinstance(worker_error, BaseException):
                raise worker_error
            if isinstance(worker_result, CommandResult):
                if _instrumentation_succeeded(worker_result):
                    if output_name is not None and not observation_checked:
                        observation_checked = True
                        self._raise_observation_error_if_present(
                            output_name, deadline
                        )
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
                # as secondary in-memory status.
                primary_error = error
                break

        if not controls:
            # Without an external routing handshake there is no polling loop
            # to call check_worker while the Java UI driver is running.  Keep
            # the worker bounded by the scenario deadline and poll the
            # privacy-safe marker so a hang reports its exact last phase.
            while worker.is_alive():
                try:
                    check_worker()
                except BaseException as error:
                    primary_error = error
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                worker.join(timeout=min(0.25, remaining))

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
                    f"android_instrumentation_worker_error={type(worker_failure).__name__}"
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

    def _raise_observation_error_if_present(
        self, output_name: str, deadline: float
    ) -> None:
        """Surface an app error before a missing external-control file masks it."""
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return
        result = self._adb(
            ("shell", "-T", "cat", f"{_APP_FILES}/{output_name}"),
            min(2.0, remaining),
            "ANDROID_OBSERVATION_UNAVAILABLE",
            allow_nonzero=True,
        )
        if result.returncode != 0:
            return
        try:
            value = json.loads(result.stdout.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            return
        if not isinstance(value, Mapping):
            return
        error_code = value.get("error_code")
        if isinstance(error_code, str) and error_code:
            raise ScenarioExecutionError(error_code)

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
            self._routing_proof(
                f"{control_file}.routing", deadline, abort=abort
            )

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
        rule: tuple[str, tuple[str, ...], int] | None = None
        removed = False
        physical: str | None = None
        vpn: str | None = None

        def record(error: BaseException) -> None:
            nonlocal primary
            if primary is None:
                primary = error
            else:
                primary.add_note(
                    f"android_routing_secondary_error={_failure_code(error)}"
                )

        try:
            ready_value = (
                ready
                if ready is not None
                else self._routing_ready(
                    control_file, "ready", deadline, abort=abort
                )
            )
            physical, transport, vpn, ipv4s, port = self._routing_ready_values(ready_value)
            self._emit_progress(
                "native-state",
                kind="routing-proof",
                platform="android",
                phase="ready",
                physical_interface=physical,
                vpn_interface=vpn,
            )
            before_rx, before_tx = self._routing_counters(vpn, deadline)
            self._routing_rule("insert", physical, ipv4s, port, deadline)
            rule = (physical, ipv4s, port)
            blocked_payload = json.dumps(
                {"operation": "observe_routing_identity", "phase": "blocked"},
                sort_keys=True,
                separators=(",", ":"),
            ).encode("ascii") + b"\n"
            self._stage_control_payload(control_file, blocked_payload, deadline)
            blocked = self._routing_ready(
                control_file, "blocked", deadline, abort=abort
            )
            # Surface the app's structured network failure before secondary
            # firewall/counter assertions can obscure the actual cause.
            tunneled_ip = self._assert_routing_blocked(blocked)
            after_rx, after_tx = self._routing_counters(vpn, deadline)
            packets = self._routing_rule_counter(physical, ipv4s, port, deadline)
            if packets <= 0:
                raise ScenarioExecutionError(
                    "ANDROID_ROUTING_RULE_NOT_HIT "
                    f"interface={physical} destinations={','.join(ipv4s)} port={port} "
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
            self._observed_tunneled_ips.add(tunneled_ip)
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
            finish_payload["error"] = _failure_code(primary)
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
        self._validated_physical_transport = transport

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
                phase = last_value.get("phase") if last_value is not None else None
                failure.add_note(
                    "android_routing_phase="
                    + (phase if isinstance(phase, str) else "invalid")
                )
                raise failure
            if abort is not None:
                abort()
            time.sleep(min(0.1, deadline - time.monotonic()))

    @staticmethod
    def _routing_ready_values(
        ready: Mapping[str, object],
    ) -> tuple[str, str | None, str, tuple[str, ...], int]:
        physical = ready.get("physical_interface")
        transport = ready.get("physical_transport")
        vpn = ready.get("vpn_interface")
        raw_ipv4s = ready.get("ipv4s")
        raw_port = ready.get("port")
        if (
            not isinstance(physical, str)
            or _ANDROID_INTERFACE.fullmatch(physical) is None
            or (
                transport is not None
                and (
                    not isinstance(transport, str)
                    or transport not in {"wifi", "ethernet"}
                )
            )
            or not isinstance(vpn, str)
            or _ANDROID_INTERFACE.fullmatch(vpn) is None
            or physical == vpn
            or not isinstance(raw_ipv4s, list)
            or not raw_ipv4s
            or not isinstance(raw_port, int)
            or isinstance(raw_port, bool)
            or not 1 <= raw_port <= 65535
        ):
            raise AndroidHostedAdapter._routing_observation_failure(
                "ANDROID_ROUTING_READY_INVALID", ready
            )
        addresses: list[str] = []
        for raw_ipv4 in raw_ipv4s:
            if not isinstance(raw_ipv4, str):
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
            if address.version != 4 or str(address) in addresses:
                raise AndroidHostedAdapter._routing_observation_failure(
                    "ANDROID_ROUTING_READY_INVALID", ready
                )
            addresses.append(str(address))
        return physical, transport, vpn, tuple(addresses), raw_port

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
        ipv4s: tuple[str, ...],
        port: int,
        deadline: float,
    ) -> None:
        if action not in {"insert", "remove"}:
            raise ScenarioExecutionError("ANDROID_ROUTING_RULE_ACTION_INVALID")
        if not ipv4s:
            raise ScenarioExecutionError("ANDROID_ROUTING_READY_INVALID")
        if action == "insert":
            # A dedicated chain is both supported by Android's minimal
            # iptables build and unmistakably qualification-owned. The
            # comment match extension is absent on some ReDroid kernels.
            self._cleanup_routing_chain(deadline)
            try:
                self._routing_chain_command("-N", deadline)
                for ipv4 in ipv4s:
                    self._routing_chain_rule(
                        "-A", physical, ipv4, port, deadline
                    )
                self._adb(
                    (
                        "shell", "iptables", "-I", "OUTPUT", "1",
                        "-j", ROUTING_RULE_CHAIN,
                    ),
                    _remaining(deadline, "ANDROID_ROUTING_RULE_TIMEOUT"),
                    "ANDROID_ROUTING_RULE_INSTALL_FAILED",
                )
            except BaseException as error:
                try:
                    self._cleanup_routing_chain(deadline)
                except BaseException as cleanup_error:
                    error.add_note(
                        f"android_routing_rollback_error={type(cleanup_error).__name__}"
                    )
                raise
            return

        self._cleanup_routing_chain(deadline, strict=True)

    def _routing_chain_rule(
        self,
        operation: str,
        physical: str,
        ipv4: str,
        port: int,
        deadline: float,
    ) -> None:
        arguments = (
            "shell", "iptables", operation, ROUTING_RULE_CHAIN,
            "-o", physical, "-d", ipv4, "-p", "tcp", "--dport", str(port),
            "-j", "REJECT",
        )
        self._adb(
            arguments,
            _remaining(deadline, "ANDROID_ROUTING_RULE_TIMEOUT"),
            "ANDROID_ROUTING_RULE_INSTALL_FAILED",
        )

    def _routing_chain_command(self, operation: str, deadline: float) -> None:
        self._adb(
            ("shell", "iptables", operation, ROUTING_RULE_CHAIN),
            _remaining(deadline, "ANDROID_ROUTING_RULE_TIMEOUT"),
            "ANDROID_ROUTING_RULE_INSTALL_FAILED",
        )

    def _routing_rule_counter(
        self, physical: str, ipv4s: tuple[str, ...], port: int, deadline: float
    ) -> int:
        if not ipv4s:
            raise ScenarioExecutionError("ANDROID_ROUTING_READY_INVALID")
        result = self._adb(
            (
                "shell", "iptables", "-L", ROUTING_RULE_CHAIN, "-v", "-n", "-x",
                "--line-numbers",
            ),
            _remaining(deadline, "ANDROID_ROUTING_RULE_COUNTER_FAILED"),
            "ANDROID_ROUTING_RULE_COUNTER_FAILED",
        )
        port_marker = f"dpt:{port}"
        expected_destinations = {
            destination
            for ipv4 in ipv4s
            for destination in (ipv4, f"{ipv4}/32")
        }
        packets_total = 0
        matched = False
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
                    and fields[offset + 8] in expected_destinations
                    and port_marker in fields[offset + 9:]
                ):
                    matched = True
                    packets_total += packets
        if matched:
            return packets_total
        failure = ScenarioExecutionError("ANDROID_ROUTING_RULE_COUNTER_INVALID")
        _append_command_result_notes(failure, result)
        raise failure

    @staticmethod
    def _routing_observation_failure(
        code: str, value: Mapping[str, object]
    ) -> ScenarioExecutionError:
        failure = ScenarioExecutionError(code)
        phase = value.get("phase")
        failure.add_note(
            "android_routing_phase="
            + (phase if isinstance(phase, str) else "invalid")
        )
        return failure

    @staticmethod
    def _assert_routing_blocked(value: Mapping[str, object]) -> str:
        direct = value.get("direct")
        vpn = value.get("vpn")
        if (
            not isinstance(direct, Mapping)
            or "status" in direct
            or direct.get("error_code") != "ANDROID_NETWORK_REQUEST_FAILED"
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
            transport = self._validated_physical_transport
            if interface is None or transport is None:
                raise ScenarioExecutionError(
                    "ANDROID_UPLINK_IDENTITY_UNAVAILABLE"
                )
            self._interrupt_uplink(interface, transport, deadline)
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

    def _interrupt_uplink(self, interface: str, transport: str, deadline: float) -> None:
        """Toggle the proven Android uplink using its matching control.

        ADB remains on an independent Android control transport. The guest-side
        transaction uses Android's Wi-Fi service or link state according to the
        proven Android network transport, then restores it from a trap.
        """

        remaining = _remaining(deadline, "ANDROID_UPLINK_TIMEOUT")
        if _ANDROID_INTERFACE.fullmatch(interface) is None:
            raise ScenarioExecutionError("ANDROID_UPLINK_IDENTITY_UNAVAILABLE")
        if transport not in {"wifi", "ethernet"}:
            raise ScenarioExecutionError("ANDROID_UPLINK_TRANSITION_UNSUPPORTED")
        transition_kind = transport
        restore_reserve = min(20.0, max(2.0, remaining / 2.0))
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
transition_kind=$3
interface=$4
restore_needed=0
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

wifi_state_is() {
    wifi_status_output=$(cmd wifi status 2>&1)
    wifi_state_rc=$?
    wifi_state_output=$(printf '%s\n' "$wifi_status_output" | sed -n '1p')
    [ "$wifi_state_rc" -eq 0 ] && [ "$wifi_state_output" = "Wifi is $1" ]
}

network_state_is() {
    state=$1
    link=$2
    if [ "$state" = present ]; then
        link_is_up "$link" || return 1
        [ "$transition_kind" != wifi ] || wifi_state_is enabled
    elif [ "$transition_kind" = wifi ]; then
        wifi_state_is disabled
    else
        ! link_is_up "$link"
    fi
}

restore_uplink() {
    [ "$restore_needed" -eq 1 ] || return 0
    if [ "$transition_kind" = wifi ]; then
        restore_command="svc wifi enable"
        restore_output=$(svc wifi enable 2>&1)
    else
        restore_command="ip link set up"
        restore_output=$(ip link set dev "$interface" up 2>&1)
    fi
    restore_rc=$?
    if [ "$restore_rc" -ne 0 ]; then
        restore_status=1
        restore_detail="$restore_command rc=$restore_rc output=$restore_output"
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
        if network_state_is present "$restore_link" && route_is_usable "$restore_routes"; then
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
        restore_detail="restore network=$transition_kind; wifi=$wifi_state_output; link=$restore_link; routes=$restore_routes"
        return 0
    fi
    restore_needed=0
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
if ! network_state_is present "$before_link" || ! route_is_usable "$route_output"; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_PRECONDITION detail=link=$before_link; routes=$route_output" >&2
    exit 14
fi
printf '%s\n' "DobbyVPN uplink interface=$interface state=present"

# Mark the proven uplink for restoration before changing it, including partial failure.
restore_needed=1
if [ "$transition_kind" = wifi ]; then
    down_command="svc wifi disable"
    down_output=$(svc wifi disable 2>&1)
else
    down_command="ip link set down"
    down_output=$(ip link set dev "$interface" down 2>&1)
fi
down_rc=$?
if [ "$down_rc" -ne 0 ]; then
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_DOWN_FAILED detail=$down_command rc=$down_rc output=$down_output" >&2
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
    if network_state_is absent "$down_link" && ! route_is_usable "$down_routes"; then
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
    printf '%s\n' "DobbyVPN uplink primary code=ANDROID_UPLINK_LOSS_NOT_OBSERVED detail=wifi=$wifi_state_output; link=$down_link; routes=$down_routes" >&2
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
                transition_kind,
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
            raise ScenarioExecutionError("ANDROID_SCRATCH_UNAVAILABLE")
        try:
            _ensure_directory(raw_directory)
        except HostedAdapterError as error:
            raise ScenarioExecutionError(error.code) from error
        token = uuid.uuid4().hex
        profile_name = f"android-hosted-{token}.profile"
        command_name = f"android-hosted-{token}.command.json"
        output_name = f"android-hosted-{token}.observation.json"
        progress_name = f"android-hosted-{token}.progress.json"
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
            "progress_file": progress_name,
            "coverage_lane": self.coverage_lane,
            "ui_mode": self.ui_mode,
            "endpoints": {
                "identity_url": self.identity_url,
                "latency_url": self.latency_url,
                "download_url": self.download_url,
                "upload_url": self.upload_url,
            },
            "operations": operations,
        }
        if self.ui_mode == "protocol-matrix" and self._selected_connection is not None:
            command["profile_index"] = self._selected_connection.index
        if self.source_sha is not None:
            command["source_sha"] = self.source_sha
        if preserve_active:
            command["preserve_active"] = True
        command_file = raw_directory / command_name
        self._scratch_files.add(command_file)
        try:
            command_file.write_text(
                json.dumps(command, sort_keys=True, separators=(",", ":")) + "\n",
                encoding="utf-8",
            )
        except OSError as error:
            raise ScenarioExecutionError("ANDROID_COMMAND_WRITE_FAILED") from error
        self._active_controls = tuple(controls)
        return command_file, profile_name, output_name

    def _cleanup_local_scratch(self) -> ScenarioExecutionError | None:
        """Remove command staging files after the device cleanup boundary."""

        failure: ScenarioExecutionError | None = None
        for path in tuple(self._scratch_files):
            try:
                path.unlink(missing_ok=True)
            except OSError as error:
                if failure is None:
                    failure = ScenarioExecutionError("ANDROID_SCRATCH_CLEANUP_FAILED")
                failure.add_note(f"scratch_file_error={type(error).__name__}")
        self._scratch_files.clear()
        return failure

    @staticmethod
    def _progress_name(command_file: Path) -> str:
        """Return the private progress filename paired with one command."""

        return command_file.name.replace(".command.json", ".progress.json")

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

    def _cleanup_routing_chain(
        self, deadline: float, *, strict: bool = False
    ) -> None:
        """Remove only the dedicated qualification chain and its jump.

        Every command tolerates absence, making this safe before a proof and
        after a killed run while never inspecting or deleting unrelated rules.
        """

        commands = (
            ("-D", "OUTPUT", "-j", ROUTING_RULE_CHAIN),
            ("-F", ROUTING_RULE_CHAIN),
            ("-X", ROUTING_RULE_CHAIN),
        )
        primary: ScenarioExecutionError | None = None
        for arguments in commands:
            result = self._adb(
                ("shell", "iptables", *arguments),
                _cleanup_timeout(deadline),
                "ANDROID_ROUTING_RULE_CLEANUP_FAILED",
                allow_nonzero=True,
            )
            # An absent jump/chain is the intended idempotent state. Other
            # iptables failures use the same nonzero status, so the subsequent
            # full-table inventory is the idempotent fail-closed authority.
            if strict and result.returncode != 0:
                failure = ScenarioExecutionError(
                    "ANDROID_ROUTING_RULE_REMOVE_FAILED"
                )
                _append_command_result_notes(failure, result)
                if primary is None:
                    primary = failure
                else:
                    primary.add_note(
                        f"android_routing_cleanup_error={type(failure).__name__}"
                    )

        inventory = self._adb(
            ("shell", "iptables", "-S"),
            _cleanup_timeout(deadline),
            "ANDROID_ROUTING_RULE_CLEANUP_FAILED",
            allow_nonzero=True,
        )
        residual = inventory.returncode != 0 or any(
            ROUTING_RULE_CHAIN in line.split()
            for line in inventory.stdout_text.splitlines()
        )
        if residual:
            failure = ScenarioExecutionError(
                "ANDROID_ROUTING_RULE_CLEANUP_FAILED"
            )
            _append_command_result_notes(failure, inventory)
            if primary is None:
                primary = failure
            else:
                primary.add_note(
                    f"android_routing_cleanup_absence_error={type(failure).__name__}"
                )
        if primary is not None:
            raise primary

    def _cleanup_device(
        self, names: tuple[str, ...], deadline: float
    ) -> ScenarioExecutionError | None:
        error: ScenarioExecutionError | None = None
        try:
            self._cleanup_routing_chain(deadline)
        except ScenarioExecutionError as failure:
            error = failure
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
            if error is None:
                error = failure
            else:
                    error.add_note(
                        f"android_app_cleanup_error={_failure_code(failure)}"
                    )

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
                        f"android_cleanup_verification_error={_failure_code(failure)}"
                    )
        return error


def _composite_failure(
    operation: str,
    failures: list[tuple[str, BaseException]],
) -> None:
    """Raise one error while retaining every Android lane failure.

    Discovery and finalization are aggregate lifecycle operations.  Calling
    only the first lane that fails would make the other lane silently absent
    from the result (or leave it unfinalized), so the composite records a
    bounded error type/code for each attempted child before raising.
    """

    if not failures:
        return
    aggregate = HostedAdapterError(f"ANDROID_COMPOSITE_{operation.upper()}_FAILED")
    for lane, failure in failures:
        code = getattr(failure, "reason_code", None)
        if not isinstance(code, str):
            code = getattr(failure, "code", None)
        suffix = f" code={code}" if isinstance(code, str) else ""
        aggregate.add_note(
            f"{lane} {operation} failure={type(failure).__name__}{suffix}"
        )
    raise aggregate


class AndroidCompositeHostedAdapter:
    """Expose rendered AUTO and protocol-matrix Android as one lane.

    The public connection inventory is deliberately contiguous: AUTO is
    external index zero and each discovered binding profile follows it.  The
    profile indexes used by the protocol-matrix child remain private, so the
    canonical runner can use the same connection/scenario loop without
    pretending that the rendered action covers every binding profile.
    """

    adapter_id = "hosted-android-composite"
    adapter_version = "v1"

    def __init__(
        self,
        *,
        gui_auto: AndroidHostedAdapter,
        protocol_matrix: AndroidHostedAdapter,
    ) -> None:
        if gui_auto.ui_mode != "gui-auto" or protocol_matrix.ui_mode != "protocol-matrix":
            raise HostedAdapterError("ANDROID_COMPOSITE_LANES_INVALID")
        self.gui_auto = gui_auto
        self.protocol_matrix = protocol_matrix
        self.runner = gui_auto.runner
        self.profile = gui_auto.profile
        self.adb = gui_auto.adb
        self.source_sha = gui_auto.source_sha
        self.identity_url = gui_auto.identity_url
        self.latency_url = gui_auto.latency_url
        self.download_url = gui_auto.download_url
        self.upload_url = gui_auto.upload_url
        self._connections: tuple[ConnectionIdentity, ...] = ()
        self._external_to_internal: dict[
            ConnectionIdentity, tuple[AndroidHostedAdapter, ConnectionIdentity]
        ] = {}
        self._selected_connection: ConnectionIdentity | None = None
        self._selected_lane: AndroidHostedAdapter | None = None

    @property
    def coverage_lane(self) -> str:
        """Identify the adapter as a composite; child events name each lane."""

        return "android-composite"

    @property
    def _lanes(self) -> tuple[tuple[str, AndroidHostedAdapter], ...]:
        return (("gui-auto", self.gui_auto), ("protocol-matrix", self.protocol_matrix))

    def set_progress_sink(
        self, sink: Callable[[str, dict[str, object]], None]
    ) -> None:
        failures: list[tuple[str, BaseException]] = []
        for lane, adapter in self._lanes:
            try:
                adapter.set_progress_sink(sink)
            except Exception as error:
                failures.append((lane, error))
        _composite_failure("progress", failures)

    def discover_connections(
        self, timeout_seconds: float = 30.0
    ) -> tuple[ConnectionIdentity, ...]:
        if timeout_seconds <= 0:
            raise HostedAdapterError("CONNECTION_DISCOVERY_TIMEOUT")
        self._connections = ()
        self._external_to_internal = {}
        self._selected_connection = None
        self._selected_lane = None

        discovered: dict[str, tuple[ConnectionIdentity, ...]] = {}
        failures: list[tuple[str, BaseException]] = []
        for lane, adapter in self._lanes:
            try:
                discovered[lane] = tuple(
                    adapter.discover_connections(timeout_seconds=timeout_seconds)
                )
            except Exception as error:
                failures.append((lane, error))

        gui_connections = discovered.get("gui-auto")
        if gui_connections is not None and gui_connections != (
            ConnectionIdentity(index=0, protocol="AUTO"),
        ):
            failures.append(
                (
                    "gui-auto",
                    HostedAdapterError("ANDROID_GUI_AUTO_INVENTORY_INVALID"),
                )
            )
        matrix_connections = discovered.get("protocol-matrix")
        if matrix_connections is not None:
            try:
                if not matrix_connections:
                    raise HostedAdapterError("ANDROID_PROTOCOL_MATRIX_EMPTY")
                if [item.index for item in matrix_connections] != list(
                    range(len(matrix_connections))
                ):
                    raise HostedAdapterError("ANDROID_PROTOCOL_MATRIX_INDEX_INVALID")
                if any(item.protocol == "AUTO" for item in matrix_connections):
                    raise HostedAdapterError("ANDROID_PROTOCOL_MATRIX_AUTO_INVALID")
            except Exception as error:
                failures.append(("protocol-matrix", error))

        _composite_failure("discovery", failures)
        assert gui_connections is not None
        assert matrix_connections is not None

        external: list[ConnectionIdentity] = [ConnectionIdentity(0, "AUTO")]
        mapping: dict[
            ConnectionIdentity, tuple[AndroidHostedAdapter, ConnectionIdentity]
        ] = {
            external[0]: (self.gui_auto, gui_connections[0]),
        }
        for internal in matrix_connections:
            identity = ConnectionIdentity(len(external), internal.protocol)
            external.append(identity)
            mapping[identity] = (self.protocol_matrix, internal)
        self._connections = tuple(external)
        self._external_to_internal = mapping
        return self._connections

    def select_connection(self, connection: ConnectionIdentity) -> None:
        try:
            adapter, internal = self._external_to_internal[connection]
        except KeyError as error:
            raise HostedAdapterError("CONNECTION_NOT_DISCOVERED") from error
        lane = "gui-auto" if adapter is self.gui_auto else "protocol-matrix"
        try:
            adapter.select_connection(internal)
        except Exception as error:
            error.add_note(
                f"Android composite selection failed for lane={lane} "
                f"external_connection={connection!r} internal_connection={internal!r}"
            )
            raise
        self._selected_connection = connection
        self._selected_lane = adapter

    @property
    def capabilities(self) -> frozenset[Capability]:
        if self._selected_lane is not None:
            return self._selected_lane.capabilities
        # Before selection, advertise only capabilities shared by both child
        # lanes.  The canonical runner selects before evaluating a scenario.
        return frozenset(self.gui_auto.capabilities & self.protocol_matrix.capabilities)

    @property
    def capability_unavailable_reasons(self) -> dict[Capability, str]:
        if self._selected_lane is not None:
            return dict(self._selected_lane.capability_unavailable_reasons)
        reasons: dict[Capability, str] = {}
        for _lane, adapter in self._lanes:
            for capability, reason in adapter.capability_unavailable_reasons.items():
                if capability in reasons and reasons[capability] != reason:
                    reasons[capability] = f"{reasons[capability]}; {reason}"
                else:
                    reasons[capability] = reason
        return reasons

    def execute_scenario(self, scenario: ScenarioDefinition) -> Mapping[str, object]:
        if self._selected_lane is None or self._selected_connection is None:
            raise HostedAdapterError("CONNECTION_NOT_SELECTED")
        try:
            return self._selected_lane.execute_scenario(scenario)
        except Exception as error:
            lane = "gui-auto" if self._selected_lane is self.gui_auto else "protocol-matrix"
            error.add_note(
                f"Android composite execution lane={lane} "
                f"external_connection={self._selected_connection!r}"
            )
            raise

    def reset(self, timeout_seconds: float = 5.0) -> None:
        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_RESET_TIMEOUT")
        failures: list[tuple[str, BaseException]] = []
        for lane, adapter in self._lanes:
            try:
                adapter.reset(timeout_seconds=timeout_seconds)
            except Exception as error:
                failures.append((lane, error))
        _composite_failure("reset", failures)

    def finalize(
        self, timeout_seconds: float = 30.0, *, deadline: float | None = None
    ) -> None:
        if timeout_seconds <= 0:
            raise HostedAdapterError("INVALID_FINALIZE_TIMEOUT")
        failures: list[tuple[str, BaseException]] = []
        for lane, adapter in self._lanes:
            try:
                adapter.finalize(timeout_seconds=timeout_seconds, deadline=deadline)
            except Exception as error:
                failures.append((lane, error))
        _composite_failure("finalize", failures)
