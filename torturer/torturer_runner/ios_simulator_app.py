"""Build and exercise the public Dobby iOS Simulator app."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from functools import partial
import json
import plistlib
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import traceback
import uuid
from typing import Callable, Protocol, Sequence, TypeVar

from torturer_runner.diagnostics import (
    add_exception_notes,
    add_stream_notes,
    emit_streams,
    output_text,
)
from torturer_runner.process_capture import exception_output, run_finite_capture
from bounded_process import terminate_process_group
from torturer_runner.ios_simulator import (
    IOSSimulatorContractError,
    SimulatorApp,
    iphonesimulator_sdk_version_command,
    simctl_boot_command,
    simctl_bootstatus_command,
    simctl_create_command,
    simctl_delete_command,
    simctl_get_app_container_command,
    simctl_install_command,
    simctl_terminate_command,
    expected_subscription_fixture_gets,
    required_ui_test_cases,
    ui_test_selection,
    xcodebuild_ui_test_without_building_command,
)
from torturer_runner.screenshot_artifacts import (
    assert_files_identical,
    png_metadata,
)


_RUNTIME = re.compile(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+(?:-\d+)*)\Z")
_SDK_VERSION = re.compile(r"\A\s*(\d+)\.(\d+)(?:\.\d+)?\s*\Z")
_PROJECT_PATH = Path("ui/apple/ios/iosApp.xcodeproj")
_CONFIGURATION = "Release"
_APP_PRODUCT = "Dobby-Vpn-Simulator.app"
_BUNDLE_IDENTIFIER = "vpn.dobby.app"
_DEFAULT_ARCHITECTURE = "arm64"
_SUPPORTED_ARCHITECTURES = frozenset(("arm64", "amd64"))
_SIMULATOR_PREFERENCES_FILE = "com.apple.iphonesimulator.plist"
_HARDWARE_KEYBOARD_PREFERENCE = "ConnectHardwareKeyboard"
_StageResult = TypeVar("_StageResult")
MAX_RUN_SECONDS = 30 * 60
CLEANUP_RESERVE_SECONDS = 120
DEFAULT_COMMAND_TIMEOUT_SECONDS = 300
IOS_NATIVE_UI_BUILD_TIMEOUT_SECONDS = 10 * 60
# XCTest products are built during preparation; give their no-build run the
# lane's functional allowance and leave the cleanup reserve to RunBudget.
IOS_UI_TEST_TIMEOUT_SECONDS = MAX_RUN_SECONDS - CLEANUP_RESERVE_SECONDS
COMMAND_TERMINATION_GRACE_SECONDS = 15
# A timed-out command can use one grace window to stop its process group, then
# the remaining reserve to drain inherited pipes and reap the direct child.
# Keep this outside each operation timeout so RunBudget retains its cleanup
# reserve.
COMMAND_TERMINATION_RESERVE_SECONDS = 3 * COMMAND_TERMINATION_GRACE_SECONDS

# These are deliberately stage-specific.  The old contract gave every
# command the same five-minute limit, which made an XCTest hang
# indistinguishable from a Simulator boot or install problem.  The values are
# upper bounds, not retry budgets: a timed-out stage fails once and cleanup
# keeps the original stage/error visible.
STAGE_TIMEOUT_SECONDS = {
    "list-devices": 30,
    "read-sdk-version": 30,
    "read-hardware-keyboard": 10,
    "write-hardware-keyboard": 10,
    "open-simulator": 30,
    "toggle-hardware-keyboard": 10,
    "restore-hardware-keyboard": 15,
    "boot": 120,
    # An erased iOS 26 Simulator can spend several minutes in its normal
    # one-time Data Migration before bootstatus reports ready. Keep this a
    # single bounded wait; the lane budget still reserves cleanup time.
    "bootstatus": 360,
    "create-simulator": 60,
    "delete-simulator": 60,
    # A slow iOS 26 boot can leave installd busy after bootstatus reports
    # ready. Keep one bounded install attempt inside the lane budget.
    "install": 300,
    "xctest-ui": IOS_UI_TEST_TIMEOUT_SECONDS,
    "get-xctest-test-results": 60,
    "export-xctest-screenshots": 120,
    "terminate": 60,
    "shutdown": 120,
    "package-ios-app": IOS_NATIVE_UI_BUILD_TIMEOUT_SECONDS,
}


def _emit_stage_progress(event: str, stage: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "kind": "dobbyvpn.ios_simulator.progress",
                "event": event,
                "timestamp_utc": datetime.now(timezone.utc)
                .isoformat(timespec="milliseconds")
                .replace("+00:00", "Z"),
                "stage": stage,
                **fields,
            },
            sort_keys=True,
            allow_nan=False,
        ),
        flush=True,
    )


def _timed_stage(stage: str, operation: Callable[[], _StageResult]) -> _StageResult:
    started_at = time.monotonic()
    _emit_stage_progress("stage-start", stage)
    try:
        result = operation()
    except BaseException as error:
        _emit_stage_progress(
            "stage-finish",
            stage,
            duration_seconds=max(0.0, time.monotonic() - started_at),
            status="failed",
            error_type=type(error).__name__,
        )
        raise
    _emit_stage_progress(
        "stage-finish",
        stage,
        duration_seconds=max(0.0, time.monotonic() - started_at),
        status="succeeded",
    )
    return result


class IOSSimulatorAppContractError(RuntimeError):
    """The fixed public Simulator check failed."""


class IOSSimulatorStageError(IOSSimulatorAppContractError):
    """A failure tied to one Simulator lifecycle/build stage.

    ``stage`` is intentionally machine-readable for local result consumers.
    Command output remains available in the error notes and is forwarded to the
    invoking process; command arguments are intentionally not copied.
    """

    def __init__(
        self,
        stage: str,
        detail: str,
        *,
        timeout_seconds: float | None = None,
        elapsed_seconds: float | None = None,
    ) -> None:
        self.stage = stage
        self.timeout_seconds = timeout_seconds
        self.elapsed_seconds = elapsed_seconds
        normalized = detail.strip() or "no command diagnostic"
        timed_out = "timed out" in normalized.lower()
        if timed_out and timeout_seconds is not None:
            elapsed = (
                f", elapsed {elapsed_seconds:.3f}s"
                if elapsed_seconds is not None
                else ""
            )
            message = (
                f"iOS Simulator stage '{stage}' timed out "
                f"(limit {timeout_seconds:g}s{elapsed}): {normalized}"
            )
        else:
            elapsed = (
                f" after {elapsed_seconds:.3f}s"
                if elapsed_seconds is not None
                else ""
            )
            message = f"iOS Simulator stage '{stage}' failed{elapsed}: {normalized}"
        super().__init__(message)


@dataclass
class SimulatorHardwareKeyboardState:
    simulator_udid: str
    was_connected: bool
    changed: bool = False


class RunBudget:
    """Keep functional commands inside a lane deadline with time reserved for cleanup."""

    def __init__(
        self,
        *,
        max_seconds: float = MAX_RUN_SECONDS,
        cleanup_reserve_seconds: float = CLEANUP_RESERVE_SECONDS,
        clock=time.monotonic,
    ) -> None:
        if max_seconds <= 0 or cleanup_reserve_seconds < 0 or cleanup_reserve_seconds >= max_seconds:
            raise ValueError("cleanup reserve must be non-negative and smaller than the run deadline")
        self.max_seconds = float(max_seconds)
        self.cleanup_reserve_seconds = float(cleanup_reserve_seconds)
        self.clock = clock
        self.started_at = clock()

    @property
    def deadline(self) -> float:
        return self.started_at + self.max_seconds

    def operation_timeout(self, requested: float | None = None) -> float:
        remaining = self.deadline - self.clock() - self.cleanup_reserve_seconds
        if remaining <= 0:
            raise IOSSimulatorAppContractError(
                "iOS Simulator lane exhausted its functional budget before cleanup reserve"
            )
        if requested is not None:
            if requested <= 0:
                raise IOSSimulatorAppContractError("iOS command timeout must be positive")
            remaining = min(float(requested), remaining)
        return remaining

    def cleanup_timeout(self) -> float:
        return max(0.0, min(self.cleanup_reserve_seconds, self.deadline - self.clock()))

    def assert_within_deadline(self) -> None:
        if self.clock() > self.deadline:
            raise IOSSimulatorAppContractError("iOS Simulator lane exceeded its strict 1800-second deadline")


@dataclass(frozen=True)
class CommandResult:
    returncode: int
    stdout: str = ""
    stderr: str = ""


class CommandRunner(Protocol):
    def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path | None = None,
        timeout_seconds: float | None = None,
    ) -> CommandResult:
        """Run shell-free and forward each captured stream exactly once."""


def _observe_simctl_timeout(
    process: subprocess.Popen[bytes],
    termination_deadline: float,
    *,
    command: Sequence[str],
    operation_deadline: float,
    observations: list[tuple[str, subprocess.CompletedProcess[bytes] | BaseException | str]],
) -> None:
    try:
        if process.poll() is not None or time.monotonic() + 0.1 < operation_deadline:
            observations.append(("failure_time_caller_skipped", "process exited or failure preceded timeout"))
            return

        pid = process.pid
        observations.append(("failure_time_caller", f"command={' '.join(command[:3])} pid={pid}"))
        started = time.monotonic()
        diagnostic_deadline = min(started + 5.0, termination_deadline - 10.0)
        available = diagnostic_deadline - started
        if available < 3.5:
            observations.append((
                "failure_time_caller_skipped",
                f"insufficient termination time for both observations; available={max(available, 0.0):.3f}s",
            ))
            return

        for label, diagnostic_command, wall_limit in (
            ("failure_time_process_snapshot", ("/bin/ps", "-p", str(pid), "-o", "pid,ppid,pgid,state,etime,command"), min(1.5, available - 2.0)),
            ("failure_time_caller_sample", ("/usr/bin/sample", str(pid), "1", "1", "-file", "/dev/stdout"), 5.0),
        ):
            remaining = min(wall_limit, diagnostic_deadline - time.monotonic())
            if remaining <= 0:
                observations.append((f"{label}_skipped", "diagnostic deadline expired"))
                continue
            cleanup_timeout = min(0.25, remaining / 4)
            try:
                observations.append((label, run_finite_capture(
                    diagnostic_command, timeout_seconds=remaining - cleanup_timeout,
                    termination_grace_seconds=(
                        cleanup_timeout / 2 if label.endswith("_sample") else 0
                    ),
                    cleanup_timeout_seconds=cleanup_timeout,
                )))
            except BaseException as error:
                observations.append((label, error))
    finally:
        terminate_process_group(process, max(termination_deadline - time.monotonic(), 0.0))


class SubprocessCommandRunner:
    """Run shell-free commands with timeouts and process-group cleanup."""

    def run(
        self,
        command: Sequence[str],
        *,
        cwd: Path | None = None,
        timeout_seconds: float | None = None,
    ) -> CommandResult:
        timeout = timeout_seconds if timeout_seconds is not None else DEFAULT_COMMAND_TIMEOUT_SECONDS
        if timeout <= 0:
            raise IOSSimulatorAppContractError("iOS command timeout must be positive")
        arguments = tuple(str(part) for part in command)
        observe_before_termination = arguments[:3] == ("xcrun", "simctl", "install")
        failure_observations: list[
            tuple[str, subprocess.CompletedProcess[bytes] | BaseException | str]
        ] = []
        operation_deadline = time.monotonic() + timeout
        terminator = partial(
            _observe_simctl_timeout,
            command=arguments,
            operation_deadline=operation_deadline,
            observations=failure_observations,
        ) if observe_before_termination else None

        try:
            try:
                completed = run_finite_capture(
                    command,
                    cwd=cwd,
                    timeout_seconds=timeout,
                    terminate=terminator,
                    termination_grace_seconds=COMMAND_TERMINATION_GRACE_SECONDS,
                    cleanup_timeout_seconds=COMMAND_TERMINATION_RESERVE_SECONDS,
                )
            except BaseException as error:
                for label, observation in failure_observations:
                    if isinstance(observation, BaseException):
                        add_exception_notes(error, label, observation)
                    elif isinstance(observation, subprocess.CompletedProcess):
                        add_stream_notes(error, label, observation.stdout, observation.stderr)
                        if observation.returncode != 0:
                            error.add_note(f"{label}_exit_code={observation.returncode}")
                    else:
                        error.add_note(f"{label}={observation}")
                raise
        except OSError as error:
            stdout, stderr = exception_output(error)
            failure = IOSSimulatorAppContractError(
                f"iOS command could not start: {type(error).__name__}"
            )
            add_stream_notes(failure, "command", stdout, stderr)
            _emit_command_streams("ios-command", stdout, stderr, failure)
            for note in getattr(error, "__notes__", ()):
                failure.add_note(f"cleanup_{note}")
            raise failure from error
        except subprocess.TimeoutExpired as error:
            stdout, stderr = exception_output(error)
            failure = IOSSimulatorAppContractError(
                f"iOS command timed out after {timeout:g}s"
            )
            add_stream_notes(failure, "command", stdout, stderr)
            _emit_command_streams("ios-command", stdout, stderr, failure)
            for note in getattr(error, "__notes__", ()):
                failure.add_note(f"cleanup_{note}")
            raise failure from error
        result = CommandResult(
            returncode=completed.returncode if completed.returncode is not None else -1,
            stdout=output_text(completed.stdout),
            stderr=output_text(completed.stderr),
        )
        _emit_command_streams("ios-command", completed.stdout, completed.stderr)
        return result


def _emit_command_streams(
    label: str,
    stdout: bytes,
    stderr: bytes,
    failure: BaseException | None = None,
) -> None:
    try:
        emit_streams(label, stdout, stderr)
    except Exception as forwarding_error:
        if failure is None:
            failure = IOSSimulatorAppContractError("iOS command output could not be forwarded")
            add_stream_notes(failure, "command", stdout, stderr)
            raise_failure = True
        else:
            raise_failure = False
        failure.stdout = stdout
        failure.stderr = stderr
        add_exception_notes(
            failure,
            "output forwarding",
            forwarding_error,
            include_streams=False,
        )
        if raise_failure:
            raise failure from forwarding_error


@dataclass(frozen=True)
class AvailableSimulator:
    udid: str
    name: str
    runtime: str
    device_type_identifier: str = ""
    temporary: bool = False


@dataclass(frozen=True)
class IOSSimulatorAppContract:
    project_relative_path: Path = _PROJECT_PATH
    configuration: str = _CONFIGURATION
    app_product_name: str = _APP_PRODUCT
    bundle_identifier: str = _BUNDLE_IDENTIFIER
    architecture: str = _DEFAULT_ARCHITECTURE

    def __post_init__(self) -> None:
        if self.project_relative_path != _PROJECT_PATH:
            raise IOSSimulatorAppContractError("iOS Simulator project path is fixed by the public contract")
        if self.configuration != _CONFIGURATION:
            raise IOSSimulatorAppContractError("iOS Simulator configuration is fixed")
        if self.app_product_name != _APP_PRODUCT or self.bundle_identifier != _BUNDLE_IDENTIFIER:
            raise IOSSimulatorAppContractError("iOS Simulator app identity is fixed by the public contract")
        if self.architecture not in _SUPPORTED_ARCHITECTURES:
            raise IOSSimulatorAppContractError("Simulator architecture must be arm64 or amd64")

    def app_path(self, work_dir: Path) -> Path:
        return work_dir / "derived-data" / "Build" / "Products" / f"{self.configuration}-iphonesimulator" / self.app_product_name


PUBLIC_IOS_SIMULATOR_APP_CONTRACT = IOSSimulatorAppContract()


def public_ios_simulator_app_contract(architecture: str) -> IOSSimulatorAppContract:
    return IOSSimulatorAppContract(architecture=architecture)


@dataclass(frozen=True)
class IOSSimulatorAppEvidence:
    simulator: AvailableSimulator
    app: SimulatorApp
    # XCTest keeps UI screenshots and failure attachments in this owned
    # result bundle. It is an extra diagnostic output; command streams remain
    # the authoritative process diagnostics.
    result_bundle: Path | None = None
    # The app's native runtime log is retained separately when the app produced
    # one. XCTest attachments never replace this stream.
    native_log: Path | None = None
    native_log_collection_error: str | None = None
    selected_tests: tuple[str, ...] = ()
    required_test_cases: tuple[str, ...] = ()
    xctest_results: dict[str, object] | None = None


def select_available_iphone(
    simctl_devices_json: str,
    sdk_version: str,
) -> AvailableSimulator:
    sdk_version_text = sdk_version.strip() if isinstance(sdk_version, str) else ""
    sdk_match = _SDK_VERSION.fullmatch(sdk_version_text)
    if sdk_match is None:
        raise IOSSimulatorAppContractError(
            "active iphonesimulator SDK version is invalid: "
            f"{sdk_version_text or '<empty>'}"
        )
    sdk_major_minor = (int(sdk_match.group(1)), int(sdk_match.group(2)))
    try:
        devices = json.loads(simctl_devices_json)["devices"]
    except (KeyError, TypeError, ValueError) as error:
        raise IOSSimulatorAppContractError("simctl did not provide a readable device inventory") from error
    if not isinstance(devices, dict):
        raise IOSSimulatorAppContractError("simctl device inventory has an invalid shape")
    candidates: list[tuple[tuple[int, ...], str, str, str, str]] = []
    for runtime, entries in devices.items():
        match = _RUNTIME.fullmatch(runtime) if isinstance(runtime, str) else None
        if match is None or not isinstance(entries, list):
            continue
        version = tuple(int(part) for part in match.group(1).split("-"))
        if version[:2] != sdk_major_minor:
            continue
        for entry in entries:
            if not isinstance(entry, dict) or entry.get("isAvailable") is not True:
                continue
            name, raw_udid = entry.get("name"), entry.get("udid")
            if not isinstance(name, str) or not name.startswith("iPhone"):
                continue
            try:
                udid = simctl_boot_command(raw_udid)[-1]
            except IOSSimulatorContractError:
                continue
            device_type = entry.get("deviceTypeIdentifier", "")
            if not isinstance(device_type, str):
                device_type = ""
            candidates.append((version, name, udid, runtime, device_type))
    if not candidates:
        raise IOSSimulatorAppContractError(
            "no available iPhone Simulator matches active iphonesimulator SDK "
            f"{sdk_version_text}"
        )
    version, name, udid, runtime, device_type = max(candidates, key=lambda item: (item[0], item[1], item[2]))
    del version
    return AvailableSimulator(
        udid=udid, name=name, runtime=runtime,
        device_type_identifier=device_type,
    )


def _active_iphonesimulator_sdk_version(
    runner: CommandRunner,
    *,
    budget: RunBudget,
) -> str:
    result = _require_success(
        runner,
        iphonesimulator_sdk_version_command(),
        "read-sdk-version",
        budget=budget,
    )
    sdk_version = result.stdout.strip()
    if _SDK_VERSION.fullmatch(sdk_version) is None:
        raise IOSSimulatorStageError(
            "read-sdk-version",
            "xcrun returned an invalid active iphonesimulator SDK version: "
            f"{sdk_version or '<empty>'}",
        )
    return sdk_version


def _create_disposable_simulator(
    selected: AvailableSimulator,
    *,
    runner: CommandRunner,
    budget: RunBudget,
) -> AvailableSimulator:
    if not selected.device_type_identifier:
        raise IOSSimulatorStageError(
            "create-simulator",
            "selected iPhone Simulator does not report its device type",
        )
    name = "DobbyVPN Torturer " + uuid.uuid4().hex[:12]
    result = _require_success(
        runner,
        simctl_create_command(name, selected.device_type_identifier, selected.runtime),
        "create-simulator",
        budget=budget,
    )
    output = result.stdout.strip()
    try:
        udid = simctl_boot_command(output)[-1]
    except IOSSimulatorContractError as error:
        raise IOSSimulatorStageError(
            "create-simulator",
            "simctl create did not return a valid Simulator identifier",
        ) from error
    return AvailableSimulator(
        udid=udid,
        name=name,
        runtime=selected.runtime,
        device_type_identifier=selected.device_type_identifier,
        temporary=True,
    )


def _write_disposable_subscription_profile(work_dir: Path) -> Path:
    """Create a synthetic twelve-profile list for the Simulator test client."""
    sections = []
    for index in range(1, 13):
        description = (
            f'Description = "Simulator fixture profile {index}"\n'
            if index < 12
            else ""
        )
        sections.append(
            "[[Outline]]\n"
            + description
            + 'Server = "192.0.2.10"\n'
            + "Port = 443\n"
            + 'Password = "fixture-only"'
        )
    path = work_dir / "simulator-subscription-profile.toml"
    path.write_text("\n\n".join(sections) + "\n", encoding="utf-8")
    return path


def _subscription_fixture_test_environment(fixture: object) -> dict[str, str]:
    """Supply run-owned URLs; control credentials stay host-side."""
    return {
        "DOBBY_IOS_TEST_SUBSCRIPTION_URL": str(getattr(fixture, "url")),
        "DOBBY_IOS_TEST_SUBSCRIPTION_FAILURE_URL": str(getattr(fixture, "failure_url")),
        "DOBBY_IOS_TEST_FIXTURE_REQUIRED": "1",
        "DOBBY_SIMULATOR_TEST_SEED_STDERR_CAPTURE": "1",
    }


def _requires_subscription_fixture(native_cases: Sequence[str] | None) -> bool:
    """Default XCTest coverage and the fixture case need disposable HTTPS setup."""
    return expected_subscription_fixture_gets(native_cases) > 0


def _assert_subscription_fixture_request_count(
    fixture: object, *, expected_gets: int = 1
) -> dict[str, int]:
    """Check host-owned stats: subscription_gets, in_flight_gets, max_in_flight_gets."""
    if isinstance(expected_gets, bool) or not isinstance(expected_gets, int) or expected_gets < 1:
        raise ValueError("expected fixture GET count must be a positive integer")
    stats = getattr(fixture, "control_stats")()
    if not isinstance(stats, dict):
        raise IOSSimulatorAppContractError(
            "rendered subscription fixture returned invalid request statistics"
        )
    expected = {
        "subscription_gets": expected_gets,
        "in_flight_gets": 0,
        "max_in_flight_gets": 1,
    }
    observed = {key: stats.get(key) for key in expected}
    if observed != expected:
        raise IOSSimulatorAppContractError(
            f"rendered subscription fixture expected {expected_gets} completed "
            f"non-overlapping GETs: {observed}"
        )
    return stats


def xcodebuild_app_command(
    contract: IOSSimulatorAppContract,
    *, work_dir: Path, source_sha: str | None = None,
) -> list[str]:
    command = [
        "/bin/bash", "scripts/package_ios_app.sh", "iossimulator",
        str(contract.app_path(work_dir)), "", contract.architecture,
    ]
    if source_sha is None:
        return command
    return ["/usr/bin/env", f"SOURCE_COMMIT={source_sha}", *command]


def _stage_timeout(
    budget: RunBudget | None,
    stage: str,
    requested: float | None = None,
) -> float:
    value = requested if requested is not None else STAGE_TIMEOUT_SECONDS.get(
        stage, DEFAULT_COMMAND_TIMEOUT_SECONDS
    )
    if budget is None:
        return value
    bounded_with_termination = budget.operation_timeout(
        value + COMMAND_TERMINATION_RESERVE_SECONDS
    )
    return bounded_with_termination - COMMAND_TERMINATION_RESERVE_SECONDS


def _stage_error(
    stage: str,
    error: BaseException | str,
    *,
    timeout_seconds: float | None = None,
    elapsed_seconds: float | None = None,
) -> IOSSimulatorStageError:
    if isinstance(error, IOSSimulatorStageError) and error.stage == stage:
        if elapsed_seconds is not None and error.elapsed_seconds is None:
            error.elapsed_seconds = elapsed_seconds
            error.add_note(f"stage_elapsed_seconds={elapsed_seconds:.3f}")
        return error
    detail = str(error).strip() or type(error).__name__
    wrapped = IOSSimulatorStageError(
        stage,
        detail,
        timeout_seconds=timeout_seconds,
        elapsed_seconds=elapsed_seconds,
    )
    for note in getattr(error, "__notes__", ()):
        wrapped.add_note(note)
    if hasattr(error, "stdout") or hasattr(error, "stderr"):
        add_stream_notes(
            wrapped,
            "command",
            getattr(error, "stdout", None),
            getattr(error, "stderr", None),
        )
    return wrapped


def _require_success(
    runner: CommandRunner,
    command: Sequence[str],
    stage: str,
    *,
    cwd: Path | None = None,
    budget: RunBudget | None = None,
    timeout_seconds: float | None = None,
    bounded_timeout: bool = False,
) -> CommandResult:
    effective_timeout: float | None = timeout_seconds
    started_at = time.monotonic()
    try:
        # Cleanup callers pass a timeout already bounded by the cleanup
        # reserve. Keep the process-stop allowance inside that same window;
        # reapplying ``operation_timeout`` would subtract the reserve twice.
        if bounded_timeout and timeout_seconds is not None:
            effective_timeout = timeout_seconds
            if budget is not None:
                effective_timeout = min(
                    effective_timeout,
                    budget.cleanup_timeout() - COMMAND_TERMINATION_RESERVE_SECONDS,
                )
        else:
            effective_timeout = _stage_timeout(budget, stage, timeout_seconds)
        if effective_timeout <= 0:
            raise IOSSimulatorAppContractError(
                f"iOS Simulator stage '{stage}' has no time remaining"
            )
        result = runner.run(command, cwd=cwd, timeout_seconds=effective_timeout)
    except BaseException as error:
        # Keep the failed lifecycle/build stage unambiguous to callers without
        # copying a command stream into the error or any result file.
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise
        elapsed = max(0.0, time.monotonic() - started_at)
        raise _stage_error(
            stage,
            error,
            timeout_seconds=effective_timeout,
            elapsed_seconds=elapsed,
        ) from error
    if result.returncode:
        failure = IOSSimulatorStageError(
            stage,
            f"exit code {result.returncode}",
            timeout_seconds=effective_timeout,
            elapsed_seconds=max(0.0, time.monotonic() - started_at),
        )
        add_stream_notes(failure, "command", result.stdout, result.stderr)
        raise failure
    return result


def _add_note(failure: BaseException | None, label: str, error: BaseException) -> BaseException:
    if failure is None:
        return error
    add_exception_notes(failure, label, error)
    return failure


def _ios_diagnostics_directory(work_dir: Path) -> Path:
    return work_dir / "diagnostics" / "ios-simulator"


def retain_ios_failure_diagnostic(work_dir: Path, failure: BaseException) -> Path:
    """Write the full setup/test exception so early CI failures have an artifact."""
    diagnostics = _ios_diagnostics_directory(work_dir)
    diagnostics.mkdir(parents=True, exist_ok=True)
    report = diagnostics / "runner-failure.txt"
    report.write_text(
        "".join(traceback.format_exception(type(failure), failure, failure.__traceback__)),
        encoding="utf-8",
    )
    report.chmod(0o600)
    return report


def _xctest_context_matches(test_case: str, context: Sequence[str]) -> bool:
    components = test_case.split("/")
    class_name, method_name = components[-2:]
    joined = " / ".join(context)
    method_matches = re.search(
        rf"(?<![A-Za-z0-9_]){re.escape(method_name)}(?:\(\))?(?![A-Za-z0-9_])",
        joined,
    ) is not None
    class_matches = re.search(
        rf"(?<![A-Za-z0-9_]){re.escape(class_name)}(?![A-Za-z0-9_])",
        joined,
    ) is not None
    return method_matches and class_matches


def _contains_xctest_case_run(node: dict[str, object]) -> bool:
    children = node.get("children", [])
    if not isinstance(children, list):
        raise IOSSimulatorAppContractError(
            "xcresult test-results node children have an invalid shape"
        )
    for child in children:
        if not isinstance(child, dict):
            raise IOSSimulatorAppContractError(
                "xcresult test-results child is not an object"
            )
        if child.get("nodeType") == "Test Case Run" or _contains_xctest_case_run(child):
            return True
    return False


def _xctest_iteration_number(*values: object) -> int | None:
    for value in values:
        if not isinstance(value, str):
            continue
        match = re.search(
            r"(?:repetition|iteration|run)\s*(?:#|:)?\s*(\d+)\b",
            value,
            re.IGNORECASE,
        )
        if match is not None:
            return int(match.group(1))
    return None


def _write_xctest_test_results(raw_results: str, work_dir: Path) -> Path:
    """Retain the complete xcresulttool JSON in the normal iOS diagnostic tree."""
    if not raw_results.strip():
        raise IOSSimulatorAppContractError(
            "xcresulttool returned no iOS XCTest test-results JSON"
        )
    destination_dir = _ios_diagnostics_directory(work_dir)
    destination_dir.mkdir(parents=True, exist_ok=True)
    report = destination_dir / "xctest-test-results.json"
    if report.exists() or report.is_symlink():
        raise IOSSimulatorAppContractError(
            f"iOS XCTest test-results report already exists: {report}"
        )
    try:
        with report.open("xb") as stream:
            stream.write(raw_results.encode("utf-8"))
        report.chmod(0o600)
    except OSError as error:
        raise IOSSimulatorAppContractError(
            f"could not retain complete iOS XCTest test-results JSON: {report}"
        ) from error
    return report


def _validate_xctest_test_results(
    raw_results: str,
    *,
    required_tests: Sequence[str],
    repeat: int,
) -> dict[str, object]:
    """Require every selected XCTest method to pass once in each iteration."""
    if type(repeat) is not int or repeat < 1:
        raise IOSSimulatorAppContractError("XCTest repeat count must be positive")
    try:
        report = json.loads(raw_results)
    except (TypeError, json.JSONDecodeError) as error:
        raise IOSSimulatorAppContractError(
            "xcresulttool returned unreadable iOS XCTest test-results JSON"
        ) from error
    if not isinstance(report, dict) or not isinstance(report.get("testNodes"), list):
        raise IOSSimulatorAppContractError(
            "xcresulttool test-results JSON has no testNodes array"
        )
    expected = tuple(required_tests)
    if not expected or len(set(expected)) != len(expected):
        raise IOSSimulatorAppContractError(
            "selected iOS XCTest methods are empty or duplicated"
        )

    records: dict[str, list[dict[str, object]]] = {test: [] for test in expected}
    aggregate_errors: list[str] = []

    def visit(
        node: object,
        parents: tuple[str, ...],
        current_iteration: int | None = None,
    ) -> None:
        if not isinstance(node, dict):
            raise IOSSimulatorAppContractError(
                "xcresult test-results node is not an object"
            )
        node_type = node.get("nodeType")
        name = node.get("name")
        identifier = node.get("nodeIdentifier")
        iteration = current_iteration
        if node_type == "Repetition":
            iteration = _xctest_iteration_number(name, identifier)
        context = parents + tuple(
            value for value in (identifier, name) if isinstance(value, str)
        )
        result = node.get("result")
        matches: list[str] = []
        if node_type in {"Test Case", "Test Case Run"}:
            matches = [test for test in expected if _xctest_context_matches(test, context)]
            if len(matches) > 1:
                raise IOSSimulatorAppContractError(
                    "xcresult contains an ambiguous selected test result: "
                    f"name={name!r}, nodeIdentifier={identifier!r}, matches={matches!r}"
                )
        if (
            node_type == "Repetition"
            or (node_type in {"Test Case", "Test Case Run"} and matches)
        ) and result is not None:
            if not isinstance(result, str) or result != "Passed":
                aggregate_errors.append(
                    f"{node_type} {name or identifier or '<unnamed>'} result={result!r}"
                )

        children = node.get("children", [])
        if not isinstance(children, list):
            raise IOSSimulatorAppContractError(
                "xcresult test-results node children have an invalid shape"
            )
        if matches and (node_type == "Test Case Run" or (
            node_type == "Test Case" and not _contains_xctest_case_run(node)
        )):
            records[matches[0]].append({
                "result": result if isinstance(result, str) else "unknown",
                "name": name if isinstance(name, str) else "",
                "node_identifier": identifier if isinstance(identifier, str) else "",
                "iteration": iteration if iteration is not None else _xctest_iteration_number(name, identifier),
            })
        for child in children:
            visit(child, context, iteration)

    for node in report["testNodes"]:
        visit(node, ())

    failures: list[str] = aggregate_errors
    for test, runs in records.items():
        if len(runs) != repeat:
            failures.append(
                f"{test}: expected {repeat} test results, found {len(runs)}"
            )
        indexed = [run["iteration"] for run in runs]
        if runs and any(index is not None for index in indexed):
            expected_one_based = list(range(1, repeat + 1))
            expected_zero_based = list(range(repeat))
            normalized = sorted(index for index in indexed if isinstance(index, int))
            if len(normalized) != len(runs) or normalized not in (
                expected_one_based, expected_zero_based,
            ):
                failures.append(
                    f"{test}: repetition indexes are incomplete or duplicated: {indexed!r}"
                )
            else:
                runs.sort(key=lambda run: int(run["iteration"]))
        elif repeat > 1:
            failures.append(
                f"{test}: XCTest did not identify each of {repeat} repetitions"
            )
        elif runs:
            runs[0]["iteration"] = 1
        for index, run in enumerate(runs, start=1):
            if run["result"] != "Passed":
                iteration = run["iteration"]
                failures.append(
                    f"{test} iteration {index if iteration is None else iteration}: "
                    f"result={run['result']!r}"
                )
    if failures:
        raise IOSSimulatorAppContractError(
            "iOS XCTest required results did not all pass: " + "; ".join(failures)
        )

    return {
        "required_tests": [
            {"test": test, "iterations": list(records[test])} for test in expected
        ],
        "repeat": repeat,
        "result": "Passed",
    }


def _retain_xctest_screenshots(
    runner: CommandRunner,
    result_bundle: Path,
    work_dir: Path,
    *,
    budget: RunBudget,
) -> Path:
    """Copy the XCTest's named UI screenshots beside the complete result bundle."""
    diagnostics = _ios_diagnostics_directory(work_dir)
    destination = diagnostics / "ui-screenshots"
    if destination.exists() or destination.is_symlink():
        raise IOSSimulatorAppContractError(
            f"iOS XCTest screenshot destination already exists: {destination}"
        )

    with tempfile.TemporaryDirectory(
        prefix="ios-xcresult-attachments-", dir=work_dir,
    ) as temporary_directory:
        exported = Path(temporary_directory)
        _require_success(
            runner,
            [
                "xcrun", "xcresulttool", "export", "attachments",
                "--path", str(result_bundle),
                "--output-path", str(exported),
            ],
            "export-xctest-screenshots",
            budget=budget,
            timeout_seconds=STAGE_TIMEOUT_SECONDS["export-xctest-screenshots"],
        )

        manifest_path = next(
            (
                candidate for candidate in (
                    exported / "manifest.json",
                    exported / "attachments" / "manifest.json",
                )
                if candidate.is_file() and not candidate.is_symlink()
            ),
            None,
        )
        if manifest_path is None:
            raise IOSSimulatorAppContractError(
                "xcresulttool attachment export did not create manifest.json"
            )
        try:
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
            raise IOSSimulatorAppContractError(
                "xcresulttool attachment manifest is unreadable"
            ) from error
        if not isinstance(manifest, list):
            raise IOSSimulatorAppContractError(
                "xcresulttool attachment manifest has an invalid shape"
            )

        screenshot_dir = manifest_path.parent
        copied: dict[str, int] = {}
        exported_files: set[str] = set()
        for test in manifest:
            attachments = test.get("attachments") if isinstance(test, dict) else None
            if not isinstance(attachments, list):
                raise IOSSimulatorAppContractError(
                    "xcresulttool attachment manifest has an invalid test entry"
                )
            for attachment in attachments:
                if not isinstance(attachment, dict):
                    raise IOSSimulatorAppContractError(
                        "xcresulttool attachment manifest has an invalid attachment entry"
                    )
                human_name = attachment.get("suggestedHumanReadableName")
                if not isinstance(human_name, str) or not human_name.startswith("dobbyvpn-ui-"):
                    continue
                if human_name.lower().endswith(".txt"):
                    # The complete .xcresult retains text diagnostics; this
                    # side export contains only screenshots.
                    continue
                if not human_name.lower().endswith(".png"):
                    raise IOSSimulatorAppContractError(
                        f"named iOS UI screenshot is not a PNG: {human_name}"
                    )
                label_match = re.fullmatch(
                    r"dobbyvpn-ui-([a-z][a-z0-9-]*)_\d+_[0-9A-Fa-f-]+\.png",
                    human_name,
                )
                exported_name = attachment.get("exportedFileName")
                if (
                    label_match is None
                    or not isinstance(exported_name, str)
                    or Path(exported_name).name != exported_name
                    or exported_name in {"", ".", ".."}
                ):
                    raise IOSSimulatorAppContractError(
                        f"xcresulttool returned an unsafe iOS UI screenshot entry: {human_name}"
                    )
                label = label_match.group(1)
                if exported_name in exported_files:
                    raise IOSSimulatorAppContractError(
                        f"xcresulttool returned a duplicate exported screenshot file: {exported_name}"
                    )
                source = screenshot_dir / exported_name
                if source.is_symlink() or not source.is_file():
                    raise IOSSimulatorAppContractError(
                        f"xcresulttool did not export complete iOS UI screenshot: {human_name}"
                    )
                diagnostics.mkdir(parents=True, exist_ok=True)
                destination.mkdir(mode=0o700, exist_ok=True)
                destination.chmod(0o700)
                occurrence = copied.get(label, 0) + 1
                suffix = "" if occurrence == 1 else f"-{occurrence:02d}"
                screenshot = destination / f"{label}{suffix}.png"
                try:
                    shutil.copy2(source, screenshot)
                    screenshot.chmod(0o600)
                    assert_files_identical(source, screenshot)
                    png_metadata(screenshot)
                except (OSError, ValueError) as error:
                    raise IOSSimulatorAppContractError(
                        f"could not retain complete iOS UI screenshot: {label}"
                    ) from error
                copied[label] = occurrence
                exported_files.add(exported_name)

        if not copied:
            raise IOSSimulatorAppContractError(
                "xcresult contained no named DobbyVPN UI screenshots"
            )
        destination.chmod(0o700)
    return destination


