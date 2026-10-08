"""Hosted Android adapters for binding and rendered Compose UI lanes.

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
from pathlib import Path
import re
import shlex
import threading
import time
from typing import Callable, Mapping
import uuid

from torturer_runner.android_diagnostics import OPTIONAL_MISSING, retained_log_sources
from torturer_runner.android_instrumentation import (
    ROUTING_RULE_CHAIN,
    parse_instrumentation_result,
)
from torturer_runner.diagnostics import (
    add_exception_notes,
    emit_streams,
)
from torturer_runner.screenshot_artifacts import (
    ScreenshotIntegrityError,
    assert_marker_matches,
    file_metadata,
)
from torturer_contract.android_observation import (
    AndroidObservationError,
    AndroidProfileObservation,
)
from torturer_contract.assertions import evaluate_assertions
from torturer_contract.capabilities import Capability
from torturer_contract.engine import ScenarioExecutionError
from torturer_contract.results import ConnectionIdentity
from torturer_contract.scenarios import (
    ScenarioDefinition,
    ScenarioStep,
    select_scenarios,
)

from .cli import (
    CommandResult,
    CommandRunner,
    AdapterError,
    _append_command_result_notes,
    _ensure_directory,
    _executable_file,
    _https_endpoint,
    _parse_external_ip,
    _profile_file,
)


_PACKAGE_NAME = "com.dobby.vpn"
_MAIN_ACTIVITY = "com.dobby.vpn/com.dobby.ui.MainActivity"
_APP_DATA = "/data/user/0/com.dobby.vpn"
_APP_FILES = "/data/user/0/com.dobby.vpn/files"
_APP_DIAGNOSTICS = f"{_APP_FILES}/diagnostics"
_NATIVE_DIAGNOSTIC_PATH = f"{_APP_DIAGNOSTICS}/native_logs.jsonl"
_GO_DIAGNOSTIC_PATH = f"{_APP_DIAGNOSTICS}/go_app_logs.jsonl"
_INSTRUMENTATION_COMPONENT = (
    "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner"
)
_INSTRUMENTATION_CLASS = "com.dobby.NativeUiHostedProfileTest"
_SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
_ALLOWED_OPERATIONS = {
    "configure",
    "consent_grant_selection",
    "connect",
    "observe_tunnel",
    "observe_routing_identity",
    "measure_stability",
    "measure_throughput",
    "disconnect",
    "reconnect",
    "inspect_cleanup",
}
_EXTERNAL_OPERATIONS = frozenset({
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
_CONSENT_N11_MAX_SECONDS = 300.0
_AUTO_RECOVERY_STOP_MAX_SECONDS = 280.0
_AUTO_RECOVERY_STOP_DISCONNECT_SECONDS = 45
_AUTO_RECOVERY_STOP_CASE = "android:auto-recovery-stop"
_ANDROID_INTERFACE = re.compile(r"^[A-Za-z0-9_.:-]{1,32}$")
_ANDROID_UI_MODES = frozenset({"protocol-matrix", "gui-auto"})
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
            "Disconnected",
            "UNKNOWN",
        }
    ),
}
_ANDROID_SCREENSHOT_PATH = re.compile(
    r"^/data/user/0/com\.dobby\.vpn/cache/"
    r"dobbyvpn-rendered-screenshots/([A-Za-z0-9_-]+\.png)$"
)
_ANDROID_RECOVERY_STOP_SCREENSHOT = re.compile(
    r"^[0-9]{4}-disconnect-recovery-stop-state\.png$"
)
_ANDROID_RECOVERY_IDLE_SCREENSHOT = re.compile(
    r"^[0-9]{4}-disconnect-disconnected-state\.png$"
)
_ANDROID_REQUIRED_RENDERED_STAGES = frozenset({"surface"})


class AndroidScreenshotCollectionError(ScenarioExecutionError):
    """A required rendered milestone could not be retained safely."""


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


def _scenario_deadlines(
    started: float, scenario_seconds: float
) -> tuple[float, float]:
    """Return work and cleanup deadlines within one lane.

    The canonical engine measures the complete adapter call against the
    scenario bound, so cleanup is reserved inside that bound. Command output
    remains in memory for parsing and assertions; the caller owns diagnostic
    collection.
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
    """Map an exception to a scenario result code."""

    for attribute in ("reason_code", "code"):
        value = getattr(error, attribute, None)
        if isinstance(value, str) and value:
            return value
    return type(error).__name__


