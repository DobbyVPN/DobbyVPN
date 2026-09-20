"""Build and exercise the public Dobby iOS Simulator app without secrets."""

from __future__ import annotations

from dataclasses import dataclass
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import time
import traceback
from typing import Protocol, Sequence

from torturer_checks.ios_simulator import (
    IOSSimulatorContractError,
    SimulatorApp,
    simctl_boot_command,
    simctl_bootstatus_command,
    simctl_install_command,
    simctl_terminate_command,
    xcodebuild_ui_test_command,
)


_RUNTIME = re.compile(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+(?:-\d+)*)\Z")
_PROJECT_PATH = Path("swift_module/iosApp.xcodeproj")
_CONFIGURATION = "Release"
_APP_PRODUCT = "Dobby-Vpn.app"
_BUNDLE_IDENTIFIER = "vpn.dobby.app"
_APP_LOG_NAME = "app_logs.txt"
_GO_APP_LOG_NAME = "go_app_logs.jsonl"
_DEFAULT_ARCHITECTURE = "arm64"
_SUPPORTED_ARCHITECTURES = frozenset(("arm64", "amd64"))
_XCODE_ARCHITECTURES = {"arm64": "arm64", "amd64": "x86_64"}
_DIAGNOSTIC_TAIL_BYTES = 1024 * 1024
_SIMULATOR_LOG_TAIL_BYTES = 256 * 1024
_FAILURE_DIAGNOSTIC_TAIL_BYTES = 128 * 1024
_SIMULATOR_DIAGNOSTIC_TIMEOUT_SECONDS = 30
_SIMULATOR_PREFERENCE_DOMAIN = "com.apple.iphonesimulator"
_HARDWARE_KEYBOARD_PREFERENCE = "ConnectHardwareKeyboard"
MAX_RUN_SECONDS = 30 * 60
CLEANUP_RESERVE_SECONDS = 120
DEFAULT_COMMAND_TIMEOUT_SECONDS = 300
IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS = 15 * 60
IOS_GO_UI_BUILD_TIMEOUT_SECONDS = 15 * 60
IOS_UI_TEST_TIMEOUT_SECONDS = 10 * 60
COMMAND_TERMINATION_GRACE_SECONDS = 15

# These are deliberately stage-specific.  The old contract gave every
# command the same five-minute limit, which made an XCTest hang
# indistinguishable from a Simulator boot or install problem.  The values are
# upper bounds, not retry budgets: a timed-out stage fails once and cleanup
# keeps the original stage/error visible.
STAGE_TIMEOUT_SECONDS = {
    "list-devices": 30,
    "read-hardware-keyboard": 10,
    "write-hardware-keyboard": 10,
    "restore-hardware-keyboard": 15,
    "boot": 120,
    # An erased iOS 26 Simulator can spend several minutes in its normal
    # one-time Data Migration before bootstatus reports ready. Keep this a
    # single bounded wait; the lane budget still reserves cleanup time.
    "bootstatus": 360,
    "install": 180,
    "locate-app-logs": 30,
    "clear-app-logs": 30,
    "xctest-ui": IOS_UI_TEST_TIMEOUT_SECONDS,
    "terminate": 60,
    "collect-core-simulator-diagnostics": 30,
    "shutdown": 120,
    "build-ios-framework": IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS,
    "package-ios-app": IOS_GO_UI_BUILD_TIMEOUT_SECONDS,
}


class IOSSimulatorAppContractError(RuntimeError):
    """The fixed public Simulator check failed."""


class IOSSimulatorStageError(IOSSimulatorAppContractError):
    """A failure tied to one Simulator lifecycle/build stage.

    ``stage`` is intentionally machine-readable for local result consumers,
    while the string includes the original command diagnostic so a timeout or
    non-zero exit is never replaced by a generic "Simulator failed" message.
    """

    def __init__(
        self,
        stage: str,
        detail: str,
        *,
        timeout_seconds: float | None = None,
    ) -> None:
        self.stage = stage
        self.timeout_seconds = timeout_seconds
        normalized = detail.strip() or "no command diagnostic"
        timed_out = "timed out" in normalized.lower()
        if timed_out and timeout_seconds is not None:
            message = (
                f"iOS Simulator stage '{stage}' timed out after "
                f"{timeout_seconds:g}s: {normalized}"
            )
        else:
            message = f"iOS Simulator stage '{stage}' failed: {normalized}"
        super().__init__(message)


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
        """Execute one argument-vector command without a shell."""