_IOS_LOG_NAMES = (
    "app_logs.txt", "ui_diagnostics.jsonl", "tunnel_native.jsonl",
    "go_app_logs.jsonl", "go_app_logs.jsonl.stderr",
    "go_tunnel_logs.jsonl", "go_tunnel_logs.jsonl.stderr",
)


def _collect_ios_native_log(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    contract: IOSSimulatorAppContract,
    work_dir: Path,
    *,
    budget: RunBudget,
) -> Path | None:
    """Retain app logs after XCTest; return None if no native app log exists."""
    result = _require_success(
        runner,
        simctl_get_app_container_command(simulator.udid, contract.bundle_identifier),
        "collect-app-container",
        budget=budget,
    )
    container_text = result.stdout.strip()
    if not container_text:
        raise IOSSimulatorStageError(
            "collect-app-container",
            "simctl returned no app data-container path",
        )
    container = Path(container_text.splitlines()[-1].strip())
    if container.is_symlink() or not container.is_dir():
        raise IOSSimulatorStageError(
            "collect-app-container",
            f"app data-container path is unavailable: {container}",
        )
    destination_dir = _ios_diagnostics_directory(work_dir)
    destination_dir.mkdir(parents=True, exist_ok=True)

    native_log = container / "tmp" / "app_logs.txt"
    if native_log.is_symlink():
        raise IOSSimulatorStageError(
            "collect-ios-native-log",
            f"native app log is a symbolic link: {native_log}",
        )
    native_log_copy: Path | None = None
    if native_log.exists():
        if not native_log.is_file():
            raise IOSSimulatorStageError(
                "collect-ios-native-log",
                f"native app log is not a regular file: {native_log}",
            )
        native_log_copy = destination_dir / "app-native.log"
        try:
            shutil.copy2(native_log, native_log_copy)
        except OSError as error:
            raise IOSSimulatorStageError(
                "collect-ios-native-log",
                f"could not copy complete native app log: {native_log_copy}",
            ) from error
        native_log_copy.chmod(0o600)
    for name in _IOS_LOG_NAMES:
        for suffix in ("", ".previous"):
            source = container / "tmp" / (name + suffix)
            if source == native_log or not (source.exists() or source.is_symlink()):
                continue
            if source.is_symlink() or not source.is_file():
                raise IOSSimulatorStageError("collect-ios-native-log", f"Diagnostic is not a regular file: {source}")
            shutil.copy2(source, destination_dir / source.name)
            (destination_dir / source.name).chmod(0o600)
    return native_log_copy


