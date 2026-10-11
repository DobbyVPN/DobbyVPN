"""Small shell-free command builders for the local iOS Simulator check."""

from __future__ import annotations

from dataclasses import dataclass
from collections.abc import Mapping, Sequence
from pathlib import Path
import re

from .native_cases import (
    IOS_LEAF_CASES,
    IOS_LOGS_FREEZE_RESUME_CASE,
    IOS_RENDERER_SEVERITY_CASE,
    IOS_SUBSCRIPTION_FIXTURE_CASE,
)


_UDID = re.compile(r"[0-9A-Fa-f]{8}-(?:[0-9A-Fa-f]{4}-){3}[0-9A-Fa-f]{12}\Z")
_BUNDLE_ID = re.compile(r"[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\Z")
_DEFAULT_UI_TEST_SELECTION = (
    "iosAppUITests/NativeUIInteractionTests",
    "iosAppUITests/NativeRendererInteractionTests/"
    "testSeverityColorsResolveForLightAndDarkAppearances",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests",
)
_NATIVE_UI_TEST_CASES = (
    "iosAppUITests/NativeUIInteractionTests/testUnsupportedTypedURLKeepsControlsUsable",
    "iosAppUITests/NativeUIInteractionTests/testAboutShowsSelectedProductRevision",
    "iosAppUITests/NativeUIInteractionTests/testClearRemovesEarlierRenderedRecordsAndRetainsLaterRecord",
    "iosAppUITests/NativeUIInteractionTests/testColdImportKeepsItsValidSource",
    "iosAppUITests/NativeUIInteractionTests/testWarmAndInvalidImportsKeepTheLatestValidSource",
    "iosAppUITests/NativeUIInteractionTests/testLargeTextKeepsLogsAndControlsVisible",
    "iosAppUITests/NativeUIInteractionTests/testCompactLandscapeKeepsConnectionControlsAndLogsReachable",
    "iosAppUITests/NativeUIInteractionTests/testEmptyAndNonTextClipboardDoesNotFillSubscriptionField",
    "iosAppUITests/NativeUIInteractionTests/testPasteReadsClipboardOnlyAfterTapAndRejectsNonHTTPSInput",
    "iosAppUITests/NativeUIInteractionTests/testValidNativePasteAcceptedOnTap",
    "iosAppUITests/NativeUIInteractionTests/testLogDetailsRevealAndCollapseTheOriginalRecord",
    "iosAppUITests/NativeUIInteractionTests/testFrozenLogsKeepTheirReadingPositionDuringConfigurationAndAbout",
    "iosAppUITests/NativeUIInteractionTests/testFrozenLogPositionSurvivesRotation",
    "iosAppUITests/NativeUIInteractionTests/testFollowingResumesWhenReaderReturnsToBottom",
)
_RENDERER_SEVERITY_TEST = _DEFAULT_UI_TEST_SELECTION[1]
_SUBSCRIPTION_FIXTURE_TEST_CASES = (
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testNativePasteStartsFixtureRequest",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testPasteFailureWaitsForExplicitRetry",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testFixtureProfilesRenderWithInventoryScopedControls",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testFixtureLogsRenderSeededStderrWithoutConnecting",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testColdImportConfiguresInventoryOnce",
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/testWarmImportsReuseLoadedInventory",
)
_UI_TEST_CLASS_CASES = {
    "iosAppUITests/NativeUIInteractionTests": _NATIVE_UI_TEST_CASES,
    "iosAppUITests/NativeRendererInteractionTests": (_RENDERER_SEVERITY_TEST,),
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests": _SUBSCRIPTION_FIXTURE_TEST_CASES,
}
SUBSCRIPTION_FIXTURE_TEST_GETS = dict(zip(
    _SUBSCRIPTION_FIXTURE_TEST_CASES,
    (1, 2, 1, 1, 1, 1),
))
_NATIVE_CASE_TEST_SELECTIONS = {
    IOS_LOGS_FREEZE_RESUME_CASE: (
        "iosAppUITests/NativeUIInteractionTests/testFrozenLogsKeepTheirReadingPositionDuringConfigurationAndAbout",
        "iosAppUITests/NativeUIInteractionTests/testFrozenLogPositionSurvivesRotation",
        "iosAppUITests/NativeUIInteractionTests/testFollowingResumesWhenReaderReturnsToBottom",
    ),
    IOS_RENDERER_SEVERITY_CASE: (_DEFAULT_UI_TEST_SELECTION[1],),
    IOS_SUBSCRIPTION_FIXTURE_CASE:
        (
            "iosAppUITests/NativeSubscriptionFixtureInteractionTests",
        ),
    "ios-subscription-paste": (_SUBSCRIPTION_FIXTURE_TEST_CASES[0],),
    "ios-subscription-failure-retry": (_SUBSCRIPTION_FIXTURE_TEST_CASES[1],),
    "ios-subscription-profiles": (_SUBSCRIPTION_FIXTURE_TEST_CASES[2],),
    "ios-subscription-logs": (_SUBSCRIPTION_FIXTURE_TEST_CASES[3],),
    "ios-subscription-cold-import": (_SUBSCRIPTION_FIXTURE_TEST_CASES[4],),
    "ios-subscription-warm-import": (_SUBSCRIPTION_FIXTURE_TEST_CASES[5],),
    "ios-logs-details": (_NATIVE_UI_TEST_CASES[10],),
    "ios-logs-clear": (_NATIVE_UI_TEST_CASES[2],),
    "ios-logs-freeze-configuration": (_NATIVE_UI_TEST_CASES[11],),
    "ios-logs-freeze-rotation": (_NATIVE_UI_TEST_CASES[12],),
    "ios-logs-follow-resume": (_NATIVE_UI_TEST_CASES[13],),
    "ios-about": (_NATIVE_UI_TEST_CASES[1],),
    "ios-layout-large-text": (_NATIVE_UI_TEST_CASES[5],),
    "ios-layout-compact-landscape": (_NATIVE_UI_TEST_CASES[6],),
}
if set(IOS_LEAF_CASES) != set(_NATIVE_CASE_TEST_SELECTIONS) - {
    IOS_LOGS_FREEZE_RESUME_CASE,
    IOS_RENDERER_SEVERITY_CASE,
    IOS_SUBSCRIPTION_FIXTURE_CASE,
}:
    raise RuntimeError("iOS native leaf case registry and XCTest selections diverged")


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
        return tuple(
            test for case in selected for test in _NATIVE_CASE_TEST_SELECTIONS[case]
        )
    except KeyError as error:
        raise IOSSimulatorContractError(
            f"unsupported iOS Simulator native case: {error.args[0]}"
        ) from error