def _signal_process_group(process: subprocess.Popen[bytes], sig: int) -> None:
    try:
        if os.name == "posix":
            os.killpg(process.pid, sig)
        elif sig == signal.SIGTERM:
            process.terminate()
        else:
            process.kill()
    except ProcessLookupError:
        pass


def _stop_process_group(process: subprocess.Popen[bytes], grace_seconds: float) -> tuple[bytes, bytes]:
    """Stop the command's process group and drain its pipes; no PID census."""
    _signal_process_group(process, signal.SIGTERM)
    try:
        stdout, stderr = process.communicate(timeout=grace_seconds)
    except subprocess.TimeoutExpired:
        _signal_process_group(process, signal.SIGKILL)
        try:
            stdout, stderr = process.communicate(timeout=max(1.0, grace_seconds))
        except subprocess.TimeoutExpired as error:
            process.kill()
            stdout, stderr = process.communicate()
            raise IOSSimulatorAppContractError(
                "iOS command pipes remained open after process-group cleanup"
            ) from error
    # The parent may have exited while a background command still shares its
    # process group but not its pipes. Reap that group as part of timeout cleanup.
    _signal_process_group(process, signal.SIGKILL)
    return stdout or b"", stderr or b""


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
        try:
            process = subprocess.Popen(
                list(command),
                cwd=cwd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                start_new_session=(os.name == "posix"),
            )
        except OSError as error:
            raise IOSSimulatorAppContractError(f"iOS command could not start: {error}") from error
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            try:
                stdout, stderr = _stop_process_group(
                    process,
                    min(COMMAND_TERMINATION_GRACE_SECONDS, max(1.0, timeout)),
                )
            except IOSSimulatorAppContractError as cleanup_error:
                stdout = error.output or b""
                stderr = error.stderr or b""
                raise IOSSimulatorAppContractError(
                    f"iOS command timed out after {timeout:g}s; cleanup failed: {cleanup_error}; "
                    f"stdout={_decode(stdout)}\nstderr={_decode(stderr)}"
                ) from error
            raise IOSSimulatorAppContractError(
                f"iOS command timed out after {timeout:g}s; "
                f"stdout={_decode(stdout)}\nstderr={_decode(stderr)}"
            ) from error
        result = CommandResult(
            returncode=process.returncode if process.returncode is not None else -1,
            stdout=_decode(stdout or b""),
            stderr=_decode(stderr or b""),
        )
        if result.stdout:
            print(result.stdout, end="")
        if result.stderr:
            print(result.stderr, file=sys.stderr, end="")
        return result


def _decode(payload: bytes) -> str:
    return payload.decode("utf-8", errors="replace")


@dataclass(frozen=True)
class AvailableSimulator:
    udid: str
    name: str
    runtime: str


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


def select_available_iphone(simctl_devices_json: str) -> AvailableSimulator:
    try:
        devices = json.loads(simctl_devices_json)["devices"]
    except (KeyError, TypeError, ValueError) as error:
        raise IOSSimulatorAppContractError("simctl did not provide a readable device inventory") from error
    if not isinstance(devices, dict):
        raise IOSSimulatorAppContractError("simctl device inventory has an invalid shape")
    candidates: list[tuple[tuple[int, ...], str, str, str]] = []
    for runtime, entries in devices.items():
        match = _RUNTIME.fullmatch(runtime) if isinstance(runtime, str) else None
        if match is None or not isinstance(entries, list):
            continue
        version = tuple(int(part) for part in match.group(1).split("-"))
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
            candidates.append((version, name, udid, runtime))
    if not candidates:
        raise IOSSimulatorAppContractError("no available iPhone Simulator was found")
    version, name, udid, runtime = max(candidates, key=lambda item: (item[0], item[1], item[2]))
    del version
    return AvailableSimulator(udid=udid, name=name, runtime=runtime)