def _run_install_observations(
    runner: CommandRunner,
    queries: Sequence[tuple[str, Sequence[str], float]],
    *,
    budget: RunBudget,
    failure: BaseException | None = None,
) -> list[tuple[str, CommandResult | BaseException]]:
    """Run read-only failure diagnostics inside the functional budget."""
    observations: list[tuple[str, CommandResult | BaseException]] = []
    for label, command, timeout in queries:
        try:
            result: CommandResult | BaseException = _require_success(
                runner, command, label, budget=budget, timeout_seconds=timeout
            )
        except BaseException as error:
            if failure is not None:
                result = error
            elif isinstance(error, (KeyboardInterrupt, SystemExit)):
                raise
            else:
                result = error
                traceback.print_exception(error, file=sys.stderr)
        if failure is None:
            observations.append((label, result))
        elif isinstance(result, BaseException):
            add_exception_notes(failure, label, result)
            observations.append((label, result))
        else:
            add_stream_notes(failure, label, result.stdout, result.stderr)
            observations.append((label, result))
    return observations


def _failure_sample_queries(
    process_snapshot: str,
    targets: dict[str, str | tuple[str, ...]],
    *,
    label_prefix: str,
    interval_ms: int = 1,
) -> tuple[list[tuple[str, Sequence[str], float]], list[str]]:
    queries: list[tuple[str, Sequence[str], float]] = []
    notes: list[str] = []
    for target, suffix in targets.items():
        suffixes = (suffix,) if isinstance(suffix, str) else suffix
        matches: dict[str, str] = {}
        for line in process_snapshot.splitlines():
            fields = line.strip().split(None, 4)
            if (
                len(fields) == 5
                and fields[0].isdecimal()
                and any(fields[4].endswith(item) for item in suffixes)
            ):
                matches[fields[0]] = fields[4]
        if len(matches) != 1:
            notes.append(
                f"{label_prefix}_target_{target}: expected one exact process; matches={matches!r}"
            )
            continue
        pid, command = next(iter(matches.items()))
        label = f"{label_prefix}_{target}_pid_{pid}"
        notes.append(f"{label_prefix}_target_{target}: pid={pid} command={command}")
        queries.append(
            (
                label,
                ["/usr/bin/sample", pid, "1", str(interval_ms), "-file", "/dev/stdout"],
                5,
            )
        )
    return queries, notes


