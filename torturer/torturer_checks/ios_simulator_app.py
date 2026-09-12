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
    simctl_launch_command,
    simctl_terminate_command,
)


_RUNTIME = re.compile(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+(?:-\d+)*)\Z")
_PROJECT_PATH = Path("swift_module/iosApp.xcodeproj")
_SCHEME_NAME = "iosApp"
_CONFIGURATION = "Debug"
_APP_PRODUCT = "doBBYVPN.app"
_BUNDLE_IDENTIFIER = "vpn.dobby.app"
_APP_LOG_CONTAINER_IDENTIFIER = "group.vpn.dobby.app"
_APP_LOG_NAME = "app_logs.txt"
_GO_APP_LOG_NAME = "go_app_logs.jsonl"
_MINI_STARTUP_MARKER = b"startup.initialized mode=mini"
_METAL_STARTUP_MARKER = b"startup.ui_attached mode=normal"
_DEFAULT_ARCHITECTURE = "arm64"
_SUPPORTED_ARCHITECTURES = frozenset(("arm64", "amd64"))
_XCODE_ARCHITECTURES = {"arm64": "arm64", "amd64": "x86_64"}
_STARTUP_WAIT_SECONDS = 60
_DIAGNOSTIC_TAIL_BYTES = 1024 * 1024
MAX_RUN_SECONDS = 30 * 60
CLEANUP_RESERVE_SECONDS = 120
DEFAULT_COMMAND_TIMEOUT_SECONDS = 300
IOS_KMP_BUILD_TIMEOUT_SECONDS = 15 * 60
COMMAND_TERMINATION_GRACE_SECONDS = 15


class IOSSimulatorAppContractError(RuntimeError):
    """The fixed public Simulator check failed."""


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
    scheme: str = _SCHEME_NAME
    configuration: str = _CONFIGURATION
    app_product_name: str = _APP_PRODUCT
    bundle_identifier: str = _BUNDLE_IDENTIFIER
    architecture: str = _DEFAULT_ARCHITECTURE

    def __post_init__(self) -> None:
        if self.project_relative_path != _PROJECT_PATH:
            raise IOSSimulatorAppContractError("iOS Simulator project path is fixed by the public contract")
        if self.scheme != _SCHEME_NAME or self.configuration != _CONFIGURATION:
            raise IOSSimulatorAppContractError("iOS Simulator scheme and configuration are fixed")
        if self.app_product_name != _APP_PRODUCT or self.bundle_identifier != _BUNDLE_IDENTIFIER:
            raise IOSSimulatorAppContractError("iOS Simulator app identity is fixed by the public contract")
        if self.architecture not in _SUPPORTED_ARCHITECTURES:
            raise IOSSimulatorAppContractError("Simulator architecture must be arm64 or amd64")

    def app_path(self, work_dir: Path) -> Path:
        return work_dir / "derived-data" / "Build" / "Products" / f"{self.configuration}-iphonesimulator" / self.app_product_name


PUBLIC_IOS_SIMULATOR_APP_CONTRACT = IOSSimulatorAppContract()
SIMULATOR_MODES = ("mini", "metal")


def _validate_mode(mode: str) -> str:
    if mode not in SIMULATOR_MODES:
        raise IOSSimulatorAppContractError(f"iOS Simulator mode must be one of: {', '.join(SIMULATOR_MODES)}")
    return mode


def metal_probe_command() -> list[str]:
    return ["swift", "-e", "import Metal; print(MTLCreateSystemDefaultDevice() != nil)"]


def require_metal(runner: CommandRunner, *, budget: RunBudget) -> None:
    result = runner.run(metal_probe_command(), timeout_seconds=budget.operation_timeout(30))
    if result.returncode or result.stdout.strip().lower() != "true":
        detail = (result.stdout + result.stderr).strip() or "no Metal device"
        raise IOSSimulatorAppContractError(
            f"iOS-Simulator-Metal requires a usable Metal device; capability probe returned {detail!r}"
        )


def public_ios_simulator_app_contract(architecture: str) -> IOSSimulatorAppContract:
    return IOSSimulatorAppContract(architecture=architecture)


@dataclass(frozen=True)
class IOSSimulatorAppEvidence:
    simulator: AvailableSimulator
    app: SimulatorApp
    mode: str


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
    mode: str = "metal",
) -> list[str]:
    mode = _validate_mode(mode)
    try:
        udid = simctl_boot_command(device_udid)[-1]
    except IOSSimulatorContractError as error:
        raise IOSSimulatorAppContractError(str(error)) from error
    command = [
        "xcodebuild", "build", "-project", str(candidate_root / contract.project_relative_path),
        "-scheme", contract.scheme, "-configuration", contract.configuration,
        "-sdk", "iphonesimulator", "-destination", f"platform=iOS Simulator,id={udid}",
        "-derivedDataPath", str(work_dir / "derived-data"),
        f"ARCHS={_XCODE_ARCHITECTURES[contract.architecture]}",
        "CODE_SIGNING_ALLOWED=YES", "CODE_SIGNING_REQUIRED=NO", "CODE_SIGN_IDENTITY=-",
    ]
    if mode == "mini":
        command.append("SWIFT_ACTIVE_COMPILATION_CONDITIONS=$(inherited) DOBBY_SIMULATOR_MINI")
    return command