def xcodebuild_app_command(
    contract: IOSSimulatorAppContract,
    *, candidate_root: Path,
    device_udid: str,
    work_dir: Path,
) -> list[str]:
    try:
        udid = simctl_boot_command(device_udid)[-1]
    except IOSSimulatorContractError as error:
        raise IOSSimulatorAppContractError(str(error)) from error
    del udid
    go_framework = candidate_root / "go_module" / "DobbyVPNRuntime.xcframework"
    return [
        "/bin/bash", "scripts/package_ios_app.sh", "iossimulator",
        str(contract.app_path(work_dir)), str(go_framework), contract.architecture,
    ]


def simctl_get_app_container_command(device_udid: str) -> list[str]:
    try:
        udid = simctl_boot_command(device_udid)[-1]
    except IOSSimulatorContractError as error:
        raise IOSSimulatorAppContractError(str(error)) from error
    # A provisioning-free Simulator app cannot receive a real App Group
    # container. The native shell deliberately uses FileManager's temporary
    # directory for Simulator builds, which lives below the app data
    # container. Physical iOS builds continue to use the App Group boundary.
    return ["xcrun", "simctl", "get_app_container", udid, _BUNDLE_IDENTIFIER, "data"]


def _stage_timeout(
    budget: RunBudget | None,
    stage: str,
    requested: float | None = None,
) -> float:
    value = requested if requested is not None else STAGE_TIMEOUT_SECONDS.get(
        stage, DEFAULT_COMMAND_TIMEOUT_SECONDS
    )
    return budget.operation_timeout(value) if budget is not None else value


def _stage_error(
    stage: str,
    error: BaseException | str,
    *,
    timeout_seconds: float | None = None,
) -> IOSSimulatorStageError:
    if isinstance(error, IOSSimulatorStageError) and error.stage == stage:
        return error
    return IOSSimulatorStageError(
        stage,
        str(error) or error.__class__.__name__,
        timeout_seconds=timeout_seconds,
    )


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
    try:
        # Cleanup callers pass a timeout already bounded by the cleanup
        # reserve. Reapplying ``operation_timeout`` there would subtract the
        # reserve twice and could skip a still-available shutdown window.
        effective_timeout = (
            timeout_seconds
            if bounded_timeout and timeout_seconds is not None
            else _stage_timeout(budget, stage, timeout_seconds)
        )
        if effective_timeout <= 0:
            raise IOSSimulatorAppContractError(
                f"iOS Simulator stage '{stage}' has no time remaining"
            )
        result = runner.run(command, cwd=cwd, timeout_seconds=effective_timeout)
    except BaseException as error:
        # Preserve the command runner's stdout/stderr and timeout wording, but
        # make the failed lifecycle/build stage unambiguous to callers.
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise
        raise _stage_error(stage, error, timeout_seconds=effective_timeout) from error
    if result.returncode:
        output = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
        detail = f"exit code {result.returncode}" + (f":\n{output}" if output else "")
        raise IOSSimulatorStageError(stage, detail, timeout_seconds=effective_timeout)
    return result


def _app_container(
    runner: CommandRunner,
    device_udid: str,
    *,
    budget: RunBudget,
    best_effort: bool = False,
) -> Path | None:
    try:
        result = _require_success(
            runner,
            simctl_get_app_container_command(device_udid),
            "locate-app-logs",
            budget=budget,
        )
        # `simctl get_app_container ... data` returns one absolute data
        # container path. The native Simulator logger writes to its
        # app-owned temporary directory below that path. Accepting a lone
        # absolute path keeps the parsing independent of simctl's wording.
        paths: list[Path] = []
        for raw_line in result.stdout.splitlines():
            line = raw_line.strip()
            if not line:
                continue
            candidate = line
            path = Path(candidate)
            if path.is_absolute():
                paths.append(path)
        if len(paths) != 1:
            raise IOSSimulatorStageError(
                "locate-app-logs",
                "simctl data output did not contain exactly one absolute "
                "app data container path",
            )
        return paths[0] / "tmp"
    except (IOSSimulatorAppContractError, OSError) as error:
        if best_effort:
            return None
        if isinstance(error, IOSSimulatorStageError):
            raise
        raise _stage_error("locate-app-logs", error) from error


def _log_bytes(path: Path) -> bytes:
    try:
        return path.read_bytes()
    except OSError:
        return b""