_INSTALL_FAILURE_SAMPLE_TARGETS = {
    "coresimulator-service": "/CoreSimulator.framework/Versions/A/XPCServices/com.apple.CoreSimulator.CoreSimulatorService.xpc/Contents/MacOS/com.apple.CoreSimulator.CoreSimulatorService",
    "simlaunchhost": (
        "/CoreSimulator.framework/Versions/A/XPCServices/SimLaunchHost.arm64.xpc/Contents/MacOS/SimLaunchHost.arm64",
        "/CoreSimulator.framework/Versions/A/XPCServices/SimLaunchHost.x86_64.xpc/Contents/MacOS/SimLaunchHost.x86_64",
    ),
    "simulator-installd": "/RuntimeRoot/usr/libexec/installd",
    "simulator-mobileinstallation": "/RuntimeRoot/System/Library/PrivateFrameworks/MobileInstallation.framework/XPCServices/com.apple.MobileInstallationHelperService.xpc/com.apple.MobileInstallationHelperService",
}
_BOOTSTATUS_FAILURE_SAMPLE_TARGETS = {
    "springboard": "/System/Library/CoreServices/SpringBoard.app/SpringBoard",
    "backboardd": "/usr/libexec/backboardd",
}


def _has_operation_not_permitted(error: BaseException) -> bool:
    parts: list[str] = []
    current: BaseException | None = error
    seen: set[int] = set()
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        parts.append(str(current))
        parts.extend(str(note) for note in getattr(current, "__notes__", ()))
        for stream in (getattr(current, "stdout", None), getattr(current, "stderr", None)):
            if stream is not None:
                parts.append(output_text(stream))
        current = current.__cause__
    diagnostic = "\n".join(parts).lower()
    return "operation not permitted" in diagnostic or re.search(r"\beperm\b", diagnostic) is not None