def simctl_get_app_container_command(device_udid: str) -> list[str]:
    try:
        udid = simctl_boot_command(device_udid)[-1]
    except IOSSimulatorContractError as error:
        raise IOSSimulatorAppContractError(str(error)) from error
    return ["xcrun", "simctl", "get_app_container", udid, _BUNDLE_IDENTIFIER, _APP_LOG_CONTAINER_IDENTIFIER]


def _require_success(
    runner: CommandRunner,
    command: Sequence[str],
    stage: str,
    *,
    cwd: Path | None = None,
    budget: RunBudget | None = None,
    timeout_seconds: float | None = None,
) -> CommandResult:
    if timeout_seconds is None:
        timeout_seconds = budget.operation_timeout(DEFAULT_COMMAND_TIMEOUT_SECONDS) if budget else DEFAULT_COMMAND_TIMEOUT_SECONDS
    result = runner.run(command, cwd=cwd, timeout_seconds=timeout_seconds)
    if result.returncode:
        output = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
        raise IOSSimulatorAppContractError(
            f"{stage} failed with exit code {result.returncode}" + (f":\n{output}" if output else "")
        )
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
            "locate iOS app logs",
            budget=budget,
        )
        paths = [line.strip() for line in result.stdout.splitlines() if line.strip()]
        if len(paths) != 1 or not Path(paths[0]).is_absolute():
            raise IOSSimulatorAppContractError("simctl returned no absolute app-group container path")
        return Path(paths[0])
    except (IOSSimulatorAppContractError, OSError):
        if best_effort:
            return None
        raise


def _log_bytes(path: Path) -> bytes:
    try:
        return path.read_bytes()
    except OSError:
        return b""


def _wait_for_startup(
    log_path: Path,
    baseline: bytes,
    *,
    mode: str,
    budget: RunBudget,
) -> None:
    marker = _MINI_STARTUP_MARKER if mode == "mini" else _METAL_STARTUP_MARKER
    deadline = min(
        budget.deadline - budget.cleanup_reserve_seconds,
        budget.clock() + _STARTUP_WAIT_SECONDS,
    )
    while True:
        current = _log_bytes(log_path)
        new_records = current[len(baseline):] if current.startswith(baseline) else current
        if marker in new_records:
            return
        remaining = deadline - budget.clock()
        if remaining <= 0:
            message = (
                "Mini Simulator app did not write startup.initialized mode=mini"
                if mode == "mini"
                else "Metal Simulator app did not attach its main view"
            )
            raise IOSSimulatorAppContractError(message)
        time.sleep(min(0.1, remaining))


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


def _add_note(failure: BaseException | None, label: str, error: BaseException) -> BaseException:
    if failure is None:
        return error
    failure.add_note(f"{label}:\n" + "".join(traceback.format_exception(error)).rstrip())
    return failure


def _shutdown_simulator(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    *,
    budget: RunBudget,
) -> None:
    result = runner.run(
        ["xcrun", "simctl", "shutdown", simulator.udid],
        timeout_seconds=budget.cleanup_timeout(),
    )
    if result.returncode:
        output = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
        raise IOSSimulatorAppContractError(
            f"Simulator shutdown failed with exit code {result.returncode}" + (f":\n{output}" if output else "")
        )


def _terminate_app(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    contract: IOSSimulatorAppContract,
    *,
    budget: RunBudget,
) -> None:
    _require_success(
        runner,
        simctl_terminate_command(simulator.udid, contract.bundle_identifier),
        "terminate Simulator app",
        budget=budget,
        timeout_seconds=budget.cleanup_timeout(),
    )