def _retain_diagnostics(container: Path | None, diagnostic_dir: Path | None) -> None:
    """Copy a bounded tail of app-owned logs when available; diagnostics never decide pass/fail."""
    if container is None or diagnostic_dir is None:
        return
    for name in (_APP_LOG_NAME, _GO_APP_LOG_NAME):
        source = container / name
        try:
            if not source.is_file():
                continue
            payload = source.read_bytes()[-_DIAGNOSTIC_TAIL_BYTES:]
            diagnostic_dir.mkdir(parents=True, exist_ok=True)
            (diagnostic_dir / name).write_bytes(payload)
        except OSError:
            pass


def _collect_simulator_diagnostics(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    *,
    budget: RunBudget,
    diagnostic_dir: Path | None,
) -> str | None:
    """Capture bounded CoreSimulator logs when launch or UI interaction fails.

    XCTest owns the app launch for this lane. A successful test-runner launch
    still only proves that SpringBoard accepted the bundle: the process can
    exit during native initialization or fail its accessibility actions, and
    the app-owned log is then legitimately empty. CoreSimulator's unified log
    is the useful diagnostic in that case. Collection is best effort and never
    replaces the original failure.
    """
    command = [
        "xcrun", "simctl", "spawn", simulator.udid, "log", "show",
        "--last", "90s", "--style", "compact",
        "--predicate",
        "process == \"Dobby-Vpn\" OR process == \"main\" OR "
        "process == \"vpn.dobby.app\" OR process == \"SpringBoard\" OR "
        "process == \"ReportCrash\" OR process == \"runningboardd\"",
    ]
    try:
        result = runner.run(
            command,
            timeout_seconds=min(
                _SIMULATOR_DIAGNOSTIC_TIMEOUT_SECONDS,
                budget.cleanup_timeout(),
            ),
        )
    except BaseException as error:
        return f"stage=collect-core-simulator-diagnostics failed: {error}"
    payload = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
    if not payload:
        return "stage=collect-core-simulator-diagnostics detail=empty"
    encoded = payload.encode("utf-8", errors="replace")
    bounded = encoded[-_SIMULATOR_LOG_TAIL_BYTES:]
    text = bounded.decode("utf-8", errors="replace")
    if diagnostic_dir is not None:
        try:
            diagnostic_dir.mkdir(parents=True, exist_ok=True)
            (diagnostic_dir / "simulator.log.txt").write_bytes(bounded)
        except OSError:
            pass
    return "stage=collect-core-simulator-diagnostics\n" + text


def _add_note(failure: BaseException | None, label: str, error: BaseException) -> BaseException:
    if failure is None:
        return error
    failure.add_note(f"{label}:\n" + "".join(traceback.format_exception(error)).rstrip())
    return failure


def _retain_failure_diagnostic(
    diagnostic_dir: Path | None,
    failure: BaseException | None,
) -> None:
    """Retain the stage and cleanup notes without replacing the raised error."""
    if diagnostic_dir is None or failure is None:
        return
    try:
        payload = "".join(traceback.format_exception(failure))
        encoded = payload.encode("utf-8", errors="replace")[-_FAILURE_DIAGNOSTIC_TAIL_BYTES:]
        diagnostic_dir.mkdir(parents=True, exist_ok=True)
        stage = getattr(failure, "stage", "unknown")
        (diagnostic_dir / "failure-stage.txt").write_text(
            str(stage) + "\n",
            encoding="utf-8",
        )
        (diagnostic_dir / "failure.txt").write_bytes(encoded)
    except OSError:
        # Diagnostics are useful but never become a second test result.
        pass


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
    timeout = min(STAGE_TIMEOUT_SECONDS["terminate"], budget.cleanup_timeout())
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
        # failure. In that state cleanup is already complete and simctl uses
        # exit 3 with this stable message; do not turn it into a second error.
        if "found nothing to terminate" in output.lower():
            return
        detail = f"exit code {result.returncode}" + (f":\n{output}" if output else "")
        raise IOSSimulatorStageError("terminate", detail, timeout_seconds=timeout)