def _install_sample_target(label: str) -> tuple[str, str, tuple[str, ...]] | None:
    for target, suffix in _INSTALL_FAILURE_SAMPLE_TARGETS.items():
        prefix = f"install_failure_sample_{target}_pid_"
        if not label.startswith(prefix):
            continue
        pid = label[len(prefix):]
        if not pid.isdecimal():
            return None
        suffixes = (suffix,) if isinstance(suffix, str) else suffix
        return target, pid, suffixes
    return None


def _matching_install_process_record(
    snapshot: str,
    *,
    pid: str,
    suffixes: tuple[str, ...],
) -> dict[str, str] | None:
    for line in snapshot.splitlines():
        fields = line.strip().split(None, 7)
        if len(fields) != 8 or fields[0] != pid:
            continue
        command = fields[7]
        if not any(command.endswith(suffix) for suffix in suffixes):
            continue
        return {
            "ppid": fields[1],
            "pgid": fields[2],
            "uid": fields[3],
            "euid": fields[4],
            "state": fields[5],
            "etime": fields[6],
            "command": command,
        }
    return None


_BOOTSTATUS_NATIVE_LOGS = (
    Path("system.log"),
    Path("MobileInstallation") / "mobile_installation.log.0",
    Path("AppConduit") / "AppConduit.log.0",
)


def _retain_bootstatus_native_logs(
    simulator_udid: str,
    work_dir: Path,
    failure: BaseException,
) -> None:
    source_dir = Path.home() / "Library" / "Logs" / "CoreSimulator" / simulator_udid
    destination_dir = _ios_diagnostics_directory(work_dir)
    try:
        destination_dir.mkdir(parents=True, exist_ok=True)
    except BaseException as error:
        add_exception_notes(failure, "bootstatus_native_logs_directory", error)
        return

    for relative_path in _BOOTSTATUS_NATIVE_LOGS:
        source = source_dir / relative_path
        destination = destination_dir / relative_path.name
        label = f"bootstatus_native_log_{relative_path.name.replace('.', '_')}"
        try:
            mode = source.lstat().st_mode
        except FileNotFoundError as error:
            failure.add_note(f"{label}_missing={source}")
            add_exception_notes(failure, label, error, include_streams=False)
            continue
        except BaseException as error:
            add_exception_notes(failure, label, error)
            continue

        created_destination = False
        copy_complete = False
        try:
            if not stat.S_ISREG(mode):
                raise OSError(f"native Simulator log is not a regular file: {source}")
            with source.open("rb") as input_stream, destination.open("xb") as output_stream:
                created_destination = True
                shutil.copyfileobj(input_stream, output_stream, length=64 * 1024)
                copy_complete = True
            destination.chmod(0o600)
            failure.add_note(f"{label}_retained={source} -> {destination}")
        except BaseException as error:
            if created_destination:
                retention = "retained" if copy_complete else "partial_retained"
                failure.add_note(f"{label}_{retention}={destination}")
            add_exception_notes(failure, label, error)


