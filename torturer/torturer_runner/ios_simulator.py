"""Small shell-free command builders for the local iOS Simulator check."""

from __future__ import annotations

from dataclasses import dataclass
from collections.abc import Sequence
from pathlib import Path
import re

from .native_cases import IOS_LOGS_FREEZE_RESUME_CASE, IOS_RENDERER_SEVERITY_CASE


_UDID = re.compile(r"[0-9A-Fa-f]{8}-(?:[0-9A-Fa-f]{4}-){3}[0-9A-Fa-f]{12}\Z")
_BUNDLE_ID = re.compile(r"[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\Z")
_DEFAULT_UI_TEST_SELECTION = (
    "iosAppUITests/NativeUIInteractionTests",
    "iosAppUITests/NativeRendererInteractionTests/"
    "testSeverityColorsResolveForLightAndDarkAppearances",
)
_NATIVE_CASE_TEST_SELECTIONS = {
    IOS_LOGS_FREEZE_RESUME_CASE:
        "iosAppUITests/NativeUIInteractionTests/testLogsFreezeAndResumeAtBottom",
    IOS_RENDERER_SEVERITY_CASE: _DEFAULT_UI_TEST_SELECTION[1],
}
_SOURCE_SHA = re.compile(r"[0-9a-f]{40}\Z")


class IOSSimulatorContractError(ValueError):
    """A Simulator command argument is outside the supported shape."""


def ui_test_selection(native_cases: Sequence[str] | None = None) -> tuple[str, ...]:
    """Return exact XCTest filters for a default or explicitly selected run."""
    if native_cases is None:
        return _DEFAULT_UI_TEST_SELECTION
    selected = tuple(native_cases)
    if not selected:
        raise IOSSimulatorContractError("at least one native XCTest case is required")
    if len(set(selected)) != len(selected):
        raise IOSSimulatorContractError("native XCTest cases must be unique")
    try:
        return tuple(_NATIVE_CASE_TEST_SELECTIONS[case] for case in selected)
    except KeyError as error:
        raise IOSSimulatorContractError(
            f"unsupported iOS Simulator native case: {error.args[0]}"
        ) from error


@dataclass(frozen=True)
class SimulatorApp:
    """The app path and fixed build labels, without an artifact attestation."""

    app_path: Path
    bundle_identifier: str
    architecture: str


def simctl_boot_command(device_udid: str) -> list[str]:
    return ["xcrun", "simctl", "boot", _validate_udid(device_udid)]


def simctl_bootstatus_command(device_udid: str) -> list[str]:
    return ["xcrun", "simctl", "bootstatus", _validate_udid(device_udid), "-b"]


def simctl_install_command(device_udid: str, app: str | Path) -> list[str]:
    app_path = Path(app)
    if app_path.suffix != ".app":
        raise IOSSimulatorContractError("Simulator install path must end in .app")
    return ["xcrun", "simctl", "install", _validate_udid(device_udid), str(app_path)]


def simctl_launch_command(
    device_udid: str,
    bundle_identifier: str,
    *,
    console: bool = False,
) -> list[str]:
    _validate_bundle_identifier(bundle_identifier)
    command = ["xcrun", "simctl", "launch"]
    if console:
        command.append("--console")
    command.extend(("--terminate-running-process", _validate_udid(device_udid), bundle_identifier))
    return command


def simctl_terminate_command(device_udid: str, bundle_identifier: str) -> list[str]:
    _validate_bundle_identifier(bundle_identifier)
    return ["xcrun", "simctl", "terminate", _validate_udid(device_udid), bundle_identifier]


def simctl_get_app_container_command(device_udid: str, bundle_identifier: str) -> list[str]:
    """Resolve the host path of an installed app's data container."""
    _validate_bundle_identifier(bundle_identifier)
    return [
        "xcrun", "simctl", "get_app_container", _validate_udid(device_udid),
        bundle_identifier, "data",
    ]


def iphonesimulator_sdk_version_command() -> list[str]:
    """Resolve the active iPhone Simulator SDK version without a shell."""
    return ["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"]


def xcodebuild_ui_test_without_building_command(
    device_udid: str,
    project: str | Path,
    derived_data: str | Path,
    result_bundle: str | Path | None = None,
    *,
    architecture: str,
    native_cases: Sequence[str] | None = None,
    source_sha: str | None = None,
) -> list[str]:
    """Run prepared XCTest products and retain their result bundle."""
    app_project = Path(project)
    if app_project.suffix != ".xcodeproj":
        raise IOSSimulatorContractError("iOS UI test project must end in .xcodeproj")
    data_path = Path(derived_data)
    if not str(data_path):
        raise IOSSimulatorContractError("iOS UI test derived-data path is required")
    result_path = (
        Path(result_bundle)
        if result_bundle is not None
        else data_path.parent / "xctest-results.xcresult"
    )
    if result_path.suffix != ".xcresult":
        raise IOSSimulatorContractError(
            "iOS UI test result bundle must end in .xcresult"
        )
    udid = _validate_udid(device_udid)
    if source_sha is not None and not _SOURCE_SHA.fullmatch(source_sha):
        raise IOSSimulatorContractError("iOS XCTest source commit must be a full lowercase SHA")
    simulator_architecture = "x86_64" if architecture == "amd64" else architecture
    selection = ui_test_selection(native_cases)
    command = [
        "xcodebuild",
        "-project", str(app_project),
        "-scheme", "iosAppUITests",
        "-configuration", "Release",
        "-sdk", "iphonesimulator",
        "-destination",
        f"platform=iOS Simulator,id={udid},arch={simulator_architecture}",
        "-derivedDataPath", str(data_path),
        "-resultBundlePath", str(result_path),
        "-parallel-testing-enabled", "NO",
        *[f"-only-testing:{test}" for test in selection],
        # Simulator XCTest runners need an installable code signature, but
        # ``-`` is the ad-hoc identity and does not require an Apple
        # Development certificate or provisioning profile.
        "CODE_SIGNING_ALLOWED=YES",
        "CODE_SIGNING_REQUIRED=NO",
        "CODE_SIGN_IDENTITY=-",
        "test-without-building",
    ]
    if source_sha is not None:
        return [
            "/usr/bin/env",
            f"SOURCE_COMMIT={source_sha}",
            f"GITHUB_SHA={source_sha}",
            *command,
        ]
    return command


def _validate_bundle_identifier(value: str) -> None:
    if not isinstance(value, str) or not _BUNDLE_ID.fullmatch(value):
        raise IOSSimulatorContractError("bundle identifier has an unsupported format")


def _validate_udid(value: str) -> str:
    if not isinstance(value, str) or not _UDID.fullmatch(value):
        raise IOSSimulatorContractError("Simulator UDID has an unsupported format")
    return value.upper()
