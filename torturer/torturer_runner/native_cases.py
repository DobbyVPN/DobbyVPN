"""Supported local native UI case selectors and their required setup suites."""

from __future__ import annotations

from collections.abc import Sequence


IOS_RENDERER_SEVERITY_CASE = (
    "NativeRendererInteractionTests::"
    "testSeverityColorsResolveForLightAndDarkAppearances"
)
IOS_LOGS_FREEZE_RESUME_CASE = "logs-freeze-resume"
IOS_SUBSCRIPTION_FIXTURE_CASE = "ios-subscription-fixture"
WINDOWS_CONFIGURE_TREE_CASE = "configure-tree"
WINDOWS_CONFIGURE_TREE_NO_UIA_CASE = "configure-tree-no-uia"
ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE = "logs-clear-process-restart"
ANDROID_SMALL_SCREEN_LOG_VIEWPORT_CASE = "small-screen-log-viewport"
MACOS_CONFIGURE_STARTUP_CASE = "configure-startup"
AUTO_RECOVERY_STOP_CASE = "auto-recovery-stop"

NATIVE_CASE_SUITES = {
    ("android", ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE): "mini",
    ("android", ANDROID_SMALL_SCREEN_LOG_VIEWPORT_CASE): "mini",
    ("ios-simulator", IOS_LOGS_FREEZE_RESUME_CASE): "mini",
    ("ios-simulator", IOS_RENDERER_SEVERITY_CASE): "mini",
    ("ios-simulator", IOS_SUBSCRIPTION_FIXTURE_CASE): "mini",
    ("macos", MACOS_CONFIGURE_STARTUP_CASE): "full",
    ("windows", WINDOWS_CONFIGURE_TREE_CASE): "full",
    ("windows", WINDOWS_CONFIGURE_TREE_NO_UIA_CASE): "full",
    ("android", AUTO_RECOVERY_STOP_CASE): "mini",
    ("windows", AUTO_RECOVERY_STOP_CASE): "full",
    ("macos", AUTO_RECOVERY_STOP_CASE): "full",
}


def validate_native_cases(
    platform: str,
    suite: str,
    cases: Sequence[str] | None,
) -> tuple[str, ...]:
    """Validate exact case names against the platform's native setup lane."""
    if cases is None:
        return ()
    selected = tuple(cases)
    if not selected:
        raise ValueError("at least one native case is required")
    if len(set(selected)) != len(selected):
        raise ValueError("native cases must be unique")
    if len(selected) > 1:
        raise ValueError("one native case may be selected per platform lane")
    for case in selected:
        expected_suite = NATIVE_CASE_SUITES.get((platform, case))
        if expected_suite is None:
            raise ValueError(f"unsupported native case for {platform}: {case}")
        if suite != expected_suite:
            raise ValueError(
                f"native case {platform}:{case} requires suite {expected_suite}"
            )
    return selected