def retain_ios_diagnostics(work_dir: Path, destination_dir: Path) -> Path:
    """Transfer the complete owned diagnostics directory into the local manifest.

    The runner keeps work and manifest paths under one run directory, so this
    is a same-filesystem ownership transfer. Hosted CI consumes the source
    diagnostics directory directly. A failed transfer leaves its source in
    place for normal failure cleanup.
    """
    source_dir = _ios_diagnostics_directory(work_dir)
    if source_dir.is_symlink() or not source_dir.is_dir():
        raise IOSSimulatorAppContractError(
            f"retained iOS diagnostics are missing: {source_dir}"
        )
    if next(source_dir.iterdir(), None) is None:
        raise IOSSimulatorAppContractError(
            f"retained iOS diagnostics contain no complete artifacts: {source_dir}"
        )
    if destination_dir.exists() or destination_dir.is_symlink():
        raise IOSSimulatorAppContractError(
            f"retained iOS diagnostics already exist: {destination_dir}"
        )
    try:
        destination_dir.parent.mkdir(parents=True, exist_ok=True)
        source_dir.chmod(0o700)
        source_dir.rename(destination_dir)
    except OSError as error:
        raise IOSSimulatorAppContractError(
            f"could not transfer complete iOS diagnostics: {destination_dir}"
        ) from error
    return destination_dir


def _shutdown_simulator(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    *,
    budget: RunBudget,
) -> None:
    _require_success(
        runner,
        ["xcrun", "simctl", "shutdown", simulator.udid],
        "shutdown",
        budget=budget,
        timeout_seconds=budget.cleanup_timeout(),
        bounded_timeout=True,
    )


def _terminate_app(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    contract: IOSSimulatorAppContract,
    *,
    budget: RunBudget,
) -> None:
    # Keep app termination inside its own stage budget. Using the entire
    # cleanup reserve here allowed a stuck CoreSimulator app operation to
    # starve diagnostics and shutdown, which are the reliable final cleanup
    # actions. Pass this bounded value directly; applying the functional
    # operation budget again would subtract the cleanup reserve twice.
    timeout = min(
        STAGE_TIMEOUT_SECONDS["terminate"],
        budget.cleanup_timeout() - COMMAND_TERMINATION_RESERVE_SECONDS,
    )
    if timeout <= 0:
        raise IOSSimulatorStageError("terminate", "cleanup deadline expired")
    try:
        result = runner.run(
            simctl_terminate_command(simulator.udid, contract.bundle_identifier),
            timeout_seconds=timeout,
        )
    except BaseException as error:
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise
        raise _stage_error("terminate", error, timeout_seconds=timeout) from error
    if result.returncode:
        output = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
        # XCTest can terminate the application itself after a UI-test
        # failure. In that state simctl uses exit 3 with this stable message.
        if "found nothing to terminate" in output.lower():
            return
        del output
        failure = IOSSimulatorStageError(
            "terminate",
            f"exit code {result.returncode}",
            timeout_seconds=timeout,
        )
        add_stream_notes(failure, "command", result.stdout, result.stderr)
        raise failure


def _read_simulator_hardware_keyboard_override(simulator_udid: str) -> bool | None:
    """Read a selected device's override; ``None`` means use the global setting.

    A global preference works on fresh hosted runners. Local Simulators may
    have a device-specific value, which takes precedence over that preference.
    """
    preferences_path = (
        Path.home() / "Library" / "Preferences" / _SIMULATOR_PREFERENCES_FILE
    )
    try:
        with preferences_path.open("rb") as preferences_file:
            preferences = plistlib.load(preferences_file)
    except FileNotFoundError:
        return None
    except (OSError, plistlib.InvalidFileException, ValueError) as error:
        raise IOSSimulatorStageError(
            "read-hardware-keyboard",
            f"could not read Simulator preferences: {type(error).__name__}: {error}",
            timeout_seconds=STAGE_TIMEOUT_SECONDS["read-hardware-keyboard"],
        ) from error

    if not isinstance(preferences, dict):
        raise IOSSimulatorStageError(
            "read-hardware-keyboard", "Simulator preferences are not a dictionary"
        )
    device_preferences = preferences.get("DevicePreferences", {})
    if not isinstance(device_preferences, dict):
        raise IOSSimulatorStageError(
            "read-hardware-keyboard", "per-device Simulator preferences are not a dictionary"
        )
    device = device_preferences.get(simulator_udid, {})
    if not isinstance(device, dict):
        raise IOSSimulatorStageError(
            "read-hardware-keyboard", "selected Simulator preferences are not a dictionary"
        )
    if _HARDWARE_KEYBOARD_PREFERENCE not in device:
        return None
    value = device[_HARDWARE_KEYBOARD_PREFERENCE]
    if isinstance(value, bool):
        return value
    if isinstance(value, int) and value in (0, 1):
        return bool(value)
    raise IOSSimulatorStageError(
        "read-hardware-keyboard", "per-device preference has an unsupported value"
    )


def _disable_simulator_hardware_keyboard(
    runner: CommandRunner, *, budget: RunBudget
) -> str | None:
    """Expose the software keyboard using the global preference on fresh hosts."""
    read_timeout = _stage_timeout(budget, "read-hardware-keyboard")
    try:
        read = runner.run(
            [
                "/usr/bin/defaults", "read", "com.apple.iphonesimulator",
                _HARDWARE_KEYBOARD_PREFERENCE,
            ],
            timeout_seconds=read_timeout,
        )
    except BaseException as error:
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise
        raise _stage_error(
            "read-hardware-keyboard", error, timeout_seconds=read_timeout
        ) from error
    if read.returncode == 0:
        previous = read.stdout.strip()
    elif "does not exist" in read.stderr:
        previous = None
    else:
        failure = IOSSimulatorStageError(
            "read-hardware-keyboard",
            f"exit code {read.returncode}",
            timeout_seconds=read_timeout,
        )
        add_stream_notes(failure, "command", read.stdout, read.stderr)
        raise failure
    if previous not in {None, "0", "1"}:
        raise IOSSimulatorStageError(
            "read-hardware-keyboard",
            "preference has an unsupported value",
            timeout_seconds=read_timeout,
        )
    _require_success(
        runner,
        [
            "/usr/bin/defaults", "write", "com.apple.iphonesimulator",
            _HARDWARE_KEYBOARD_PREFERENCE, "-bool", "false",
        ],
        "write-hardware-keyboard",
        budget=budget,
        timeout_seconds=STAGE_TIMEOUT_SECONDS["write-hardware-keyboard"],
    )
    return previous


def _restore_simulator_hardware_keyboard(
    runner: CommandRunner, previous: str | None, *, budget: RunBudget
) -> None:
    if previous is None:
        command = [
            "/usr/bin/defaults", "delete", "com.apple.iphonesimulator",
            _HARDWARE_KEYBOARD_PREFERENCE,
        ]
    else:
        command = [
            "/usr/bin/defaults", "write", "com.apple.iphonesimulator",
            _HARDWARE_KEYBOARD_PREFERENCE, "-bool",
            "true" if previous == "1" else "false",
        ]
    _require_success(
        runner,
        command,
        "restore-hardware-keyboard",
        budget=budget,
        timeout_seconds=budget.cleanup_timeout(),
        bounded_timeout=True,
    )


def _open_simulator(
    runner: CommandRunner,
    simulator_udid: str,
    *,
    budget: RunBudget,
    cleanup: bool = False,
) -> None:
    _require_success(
        runner,
        [
            "/usr/bin/open", "-a", "Simulator", "--args",
            "-CurrentDeviceUDID", simulator_udid,
        ],
        "open-simulator",
        budget=budget,
        timeout_seconds=budget.cleanup_timeout() if cleanup else None,
        bounded_timeout=cleanup,
    )


def _toggle_simulator_hardware_keyboard(
    runner: CommandRunner,
    *,
    stage: str,
    budget: RunBudget,
    cleanup: bool = False,
) -> None:
    keyboard_timeout = (
        budget.cleanup_timeout()
        if cleanup
        else _stage_timeout(budget, stage)
    )
    _require_success(
        runner,
        [
            "/usr/bin/osascript", "-e",
            'tell application id "com.apple.iphonesimulator" to activate',
            "-e",
            'tell application "System Events" to tell process "Simulator" '
            'to keystroke "k" using {command down, shift down}',
        ],
        stage,
        budget=budget,
        timeout_seconds=keyboard_timeout,
        bounded_timeout=cleanup,
    )


def _disable_per_device_hardware_keyboard(
    runner: CommandRunner,
    state: SimulatorHardwareKeyboardState,
    *,
    budget: RunBudget,
) -> None:
    if not state.was_connected:
        return
    # Mark the state before posting the shortcut: if the automation command
    # times out after delivering its key event, cleanup still checks and
    # restores the effective setting.
    state.changed = True
    _toggle_simulator_hardware_keyboard(
        runner,
        stage="toggle-hardware-keyboard",
        budget=budget,
    )
    if _read_simulator_hardware_keyboard_override(state.simulator_udid) is not False:
        raise IOSSimulatorStageError(
            "toggle-hardware-keyboard",
            "Simulator still reports its hardware keyboard connected",
            timeout_seconds=STAGE_TIMEOUT_SECONDS["toggle-hardware-keyboard"],
        )


def _restore_per_device_hardware_keyboard(
    runner: CommandRunner,
    state: SimulatorHardwareKeyboardState,
    *,
    budget: RunBudget,
) -> None:
    if not state.changed:
        return
    if _read_simulator_hardware_keyboard_override(state.simulator_udid) == state.was_connected:
        state.changed = False
        return
    _open_simulator(
        runner,
        state.simulator_udid,
        budget=budget,
        cleanup=True,
    )
    _toggle_simulator_hardware_keyboard(
        runner,
        stage="restore-hardware-keyboard",
        budget=budget,
        cleanup=True,
    )
    if _read_simulator_hardware_keyboard_override(state.simulator_udid) != state.was_connected:
        raise IOSSimulatorStageError(
            "restore-hardware-keyboard",
            "Simulator did not restore the prior hardware keyboard setting",
            timeout_seconds=budget.cleanup_timeout(),
        )
    state.changed = False


