"""Build and exercise the public Dobby iOS Simulator app without secrets."""

from __future__ import annotations

from dataclasses import dataclass
import gzip
import importlib.util
import json
import os
import plistlib
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from typing import Protocol, Sequence

from torturer_checks.diagnostics import (
    add_exception_notes,
    add_stream_notes,
    emit_streams,
)
from torturer_checks.ios_simulator import (
    IOSSimulatorContractError,
    SimulatorApp,
    iphonesimulator_sdk_version_command,
    simctl_boot_command,
    simctl_bootstatus_command,
    simctl_get_app_container_command,
    simctl_install_command,
    simctl_terminate_command,
    xcodebuild_ui_test_command,
)
from torturer_checks.screenshot_artifacts import png_metadata


def _load_build_runtime_framework_validator():
    """Load the validator owned by the product's iOS build scripts.

    The functional suite consumes this module for the same preflight that the
    package script runs.  Loading by source path keeps the test package from
    becoming a production/build dependency while preserving one validator.
    """
    validator_path = (
        Path(__file__).resolve().parents[2]
        / "go_module"
        / "scripts"
        / "ios_runtime_framework.py"
    )
    spec = importlib.util.spec_from_file_location(
        "dobbyvpn_ios_runtime_framework", validator_path
    )
    if spec is None or spec.loader is None:
        raise ImportError(f"could not load iOS runtime validator: {validator_path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


_IOS_RUNTIME_FRAMEWORK = _load_build_runtime_framework_validator()
IOSRuntimeFrameworkError = _IOS_RUNTIME_FRAMEWORK.IOSRuntimeFrameworkError
validate_runtime_framework = _IOS_RUNTIME_FRAMEWORK.validate_runtime_framework


_RUNTIME = re.compile(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+(?:-\d+)*)\Z")
_SDK_VERSION = re.compile(r"\A\s*(\d+)\.(\d+)(?:\.\d+)?\s*\Z")
_PROJECT_PATH = Path("swift_module/iosApp.xcodeproj")
_CONFIGURATION = "Release"
_APP_PRODUCT = "Dobby-Vpn.app"
_BUNDLE_IDENTIFIER = "vpn.dobby.app"
_DEFAULT_ARCHITECTURE = "arm64"
_SUPPORTED_ARCHITECTURES = frozenset(("arm64", "amd64"))
_SIMULATOR_PREFERENCES_FILE = "com.apple.iphonesimulator.plist"
_HARDWARE_KEYBOARD_PREFERENCE = "ConnectHardwareKeyboard"
MAX_RUN_SECONDS = 30 * 60
CLEANUP_RESERVE_SECONDS = 120
DEFAULT_COMMAND_TIMEOUT_SECONDS = 300
IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS = 15 * 60
IOS_RUNTIME_FRAMEWORK_VALIDATION_TIMEOUT_SECONDS = 30
IOS_GO_UI_BUILD_TIMEOUT_SECONDS = 15 * 60
# A cold Xcode 26 build on the local x86_64 VM can spend almost ten minutes
# compiling and signing the XCTest runner before the first UI assertion runs.
# Keep one bounded attempt, but leave enough time for the actual interaction
# contract; RunBudget still enforces the 30-minute lane and cleanup reserve.
IOS_UI_TEST_TIMEOUT_SECONDS = 15 * 60
COMMAND_TERMINATION_GRACE_SECONDS = 15

# These are deliberately stage-specific.  The old contract gave every
# command the same five-minute limit, which made an XCTest hang
# indistinguishable from a Simulator boot or install problem.  The values are
# upper bounds, not retry budgets: a timed-out stage fails once and cleanup
# keeps the original stage/error visible.
STAGE_TIMEOUT_SECONDS = {
    "list-devices": 30,
    "read-sdk-version": 30,
    "read-hardware-keyboard": 10,
    "open-simulator": 30,
    "toggle-hardware-keyboard": 10,
    "restore-hardware-keyboard": 15,
    "boot": 120,
    # An erased iOS 26 Simulator can spend several minutes in its normal
    # one-time Data Migration before bootstatus reports ready. Keep this a
    # single bounded wait; the lane budget still reserves cleanup time.
    "bootstatus": 360,
    "install": 180,
    "xctest-ui": IOS_UI_TEST_TIMEOUT_SECONDS,
    "export-xctest-screenshots": 120,
    "terminate": 60,
    "shutdown": 120,
    "build-ios-framework": IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS,
    "validate-ios-framework": IOS_RUNTIME_FRAMEWORK_VALIDATION_TIMEOUT_SECONDS,
    "package-ios-app": IOS_GO_UI_BUILD_TIMEOUT_SECONDS,
}


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
            failure = IOSSimulatorAppContractError(
                f"iOS command could not start: {type(error).__name__}"
            )
            add_stream_notes(
                failure,
                "command",
                getattr(error, "stdout", None),
                getattr(error, "stderr", None),
            )
            raise failure from None
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            partial_stdout = error.output or b""
            partial_stderr = error.stderr or b""
            cleanup_error: BaseException | None = None
            try:
                stdout, stderr = _stop_process_group(
                    process,
                    min(COMMAND_TERMINATION_GRACE_SECONDS, max(1.0, timeout)),
                )
            except IOSSimulatorAppContractError as cleanup_failure:
                stdout, stderr = partial_stdout, partial_stderr
                cleanup_error = cleanup_failure
            failure = IOSSimulatorAppContractError(
                f"iOS command timed out after {timeout:g}s"
            )
            add_stream_notes(failure, "command", stdout, stderr)
            emit_streams("ios-command", stdout, stderr)
            if cleanup_error is not None:
                add_exception_notes(failure, "cleanup", cleanup_error)
            raise failure from None
        result = CommandResult(
            returncode=process.returncode if process.returncode is not None else -1,
            stdout=_decode(stdout or b""),
            stderr=_decode(stderr or b""),
        )
        emit_streams("ios-command", result.stdout, result.stderr)
        return result


def _decode(payload: bytes) -> str:
    # Keep invalid bytes visible through a reversible display form. The
    # original byte stream remains attached to failures by diagnostics.py;
    # replacement decoding would silently collapse distinct diagnostics.
    return payload.decode("utf-8", errors="backslashreplace")


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
    # XCTest keeps UI screenshots and failure attachments in this owned
    # result bundle. It is an extra diagnostic output; command streams remain
    # the authoritative process diagnostics.
    result_bundle: Path | None = None
    # The app's own complete gzip export is retained separately from the
    # XCTest result bundle.  XCTest attachments never replace this stream.
    log_exports: tuple[Path, ...] = ()


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
    candidates: list[tuple[tuple[int, ...], str, str, str]] = []
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
            candidates.append((version, name, udid, runtime))
    if not candidates:
        raise IOSSimulatorAppContractError(
            "no available iPhone Simulator matches active iphonesimulator SDK "
            f"{sdk_version_text}"
        )
    version, name, udid, runtime = max(candidates, key=lambda item: (item[0], item[1], item[2]))
    del version
    return AvailableSimulator(udid=udid, name=name, runtime=runtime)


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


def xcodebuild_app_command(
    contract: IOSSimulatorAppContract,
    *, candidate_root: Path,
    work_dir: Path,
    runtime_framework: Path | None = None,
) -> list[str]:
    go_framework = runtime_framework or candidate_root / "go_module" / "DobbyVPNRuntime.xcframework"
    return [
        "/bin/bash", "scripts/package_ios_app.sh", "iossimulator",
        str(contract.app_path(work_dir)), str(go_framework), contract.architecture,
    ]


def _validate_runtime_framework(
    runtime_framework: Path,
    architecture: str,
    *,
    runner: CommandRunner | None = None,
    timeout_seconds: float = IOS_RUNTIME_FRAMEWORK_VALIDATION_TIMEOUT_SECONDS,
) -> Path:
    try:
        return validate_runtime_framework(
            runtime_framework,
            architecture,
            platform_variant="simulator",
            runner=runner,
            timeout_seconds=timeout_seconds,
        )
    except IOSRuntimeFrameworkError as error:
        raise IOSSimulatorAppContractError(str(error)) from error


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
    detail = str(error).strip() or type(error).__name__
    wrapped = IOSSimulatorStageError(stage, detail, timeout_seconds=timeout_seconds)
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
        # Keep the failed lifecycle/build stage unambiguous to callers without
        # copying a command stream into the error or any result file.
        if isinstance(error, (KeyboardInterrupt, SystemExit)):
            raise
        raise _stage_error(stage, error, timeout_seconds=effective_timeout) from error
    if result.returncode:
        failure = IOSSimulatorStageError(
            stage,
            f"exit code {result.returncode}",
            timeout_seconds=effective_timeout,
        )
        emit_streams(f"ios-{stage}", result.stdout, result.stderr)
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


def _copy_complete_directory(source: Path, destination: Path, *, label: str) -> Path:
    """Copy one owned directory without accepting a partial destination."""
    if source.is_symlink() or not source.is_dir():
        raise IOSSimulatorAppContractError(f"{label} is not a complete directory: {source}")
    if destination.exists() or destination.is_symlink():
        try:
            if destination.is_symlink() or destination.is_file():
                destination.unlink()
            else:
                shutil.rmtree(destination)
        except OSError as error:
            raise IOSSimulatorAppContractError(
                f"could not replace retained {label}: {destination}"
            ) from error
    try:
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(source, destination)
    except OSError as error:
        raise IOSSimulatorAppContractError(
            f"could not retain complete {label}: {destination}"
        ) from error
    return destination


def _validate_gzip_export(path: Path) -> None:
    if path.is_symlink() or not path.is_file():
        raise IOSSimulatorAppContractError(f"iOS app log export is not a regular file: {path}")
    try:
        with gzip.open(path, "rb") as archive:
            payload = archive.read()
    except (OSError, EOFError) as error:
        raise IOSSimulatorAppContractError(
            f"iOS app log export gzip is unreadable: {path}"
        ) from error
    if not payload:
        raise IOSSimulatorAppContractError(f"iOS app log export gzip is empty: {path}")


def _retain_xctest_result_bundle(result_bundle: Path, work_dir: Path) -> Path:
    destination = _ios_diagnostics_directory(work_dir) / result_bundle.name
    return _copy_complete_directory(
        result_bundle,
        destination,
        label="XCTest result bundle",
    )


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
        copied: set[str] = set()
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
                if label in copied:
                    raise IOSSimulatorAppContractError(
                        f"xcresulttool returned duplicate iOS UI screenshot: {label}"
                    )
                source = screenshot_dir / exported_name
                if source.is_symlink() or not source.is_file():
                    raise IOSSimulatorAppContractError(
                        f"xcresulttool did not export complete iOS UI screenshot: {human_name}"
                    )
                diagnostics.mkdir(parents=True, exist_ok=True)
                destination.mkdir(mode=0o700, exist_ok=True)
                destination.chmod(0o700)
                screenshot = destination / f"{label}.png"
                try:
                    shutil.copy2(source, screenshot)
                    screenshot.chmod(0o600)
                    png_metadata(screenshot)
                except (OSError, ValueError) as error:
                    raise IOSSimulatorAppContractError(
                        f"could not retain complete iOS UI screenshot: {label}"
                    ) from error
                copied.add(label)

        if not copied:
            raise IOSSimulatorAppContractError(
                "xcresult contained no named DobbyVPN UI screenshots"
            )
        destination.chmod(0o700)
    return destination


def _collect_ios_app_log_exports(
    runner: CommandRunner,
    simulator: AvailableSimulator,
    contract: IOSSimulatorAppContract,
    work_dir: Path,
    *,
    budget: RunBudget,
) -> tuple[Path, ...]:
    """Retain the native app log and every complete app-created gzip."""
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
    if native_log.is_symlink() or not native_log.is_file():
        raise IOSSimulatorStageError(
            "collect-ios-native-log",
            "Simulator app did not leave its complete app_logs.txt diagnostic",
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

    # A failed app may not reach the UI action that exports its structured
    # gzip log. Preserve the native runtime log first so that absence of that
    # optional export cannot discard the diagnostics most useful for a crash.
    candidates = sorted(
        (
            path for path in container.rglob("DobbyVPN_logs_*.jsonl.gz")
            if not path.is_symlink() and path.is_file()
        ),
        key=lambda path: str(path),
    )
    if not candidates:
        raise IOSSimulatorStageError(
            "collect-app-log-export",
            "Simulator app did not leave a DobbyVPN_logs_*.jsonl.gz export",
        )

    retained: list[Path] = []
    for source in candidates:
        _validate_gzip_export(source)
        destination = destination_dir / source.name
        try:
            shutil.copy2(source, destination)
        except OSError as error:
            raise IOSSimulatorStageError(
                "collect-app-log-export",
                f"could not copy complete app log export: {destination}",
            ) from error
        try:
            _validate_gzip_export(destination)
        except IOSSimulatorAppContractError as error:
            raise IOSSimulatorStageError(
                "collect-app-log-export",
                f"retained app log export failed validation: {destination}",
            ) from error
        retained.append(destination)
    return tuple(retained)


def retain_ios_diagnostics(work_dir: Path, destination_dir: Path) -> tuple[Path, ...]:
    """Copy retained iOS artifacts into a local guest manifest directory."""
    source_dir = _ios_diagnostics_directory(work_dir)
    if source_dir.is_symlink() or not source_dir.is_dir():
        raise IOSSimulatorAppContractError(
            f"retained iOS diagnostics are missing: {source_dir}"
        )
    destination_dir.mkdir(parents=True, exist_ok=True)
    retained: list[Path] = []
    for source in sorted(source_dir.iterdir(), key=lambda path: path.name):
        destination = destination_dir / source.name
        if source.name.endswith(".xcresult"):
            retained.append(_copy_complete_directory(source, destination, label="XCTest result bundle"))
            continue
        if source.name == "ui-screenshots":
            retained.append(_copy_complete_directory(source, destination, label="iOS UI screenshots"))
            for screenshot in destination.iterdir():
                png_metadata(screenshot)
                screenshot.chmod(0o600)
            destination.chmod(0o700)
            continue
        if source.name == "app-native.log":
            if source.is_symlink() or not source.is_file():
                raise IOSSimulatorAppContractError(
                    f"native app log is not a regular file: {source}"
                )
            try:
                shutil.copy2(source, destination)
            except OSError as error:
                raise IOSSimulatorAppContractError(
                    f"could not retain complete native app log: {destination}"
                ) from error
            destination.chmod(0o600)
            retained.append(destination)
            continue
        if source.name.startswith("DobbyVPN_logs_") and source.name.endswith(".jsonl.gz"):
            _validate_gzip_export(source)
            try:
                shutil.copy2(source, destination)
            except OSError as error:
                raise IOSSimulatorAppContractError(
                    f"could not retain iOS app log export in guest logs: {destination}"
                ) from error
            _validate_gzip_export(destination)
            retained.append(destination)
    if not retained:
        raise IOSSimulatorAppContractError(
            f"retained iOS diagnostics contain no complete artifacts: {source_dir}"
        )
    return tuple(retained)


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
        del output
        failure = IOSSimulatorStageError(
            "terminate",
            f"exit code {result.returncode}",
            timeout_seconds=timeout,
        )
        emit_streams("ios-terminate", result.stdout, result.stderr)
        add_stream_notes(failure, "command", result.stdout, result.stderr)
        raise failure


def _read_simulator_hardware_keyboard(simulator_udid: str) -> bool:
    """Read the effective per-device Simulator keyboard setting.

    Simulator keeps this override under DevicePreferences/<UDID>. Its
    top-level ConnectHardwareKeyboard default does not override an existing
    per-device value.
    """
    preferences_path = (
        Path.home() / "Library" / "Preferences" / _SIMULATOR_PREFERENCES_FILE
    )
    try:
        with preferences_path.open("rb") as preferences_file:
            preferences = plistlib.load(preferences_file)
    except FileNotFoundError:
        return True
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
    value = device.get(_HARDWARE_KEYBOARD_PREFERENCE, True)
    if isinstance(value, bool):
        return value
    if isinstance(value, int) and value in (0, 1):
        return bool(value)
    raise IOSSimulatorStageError(
        "read-hardware-keyboard", "per-device preference has an unsupported value"
    )


def _toggle_simulator_hardware_keyboard(
    runner: CommandRunner,
    simulator_udid: str,
    *,
    stage: str,
    budget: RunBudget,
    cleanup: bool = False,
) -> None:
    open_timeout = (
        budget.cleanup_timeout()
        if cleanup
        else _stage_timeout(budget, "open-simulator")
    )
    _require_success(
        runner,
        [
            "/usr/bin/open", "-a", "Simulator", "--args",
            "-CurrentDeviceUDID", simulator_udid,
        ],
        "open-simulator",
        budget=budget,
        timeout_seconds=open_timeout,
        bounded_timeout=cleanup,
    )
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


def _disable_simulator_hardware_keyboard(
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
        state.simulator_udid,
        stage="toggle-hardware-keyboard",
        budget=budget,
    )
    if _read_simulator_hardware_keyboard(state.simulator_udid):
        raise IOSSimulatorStageError(
            "toggle-hardware-keyboard",
            "Simulator still reports its hardware keyboard connected",
            timeout_seconds=STAGE_TIMEOUT_SECONDS["toggle-hardware-keyboard"],
        )


def _restore_simulator_hardware_keyboard(
    runner: CommandRunner,
    state: SimulatorHardwareKeyboardState,
    *,
    budget: RunBudget,
) -> None:
    if not state.changed:
        return
    if _read_simulator_hardware_keyboard(state.simulator_udid) == state.was_connected:
        state.changed = False
        return
    _toggle_simulator_hardware_keyboard(
        runner,
        state.simulator_udid,
        stage="restore-hardware-keyboard",
        budget=budget,
        cleanup=True,
    )
    if _read_simulator_hardware_keyboard(state.simulator_udid) != state.was_connected:
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
) -> IOSSimulatorAppEvidence:
    """Run the one comprehensive, rendered Go/Fyne Simulator mini contract.

    Simulator UI proves accessibility, native input, lifecycle and visible
    error/presentation behavior only.  It never claims NetworkExtension or
    packet-tunnel success.
    """
    budget = budget or RunBudget()
    work_dir.mkdir(parents=True, exist_ok=True)
    simulator: AvailableSimulator | None = None
    app_installed = False
    boot_started = False
    keyboard_state: SimulatorHardwareKeyboardState | None = None
    failure: BaseException | None = None
    app_path = contract.app_path(work_dir)
    result_bundle = work_dir / "xctest-results.xcresult"
    xctest_started = False
    retained_result_bundle: Path | None = None
    retained_log_exports: tuple[Path, ...] = ()

    try:
        inventory = _require_success(
            runner,
            ["xcrun", "simctl", "list", "devices", "available", "-j"],
            "list-devices",
            budget=budget,
        )
        sdk_version = _active_iphonesimulator_sdk_version(runner, budget=budget)
        try:
            simulator = select_available_iphone(inventory.stdout, sdk_version)
        except BaseException as error:
            if isinstance(error, (KeyboardInterrupt, SystemExit)):
                raise
            raise _stage_error("select-device", error) from error
        keyboard_state = SimulatorHardwareKeyboardState(
            simulator_udid=simulator.udid,
            was_connected=_read_simulator_hardware_keyboard(simulator.udid),
        )
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
            failure = IOSSimulatorStageError(
                "boot",
                f"exit code {boot.returncode}",
                timeout_seconds=boot_timeout,
            )
            emit_streams("ios-boot", boot.stdout, boot.stderr)
            add_stream_notes(failure, "command", boot.stdout, boot.stderr)
            raise failure
        _require_success(
            runner,
            simctl_bootstatus_command(simulator.udid),
            "bootstatus",
            budget=budget,
        )
        _disable_simulator_hardware_keyboard(
            runner, keyboard_state, budget=budget
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
        _require_success(
            runner,
            xcodebuild_ui_test_command(
                simulator.udid,
                project,
                work_dir / "ui-tests",
                result_bundle=result_bundle,
            ),
            "xctest-ui",
            cwd=candidate_root,
            budget=budget,
            timeout_seconds=STAGE_TIMEOUT_SECONDS["xctest-ui"],
        )

    except BaseException as error:
        failure = error
    finally:
        # XCTest attachments are extra artifacts, not a replacement for its
        # complete stdout/stderr streams. Retain the result bundle and the
        # app-created gzip while the app data container is still installed;
        # collection failures remain explicit and secondary to a product
        # assertion so cleanup can still run.
        if xctest_started:
            if result_bundle.exists():
                try:
                    retained_result_bundle = _retain_xctest_result_bundle(
                        result_bundle, work_dir
                    )
                    _retain_xctest_screenshots(
                        runner, retained_result_bundle, work_dir, budget=budget
                    )
                except BaseException as error:
                    failure = _add_note(failure, "iOS XCTest result collection failed", error)
            else:
                failure = _add_note(
                    failure,
                    "iOS XCTest result collection failed",
                    IOSSimulatorStageError(
                        "collect-xctest-result",
                        f"complete XCTest result bundle is missing: {result_bundle}",
                    ),
                )
            if simulator is not None and app_installed:
                try:
                    retained_log_exports = _collect_ios_app_log_exports(
                        runner,
                        simulator,
                        contract,
                        work_dir,
                        budget=budget,
                    )
                except BaseException as error:
                    failure = _add_note(
                        failure, "iOS app log export collection failed", error
                    )
        if simulator is not None:
            if app_installed:
                try:
                    _terminate_app(runner, simulator, contract, budget=budget)
                except BaseException as error:
                    failure = _add_note(failure, "Simulator app cleanup also failed", error)
            if keyboard_state is not None:
                try:
                    _restore_simulator_hardware_keyboard(
                        runner, keyboard_state, budget=budget
                    )
                except BaseException as error:
                    failure = _add_note(
                        failure, "Simulator keyboard preference cleanup also failed", error
                    )
            if boot_started:
                try:
                    _shutdown_simulator(runner, simulator, budget=budget)
                except BaseException as error:
                    failure = _add_note(failure, "Simulator shutdown also failed", error)
    if failure is not None:
        raise failure.with_traceback(failure.__traceback__)
    if simulator is None or retained_result_bundle is None or not retained_log_exports:
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
        log_exports=retained_log_exports,
    )


def prepare_ios_simulator_candidate(
    *,
    candidate_root: Path,
    work_dir: Path,
    runner: CommandRunner,
    contract: IOSSimulatorAppContract,
    budget: RunBudget,
    runtime_framework: Path | None = None,
) -> None:
    """Build or consume the native framework needed by the Simulator app.

    Local runs omit ``runtime_framework`` and keep the pinned, simulator-only
    Go build. Hosted runs provide the already-qualified XCFramework artifact so
    this stage never needs gomobile/gobind installed on the UI runner.
    """
    candidate_root = Path(candidate_root).resolve()
    work_dir.mkdir(parents=True, exist_ok=True)
    go_root = candidate_root / "go_module"
    app_path = contract.app_path(work_dir)

    if runtime_framework is None:
        go_framework = go_root / "DobbyVPNRuntime.xcframework"
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
    else:
        go_framework = Path(runtime_framework)

    try:
        go_framework = _validate_runtime_framework(
            go_framework,
            contract.architecture,
            runner=runner,
            timeout_seconds=_stage_timeout(budget, "validate-ios-framework"),
        )
    except IOSSimulatorAppContractError as error:
        raise IOSSimulatorStageError("validate-ios-framework", str(error)) from error

    _require_success(
        runner,
        xcodebuild_app_command(
            contract,
            candidate_root=candidate_root,
            work_dir=work_dir,
            runtime_framework=go_framework,
        ),
        "package-ios-app",
        cwd=go_root,
        timeout_seconds=IOS_GO_UI_BUILD_TIMEOUT_SECONDS,
    )
    if not app_path.is_dir():
        raise IOSSimulatorStageError(
            "package-ios-app",
            f"Go/Fyne build produced no Simulator app: {app_path}",
        )