def _disable_simulator_hardware_keyboard(
    runner: CommandRunner, *, budget: RunBudget
) -> str | None:
    """Expose the software keyboard that XCTest taps for real Fyne input."""
    read_timeout = _stage_timeout(budget, "read-hardware-keyboard")
    try:
        read = runner.run(
            [
                "/usr/bin/defaults", "read", _SIMULATOR_PREFERENCE_DOMAIN,
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
        output = "\n".join(
            part for part in (read.stdout, read.stderr) if part
        ).strip()
        raise IOSSimulatorStageError(
            "read-hardware-keyboard",
            "exit code 1" + (f":\n{output}" if output else ""),
            timeout_seconds=read_timeout,
        )
    if previous not in {None, "0", "1"}:
        raise IOSSimulatorStageError(
            "read-hardware-keyboard",
            "preference has an unsupported value",
            timeout_seconds=read_timeout,
        )
    _require_success(
        runner,
        [
            "/usr/bin/defaults", "write", _SIMULATOR_PREFERENCE_DOMAIN,
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
            "/usr/bin/defaults", "delete", _SIMULATOR_PREFERENCE_DOMAIN,
            _HARDWARE_KEYBOARD_PREFERENCE,
        ]
    else:
        command = [
            "/usr/bin/defaults", "write", _SIMULATOR_PREFERENCE_DOMAIN,
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


def run_ios_simulator_app_contract(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    contract: IOSSimulatorAppContract = PUBLIC_IOS_SIMULATOR_APP_CONTRACT,
    budget: RunBudget | None = None,
    diagnostic_dir: Path | None = None,
) -> IOSSimulatorAppEvidence:
    """Run the one comprehensive, rendered Go/Fyne Simulator mini contract.

    Simulator UI proves accessibility, native input, lifecycle and visible
    error/presentation behavior only.  It never claims NetworkExtension or
    packet-tunnel success.
    """
    budget = budget or RunBudget()
    work_dir.mkdir(parents=True, exist_ok=True)
    simulator: AvailableSimulator | None = None
    container: Path | None = None
    app_installed = False
    boot_started = False
    keyboard_preference_configured = False
    previous_keyboard_preference: str | None = None
    failure: BaseException | None = None
    evidence: IOSSimulatorAppEvidence | None = None
    app_path = contract.app_path(work_dir)

    try:
        inventory = _require_success(
            runner,
            ["xcrun", "simctl", "list", "devices", "available", "-j"],
            "list-devices",
            budget=budget,
        )
        try:
            simulator = select_available_iphone(inventory.stdout)
        except BaseException as error:
            if isinstance(error, (KeyboardInterrupt, SystemExit)):
                raise
            raise _stage_error("select-device", error) from error
        previous_keyboard_preference = _disable_simulator_hardware_keyboard(
            runner, budget=budget
        )
        keyboard_preference_configured = True
        boot_started = True
        boot_timeout = _stage_timeout(budget, "boot")
        try:
            boot = runner.run(
                simctl_boot_command(simulator.udid),
                timeout_seconds=boot_timeout,
            )
        except BaseException as error:
            if isinstance(error, (KeyboardInterrupt, SystemExit)):
                raise
            raise _stage_error("boot", error, timeout_seconds=boot_timeout) from error
        if boot.returncode and "current state: Booted" not in (boot.stdout + boot.stderr):
            output = "\n".join(part for part in (boot.stdout, boot.stderr) if part).strip()
            raise IOSSimulatorStageError(
                "boot",
                f"exit code {boot.returncode}" + (f":\n{output}" if output else ""),
                timeout_seconds=boot_timeout,
            )
        _require_success(
            runner,
            simctl_bootstatus_command(simulator.udid),
            "bootstatus",
            budget=budget,
        )

        if not app_path.is_dir():
            raise IOSSimulatorStageError(
                "verify-app-bundle",
                f"Go/Fyne Simulator build produced no app bundle: {app_path}",
            )
        _require_success(
            runner,
            simctl_install_command(simulator.udid, app_path),
            "install",
            budget=budget,
        )
        app_installed = True
        # App-owned logs are diagnostic only. The UI test below is the pass
        # condition, so a missing sandbox path must not turn a real accessible
        # UI into a marker-based claim or block cleanup.
        container = _app_container(runner, simulator.udid, budget=budget, best_effort=True)
        if container is not None:
            log_path = container / _APP_LOG_NAME
            try:
                log_path.parent.mkdir(parents=True, exist_ok=True)
                log_path.write_bytes(b"")
            except OSError:
                container = None
        project = candidate_root / _PROJECT_PATH
        if not project.is_dir():
            raise IOSSimulatorStageError(
                "verify-ui-test-project",
                f"iOS XCTest project is unavailable: {project}",
            )
        # Do not pre-launch with simctl. The XCTest target owns the first app
        # launch and its in-test terminate/reopen lifecycle; handing it an
        # already-running simctl process can block setUp before test output.
        _require_success(
            runner,
            xcodebuild_ui_test_command(
                simulator.udid,
                project,
                work_dir / "ui-tests",
            ),
            "xctest-ui",
            cwd=candidate_root,
            budget=budget,
            timeout_seconds=STAGE_TIMEOUT_SECONDS["xctest-ui"],
        )

        evidence = IOSSimulatorAppEvidence(
            simulator=simulator,
            app=SimulatorApp(app_path=app_path,
                             bundle_identifier=contract.bundle_identifier,
                             architecture=contract.architecture),
        )
    except BaseException as error:
        failure = error
    finally:
        if simulator is not None:
            if app_installed:
                try:
                    _terminate_app(runner, simulator, contract, budget=budget)
                except BaseException as error:
                    failure = _add_note(failure, "Simulator app cleanup also failed", error)
            if diagnostic_dir is not None:
                if container is None and boot_started:
                    container = _app_container(
                        runner, simulator.udid, budget=budget, best_effort=True,
                    )
                _retain_diagnostics(container, Path(diagnostic_dir))
            if failure is not None and boot_started:
                diagnostics = _collect_simulator_diagnostics(
                    runner,
                    simulator,
                    budget=budget,
                    diagnostic_dir=Path(diagnostic_dir) if diagnostic_dir is not None else None,
                )
                if diagnostics:
                    failure.add_note("CoreSimulator diagnostics:\n" + diagnostics)
            if boot_started:
                try:
                    _shutdown_simulator(runner, simulator, budget=budget)
                except BaseException as error:
                    failure = _add_note(failure, "Simulator shutdown also failed", error)
        if keyboard_preference_configured:
            try:
                _restore_simulator_hardware_keyboard(
                    runner, previous_keyboard_preference, budget=budget
                )
            except BaseException as error:
                failure = _add_note(
                    failure, "Simulator keyboard preference cleanup also failed", error
                )
        _retain_failure_diagnostic(
            Path(diagnostic_dir) if diagnostic_dir is not None else None,
            failure,
        )
    if failure is not None:
        raise failure.with_traceback(failure.__traceback__)
    if evidence is None:
        raise IOSSimulatorAppContractError("iOS Simulator check produced no evidence")
    budget.assert_within_deadline()
    return evidence


def prepare_ios_simulator_candidate(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    contract: IOSSimulatorAppContract,
    budget: RunBudget,
) -> None:
    """Build and stage the native frameworks needed by the Simulator app."""
    candidate_root = Path(candidate_root).resolve()
    work_dir.mkdir(parents=True, exist_ok=True)
    go_root = candidate_root / "go_module"
    go_framework = go_root / "DobbyVPNRuntime.xcframework"
    app_path = contract.app_path(work_dir)

    _require_success(
        runner,
        ["/bin/bash", "scripts/build_ios_xcframework.sh", "--simulator-architecture", contract.architecture],
        "build-ios-framework",
        cwd=go_root,
        budget=budget,
        timeout_seconds=IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS,
    )
    if not go_framework.is_dir():
        raise IOSSimulatorStageError(
            "build-ios-framework",
            f"iOS Go build produced no XCFramework: {go_framework}",
        )
    _require_success(
        runner,
        ["/bin/bash", "scripts/package_ios_app.sh", "iossimulator", str(app_path), str(go_framework), contract.architecture],
        "package-ios-app",
        cwd=go_root,
        timeout_seconds=IOS_GO_UI_BUILD_TIMEOUT_SECONDS,
    )
    if not app_path.is_dir():
        raise IOSSimulatorStageError(
            "package-ios-app",
            f"Go/Fyne build produced no Simulator app: {app_path}",
        )