def run_ios_simulator_app_contract(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    contract: IOSSimulatorAppContract = PUBLIC_IOS_SIMULATOR_APP_CONTRACT,
    budget: RunBudget | None = None,
    native_cases: Sequence[str] | None = None,
    source_sha: str | None = None,
    repeat: int = 1,
) -> IOSSimulatorAppEvidence:
    """Run the native SwiftUI Simulator mini contract.

    Simulator UI proves accessibility, native input, lifecycle and visible
    error/presentation behavior only.  It never claims NetworkExtension or
    packet-tunnel success.
    """
    budget = budget or RunBudget()
    selected_tests = ui_test_selection(native_cases)
    required_test_cases = required_ui_test_cases(native_cases)
    if type(repeat) is not int or repeat < 1:
        raise IOSSimulatorAppContractError("repeat count must be positive")
    work_dir.mkdir(parents=True, exist_ok=True)
    simulator: AvailableSimulator | None = None
    app_installed = False
    boot_started = False
    keyboard_preference_configured = False
    previous_keyboard_preference: str | None = None
    keyboard_state: SimulatorHardwareKeyboardState | None = None
    failure: BaseException | None = None
    app_path = contract.app_path(work_dir)
    diagnostics_dir = _ios_diagnostics_directory(work_dir)
    diagnostics_dir.mkdir(parents=True, exist_ok=True)
    result_bundle = diagnostics_dir / "xctest-results.xcresult"
    xctest_started = False
    retained_result_bundle: Path | None = None
    retained_native_log: Path | None = None
    native_log_collection_error: str | None = None
    xctest_results: dict[str, object] | None = None
    subscription_fixture = None
    subscription_fixture_directory = work_dir / "native-subscription-fixture"
    requires_subscription_fixture = _requires_subscription_fixture(native_cases)

    try:
        inventory = _require_success(
            runner,
            ["xcrun", "simctl", "list", "devices", "available", "-j"],
            "list-devices",
            budget=budget,
        )
        sdk_version = _active_iphonesimulator_sdk_version(runner, budget=budget)
        try:
            selected_simulator = select_available_iphone(inventory.stdout, sdk_version)
        except BaseException as error:
            if isinstance(error, (KeyboardInterrupt, SystemExit)):
                raise
            raise _stage_error("select-device", error) from error
        simulator = (
            _create_disposable_simulator(
                selected_simulator, runner=runner, budget=budget
            )
            if requires_subscription_fixture
            else selected_simulator
        )
        keyboard_override = _read_simulator_hardware_keyboard_override(simulator.udid)
        if keyboard_override is None:
            previous_keyboard_preference = _disable_simulator_hardware_keyboard(
                runner, budget=budget
            )
            keyboard_preference_configured = True
        else:
            keyboard_state = SimulatorHardwareKeyboardState(
                simulator_udid=simulator.udid,
                was_connected=keyboard_override,
            )
        boot_started = True

        def boot_simulator() -> None:
            boot_timeout = _stage_timeout(budget, "boot")
            try:
                boot = _timed_stage(
                    "simctl-boot",
                    lambda: runner.run(
                        simctl_boot_command(simulator.udid),
                        timeout_seconds=boot_timeout,
                    ),
                )
            except BaseException as error:
                if isinstance(error, (KeyboardInterrupt, SystemExit)):
                    raise
                raise _stage_error("boot", error, timeout_seconds=boot_timeout) from error
            if boot.returncode and "current state: Booted" not in (boot.stdout + boot.stderr):
                failure = IOSSimulatorStageError(
                    "boot",
                    f"exit code {boot.returncode}",
                    timeout_seconds=boot_timeout,
                )
                add_stream_notes(failure, "command", boot.stdout, boot.stderr)
                raise failure
            try:
                _timed_stage(
                    "simctl-bootstatus",
                    lambda: _require_success(
                        runner,
                        simctl_bootstatus_command(simulator.udid),
                        "bootstatus",
                        budget=budget,
                    ),
                )
            except IOSSimulatorStageError as error:
                if error.stage != "bootstatus":
                    raise
                error.add_note(
                    f"bootstatus_failure_simulator_udid={simulator.udid}"
                )
                process_observations = _run_install_observations(
                    runner,
                    ((
                        "bootstatus_failure_processes",
                        ["/bin/ps", "-A", "-o", "pid,ppid,state,etime,comm"],
                        5,
                    ),),
                    budget=budget,
                    failure=error,
                )
                process_result = (
                    process_observations[0][1] if process_observations else None
                )
                if isinstance(process_result, CommandResult):
                    sample_queries, sample_notes = _failure_sample_queries(
                        process_result.stdout,
                        _BOOTSTATUS_FAILURE_SAMPLE_TARGETS,
                        label_prefix="bootstatus_failure_sample",
                        interval_ms=10,
                    )
                    for note in sample_notes:
                        error.add_note(note)
                    if sample_queries:
                        _run_install_observations(
                            runner,
                            sample_queries,
                            budget=budget,
                            failure=error,
                        )
                    else:
                        error.add_note(
                            "bootstatus_failure_peer_samples: skipped because no unique System App peers were found"
                        )
                else:
                    error.add_note(
                        "bootstatus_failure_peer_samples: skipped because the process snapshot was unavailable"
                    )
                _retain_bootstatus_native_logs(simulator.udid, work_dir, error)
                raise
            if keyboard_state is not None and keyboard_state.was_connected:
                _timed_stage(
                    "open-simulator",
                    lambda: _open_simulator(runner, simulator.udid, budget=budget),
                )

        _timed_stage("boot", boot_simulator)
        if keyboard_state is not None:
            _disable_per_device_hardware_keyboard(
                runner, keyboard_state, budget=budget
            )

        if not app_path.is_dir():
            raise IOSSimulatorStageError(
                "verify-app-bundle",
                f"SwiftUI Simulator build produced no app bundle: {app_path}",
            )

        def install_app() -> CommandResult:
            preflight_queries = (
                ("install_preflight_disk_free", ["/bin/df", "-Pk", str(app_path)], 2),
                ("install_preflight_app_size", ["/usr/bin/du", "-sk", str(app_path)], 2),
                (f"install_preflight_simulator_state_{simulator.udid}", ["xcrun", "simctl", "list", "devices", "-j"], 2),
            )
            observations = _run_install_observations(runner, preflight_queries, budget=budget)
            started_at = datetime.now(timezone.utc)
            try:
                result = _require_success(
                    runner,
                    simctl_install_command(simulator.udid, app_path),
                    "install",
                    budget=budget,
                )
            except Exception as install_error:
                for label, observation in observations:
                    if isinstance(observation, BaseException):
                        add_exception_notes(install_error, label, observation)
                    else:
                        add_stream_notes(install_error, label, observation.stdout, observation.stderr)
                try:
                    ended_at = datetime.now(timezone.utc)
                    log_start = started_at.astimezone().strftime("%Y-%m-%d %H:%M:%S")
                    log_end = ended_at.astimezone().strftime("%Y-%m-%d %H:%M:%S")
                    log_query = [
                        "/usr/bin/log", "show", "--style", "compact", "--start", log_start, "--end", log_end, "--predicate",
                        '(process == "installd" OR process == "CoreSimulatorService" '
                        'OR process == "SimulatorTrampoline" OR process == "simctl")',
                    ]
                    process_observations = _run_install_observations(
                        runner,
                        (("install_failure_processes", ["/bin/ps", "-A", "-o", "pid,ppid,state,etime,comm"], 5),),
                        budget=budget, failure=install_error,
                    )
                    process_result = process_observations[0][1] if process_observations else None
                    if isinstance(process_result, CommandResult):
                        sample_queries, sample_notes = _failure_sample_queries(
                            process_result.stdout,
                            _INSTALL_FAILURE_SAMPLE_TARGETS,
                            label_prefix="install_failure_sample",
                        )
                        for note in sample_notes:
                            install_error.add_note(note)
                        sample_observations = _run_install_observations(
                            runner, sample_queries, budget=budget, failure=install_error
                        )
                        for sample_label, sample_observation in sample_observations:
                            if not isinstance(sample_observation, BaseException):
                                continue
                            if not _has_operation_not_permitted(sample_observation):
                                continue
                            sample_target = _install_sample_target(sample_label)
                            if sample_target is None:
                                continue
                            target, pid, suffixes = sample_target
                            identity_label = f"install_failure_sample_{target}_pid_{pid}_identity"
                            identity_observations = _run_install_observations(
                                runner,
                                ((
                                    identity_label,
                                    ["/bin/ps", "-ww", "-p", pid, "-o", "pid,ppid,pgid,uid,euid,state,etime,command"],
                                    2,
                                ),),
                                budget=budget,
                                failure=install_error,
                            )
                            identity_result = (
                                identity_observations[0][1]
                                if identity_observations
                                else None
                            )
                            if not isinstance(identity_result, CommandResult):
                                install_error.add_note(
                                    f"{identity_label}_sample_skipped=identity snapshot unavailable"
                                )
                                continue
                            identity = _matching_install_process_record(
                                identity_result.stdout,
                                pid=pid,
                                suffixes=suffixes,
                            )
                            if identity is None:
                                install_error.add_note(
                                    f"{identity_label}_sample_skipped=exact target identity was not present"
                                )
                                continue
                            if identity["state"].upper().startswith("Z"):
                                install_error.add_note(
                                    f"{identity_label}_sample_skipped=target is not live; "
                                    f"state={identity['state']}"
                                )
                                continue
                            install_error.add_note(
                                f"{identity_label}_matched: ppid={identity['ppid']} "
                                f"pgid={identity['pgid']} uid={identity['uid']} "
                                f"euid={identity['euid']} state={identity['state']} "
                                f"etime={identity['etime']} command={identity['command']}"
                            )
                            _run_install_observations(
                                runner,
                                ((
                                    f"install_failure_privileged_sample_{target}_pid_{pid}",
                                    ["/usr/bin/sudo", "-n", "/usr/bin/sample", pid, "1", "1", "-file", "/dev/stdout"],
                                    5,
                                ),),
                                budget=budget,
                                failure=install_error,
                            )
                    else:
                        install_error.add_note(
                            "install_failure_process_samples: skipped because the process snapshot was unavailable"
                        )
                    _run_install_observations(
                        runner,
                        (("install_failure_unified_logs", log_query, 10),),
                        budget=budget, failure=install_error,
                    )
                    install_error.add_note(f"install_interval_utc={started_at.isoformat(timespec='milliseconds')}/{ended_at.isoformat(timespec='milliseconds')}")
                except BaseException as diagnostic_error:
                    add_exception_notes(install_error, "install_failure_diagnostic_collection", diagnostic_error)
                raise
            return result

        _timed_stage(
            "install",
            install_app,
        )
        app_installed = True
        test_environment = None
        if requires_subscription_fixture:
            from torturer_runner.subscription_fixture import SubscriptionFixture

            subscription_fixture = SubscriptionFixture(
                _write_disposable_subscription_profile(work_dir),
                subscription_fixture_directory,
                "ios_simulator",
                simulator_udid=simulator.udid,
                simulator_temporary=simulator.temporary,
            )
            # start() records Simulator trust before installing it, so the
            # common finally cleanup can handle partial setup failures too.
            subscription_fixture.start()
            test_environment = _subscription_fixture_test_environment(subscription_fixture)
        project = candidate_root / _PROJECT_PATH
        if not project.is_dir():
            raise IOSSimulatorStageError(
                "verify-ui-test-project",
                f"iOS XCTest project is unavailable: {project}",
            )
        # xcodebuild refuses to overwrite an existing result bundle. This path
        # is owned by this disposable run, so remove only that exact prior
        # bundle before starting the next candidate attempt.
        if result_bundle.exists():
            try:
                if result_bundle.is_dir():
                    shutil.rmtree(result_bundle)
                else:
                    result_bundle.unlink()
            except OSError as error:
                raise IOSSimulatorStageError(
                    "prepare-xctest-result-bundle",
                    f"could not remove prior result bundle: {type(error).__name__}",
                ) from error
        # Do not pre-launch with simctl. The XCTest target owns the first app
        # launch and its in-test terminate/reopen lifecycle; handing it an
        # already-running simctl process can block setUp before test output.
        xctest_started = True
        _timed_stage(
            "xctest",
            lambda: _require_success(
                runner,
                xcodebuild_ui_test_without_building_command(
                    simulator.udid,
                    project,
                    work_dir / "derived-data",
                    result_bundle=result_bundle,
                    architecture=contract.architecture,
                    native_cases=native_cases,
                    test_environment=test_environment,
                    repeat=repeat,
                ),
                "xctest-ui",
                cwd=candidate_root,
                budget=budget,
                timeout_seconds=STAGE_TIMEOUT_SECONDS["xctest-ui"],
            ),
        )
        test_results = _require_success(
            runner,
            [
                "xcrun", "xcresulttool", "get", "test-results", "tests",
                "--path", str(result_bundle),
            ],
            "get-xctest-test-results",
            budget=budget,
            timeout_seconds=STAGE_TIMEOUT_SECONDS["get-xctest-test-results"],
        )
        _write_xctest_test_results(test_results.stdout, work_dir)
        xctest_results = _validate_xctest_test_results(
            test_results.stdout,
            required_tests=required_test_cases,
            repeat=repeat,
        )
        if subscription_fixture is not None:
            _assert_subscription_fixture_request_count(
                subscription_fixture,
                expected_gets=expected_subscription_fixture_gets(native_cases) * repeat,
            )

    except BaseException as error:
        failure = error
    finally:
        # XCTest attachments are extra artifacts, not a replacement for its
        # complete stdout/stderr streams. Keep the original result bundle in
        # work through attachment validation and native-log collection; the
        # local adapter transfers it to the manifest after this function ends.
        # Collection failures remain explicit and secondary to a product
        # assertion so simulator cleanup can still run.
        cleanup_errors: list[tuple[str, BaseException]] = []

        def cleanup_simulator() -> None:
            nonlocal retained_result_bundle, retained_native_log, native_log_collection_error
            if xctest_started:
                if result_bundle.exists():
                    try:
                        retained_result_bundle = result_bundle
                        _retain_xctest_screenshots(
                            runner, result_bundle, work_dir, budget=budget
                        )
                    except BaseException as error:
                        cleanup_errors.append(("iOS XCTest result collection failed", error))
                else:
                    cleanup_errors.append(
                        (
                            "iOS XCTest result collection failed",
                            IOSSimulatorStageError(
                                "collect-xctest-result",
                                f"complete XCTest result bundle is missing: {result_bundle}",
                            ),
                        )
                    )
                if simulator is not None and app_installed:
                    try:
                        retained_native_log = _collect_ios_native_log(
                            runner,
                            simulator,
                            contract,
                            work_dir,
                            budget=budget,
                        )
                        if retained_native_log is None:
                            native_log_collection_error = (
                                "app_logs.txt was absent; no native app log bytes were produced"
                            )
                            if failure is not None:
                                failure.add_note(
                                    "iOS app log collection error="
                                    + native_log_collection_error
                                )
                    except BaseException as error:
                        cleanup_errors.append(
                            ("iOS app log export collection failed", error)
                        )
            if subscription_fixture is not None:
                try:
                    subscription_fixture.close()
                except BaseException as error:
                    cleanup_errors.append(("iOS subscription fixture cleanup failed", error))
                    try:
                        SubscriptionFixture.cleanup_interrupted(subscription_fixture_directory)
                    except BaseException as retry_error:
                        cleanup_errors.append(
                            ("iOS subscription fixture retry cleanup failed", retry_error)
                        )
            if simulator is not None:
                if app_installed:
                    try:
                        _terminate_app(runner, simulator, contract, budget=budget)
                    except BaseException as error:
                        cleanup_errors.append(("Simulator app cleanup also failed", error))
                if keyboard_state is not None:
                    try:
                        _restore_per_device_hardware_keyboard(
                            runner, keyboard_state, budget=budget
                        )
                    except BaseException as error:
                        cleanup_errors.append(
                            ("Simulator keyboard preference cleanup also failed", error)
                        )
                if boot_started:
                    try:
                        _shutdown_simulator(runner, simulator, budget=budget)
                    except BaseException as error:
                        cleanup_errors.append(("Simulator shutdown also failed", error))
                if simulator.temporary:
                    try:
                        _require_success(
                            runner,
                            simctl_delete_command(simulator.udid),
                            "delete-simulator",
                            timeout_seconds=max(
                                1,
                                budget.cleanup_timeout() - COMMAND_TERMINATION_RESERVE_SECONDS,
                            ),
                            bounded_timeout=True,
                        )
                    except BaseException as error:
                        cleanup_errors.append(("Disposable Simulator deletion failed", error))
            if keyboard_preference_configured:
                try:
                    _restore_simulator_hardware_keyboard(
                        runner, previous_keyboard_preference, budget=budget
                    )
                except BaseException as error:
                    cleanup_errors.append(
                        ("Simulator keyboard preference cleanup also failed", error)
                    )
            if cleanup_errors:
                raise cleanup_errors[0][1]

        try:
            _timed_stage("cleanup", cleanup_simulator)
        except BaseException:
            if cleanup_errors:
                for label, cleanup_error in cleanup_errors:
                    failure = _add_note(failure, label, cleanup_error)
            else:
                raise
    if failure is not None:
        raise failure.with_traceback(failure.__traceback__)
    if simulator is None or retained_result_bundle is None:
        raise IOSSimulatorAppContractError("iOS Simulator check produced no result")
    budget.assert_within_deadline()
    return IOSSimulatorAppEvidence(
        simulator=simulator,
        app=SimulatorApp(
            app_path=app_path,
            bundle_identifier=contract.bundle_identifier,
            architecture=contract.architecture,
        ),
        result_bundle=retained_result_bundle,
        native_log=retained_native_log,
        native_log_collection_error=native_log_collection_error,
        selected_tests=selected_tests,
        required_test_cases=required_test_cases,
        xctest_results=xctest_results,
    )


