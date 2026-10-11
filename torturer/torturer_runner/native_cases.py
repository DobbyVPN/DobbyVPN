"""Supported local native UI case selectors and their required setup suites."""

from __future__ import annotations

from collections.abc import Sequence


IOS_RENDERER_SEVERITY_CASE = (
    "NativeRendererInteractionTests::"
    "testSeverityColorsResolveForLightAndDarkAppearances"
)
IOS_LOGS_FREEZE_RESUME_CASE = "logs-freeze-resume"
IOS_SUBSCRIPTION_FIXTURE_CASE = "ios-subscription-fixture"
ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE = "logs-clear-process-restart"
ANDROID_SMALL_SCREEN_LOG_VIEWPORT_CASE = "small-screen-log-viewport"
AUTO_RECOVERY_STOP_CASE = "auto-recovery-stop"
CONNECTED_SESSION_CASE = "connected-session"
CONNECTED_LAYOUT_CASE = "connected-layout"
WINDOWS_LOG_SEVERITY_CASE = "logs-severity"
DESKTOP_LOG_CASES = (
    "logs-details", "logs-freeze-resume", "logs-clear", "logs-reopen-clear-boundary",
)
WINDOWS_PRESENTATION_CASES = (
    "configure-acceptance", "paste-input", "profiles-layout", "about",
    "imports-cold", "imports-warm", "text-size-layout",
)
MACOS_PRESENTATION_CASES = (
    "configure-acceptance", "paste-input", "profiles-layout", "about",
    "imports-cold", "imports-warm", "text-size-eligibility",
)
DESKTOP_PERFORMANCE_CASE = "subscription-load-performance"
ANDROID_PERFORMANCE_CASE = "subscription-load-performance"
IOS_LEAF_CASES = (
    "ios-subscription-paste", "ios-subscription-failure-retry",
    "ios-subscription-profiles", "ios-subscription-logs",
    "ios-subscription-cold-import", "ios-subscription-warm-import",
    "ios-logs-details", "ios-logs-clear", "ios-logs-freeze-configuration",
    "ios-logs-freeze-rotation", "ios-logs-follow-resume", "ios-about",
    "ios-layout-large-text", "ios-layout-compact-landscape",
)
ANDROID_PRESENTATION_CASES = (
    "configure-acceptance", "profile-selection", "typed-url-coalescing",
    "immediate-source-actions", "stale-permission-target",
)

NATIVE_CASE_SUITES = {
    **{(platform, case): "full" for platform in ("windows", "macos")
       for case in (CONNECTED_SESSION_CASE, CONNECTED_LAYOUT_CASE)},
    ("windows", WINDOWS_LOG_SEVERITY_CASE): "full",
    **{("macos", case): "full" for case in MACOS_PRESENTATION_CASES},
    **{(platform, DESKTOP_PERFORMANCE_CASE): "full" for platform in ("windows", "macos")},
    **{("windows", case): "full" for case in WINDOWS_PRESENTATION_CASES},
    **{("ios-simulator", case): "mini" for case in IOS_LEAF_CASES},
    ("android", ANDROID_PERFORMANCE_CASE): "mini",
    **{("android", case): "mini" for case in ANDROID_PRESENTATION_CASES},
    **{(platform, case): "full" for platform in ("windows", "macos")
       for case in DESKTOP_LOG_CASES},
    ("android", ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE): "mini",
    ("android", ANDROID_SMALL_SCREEN_LOG_VIEWPORT_CASE): "mini",
    ("ios-simulator", IOS_LOGS_FREEZE_RESUME_CASE): "mini",
    ("ios-simulator", IOS_RENDERER_SEVERITY_CASE): "mini",
    ("ios-simulator", IOS_SUBSCRIPTION_FIXTURE_CASE): "mini",
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
    if AUTO_RECOVERY_STOP_CASE in selected and len(selected) != 1:
        raise ValueError("auto-recovery-stop requires its separate build-local setup")
    for case in selected:
        expected_suite = NATIVE_CASE_SUITES.get((platform, case))
        if expected_suite is None:
            raise ValueError(f"unsupported native case for {platform}: {case}")
        if suite != expected_suite:
            raise ValueError(
                f"native case {platform}:{case} requires suite {expected_suite}"
            )
    return selected