def run_ios_simulator_app_contract(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    mode: str = "metal",
    contract: IOSSimulatorAppContract = PUBLIC_IOS_SIMULATOR_APP_CONTRACT,
    budget: RunBudget | None = None,
    diagnostic_dir: Path | None = None,
) -> IOSSimulatorAppEvidence:
    """Launch the app, verify its startup marker, then shut down the Simulator."""
    mode = _validate_mode(mode)
    budget = budget or RunBudget()
    if mode == "metal":
        require_metal(runner, budget=budget)
    work_dir.mkdir(parents=True, exist_ok=True)
    simulator: AvailableSimulator | None = None
    container: Path | None = None
    app_launched = False
    boot_started = False
    failure: BaseException | None = None
    evidence: IOSSimulatorAppEvidence | None = None
    app_path = contract.app_path(work_dir)

    try:
        inventory = _require_success(
            runner,
            ["xcrun", "simctl", "list", "devices", "available", "-j"],
            "list Simulators",
            budget=budget,
        )
        simulator = select_available_iphone(inventory.stdout)
        boot_started = True
        boot = runner.run(
            simctl_boot_command(simulator.udid),
            timeout_seconds=budget.operation_timeout(60),
        )
        if boot.returncode and "current state: Booted" not in (boot.stdout + boot.stderr):
            output = "\n".join(part for part in (boot.stdout, boot.stderr) if part).strip()
            raise IOSSimulatorAppContractError(f"boot Simulator failed" + (f":\n{output}" if output else ""))
        _require_success(
            runner,
            simctl_bootstatus_command(simulator.udid),
            "wait for Simulator boot",
            budget=budget,
        )

        _require_success(
            runner,
            xcodebuild_app_command(
                contract,
                candidate_root=candidate_root,
                device_udid=simulator.udid,
                work_dir=work_dir,
                mode=mode,
            ),
            f"build {mode.title()} Simulator app",
            budget=budget,
        )
        if not app_path.is_dir():
            raise IOSSimulatorAppContractError(f"iOS Simulator build produced no app bundle: {app_path}")
        _require_success(
            runner,
            simctl_install_command(simulator.udid, app_path),
            "install Simulator app",
            budget=budget,
        )
        container = _app_container(runner, simulator.udid, budget=budget)
        if container is None:
            raise IOSSimulatorAppContractError("Simulator app log container is unavailable")
        log_path = container / _APP_LOG_NAME
        baseline = _log_bytes(log_path)
        _require_success(
            runner,
            simctl_launch_command(simulator.udid, contract.bundle_identifier),
            "launch Simulator app",
            budget=budget,
        )
        app_launched = True
        _wait_for_startup(log_path, baseline, mode=mode, budget=budget)

        evidence = IOSSimulatorAppEvidence(
            simulator=simulator,
            app=SimulatorApp(app_path=app_path,
                             bundle_identifier=contract.bundle_identifier,
                             architecture=contract.architecture),
            mode=mode,
        )
    except BaseException as error:
        failure = error
    finally:
        if simulator is not None:
            if app_launched:
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
            if boot_started:
                try:
                    _shutdown_simulator(runner, simulator, budget=budget)
                except BaseException as error:
                    failure = _add_note(failure, "Simulator shutdown also failed", error)
    if failure is not None:
        raise failure.with_traceback(failure.__traceback__)
    if evidence is None:
        raise IOSSimulatorAppContractError("iOS Simulator check produced no evidence")
    budget.assert_within_deadline()
    return evidence


def _ios_test_tasks(architecture: str) -> tuple[str, str]:
    if architecture == "arm64":
        return (":app:linkDebugFrameworkIosSimulatorArm64", ":app:iosSimulatorArm64Test")
    if architecture == "amd64":
        return (":app:linkDebugFrameworkIosX64", ":app:iosX64Test")
    raise IOSSimulatorAppContractError(f"unsupported iOS Simulator architecture: {architecture}")


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
    kmp_root = candidate_root / "kmp_module"
    swift_root = candidate_root / "swift_module"
    go_framework = go_root / "DobbyVPNRuntime.xcframework"
    staged_go_framework = swift_root / "DobbyVPNRuntime.xcframework"
    staged_kmp_framework = swift_root / "app.framework"

    _require_success(
        runner,
        ["/bin/bash", "scripts/build_ios_xcframework.sh", "--simulator-architecture", contract.architecture],
        "build iOS Go framework",
        cwd=go_root,
        budget=budget,
    )
    if not go_framework.is_dir():
        raise IOSSimulatorAppContractError(f"iOS Go build produced no XCFramework: {go_framework}")
    _require_success(
        runner,
        ["/usr/bin/ditto", str(go_framework), str(staged_go_framework)],
        "stage iOS Go framework",
        budget=budget,
    )

    link_task, test_task = _ios_test_tasks(contract.architecture)
    _require_success(
        runner,
        ["./gradlew", link_task, test_task, "--rerun-tasks", "--no-daemon", "--stacktrace"],
        "run iOS KMP tests",
        cwd=kmp_root,
        timeout_seconds=budget.operation_timeout(IOS_KMP_BUILD_TIMEOUT_SECONDS),
    )
    framework_relative = "iosSimulatorArm64" if contract.architecture == "arm64" else "iosX64"
    framework_path = kmp_root / "app" / "build" / "bin" / framework_relative / "debugFramework" / "app.framework"
    if not framework_path.is_dir():
        raise IOSSimulatorAppContractError(f"iOS KMP build produced no Simulator framework: {framework_path}")
    _require_success(
        runner,
        ["/bin/rm", "-rf", str(staged_kmp_framework)],
        "clear staged iOS KMP framework",
        budget=budget,
    )
    _require_success(
        runner,
        ["/usr/bin/ditto", str(framework_path), str(staged_kmp_framework)],
        "stage iOS KMP framework",
        budget=budget,
    )