def prepare_ios_simulator_candidate(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    contract: IOSSimulatorAppContract,
    budget: RunBudget,
    source_sha: str | None = None,
) -> None:
    """Build the SwiftUI simulator app without a packet tunnel or Go runtime."""
    candidate_root = Path(candidate_root).resolve()
    work_dir.mkdir(parents=True, exist_ok=True)
    app_path = contract.app_path(work_dir)
    go_root = candidate_root / "core"
    project = candidate_root / _PROJECT_PATH
    if not project.is_dir():
        raise IOSSimulatorStageError(
            "verify-ui-test-project",
            f"iOS XCTest project is unavailable: {project}",
        )

    def build_app() -> None:
        _require_success(
            runner,
            xcodebuild_app_command(
                contract, work_dir=work_dir, source_sha=source_sha
            ),
            "package-ios-app",
            cwd=go_root,
            budget=budget,
            timeout_seconds=IOS_NATIVE_UI_BUILD_TIMEOUT_SECONDS,
        )
        if not app_path.is_dir():
            raise IOSSimulatorStageError(
                "package-ios-app",
                f"SwiftUI build produced no Simulator app: {app_path}",
            )
        if source_sha is not None:
            try:
                with (app_path / "Info.plist").open("rb") as source:
                    metadata = plistlib.load(source)
            except (OSError, plistlib.InvalidFileException, ValueError) as error:
                raise _stage_error("verify-source-commit", error) from error
            observed_sha = metadata.get("DobbySourceCommit") if isinstance(metadata, dict) else None
            if observed_sha != source_sha:
                raise IOSSimulatorStageError(
                    "verify-source-commit",
                    "Simulator app source commit metadata mismatch: "
                    f"expected={source_sha!r} observed={observed_sha!r}",
                )

    _timed_stage("build", build_app)