class AndroidAdapter:
    """Run canonical scenarios through DobbyVPN Android instrumentation."""

    adapter_id = "hosted-android-app"
    adapter_version = "v8"

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
        app_apk: Path | None = None,
        test_companion_apk: Path | None = None,
        **kwargs: object,
    ) -> None:
        if kwargs:
            raise AdapterError("ANDROID_ARGUMENT_UNEXPECTED")
        _profile_file(profile)
        if adb is None:
            raise AdapterError("ANDROID_ADB_UNAVAILABLE")
        _executable_file(adb, "ANDROID_ADB_UNAVAILABLE")
        if source_sha is not None and _SOURCE_SHA.fullmatch(source_sha) is None:
            raise AdapterError("SOURCE_SHA_INVALID")
        if ui_mode not in _ANDROID_UI_MODES:
            raise AdapterError(
                "ANDROID_UI_MODE_INVALID: expected protocol-matrix or gui-auto"
            )
        if (app_apk is None) != (test_companion_apk is None):
            raise AdapterError("ANDROID_APK_PAIR_REQUIRED")
        self.app_apk = self._candidate_apk(app_apk, "ANDROID_APP_APK_UNAVAILABLE")
        self.test_companion_apk = self._candidate_apk(
            test_companion_apk, "ANDROID_TEST_COMPANION_APK_UNAVAILABLE"
        )
        endpoint_values = (identity_url, latency_url, download_url, upload_url)
        if not all(value is not None for value in endpoint_values):
            raise AdapterError("ENDPOINTS_REQUIRED")
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
        self._last_observation: AndroidProfileObservation | None = None
        self._observed_baseline_ip: str | None = None
        self._observed_tunneled_ips: set[str] = set()
        self._progress_sink: Callable[[str, dict[str, object]], None] | None = None
        self._progress_scenario_id: str | None = None
        self._scratch_files: set[Path] = set()
        self._subscription_fixture = None
        self._process_cold_import_queued = False
        self._saved_source_restore_preverified = False
        self._diagnostic_collection_sequence = 0

    @staticmethod
    def _candidate_apk(path: Path | None, failure_code: str) -> Path | None:
        if path is None:
            return None
        candidate = path.resolve()
        if not candidate.is_file():
            raise AdapterError(failure_code)
        return candidate

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
            raise AdapterError("CONNECTION_DISCOVERY_TIMEOUT")
        self._selected_connection = None
        if self.ui_mode == "gui-auto":
            # The rendered lane deliberately does not probe the binding to
            # discover per-profile entries.  Its contract is one visible
            # AUTO action; the protocol matrix remains a separate adapter
            # lane using the binding mode below.
            self._connections = (ConnectionIdentity(index=0, protocol="AUTO"),)
            return self._connections
        self.execute_scenario(
            select_scenarios(scenario_ids=["functional.configure"])[0]
        )
        observation = self._last_observation
        if observation is None:
            raise AdapterError("CONNECTION_INVENTORY_INVALID")
        self._connections = observation.connections
        return self._connections

    def select_connection(self, connection: ConnectionIdentity) -> None:
        if connection not in self._connections:
            raise AdapterError("CONNECTION_NOT_DISCOVERED")
        if self.ui_mode == "gui-auto" and connection != ConnectionIdentity(
            index=0, protocol="AUTO"
        ):
            raise AdapterError("ANDROID_GUI_CONNECTION_INVALID")
        self._selected_connection = connection

    def run_unchanged_consent_selection(
        self, *, deadline: float | None
    ) -> dict[str, object]:
        """Prove rendered nondefault selection survives a fresh VPN grant.

        The exact candidate APK pair is uninstalled and reinstalled before the
        case so prior runs cannot supply VPN consent. The same pair is
        reinstalled after the case to restore a pending-consent boundary for
        the existing deny/stale-consent journey.
        """

        if self.ui_mode != "gui-auto":
            raise ScenarioExecutionError("ANDROID_CONSENT_N11_REQUIRES_GUI_AUTO")
        if self.app_apk is None or self.test_companion_apk is None:
            raise ScenarioExecutionError("ANDROID_CONSENT_APK_PATHS_REQUIRED")
        started = time.monotonic()
        phase_deadline = started + _CONSENT_N11_MAX_SECONDS
        if deadline is not None:
            phase_deadline = min(phase_deadline, deadline)
        if phase_deadline <= started + 10.0:
            raise ScenarioExecutionError("HOSTED_LANE_DEADLINE_EXCEEDED_BEFORE_ANDROID_CONSENT_N11")
        cleanup_deadline = phase_deadline
        work_deadline = phase_deadline - min(
            _MAX_CLEANUP_RESERVE_SECONDS,
            max(_MIN_CLEANUP_RESERVE_SECONDS, _CONSENT_N11_MAX_SECONDS * _CLEANUP_RESERVE_FRACTION),
        )
        scenario = select_scenarios(scenario_ids=["functional.configure"])[0]
        steps = (
            ScenarioStep(id="configure", operation="configure", timeout_seconds=90),
            ScenarioStep(
                id="consent-grant-selection",
                operation="consent_grant_selection",
                timeout_seconds=120,
            ),
        )
        device_files: list[str] = []
        primary_error: BaseException | None = None
        selection: Mapping[str, object] | None = None
        self._progress_scenario_id = "android.n11-consent-grant-selection"
        try:
            self._install_fresh_apk_pair(work_deadline)
            observation = self._execute_phase(
                scenario,
                steps,
                work_deadline,
                device_files,
            )
            self._validate_gui_observation(scenario, observation, steps=steps)
            if not (
                observation.configured
                and observation.connected
                and observation.vpn_consent_handled
                and observation.disconnect_clean
                and observation.final_disconnect_clean
                and observation.cleanup_verified
            ):
                raise ScenarioExecutionError(
                    "ANDROID_CONSENT_N11_LIFECYCLE_OBSERVATION_INVALID"
                )
            selection = observation.consent_grant_selection
            if selection is None:
                raise ScenarioExecutionError("ANDROID_CONSENT_N11_OBSERVATION_MISSING")
        except BaseException as error:
            primary_error = error
            self._collect_functional_failure_diagnostics(
                error,
                "android.n11-consent-grant-selection",
                cleanup_deadline,
            )
            raise
        finally:
            cleanup_error = self._cleanup_device(
                tuple(device_files), cleanup_deadline
            )
            scratch_error = self._cleanup_local_scratch()
            self._active_controls = ()
            self._progress_scenario_id = None
            if primary_error is not None:
                if cleanup_error is not None:
                    add_exception_notes(primary_error, "android_n11_cleanup", cleanup_error)
                if scratch_error is not None:
                    add_exception_notes(primary_error, "android_n11_scratch_cleanup", scratch_error)
            elif cleanup_error is not None:
                if scratch_error is not None:
                    add_exception_notes(cleanup_error, "android_n11_scratch_cleanup", scratch_error)
                self._collect_functional_failure_diagnostics(
                    cleanup_error,
                    "android.n11-consent-grant-selection",
                    cleanup_deadline,
                )
                raise cleanup_error
            elif scratch_error is not None:
                self._collect_functional_failure_diagnostics(
                    scratch_error,
                    "android.n11-consent-grant-selection",
                    cleanup_deadline,
                )
                raise scratch_error

        self._install_fresh_apk_pair(cleanup_deadline)
        if selection is None:
            raise ScenarioExecutionError("ANDROID_CONSENT_N11_OBSERVATION_MISSING")
        return {"passed": True, **dict(selection)}

    def run_auto_recovery_stop(
        self, *, deadline: float | None = None
    ) -> dict[str, object]:
        """Run rendered Auto recovery and stop through the shared core journey.

        The native stop assertions are carried separately from the stable
        Android observation schema. The normal core-connection assertion IDs
        and adapter measurements still come directly from their shared
        scenario definition.
        """

        if self.ui_mode != "gui-auto":
            raise ScenarioExecutionError("ANDROID_AUTO_RECOVERY_STOP_REQUIRES_GUI_AUTO")
        if self.app_apk is None or self.test_companion_apk is None:
            raise ScenarioExecutionError("ANDROID_CONSENT_APK_PATHS_REQUIRED")

        core_scenario = select_scenarios(
            scenario_ids=["functional.core-connection"]
        )[0]
        steps = tuple(
            ScenarioStep(
                id=step.id,
                operation=step.operation,
                timeout_seconds=(
                    _AUTO_RECOVERY_STOP_DISCONNECT_SECONDS
                    if step.operation == "disconnect"
                    else step.timeout_seconds
                ),
            )
            for step in core_scenario.steps
        )
        scenario = ScenarioDefinition(
            id=core_scenario.id,
            steps=steps,
            assertion_ids=core_scenario.assertion_ids,
            max_duration_seconds=int(_AUTO_RECOVERY_STOP_MAX_SECONDS),
        )
        started = time.monotonic()
        run_deadline = started + _AUTO_RECOVERY_STOP_MAX_SECONDS
        if deadline is not None:
            run_deadline = min(run_deadline, deadline)
        cleanup_reserve = min(
            _MAX_CLEANUP_RESERVE_SECONDS,
            max(
                _MIN_CLEANUP_RESERVE_SECONDS,
                _AUTO_RECOVERY_STOP_MAX_SECONDS * _CLEANUP_RESERVE_FRACTION,
            ),
        )
        work_deadline = run_deadline - cleanup_reserve
        if work_deadline <= started + 30.0:
            raise ScenarioExecutionError(
                "HOSTED_LANE_DEADLINE_EXCEEDED_BEFORE_ANDROID_AUTO_RECOVERY_STOP"
            )

        connection = ConnectionIdentity(index=0, protocol="AUTO")
        self._connections = (connection,)
        self._selected_connection = connection
        self._progress_scenario_id = _AUTO_RECOVERY_STOP_CASE
        device_files: list[str] = []
        primary_error: BaseException | None = None
        observation: AndroidProfileObservation | None = None
        native_case_facts: dict[str, object] = {}
        try:
            self._install_fresh_apk_pair(work_deadline)
            observation = self._execute_phase(
                scenario,
                steps,
                work_deadline,
                device_files,
                test_case=_AUTO_RECOVERY_STOP_CASE,
                native_case_facts=native_case_facts,
            )
            self._validate_observation_identity(observation)
            self._validate_gui_observation(scenario, observation, steps=steps)
            observations = self._observations(observation)
            assertions = evaluate_assertions(
                core_scenario.assertion_ids, observations
            )
            failed_assertions = [item.id for item in assertions if not item.passed]
            if failed_assertions:
                failure = ScenarioExecutionError(
                    "ANDROID_AUTO_RECOVERY_STOP_CORE_ASSERTIONS_FAILED"
                )
                failure.add_note("failed_assertions=" + ",".join(failed_assertions))
                raise failure
            self._last_observation = observation
            return {
                "case_id": _AUTO_RECOVERY_STOP_CASE,
                "scenario_id": core_scenario.id,
                "passed": True,
                "assertions": [item.to_dict() for item in assertions],
                "observations": observations,
                "native_case_facts": native_case_facts,
            }
        except BaseException as error:
            primary_error = error
            self._collect_functional_failure_diagnostics(
                error,
                _AUTO_RECOVERY_STOP_CASE,
                run_deadline,
            )
            raise
        finally:
            cleanup_error = self._cleanup_device(
                tuple(device_files), run_deadline
            )
            scratch_error = self._cleanup_local_scratch()
            fixture_error: BaseException | None = None
            if self._subscription_fixture is not None:
                try:
                    self._subscription_fixture.close()
                except BaseException as error:
                    fixture_error = error
                finally:
                    self._subscription_fixture = None
            self._active_controls = ()
            self._progress_scenario_id = None
            self._selected_connection = None
            self._connections = ()
            cleanup_failures = tuple(
                error
                for error in (cleanup_error, scratch_error, fixture_error)
                if error is not None
            )
            if primary_error is not None:
                for index, error in enumerate(cleanup_failures):
                    add_exception_notes(
                        primary_error,
                        f"android_auto_recovery_stop_cleanup_{index + 1}",
                        error,
                    )
            elif cleanup_failures:
                cleanup_failure = cleanup_failures[0]
                for index, error in enumerate(cleanup_failures[1:], start=2):
                    add_exception_notes(
                        cleanup_failure,
                        f"android_auto_recovery_stop_cleanup_{index}",
                        error,
                    )
                self._collect_functional_failure_diagnostics(
                    cleanup_failure,
                    _AUTO_RECOVERY_STOP_CASE,
                    run_deadline,
                )
                raise cleanup_failure

    def _install_fresh_apk_pair(self, deadline: float) -> None:
        """Reset VPN consent by reinstalling this run's exact APK pair."""

        if self.app_apk is None or self.test_companion_apk is None:
            raise ScenarioExecutionError("ANDROID_CONSENT_APK_PATHS_REQUIRED")
        for package in ("com.dobby.vpn.test", _PACKAGE_NAME):
            self._adb(
                ("uninstall", package),
                min(30.0, _remaining(deadline, "ANDROID_N11_APK_UNINSTALL_TIMEOUT")),
                "ANDROID_N11_APK_UNINSTALL_FAILED",
            )
        for package, apk, install_args in (
            (_PACKAGE_NAME, self.app_apk, ("install", "--no-incremental", "-r", "-t")),
            (
                "com.dobby.vpn.test",
                self.test_companion_apk,
                ("install", "--no-incremental", "-r", "-t"),
            ),
        ):
            self._adb(
                (*install_args, str(apk)),
                min(45.0, _remaining(deadline, "ANDROID_N11_APK_INSTALL_TIMEOUT")),
                "ANDROID_N11_APK_INSTALL_FAILED",
            )
            installed = self._adb(
                ("shell", "pm", "path", package),
                min(15.0, _remaining(deadline, "ANDROID_N11_APK_VERIFY_TIMEOUT")),
                "ANDROID_N11_APK_VERIFY_FAILED",
            )
            if not installed.stdout_text.strip().startswith("package:"):
                failure = ScenarioExecutionError("ANDROID_N11_APK_VERIFY_FAILED")
                _append_command_result_notes(failure, installed)
                raise failure
        # A fresh install starts a new app process and invalidates any queued
        # first-process import from the uninstalled app pair. Keep the
        # adapter-owned saved-source verification result; it was observed
        # against this candidate and fixture before the reinstall.
        self._process_cold_import_queued = False

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
        return _CORE_CAPABILITIES | frozenset({Capability.PROCESS_LOSS})

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
            if execution_error is not None:
                self._collect_functional_failure_diagnostics(
                    execution_error,
                    scenario.id,
                    cleanup_deadline,
                )
            cleanup_error = self._cleanup_device(tuple(device_files), cleanup_deadline)
            scratch_error = self._cleanup_local_scratch()
            self._active_controls = ()
            if execution_error is not None:
                if cleanup_error is not None:
                    add_exception_notes(execution_error, "android_cleanup", cleanup_error)
                if scratch_error is not None:
                    add_exception_notes(execution_error, "android_scratch_cleanup", scratch_error)
            elif cleanup_error is not None:
                if scratch_error is not None:
                    add_exception_notes(cleanup_error, "android_scratch_cleanup", scratch_error)
                self._collect_functional_failure_diagnostics(
                    cleanup_error,
                    scenario.id,
                    cleanup_deadline,
                )
                raise cleanup_error
            elif scratch_error is not None:
                self._collect_functional_failure_diagnostics(
                    scratch_error,
                    scenario.id,
                    cleanup_deadline,
                )
                raise scratch_error

    def _collect_functional_failure_diagnostics(
        self,
        primary: BaseException,
        scenario_id: str,
        deadline: float,
    ) -> None:
        """Retain the app-owned logs and full Logcat after a failed scenario."""

        runner = getattr(self, "runner", None)
        raw_directory = getattr(runner, "raw_directory", None)
        if not isinstance(raw_directory, (Path, str)):
            primary.add_note(
                "ANDROID_DIAGNOSTIC_RETENTION_UNAVAILABLE: runner has no raw directory"
            )
            return
        try:
            destination = Path(raw_directory)
            _ensure_directory(destination)
        except BaseException as error:
            add_exception_notes(primary, "android_diagnostic_retention", error)
            return

        self._diagnostic_collection_sequence = (
            getattr(self, "_diagnostic_collection_sequence", 0) + 1
        )
        scenario_token = re.sub(r"[^A-Za-z0-9_-]+", "-", scenario_id).strip("-") or "unknown"
        connection = getattr(self, "_selected_connection", None)
        if connection is None:
            connection_token = "unselected"
        else:
            connection_token = (
                f"c{connection.index}-{re.sub(r'[^A-Za-z0-9_-]+', '-', connection.protocol).strip('-')}"
            )
        stem = (
            f"android-functional-{getattr(self, 'ui_mode', 'unknown')}-"
            f"{self._diagnostic_collection_sequence:02d}-"
            f"{connection_token}-{scenario_token}"
        )
        sources = (
            (
                "go-app-logs",
                "ANDROID_GO_APP_LOG_COLLECTION_FAILED",
                ("shell", "-T", "cat", _GO_DIAGNOSTIC_PATH),
                "go_app_logs.jsonl",
                True,
            ),
            (
                "native-logs",
                "ANDROID_NATIVE_LOG_COLLECTION_FAILED",
                ("shell", "-T", "cat", _NATIVE_DIAGNOSTIC_PATH),
                "native_logs.jsonl",
                True,
            ),
            *((label, code, tuple(command), filename, nonempty)
              for code, label, command, filename, nonempty in retained_log_sources()),
            (
                "logcat",
                "ANDROID_LOGCAT_COLLECTION_FAILED",
                ("shell", "logcat", "-d", "-v", "raw"),
                "logcat.txt",
                False,
            ),
        )
        for label, failure_code, command, filename, nonempty in sources:
            output_path = destination / f"{stem}-{filename}"
            stderr_path = output_path.with_name(f"{output_path.name}.stderr.log")
            try:
                result = self._adb(
                    command,
                    min(_remaining(deadline, "ANDROID_DIAGNOSTIC_TIMEOUT"), 5.0),
                    failure_code,
                    allow_nonzero=True,
                )
                if (failure_code == "ANDROID_RETAINED_LOG_COLLECTION_FAILED"
                        and result.returncode == OPTIONAL_MISSING
                        and not result.stdout and not result.stderr):
                    continue
                output_path.write_bytes(result.stdout)
                stderr_path.write_bytes(result.stderr)
                if result.returncode != 0:
                    failure = ScenarioExecutionError(failure_code)
                    failure.add_note(f"command_returncode={result.returncode}")
                    failure.stdout = result.stdout
                    failure.stderr = result.stderr
                    raise failure
                if nonempty and not result.stdout:
                    failure = ScenarioExecutionError(
                        f"{failure_code}: app diagnostic file is empty"
                    )
                    failure.stdout = result.stdout
                    failure.stderr = result.stderr
                    raise failure
            except BaseException as error:
                add_exception_notes(primary, f"android_{label}_collection", error)

    def _execute_phase(
        self,
        scenario: ScenarioDefinition,
        steps: tuple[ScenarioStep, ...],
        deadline: float,
        device_files: list[str],
        *,
        preserve_active: bool = False,
        test_case: str | None = None,
        native_case_facts: dict[str, object] | None = None,
        saved_source_restore_only: bool = False,
        saved_source_restore_preverified: bool = False,
    ) -> AndroidProfileObservation:
        configure_step = next(
            (step for step in steps if step.operation == "configure"), None
        )
        saved_source_restore_preverified = saved_source_restore_preverified or (
            configure_step is not None
            and getattr(self, "_saved_source_restore_preverified", False)
        )
        run_saved_source_restore_phase = (
            self.ui_mode == "gui-auto"
            and test_case is None
            and not preserve_active
            and not saved_source_restore_only
            and not saved_source_restore_preverified
            and not getattr(self, "_process_cold_import_queued", False)
            and configure_step is not None
        )
        if run_saved_source_restore_phase:
            restored = self._execute_phase(
                scenario,
                (configure_step,),
                deadline,
                device_files,
                saved_source_restore_only=True,
            )
            self._validate_observation_identity(restored)
            self._validate_gui_observation(scenario, restored, steps=(configure_step,))
            if not restored.configured or restored.connected or not restored.gui_auto_verified:
                raise ScenarioExecutionError(
                    "ANDROID_SAVED_SOURCE_RESTORE_PREPHASE_INVALID"
                )
            self._saved_source_restore_preverified = True
            saved_source_restore_preverified = True
        command_file, profile_name, output_name = self._write_command(
            scenario,
            steps=steps,
            preserve_active=preserve_active,
            test_case=test_case,
            saved_source_restore_only=saved_source_restore_only,
            saved_source_restore_preverified=saved_source_restore_preverified,
        )
        progress_name = self._progress_name(command_file)
        device_files.extend(
            (
                command_file.name,
                f"{command_file.name}.tmp",
                output_name,
                progress_name,
                f"{progress_name}.tmp",
            )
        )
        for control_file, _, _ in self._active_controls:
            device_files.extend(
                (control_file, f"{control_file}.ready", f"{control_file}.tmp")
            )
        if self.ui_mode == "protocol-matrix":
            device_files.extend((profile_name, f"{profile_name}.tmp"))
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
            command_file.name,
            deadline,
            preserve_active=preserve_active,
            output_name=output_name,
            progress_name=progress_name,
            process_cold_import_url=(
                self._subscription_fixture.url
                if self.ui_mode == "gui-auto"
                and self._subscription_fixture is not None
                and json.loads(command_bytes.decode("utf-8")).get("process_cold_import") is True
                else None
            ),
        )
        if (
            not _instrumentation_succeeded(instrument)
        ):
            failure = _instrumentation_failure(instrument)
            try:
                output = self._adb(
                    ("shell", "-T", "cat", f"{_APP_FILES}/{output_name}"),
                    _remaining(deadline, "ANDROID_OBSERVATION_TIMEOUT"),
                    "ANDROID_OBSERVATION_UNAVAILABLE",
                )
                emit_streams("android-observation", output.stdout, output.stderr)
            except BaseException as collection_error:
                add_exception_notes(
                    failure,
                    "android_observation_collection",
                    collection_error,
                )
            raise failure
        output = self._adb(
            ("shell", "-T", "cat", f"{_APP_FILES}/{output_name}"),
            _remaining(deadline, "ANDROID_OBSERVATION_TIMEOUT"),
            "ANDROID_OBSERVATION_UNAVAILABLE",
        )
        try:
            value = json.loads(output.stdout.decode("utf-8"))
            if test_case is not None:
                if test_case != _AUTO_RECOVERY_STOP_CASE:
                    raise ScenarioExecutionError("ANDROID_NATIVE_CASE_UNSUPPORTED")
                if not isinstance(value, Mapping) or value.get("test_case") != test_case:
                    raise ScenarioExecutionError(
                        "ANDROID_AUTO_RECOVERY_STOP_FACTS_MISSING"
                    )
            observation = AndroidProfileObservation.from_mapping(
                value, expected_source_sha=self.source_sha
            )
            if observation.error_code is not None:
                raise ScenarioExecutionError(observation.error_code)
            if test_case is not None:
                if native_case_facts is None:
                    raise ScenarioExecutionError(
                        "ANDROID_AUTO_RECOVERY_STOP_FACTS_UNAVAILABLE"
                    )
                native_case_facts.update(
                    self._validated_auto_recovery_stop_facts(
                        value.get("native_case_facts"),
                        command_file.name,
                    )
                )
        except UnicodeDecodeError as error:
            raise ScenarioExecutionError("ANDROID_OBSERVATION_ENCODING_INVALID") from error
        except json.JSONDecodeError as error:
            raise ScenarioExecutionError("ANDROID_OBSERVATION_JSON_INVALID") from error
        except AndroidObservationError as error:
            raise ScenarioExecutionError(_observation_error_code(error)) from error
        return observation

    def _validated_auto_recovery_stop_facts(
        self,
        value: object,
        command_name: str,
    ) -> dict[str, object]:
        """Validate private seam assertions and retain their rendered frames."""

        if not isinstance(value, Mapping) or value.get("case_id") != _AUTO_RECOVERY_STOP_CASE:
            raise ScenarioExecutionError("ANDROID_AUTO_RECOVERY_STOP_FACTS_INVALID")
        for name in (
            "passed",
            "seam_enabled",
            "seam_armed",
            "arm_after_tunnel_route_and_traffic",
            "recovery_observed",
            "recovery_ui_reconnecting",
            "main_stop_visible",
            "main_stop_enabled",
            "competing_profile_actions_disabled",
            "profile_actions_verified_while_visible",
            "stop_clicked",
            "final_idle",
            "cleanup_complete",
            "recovering_cleared",
            "pending_cleared",
            "active_profile_cleared",
            "vpn_network_absent",
            "no_later_generation",
        ):
            if value.get(name) is not True:
                raise ScenarioExecutionError(
                    f"ANDROID_AUTO_RECOVERY_STOP_FACT_{name.upper()}_INVALID"
                )
        for name in (
            "initial_generation",
            "recovery_generation",
            "final_generation",
            "recovery_hold_sample_count",
            "recovery_poll_interval_ms",
            "competing_profile_action_count",
            "final_stable_sample_count",
        ):
            item = value.get(name)
            if not isinstance(item, int) or isinstance(item, bool) or item <= 0:
                raise ScenarioExecutionError(
                    f"ANDROID_AUTO_RECOVERY_STOP_FACT_{name.upper()}_INVALID"
                )
        if not (
            value["recovery_generation"] > value["initial_generation"]
            and value["final_generation"] == value["recovery_generation"]
            and value["recovery_hold_sample_count"] >= 10
            and value["recovery_poll_interval_ms"] == 100
            and value["competing_profile_action_count"] >= 2
            and value["final_stable_sample_count"] >= 10
        ):
            raise ScenarioExecutionError(
                "ANDROID_AUTO_RECOVERY_STOP_FACTS_INVALID"
            )
        stop_label = value.get("stop_screenshot_label")
        idle_label = value.get("idle_screenshot_label")
        labels = value.get("screenshot_labels")
        if (
            not isinstance(stop_label, str)
            or _ANDROID_RECOVERY_STOP_SCREENSHOT.fullmatch(stop_label) is None
            or not isinstance(idle_label, str)
            or _ANDROID_RECOVERY_IDLE_SCREENSHOT.fullmatch(idle_label) is None
            or not isinstance(labels, list)
            or stop_label not in labels
            or idle_label not in labels
        ):
            raise ScenarioExecutionError(
                "ANDROID_AUTO_RECOVERY_STOP_SCREENSHOTS_INVALID"
            )
        raw_directory = getattr(self.runner, "raw_directory", None)
        if not isinstance(raw_directory, Path):
            raise ScenarioExecutionError("ANDROID_SCRATCH_UNAVAILABLE")
        command_id = Path(command_name).name.removesuffix(".command.json")
        screenshot_root = (
            raw_directory / "screenshots" / "android" / command_id
        )
        screenshot_paths = {
            "recovery_stop": screenshot_root / stop_label,
            "disconnected": screenshot_root / idle_label,
        }
        if any(not path.is_file() for path in screenshot_paths.values()):
            raise AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: auto-recovery Stop frames were not retained"
            )
        facts = dict(value)
        facts["screenshot_paths"] = {
            name: str(path) for name, path in screenshot_paths.items()
        }
        return facts

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
            raise AdapterError("INVALID_RESET_TIMEOUT")
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
            raise AdapterError("INVALID_FINALIZE_TIMEOUT")
        if deadline is not None and deadline <= time.monotonic():
            raise AdapterError("SERVICE_FINALIZE_TIMEOUT")
        if self._subscription_fixture is not None:
            self._subscription_fixture.close()
            self._subscription_fixture = None

    def _run_instrumentation(
        self,
        command_name: str,
        deadline: float,
        *,
        preserve_active: bool = False,
        output_name: str | None = None,
        progress_name: str | None = None,
        process_cold_import_url: str | None = None,
    ) -> CommandResult:
        controls = self._active_controls
        # A rendered GUI invocation must keep the instrumentation boundary
        # live even when the caller did not install a human-facing progress
        # sink: required milestone screenshots are collected from the
        # cumulative progress manifest while the worker runs.
        observe_live = bool(
            controls
            or self._progress_sink
            or (self.ui_mode == "gui-auto" and progress_name is not None)
        )
        if not preserve_active:
            # A preceding real-renderer invocation can leave the app's
            # activity process alive after Android has torn down the
            # instrumentation session.  Starting the next runner against
            # that process can produce a blank surface and leave
            # NativeUiHostedProfileTest waiting until its outer deadline.  The
            # runner has not started yet, so this controller-side stop cannot
            # kill an active instrumentation process.  Preserve the live
            # process deliberately for the first half of process-loss
            # coverage, where --no-restart depends on it.
            self._adb(
                ("shell", "am", "force-stop", _PACKAGE_NAME),
                _remaining(deadline, "ANDROID_COLD_START_TIMEOUT"),
                "ANDROID_COLD_START_FAILED",
            )
            if process_cold_import_url is not None:
                from urllib.parse import quote

                link = "dobbyvpn://import?url=" + quote(process_cold_import_url, safe="")
                start = self._adb(
                    (
                        "shell", "am", "start", "-W", "-a",
                        "android.intent.action.VIEW", "-d", link, "-n", _MAIN_ACTIVITY,
                    ),
                    _remaining(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"),
                    "ANDROID_COLD_IMPORT_LAUNCH_FAILED",
                    allow_nonzero=True,
                )
                if start.returncode != 0 or b"Status: ok" not in start.stdout or b"Complete" not in start.stdout:
                    failure = ScenarioExecutionError("ANDROID_COLD_IMPORT_LAUNCH_FAILED")
                    _append_command_result_notes(failure, start)
                    raise failure
        if preserve_active:
            # The rendered process-loss phase must not inherit the previous
            # scenario's editor/activity state. A preserved Activity
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
            *(("--no-restart",) if preserve_active or process_cold_import_url is not None else ()),
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
        screenshot_failure: BaseException | None = None
        required_screenshot_seen = False
        seen_screenshot_paths: set[str] = set()
        screenshot_history_tuples: dict[
            str, tuple[str, int, str, int, int]
        ] = {}
        progress_tuples: dict[int, tuple[str, str, str]] = {}
        required_milestones: dict[
            tuple[str, str, str], tuple[str, int, str, int, int]
        ] = {}
        failed_milestone_seen = False
        highest_progress_sequence = -1

        def poll_ui_progress() -> None:
            """Read and validate the Android UI progress record."""

            nonlocal last_ui_progress, required_screenshot_seen
            nonlocal failed_milestone_seen, highest_progress_sequence
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
                # A transient marker read is retried while the worker runs;
                # the final required-milestone check below makes permanent
                # absence an explicit collection failure.
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
            progress_tuple = (operation, stage, state)
            prior_progress = progress_tuples.get(sequence)
            if prior_progress is not None and prior_progress != progress_tuple:
                raise AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: duplicate progress "
                    f"sequence {sequence} conflicts with its earlier marker"
                )
            progress_tuples[sequence] = progress_tuple
            if sequence < highest_progress_sequence:
                raise AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: progress sequence moved backwards"
                )
            highest_progress_sequence = max(highest_progress_sequence, sequence)
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
            screenshot_path = value.get("screenshot_path")
            screenshot_label = value.get("screenshot_label")
            screenshot_bytes = value.get("screenshot_bytes")
            screenshot_sha256 = value.get("screenshot_sha256")
            screenshot_width = value.get("screenshot_width")
            screenshot_height = value.get("screenshot_height")
            required_screenshot = (
                self.ui_mode == "gui-auto"
                and progress_name is not None
                and state in {"completed", "observed", "failed"}
                and (
                    state == "failed"
                    or stage in _ANDROID_REQUIRED_RENDERED_STAGES
                    or stage.startswith("post-tap-")
                    or stage.endswith("-state")
                    or stage == "consent-diagnosis"
                )
            )
            current_metadata = (
                screenshot_path,
                screenshot_label,
                screenshot_bytes,
                screenshot_sha256,
                screenshot_width,
                screenshot_height,
            )
            has_current_metadata = any(item is not None for item in current_metadata)
            # The Android driver captures a rendered surface at phase start,
            # before the phase can reach a terminal state.  Once that marker
            # carries a complete validated screenshot, it is a required
            # rendered milestone just like a terminal-state marker.
            if (
                not required_screenshot
                and self.ui_mode == "gui-auto"
                and progress_name is not None
                and has_current_metadata
                and (
                    stage in _ANDROID_REQUIRED_RENDERED_STAGES
                    or stage.startswith("post-tap-")
                    or stage.endswith("-state")
                    or stage == "consent-diagnosis"
                )
            ):
                required_screenshot = True
            if has_current_metadata and not all(
                item is not None for item in current_metadata
            ):
                raise AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot marker "
                    f"{operation}/{stage} has partial metadata"
                )
            if required_screenshot and not all(item is not None for item in current_metadata):
                raise AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: required milestone "
                    f"{operation}/{stage} has no complete screenshot metadata"
                )
            raw_screenshots = value.get("screenshots")
            if raw_screenshots is None:
                raw_screenshots = []
                if any(item is not None for item in current_metadata):
                    raw_screenshots.append({
                        "path": screenshot_path,
                        "label": screenshot_label,
                        "bytes": screenshot_bytes,
                        "sha256": screenshot_sha256,
                        "width": screenshot_width,
                        "height": screenshot_height,
                    })
            if not isinstance(raw_screenshots, list):
                raise AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot history is invalid"
                )
            pulled_screenshots: list[Path] = []
            current_seen = False
            batch_paths: set[str] = set()
            for raw_screenshot in raw_screenshots:
                if not isinstance(raw_screenshot, Mapping):
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot history entry is invalid"
                    )
                screenshot_path = raw_screenshot.get("path")
                screenshot_label = raw_screenshot.get("label")
                screenshot_bytes = raw_screenshot.get("bytes")
                screenshot_sha256 = raw_screenshot.get("sha256")
                screenshot_width = raw_screenshot.get("width")
                screenshot_height = raw_screenshot.get("height")
                if (
                    not isinstance(screenshot_path, str)
                    or not isinstance(screenshot_label, str)
                    or not isinstance(screenshot_bytes, int)
                    or isinstance(screenshot_bytes, bool)
                    or screenshot_bytes <= 8
                    or not isinstance(screenshot_sha256, str)
                    or re.fullmatch(r"[0-9a-f]{64}", screenshot_sha256) is None
                    or not isinstance(screenshot_width, int)
                    or isinstance(screenshot_width, bool)
                    or screenshot_width <= 0
                    or not isinstance(screenshot_height, int)
                    or isinstance(screenshot_height, bool)
                    or screenshot_height <= 0
                ):
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot metadata is invalid"
                    )
                screenshot_match = _ANDROID_SCREENSHOT_PATH.fullmatch(screenshot_path)
                if screenshot_match is None or screenshot_label != screenshot_match.group(1):
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot path/label is invalid"
                    )
                if screenshot_path in batch_paths:
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: duplicate screenshot "
                        f"history entry for {screenshot_path}"
                    )
                batch_paths.add(screenshot_path)
                history_tuple = (
                    screenshot_label,
                    screenshot_bytes,
                    screenshot_sha256,
                    screenshot_width,
                    screenshot_height,
                )
                prior_history = screenshot_history_tuples.get(screenshot_path)
                if prior_history is not None and prior_history != history_tuple:
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: duplicate screenshot "
                        f"history metadata conflicts for {screenshot_path}"
                    )
                screenshot_history_tuples[screenshot_path] = history_tuple
                if screenshot_path == value.get("screenshot_path"):
                    current_seen = True
                if screenshot_path not in seen_screenshot_paths:
                    screenshot = self._pull_rendered_screenshot(
                        command_name,
                        screenshot_path,
                        screenshot_label,
                        deadline,
                        expected_bytes=screenshot_bytes,
                        expected_sha256=screenshot_sha256,
                        expected_width=screenshot_width,
                        expected_height=screenshot_height,
                    )
                    seen_screenshot_paths.add(screenshot_path)
                    pulled_screenshots.append(screenshot)
            if has_current_metadata:
                current_path = value["screenshot_path"]
                current_tuple = (
                    value["screenshot_label"],
                    value["screenshot_bytes"],
                    value["screenshot_sha256"],
                    value["screenshot_width"],
                    value["screenshot_height"],
                )
                if (
                    current_path not in batch_paths
                    or screenshot_history_tuples.get(current_path) != current_tuple
                ):
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: current marker "
                        f"{operation}/{stage} disagrees with screenshot history"
                    )
            if required_screenshot:
                if not current_seen:
                    raise AndroidScreenshotCollectionError(
                        "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: required milestone "
                        f"{operation}/{stage} is absent from cumulative screenshot history"
                    )
                required_screenshot_seen = True
                required_milestones[(operation, stage, state)] = current_tuple
                if state == "failed":
                    failed_milestone_seen = True
            elif state == "failed":
                # Every explicit failure marker is a required diagnostic,
                # regardless of which operation/stage produced it.
                failed_milestone_seen = True
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
                tuple(
                    sorted(
                        (
                            path,
                            *screenshot_history_tuples[path],
                        )
                        for path in screenshot_history_tuples
                    )
                ),
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
            for screenshot in pulled_screenshots:
                self._emit_progress(
                    "native-state",
                    kind="ui-screenshot",
                    platform="android",
                    operation=operation,
                    phase=stage,
                    state=state,
                    progress_sequence=sequence,
                    screenshot_path=str(screenshot),
                )

        def check_worker() -> None:
            nonlocal observation_checked, screenshot_failure
            if screenshot_failure is None:
                try:
                    poll_ui_progress()
                except AndroidScreenshotCollectionError as error:
                    # Keep polling/draining so a later product assertion can
                    # remain primary; attach this collection failure below.
                    screenshot_failure = error
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

        # A short successful instrumentation run can finish before the first
        # polling tick. Read the final cumulative manifest once after the
        # worker has joined so required rendered milestones are not lost just
        # because the producer was fast.
        if screenshot_failure is None:
            try:
                poll_ui_progress()
            except AndroidScreenshotCollectionError as error:
                screenshot_failure = error

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

        if self.ui_mode == "gui-auto" and progress_name is not None:
            surface_seen = any(
                stage in _ANDROID_REQUIRED_RENDERED_STAGES
                and state in {"completed", "observed"}
                for _operation, stage, state in required_milestones
            )
            # Progress files are replaced for every phase, so a short-lived
            # ``surface/completed`` marker can be overwritten before a poll.
            # The cumulative history is the durable marker: a validated
            # ``*-surface.png`` can only be produced by that surface phase.
            surface_seen = surface_seen or any(
                label.endswith("-surface.png")
                for label, *_metadata in screenshot_history_tuples.values()
            )
            if worker_failure is None and not surface_seen:
                missing_surface = AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: hosted GUI run "
                    "did not publish the required surface milestone"
                )
                screenshot_failure = screenshot_failure or missing_surface
            if worker_failure is not None and not failed_milestone_seen:
                missing_failure = AndroidScreenshotCollectionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: failed hosted GUI "
                    "run did not publish a failure milestone"
                )
                screenshot_failure = screenshot_failure or missing_failure

        # The cumulative history is populated only after a screenshot has
        # passed path, metadata, byte, digest, and PNG-dimension validation.
        # It is therefore the durable indication that a rendered milestone
        # was retained, even when the producer reported that marker as
        # ``started`` or stored the current frame only inside ``screenshots``.
        if (
            self.ui_mode == "gui-auto"
            and progress_name is not None
            and not required_screenshot_seen
            and screenshot_history_tuples
        ):
            required_screenshot_seen = True

        if (
            self.ui_mode == "gui-auto"
            and progress_name is not None
            and not required_screenshot_seen
        ):
            missing = AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: no required rendered milestone was retained"
            )
            screenshot_failure = screenshot_failure or missing
        if screenshot_failure is not None:
            if primary_error is None:
                primary_error = screenshot_failure
            elif screenshot_failure is not primary_error:
                add_exception_notes(
                    primary_error,
                    "Android rendered screenshot collection also failed",
                    screenshot_failure,
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

    def _pull_rendered_screenshot(
        self,
        command_name: str,
        remote: str,
        label: str,
        deadline: float,
        *,
        expected_bytes: int,
        expected_sha256: str,
        expected_width: int,
        expected_height: int,
    ) -> Path:
        """Pull one frame and verify its exact producer-reported bytes."""

        raw_directory = getattr(self.runner, "raw_directory", None)
        if not isinstance(raw_directory, Path):
            raise AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: hosted raw directory is unavailable"
            )
        match = _ANDROID_SCREENSHOT_PATH.fullmatch(remote)
        if match is None or match.group(1) != label:
            raise AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot path/label mismatch"
            )
        command_id = Path(command_name).name.removesuffix(".command.json")
        directory = raw_directory / "screenshots" / "android" / command_id
        try:
            _ensure_directory(directory)
        except BaseException as error:
            raise AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot directory unavailable"
            ) from error
        destination = directory / label
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise AndroidScreenshotCollectionError(
                "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: screenshot pull deadline expired"
            )
        try:
            result = self._adb(
                ("pull", remote, str(destination)),
                min(2.0, remaining),
                "ANDROID_SCREENSHOT_PULL_FAILED",
                allow_nonzero=True,
            )
        except BaseException as error:
            raise AndroidScreenshotCollectionError(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: pull failed for {label}"
            ) from error
        if result.returncode != 0:
            raise AndroidScreenshotCollectionError(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: pull returned {result.returncode} for {label}"
        )
        try:
            metadata = file_metadata(destination)
            assert_marker_matches(
                metadata,
                bytes_count=expected_bytes,
                sha256_value=expected_sha256,
            )
        except (ScreenshotIntegrityError, OSError) as error:
            try:
                destination.unlink(missing_ok=True)
            except OSError as cleanup_error:
                raise AndroidScreenshotCollectionError(
                    f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: invalid {label}; "
                    f"cleanup also failed: {cleanup_error}"
                ) from error
            raise AndroidScreenshotCollectionError(
                f"ANDROID_UI_SCREENSHOT_COLLECTION_FAILED: invalid {label}: {error}"
            ) from error
        return destination

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
        screenshot_error_code = value.get("screenshot_error_code")
        if screenshot_error_code == "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED":
            secondary = ScenarioExecutionError(screenshot_error_code)
            if isinstance(error_code, str) and error_code:
                primary = ScenarioExecutionError(error_code)
                add_exception_notes(
                    primary,
                    "Android rendered screenshot collection also failed",
                    secondary,
                )
                raise primary
            raise secondary
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
        if operation != "observe_routing_identity":
            raise ScenarioExecutionError("ANDROID_OPERATION_UNSUPPORTED")
        self._routing_proof(
            control_file, deadline, ready=ready, abort=abort
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
                for note in getattr(error, "__notes__", ()):
                    primary.add_note(f"android_routing_secondary_{note}")

        try:
            ready_value = (
                ready
                if ready is not None
                else self._routing_ready(
                    control_file, "ready", deadline, abort=abort
                )
            )
            physical, vpn, ipv4s, port = self._routing_ready_values(ready_value)
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
            # Keep the provider/traffic observation as the primary semantic
            # error, but collect the independent physical-rule and TUN facts
            # before surfacing it. A provider failure alone cannot distinguish
            # VPN capture from a protocol data-plane failure.
            tunneled_ip: str | None = None
            try:
                tunneled_ip = self._assert_routing_blocked(blocked)
            except BaseException as error:
                record(error)

            after_rx: int | None = None
            after_tx: int | None = None
            try:
                after_rx, after_tx = self._routing_counters(vpn, deadline)
            except BaseException as error:
                record(error)

            packets: int | None = None
            try:
                packets = self._routing_rule_counter(
                    physical, ipv4s, port, deadline
                )
            except BaseException as error:
                record(error)

            if after_rx is not None and after_tx is not None:
                rx_delta = after_rx - before_rx
                tx_delta = after_tx - before_tx
                if primary is not None:
                    primary.add_note(f"android_routing_tun_rx_delta={rx_delta}")
                    primary.add_note(f"android_routing_tun_tx_delta={tx_delta}")
                elif rx_delta <= 0 or tx_delta <= 0:
                    record(ScenarioExecutionError(
                        "ANDROID_ROUTING_TRAFFIC_NOT_OBSERVED "
                        f"interface={vpn} before_rx={before_rx} after_rx={after_rx} "
                        f"before_tx={before_tx} after_tx={after_tx}"
                    ))

            if packets is not None:
                if primary is not None:
                    primary.add_note(f"android_routing_rule_packets={packets}")
                elif packets <= 0:
                    record(ScenarioExecutionError(
                        "ANDROID_ROUTING_RULE_NOT_HIT "
                        f"interface={physical} destinations={','.join(ipv4s)} "
                        f"port={port} packets={packets}"
                    ))

            if primary is None and tunneled_ip is not None:
                assert after_rx is not None and after_tx is not None
                assert packets is not None
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

        def phase_timeout() -> ScenarioExecutionError:
            failure = ScenarioExecutionError("ANDROID_ROUTING_PHASE_TIMEOUT")
            last_phase = last_value.get("phase") if last_value is not None else None
            failure.add_note(
                "android_routing_phase="
                + (last_phase if isinstance(last_phase, str) else "invalid")
            )
            failure.add_note(f"android_routing_expected_phase={phase}")
            return failure

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
                raise phase_timeout()
            if abort is not None:
                abort()
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise phase_timeout()
            time.sleep(min(0.1, remaining))

    @staticmethod
    def _routing_ready_values(
        ready: Mapping[str, object],
    ) -> tuple[str, str, tuple[str, ...], int]:
        physical = ready.get("physical_interface")
        vpn = ready.get("vpn_interface")
        raw_ipv4s = ready.get("ipv4s")
        raw_port = ready.get("port")
        if (
            not isinstance(physical, str)
            or _ANDROID_INTERFACE.fullmatch(physical) is None
            or not isinstance(vpn, str)
            or _ANDROID_INTERFACE.fullmatch(vpn) is None
            or physical == vpn
            or not isinstance(raw_ipv4s, list)
            or not raw_ipv4s
            or not isinstance(raw_port, int)
            or isinstance(raw_port, bool)
            or not 1 <= raw_port <= 65535
        ):
            raise AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_READY_INVALID", ready
            )
        addresses: list[str] = []
        for raw_ipv4 in raw_ipv4s:
            if not isinstance(raw_ipv4, str):
                raise AndroidAdapter._routing_observation_failure(
                    "ANDROID_ROUTING_READY_INVALID", ready
                )
            try:
                address = ipaddress.ip_address(raw_ipv4)
            except ValueError as error:
                failure = AndroidAdapter._routing_observation_failure(
                    "ANDROID_ROUTING_READY_INVALID", ready
                )
                raise failure from error
            if address.version != 4 or str(address) in addresses:
                raise AndroidAdapter._routing_observation_failure(
                    "ANDROID_ROUTING_READY_INVALID", ready
                )
            addresses.append(str(address))
        return physical, vpn, tuple(addresses), raw_port

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

        self._cleanup_routing_chain(deadline)

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
            raise AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_DIRECT_NOT_BLOCKED", value
            )
        if not isinstance(vpn, Mapping):
            raise AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
        provider_error = vpn.get("error_code")
        if provider_error == "ANDROID_NETWORK_REQUEST_FAILED":
            failure = AndroidAdapter._routing_observation_failure(
                provider_error, value
            )
            detail = vpn.get("error_detail")
            if isinstance(detail, str) and detail:
                failure.add_note("android_routing_vpn_error_detail:\n" + detail)
            raise failure
        if isinstance(provider_error, str) and provider_error in {
            "ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED",
            "ANDROID_NETWORK_PROBE_PROVIDER_FAILED",
            "ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID",
            "ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID",
            "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN",
        }:
            raise AndroidAdapter._routing_observation_failure(
                provider_error, value
            )
        status = vpn.get("status")
        body = vpn.get("body")
        if (
            isinstance(status, bool)
            or not isinstance(status, int)
            or not 200 <= status < 300
            or not isinstance(body, str)
        ):
            raise AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
        try:
            return _parse_external_ip(body)
        except ScenarioExecutionError as error:
            failure = AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_VPN_INVALID", value
            )
            raise failure from error

    @staticmethod
    def _assert_routing_recovery(value: Mapping[str, object]) -> str:
        direct = value.get("direct")
        if not isinstance(direct, Mapping):
            raise AndroidAdapter._routing_observation_failure(
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
            raise AndroidAdapter._routing_observation_failure(
                "ANDROID_ROUTING_RECOVERY_INVALID", value
            )
        try:
            return _parse_external_ip(body)
        except ScenarioExecutionError as error:
            failure = AndroidAdapter._routing_observation_failure(
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
        test_case: str | None = None,
        saved_source_restore_only: bool = False,
        saved_source_restore_preverified: bool = False,
    ) -> tuple[Path, str, str]:
        if test_case is not None and (
            test_case != _AUTO_RECOVERY_STOP_CASE or self.ui_mode != "gui-auto"
        ):
            raise ScenarioExecutionError("ANDROID_NATIVE_CASE_UNSUPPORTED")
        if saved_source_restore_only and (
            self.ui_mode != "gui-auto"
            or test_case is not None
            or saved_source_restore_preverified
            or steps is None
            or len(steps) != 1
            or steps[0].operation != "configure"
        ):
            raise ScenarioExecutionError("ANDROID_SAVED_SOURCE_RESTORE_PHASE_INVALID")
        if saved_source_restore_preverified and (
            self.ui_mode != "gui-auto" or test_case is not None
        ):
            raise ScenarioExecutionError("ANDROID_SAVED_SOURCE_RESTORE_PHASE_INVALID")
        self._active_controls = ()
        raw_directory = getattr(self.runner, "raw_directory", None)
        if not isinstance(raw_directory, Path):
            raise ScenarioExecutionError("ANDROID_SCRATCH_UNAVAILABLE")
        try:
            _ensure_directory(raw_directory)
        except AdapterError as error:
            failure = ScenarioExecutionError(error.code)
            add_exception_notes(
                failure,
                "android-command",
                error,
            )
            raise failure from error
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
        subscription_url = None
        subscription_control_url = None
        subscription_control_key = None
        subscription_control_ca_pem = None
        if self.ui_mode == "gui-auto":
            if self._subscription_fixture is None:
                from torturer_runner.subscription_fixture import SubscriptionFixture
                self._subscription_fixture = SubscriptionFixture(self.profile, self.profile.parent / "android-subscription-fixture", "android", adb=[str(self.adb)])
                self._subscription_fixture.start()
            subscription_url = self._subscription_fixture.url
            subscription_control_url = self._subscription_fixture.control_url
            subscription_control_key = self._subscription_fixture.control_key
            try:
                subscription_control_ca_pem = self._subscription_fixture.certificate.read_text(
                    encoding="ascii"
                )
            except (OSError, UnicodeDecodeError) as error:
                raise ScenarioExecutionError(
                    "ANDROID_SUBSCRIPTION_FIXTURE_CA_UNAVAILABLE"
                ) from error
        command = {
            "subscription_url": subscription_url,
            "subscription_control_url": subscription_control_url,
            "subscription_control_key": subscription_control_key,
            "subscription_control_ca_pem": subscription_control_ca_pem,
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
        if test_case is not None:
            command["test_case"] = test_case
        if saved_source_restore_only:
            command["saved_source_restore_only"] = True
        if saved_source_restore_preverified:
            command["saved_source_restore_preverified"] = True
        process_cold_import = (
            self.ui_mode == "gui-auto"
            and test_case is None
            and not saved_source_restore_only
            and not self._process_cold_import_queued
            and any(operation.get("operation") == "configure" for operation in operations)
        )
        if process_cold_import:
            command["process_cold_import"] = True
            if self._subscription_fixture is None:
                raise ScenarioExecutionError("ANDROID_SUBSCRIPTION_FIXTURE_UNAVAILABLE")
            command["process_cold_import_request_count"] = (
                self._subscription_fixture.control_stats()["subscription_gets"]
            )
        if self.ui_mode == "protocol-matrix":
            command["profile_file"] = profile_name
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
        if process_cold_import:
            self._process_cold_import_queued = True
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
        except AdapterError as error:
            failure = ScenarioExecutionError(error.code)
            failure.stdout = error.stdout
            failure.stderr = error.stderr
            raise failure from error
        if result.timed_out:
            failure = ScenarioExecutionError("ANDROID_COMMAND_TIMEOUT")
            failure.stdout = result.stdout
            failure.stderr = result.stderr
            _append_command_result_notes(failure, result)
            raise failure
        if result.returncode != 0 and not allow_nonzero:
            failure = ScenarioExecutionError(failure_code)
            failure.stdout = result.stdout
            failure.stderr = result.stderr
            _append_command_result_notes(failure, result)
            raise failure
        return result

    def _cleanup_routing_chain(self, deadline: float) -> None:
        """Remove only the dedicated qualification chain and its jump.

        Inventory first so an absent chain never produces noisy, expected
        iptables errors.  The final inventory remains the fail-closed proof
        that no qualification-owned rule survived.  No unrelated rule is
        inspected for mutation or removed.
        """
        primary: ScenarioExecutionError | None = None
        inventory = self._adb(
            ("shell", "iptables", "-S"),
            _cleanup_timeout(deadline),
            "ANDROID_ROUTING_RULE_CLEANUP_FAILED",
            allow_nonzero=True,
        )
        if inventory.returncode != 0:
            failure = ScenarioExecutionError(
                "ANDROID_ROUTING_RULE_CLEANUP_FAILED"
            )
            _append_command_result_notes(failure, inventory)
            raise failure

        lines = [line.split() for line in inventory.stdout_text.splitlines()]
        jump = ["-A", "OUTPUT", "-j", ROUTING_RULE_CHAIN]
        jump_count = sum(tokens == jump for tokens in lines)
        chain_exists = any(
            tokens[:2] == ["-N", ROUTING_RULE_CHAIN]
            or tokens[:2] == ["-A", ROUTING_RULE_CHAIN]
            for tokens in lines
        )
        commands: list[tuple[str, ...]] = [
            ("-D", "OUTPUT", "-j", ROUTING_RULE_CHAIN),
        ] * jump_count
        if chain_exists:
            commands.extend((
                ("-F", ROUTING_RULE_CHAIN),
                ("-X", ROUTING_RULE_CHAIN),
            ))

        for arguments in commands:
            result = self._adb(
                ("shell", "iptables", *arguments),
                _cleanup_timeout(deadline),
                "ANDROID_ROUTING_RULE_CLEANUP_FAILED",
                allow_nonzero=True,
            )
            if result.returncode != 0:
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