def required_ui_test_cases(native_cases: Sequence[str] | None = None) -> tuple[str, ...]:
    """Expand selected class filters into the exact required test methods."""
    expanded: list[str] = []
    for selection in ui_test_selection(native_cases):
        expanded.extend(_UI_TEST_CLASS_CASES.get(selection, (selection,)))
    if len(set(expanded)) != len(expanded):
        raise IOSSimulatorContractError(
            "selected native XCTest cases overlap and would run a method more than once"
        )
    return tuple(expanded)


def expected_subscription_fixture_gets(
    native_cases: Sequence[str] | None = None,
) -> int:
    """Return the exact subscription GET count implied by selected XCTest leaves."""
    return sum(
        SUBSCRIPTION_FIXTURE_TEST_GETS.get(test, 0)
        for test in required_ui_test_cases(native_cases)
    )


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


def simctl_create_command(name: str, device_type_identifier: str, runtime_identifier: str) -> list[str]:
    if not isinstance(name, str) or not name.strip() or "\x00" in name or "\n" in name:
        raise IOSSimulatorContractError("Simulator name is invalid")
    if not isinstance(device_type_identifier, str) or not re.fullmatch(
        r"com\.apple\.CoreSimulator\.SimDeviceType\.[A-Za-z0-9.-]+",
        device_type_identifier,
    ):
        raise IOSSimulatorContractError("Simulator device type is invalid")
    if not isinstance(runtime_identifier, str) or not re.fullmatch(
        r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-[0-9]+(?:-[0-9]+)*",
        runtime_identifier,
    ):
        raise IOSSimulatorContractError("Simulator runtime is invalid")
    return ["xcrun", "simctl", "create", name, device_type_identifier, runtime_identifier]


def simctl_delete_command(device_udid: str) -> list[str]:
    return ["xcrun", "simctl", "delete", _validate_udid(device_udid)]


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
    test_environment: Mapping[str, str] | None = None,
    repeat: int = 1,
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
    simulator_architecture = "x86_64" if architecture == "amd64" else architecture
    selection = ui_test_selection(native_cases)
    if type(repeat) is not int or repeat < 1:
        raise IOSSimulatorContractError("XCTest repeat count must be positive")
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
    # Xcode rejects an explicit one-iteration request. Its default is one run;
    # pass the option only when the caller requests repeated XCTest execution.
    if repeat > 1:
        command[command.index("-only-testing:" + selection[0]):command.index("-only-testing:" + selection[0])] = [
            "-test-iterations", str(repeat),
        ]
    if test_environment is None:
        return command
    environment_arguments: list[str] = []
    for key, value in sorted(test_environment.items()):
        if not isinstance(key, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise IOSSimulatorContractError("XCTest environment variable name is invalid")
        if not isinstance(value, str) or "\x00" in value:
            raise IOSSimulatorContractError("XCTest environment variable value is invalid")
        # Xcode only forwards TEST_RUNNER_* variables from its own process
        # environment into XCTest, stripping this prefix for the test code.
        environment_arguments.append(f"TEST_RUNNER_{key}={value}")
    return ["/usr/bin/env", *environment_arguments, *command]


def _validate_bundle_identifier(value: str) -> None:
    if not isinstance(value, str) or not _BUNDLE_ID.fullmatch(value):
        raise IOSSimulatorContractError("bundle identifier has an unsupported format")


def _validate_udid(value: str) -> str:
    if not isinstance(value, str) or not _UDID.fullmatch(value):
        raise IOSSimulatorContractError("Simulator UDID has an unsupported format")
    return value.upper()
