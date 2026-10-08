"""Interleave real desktop-window actions with the existing platform adapter.

The ordinary functional lane remains the canonical headless mini suite.  This
module is the additional local desktop-full lane: it owns only native user
actions and asks the platform adapter for independent tunnel, routing, traffic,
process-loss, and cleanup evidence.  It is intentionally a command-line
entrypoint so Windows can run the complete journey through the existing
interactive scheduled-task boundary.
"""

from __future__ import annotations

import argparse
from contextlib import contextmanager
import json
import math
import os
from pathlib import Path
import sys
import threading
import time
import traceback
import uuid
from datetime import datetime, timezone
from typing import Any, Iterator

from torturer_contract.scenarios import ScenarioStep
from torturer_runner.native_cases import (
    AUTO_RECOVERY_STOP_CASE,
    MACOS_CONFIGURE_STARTUP_CASE,
    NATIVE_CASE_SUITES,
    WINDOWS_CONFIGURE_TREE_CASE,
    WINDOWS_CONFIGURE_TREE_NO_UIA_CASE,
    validate_native_cases,
)

from ..adapters.cli import SubprocessRunner, _ensure_directory
from ..adapters.factory import adapter_for_platform
from ..diagnostics import add_exception_notes
from . import smoke


# Keep each native action below the hosted journey's step deadline, leaving
# time for screenshots and exact UI-process cleanup.
_REQUEST_TIMEOUT = 300.0
_SMOKE_DIAGNOSTIC_RESERVE_SECONDS = 30.0


class NativeUIJourneyError(RuntimeError):
    """A native UI or base-adapter journey failure.

    ``operation`` and ``stage`` identify the failed step within a bounded
    timeout.
    """

    def __init__(
        self,
        message: str,
        *,
        operation: str | None = None,
        stage: str | None = None,
    ) -> None:
        super().__init__(message)
        self.operation = operation
        self.stage = stage


def _exception_details(error: BaseException) -> str:
    return "".join(traceback.format_exception(error)).rstrip()


def _valid_windows_content_root_probe(probe: object) -> bool:
    """Validate the direct XAML editor observation from configure-tree."""

    if not isinstance(probe, dict):
        return False
    if (
        probe.get("schema") != "dobbyvpn.windows-content-root-peers/v2"
        or probe.get("completed") is not True
        or probe.get("diagnosticOnly") is not True
        or probe.get("dispatcherThreadAccess") is not True
        or probe.get("windowContentIsRoot") is not True
        or not isinstance(probe.get("windowContentType"), str)
        or not probe["windowContentType"].strip()
        or type(probe.get("rootPeerCreated")) is not bool
        or "rootPeerType" not in probe
    ):
        return False
    root_peer_type = probe["rootPeerType"]
    if probe["rootPeerCreated"]:
        if not isinstance(root_peer_type, str) or not root_peer_type.strip():
            return False
    elif root_peer_type is not None:
        return False

    editor = probe.get("editor")
    if not isinstance(editor, dict) or (
        editor.get("automationId") != "Connection configuration"
        or editor.get("name") != "Subscription URL"
        or editor.get("controlType") != "Edit"
        or not isinstance(editor.get("peerType"), str)
        or not editor["peerType"].strip()
        or editor.get("isControlElement") is not True
        or editor.get("isContentElement") is not True
        or editor.get("isLoaded") is not True
        or editor.get("isVisible") is not True
        or editor.get("isEnabled") is not True
    ):
        return False
    geometry = editor.get("geometry")
    if not isinstance(geometry, dict):
        return False
    coordinates: list[float] = []
    for key in ("x", "y", "width", "height"):
        value = geometry.get(key)
        if type(value) not in (int, float) or not math.isfinite(value):
            return False
        coordinates.append(float(value))
    return coordinates[2] > 0 and coordinates[3] > 0


_REQUIRED_TRUE_CHECKS = frozenset({
    "configure_native",
    "connect_native",
    "tunnel_interface",
    "routing_verified",
    "stability_verified",
    "throughput_positive",
    "disconnect_native",
    "disconnect_clean",
    "reconnect_native",
    "reconnect_completed",
    "about_version",
    "about_source_commit",
    "manual_selection_native",
    "profile_switch_native",
    "manual_state_reopened",
    "profile_switch_transition_controls",
    "auto_stop_during_auto_selection",
    "import_during_connecting_preserves_request",
    "logs_freeze_resize_preserves_reading_position",
    "long_profile_list_keeps_logs_accessible",
    "deep_link_rejections_preserve_connection",
    "loading_disables_stale_actions",
    "failed_load_preserves_tunnel",
    "warm_import_native",
    "clear_logs_native",
    "clear_boundary_survives_reopen",
    "native_paste_immediate",
    "typed_debounce",
    "coalesced_latest_subscription",
    "loading_ui_responsive",
    "new_inventory_keeps_active_disconnectable",
    "profile_rows_source_order",
    "empty_description_fallback",
    "retry_single_attempt",
    "retry_success_native",
    "duplicate_import_single_load",
    "import_preserves_active_generation",
    "bare_deep_link_native",
    "warm_link_same_window",
    "clear_resumes_following",
    "inventory_reused_after_reopen",
    "inventory_restored_from_saved_url",
    "clear_boundary_survives_frontend_reopen",
    "absent_active_profile_disconnect",
    "disconnect_responsive_during_configure",
    "invalid_http_native_paste_no_fetch",
    "close_window",
    "reopen_connected",
    "cold_os_scheme_launch",
    "cold_import_native",
    "process_loss_verified",
    "ui_process_loss_recovered",
    "reconnect_tunnel_interface",
    "reconnect_routing_verified",
    "reconnect_stability_verified",
    "reconnect_throughput_positive",
    "process_loss_tunnel_interface",
    "process_loss_routing_verified",
    "process_loss_stability_verified",
    "process_loss_throughput_positive",
    "final_disconnect_native",
    "final_cleanup_verified",
})


def _require_complete_checks(checks: dict[str, object], platform: str | None = None) -> None:
    """Fail closed when an evidence-producing step returned false/missing data."""
    required = set(_REQUIRED_TRUE_CHECKS)
    if platform == "windows":
        required.update({"rendered_stderr_capture_label", "windows_rendered_log_palette"})
    failed = sorted(key for key in required if checks.get(key) is not True)
    if failed:
        raise NativeUIJourneyError(
            "required native UI checks did not pass: " + ", ".join(failed)
        )


@contextmanager
def _windows_diagnostic_log_lock(log_path: Path) -> Iterator[None]:
    """Share the diagnostics writer's byte-zero lock during the short append."""
    if os.name != "nt":
        yield
        return
    import msvcrt

    lock_path = log_path.with_name(log_path.name + ".lock")
    with lock_path.open("r+b", buffering=0) as lock_file:
        lock_file.seek(0)
        msvcrt.locking(lock_file.fileno(), msvcrt.LK_LOCK, 1)
        try:
            yield
        finally:
            lock_file.seek(0)
            msvcrt.locking(lock_file.fileno(), msvcrt.LK_UNLCK, 1)


def _append_windows_palette_fixture(
    log_path: Path, marker: str, severity: str, process_sequence: int
) -> dict[str, object]:
    """Append one deterministic display row without replacing the real backend log."""
    if (
        not marker
        or severity not in smoke._WINDOWS_PALETTE_SEVERITIES
        or type(process_sequence) is not int
        or process_sequence < 1
        or not log_path.is_file()
    ):
        raise NativeUIJourneyError(f"existing run-scoped Windows backend log is unavailable: {log_path}")
    row = {
        "schema": "dobby.log/v1",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "source": "windows-native-ui-test",
        "level": severity,
        "event": "test.rendered-palette",
        "message": f"synthetic rendered palette fixture {marker} {severity}",
        "process_id": os.getpid(),
        "run_id": marker,
        "process_sequence": process_sequence,
    }
    payload = b"\n" + json.dumps(
        row, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8") + b"\n"
    try:
        with _windows_diagnostic_log_lock(log_path):
            descriptor = os.open(
                str(log_path), os.O_WRONLY | os.O_APPEND | getattr(os, "O_BINARY", 0)
            )
            try:
                written = os.write(descriptor, payload)
                if written != len(payload):
                    raise OSError(f"short append: wrote {written} of {len(payload)} bytes")
            finally:
                os.close(descriptor)
    except OSError as error:
        raise NativeUIJourneyError(f"could not append Windows palette fixtures to {log_path}: {error}") from error
    return {
        "marker": marker,
        "synthetic": True,
        "severity": severity,
    }


def _exercise_windows_rendered_log_palette(
    ui: Any,
    log_path: Path,
    timeout: float,
    checks: dict[str, object],
) -> None:
    """Render and inspect each severity color under both WinUI themes."""
    evidence: dict[str, dict[str, object]] = {}
    checks["windows_rendered_log_palette_evidence"] = evidence
    primary: BaseException | None = None
    restore_errors: list[BaseException] = []
    fixture_sequence = 0
    try:
        ui.close()
        for theme in ("Light", "Dark"):
            _native_ui_action(
                ui,
                f"start-{theme.lower()}-palette-frontend",
                f"windows-palette-{theme.lower()}-startup",
                timeout,
                lambda theme=theme: ui.start(windows_requested_theme=theme),
            )
            marker = f"windows-l3-{theme.lower()}-{uuid.uuid4().hex}"
            try:
                log_path.resolve().relative_to(ui.logs.resolve())
            except ValueError as error:
                raise NativeUIJourneyError(
                    f"Windows palette fixture path is outside the current run logs: {log_path}"
                ) from error
            theme_evidence: dict[str, object] = {
                "marker": marker,
                "fixtures": [],
                "palette": {},
                "uia_palette": {},
                "rows": {},
                "logs_viewport": {},
                "screenshots": {},
            }
            evidence[theme] = theme_evidence
            for severity in smoke._WINDOWS_PALETTE_SEVERITIES:
                fixture_sequence += 1
                fixture = _append_windows_palette_fixture(
                    log_path, marker, severity, fixture_sequence
                )
                fixtures = theme_evidence["fixtures"]
                assert isinstance(fixtures, list)
                fixtures.append(fixture)
                rendered = _native_ui_action(
                    ui,
                    f"verify-{theme.lower()}-{severity.lower()}-palette",
                    f"windows-palette-{theme.lower()}-{severity.lower()}-rendered",
                    timeout,
                    lambda marker=marker, severity=severity: ui.verify_windows_palette_fixture(
                        marker, severity
                    ),
                    milestone=f"windows-l3-palette-{theme.lower()}-{severity.lower()}",
                )
                palette = theme_evidence["palette"]
                uia_palette = theme_evidence["uia_palette"]
                rows = theme_evidence["rows"]
                viewports = theme_evidence["logs_viewport"]
                screenshots = theme_evidence["screenshots"]
                assert isinstance(palette, dict)
                assert isinstance(uia_palette, dict)
                assert isinstance(rows, dict)
                assert isinstance(viewports, dict)
                assert isinstance(screenshots, dict)
                rendered_rows = rendered["rows"]
                if not isinstance(rendered_rows, dict):
                    raise NativeUIJourneyError(f"Windows {severity} row evidence was unavailable")
                row = rendered_rows.get(severity)
                if not isinstance(row, dict):
                    raise NativeUIJourneyError(f"Windows {severity} row bounds were unavailable")
                screenshot = rendered.get("screenshot")
                if not isinstance(screenshot, dict):
                    raise NativeUIJourneyError(f"Windows {severity} screenshot metadata was unavailable")
                pixel_measurement = smoke._measure_windows_palette_pixels(
                    screenshot, row.get("bounds")
                )
                palette[severity] = pixel_measurement["foreground_rgb"]
                uia_colors = rendered.get("palette")
                uia_palette[severity] = (
                    uia_colors.get(severity) if isinstance(uia_colors, dict) else None
                )
                rows[severity] = {
                    **row,
                    "pixel_measurement": pixel_measurement,
                }
                viewports[severity] = rendered["logs_viewport"]
                screenshots[severity] = rendered["screenshot"]
            palette = theme_evidence["palette"]
            if not isinstance(palette, dict) or set(palette) != set(smoke._WINDOWS_PALETTE_SEVERITIES):
                raise NativeUIJourneyError(
                    f"Windows {theme} palette evidence did not cover all four severities"
                )
            measured_colors = {
                tuple(color) for color in palette.values()
                if isinstance(color, list) and len(color) == 3
            }
            if len(measured_colors) != len(smoke._WINDOWS_PALETTE_SEVERITIES):
                raise NativeUIJourneyError(
                    f"Windows {theme} severity logs did not render four distinct captured foreground colors"
                )
            rows = theme_evidence["rows"]
            assert isinstance(rows, dict)
            debug_row, info_row = rows.get("DEBUG"), rows.get("INFO")
            debug_pixels = debug_row.get("pixel_measurement") if isinstance(debug_row, dict) else None
            info_pixels = info_row.get("pixel_measurement") if isinstance(info_row, dict) else None
            debug_distance = debug_pixels.get("distance_squared") if isinstance(debug_pixels, dict) else None
            info_distance = info_pixels.get("distance_squared") if isinstance(info_pixels, dict) else None
            if (
                type(debug_distance) is not int or type(info_distance) is not int
                or debug_distance >= info_distance
            ):
                raise NativeUIJourneyError(
                    f"Windows {theme} DEBUG text was not visually more muted than INFO: "
                    f"DEBUG distance={debug_distance!r} INFO distance={info_distance!r}"
                )
            warn, error_color = palette.get("WARN"), palette.get("ERROR")
            if not isinstance(warn, list) or not (warn[0] >= warn[1] > warn[2]):
                raise NativeUIJourneyError(
                    f"Windows {theme} WARN logs were not captured in an amber severity color: {warn!r}"
                )
            if not isinstance(error_color, list) or not (
                error_color[0] > error_color[1] and error_color[0] > error_color[2]
            ):
                raise NativeUIJourneyError(
                    f"Windows {theme} ERROR logs were not captured in a red severity color: {error_color!r}"
                )
            _native_ui_action(
                ui,
                f"close-{theme.lower()}-palette-frontend",
                f"windows-palette-{theme.lower()}-close",
                timeout,
                ui.close,
            )

        light = evidence["Light"]["palette"]
        dark = evidence["Dark"]["palette"]
        if not isinstance(light, dict) or not isinstance(dark, dict):
            raise NativeUIJourneyError("Windows light/dark palette evidence was not retained")
        if light.get("INFO") == dark.get("INFO"):
            raise NativeUIJourneyError(
                "Windows INFO logs captured the same foreground color in forced light and dark themes"
            )
    except BaseException as error:
        primary = error
    finally:
        try:
            with ui.bounded_by(min(timeout, 15.0)):
                ui.close_for_cleanup()
        except BaseException as error:
            restore_errors.append(error)
        try:
            _native_ui_action(
                ui,
                "restart-normal-palette-frontend",
                "windows-palette-normal-frontend-restore",
                timeout,
                ui.start,
            )
            evidence["normal_frontend"] = {"restarted": True, "requested_theme": None}
        except BaseException as error:
            restore_errors.append(error)

    if primary is not None:
        for index, error in enumerate(restore_errors, start=1):
            add_exception_notes(primary, f"Windows palette restore {index}", error)
        raise primary
    if restore_errors:
        failure = NativeUIJourneyError("Windows palette test could not restore the normal frontend")
        for index, error in enumerate(restore_errors, start=1):
            add_exception_notes(failure, f"Windows palette restore {index}", error)
        raise failure from restore_errors[0]
    checks["windows_rendered_log_palette"] = True


def same_active_generation(current: dict, previous: dict) -> bool:
    return (
        current.get("state") == "CONNECTED"
        and current.get("generation") == previous.get("generation")
        and current.get("active_digest") == previous.get("active_digest")
        and current.get("active_mode") == previous.get("active_mode")
        and current.get("active_index") == previous.get("active_index")
    )


def _timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a finite number") from error
    if result <= 0 or not math.isfinite(result):
        raise argparse.ArgumentTypeError("timeout must be positive and finite")
    return result


def _path(value: str) -> Path:
    result = Path(value)
    if not result.is_absolute():
        raise argparse.ArgumentTypeError("path must be absolute")
    return result


def _ui_path_is_launchable(platform: str, path: Path) -> bool:
    if path.is_file():
        return True
    if platform != "macos" or path.suffix != ".app":
        return False
    return (path / "Contents" / "MacOS" / "DobbyVPNMacApp").is_file()


def _smoke_timeout(response_timeout: float) -> float:
    """Keep native UI work below its hosted step deadline for cleanup."""

    if response_timeout <= 0:
        raise ValueError("native UI response timeout must be positive")
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, response_timeout / 3.0)
    return min(_REQUEST_TIMEOUT, response_timeout - reserve)


def _native_ui_action(
    controller: Any,
    operation: str,
    stage: str,
    timeout: float,
    action: Any,
    *,
    milestone: str | None = None,
) -> dict[str, object]:
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, timeout / 3.0)
    work_timeout = timeout - reserve
    try:
        with controller.bounded_by(work_timeout):
            result = action()
            if not isinstance(result, dict):
                raise NativeUIJourneyError(f"native UI {operation} returned an invalid result")
    except Exception as error:
        failure = NativeUIJourneyError(
            f"{type(error).__name__}: {error}",
            operation=operation,
            stage=stage,
        )
        for note in getattr(error, "__notes__", ()):
            failure.add_note(note)
        try:
            with controller.bounded_by(reserve):
                screenshot = controller.capture(f"failure-{operation}")
            failure.add_note(
                "native-ui-failure-screenshot: "
                + json.dumps(screenshot, sort_keys=True, separators=(",", ":"))
            )
        except Exception as screenshot_error:
            failure.add_note(
                "native-ui-failure-screenshot: "
                + _exception_details(screenshot_error)
            )
        raise failure from error
    if milestone is not None:
        try:
            with controller.bounded_by(reserve):
                screenshot = controller.capture(milestone)
        except Exception as screenshot_error:
            failure = NativeUIJourneyError(
                f"native UI {operation} milestone screenshot {milestone} failed: "
                f"{type(screenshot_error).__name__}: {screenshot_error}",
                operation=operation,
                stage=stage,
            )
            for note in getattr(screenshot_error, "__notes__", ()):
                failure.add_note(note)
            raise failure from screenshot_error
        if not isinstance(screenshot, dict) or not isinstance(screenshot.get("path"), str):
            raise NativeUIJourneyError(
                f"native UI {operation} milestone screenshot {milestone} was not retained: "
                f"{screenshot!r}",
                operation=operation,
                stage=stage,
            )
        result = {**result, "screenshot": screenshot}
    return result


def _start_native_ui(controller: Any, timeout: float) -> None:
    reserve = min(_SMOKE_DIAGNOSTIC_RESERVE_SECONDS, timeout / 3.0)
    work_timeout = timeout - reserve
    try:
        with controller.bounded_by(work_timeout):
            controller.start()
    except BaseException as error:
        with controller.bounded_by(reserve):
            try:
                screenshot = controller.capture("failure-start")
                error.add_note(
                    "native-ui-failure-screenshot: "
                    + json.dumps(screenshot, sort_keys=True, separators=(",", ":"))
                )
            except BaseException as screenshot_error:
                error.add_note(
                    "native-ui-failure-screenshot: "
                    f"{type(screenshot_error).__name__}: {screenshot_error}"
                )
            try:
                controller.close_for_cleanup()
            except BaseException as cleanup_error:
                error.add_note(
                    f"native-ui-cleanup: {type(cleanup_error).__name__}: {cleanup_error}"
                )
        failure = NativeUIJourneyError(
            f"{type(error).__name__}: {error}",
            operation="start",
            stage="window-discovery",
        )
        for note in getattr(error, "__notes__", ()):
            failure.add_note(note)
        raise failure from error


def _step(identifier: str, operation: str, timeout: float) -> ScenarioStep:
    # ScenarioStep's public contract uses integer operation bounds.  The
    # native lane's remaining budget is bounded separately by this process.
    return ScenarioStep(id=identifier, operation=operation, timeout_seconds=max(1, int(timeout)))


def _base_execute(base: Any, identifier: str, operation: str, timeout: float) -> dict[str, object]:
    try:
        value = base.execute(_step(identifier, operation, timeout))
    except Exception as error:
        raise NativeUIJourneyError(f"base adapter {operation} failed: {error}") from error
    if not isinstance(value, dict):
        raise NativeUIJourneyError(f"base adapter {operation} returned an invalid result")
    return value


def _restart_service_for_native_ui(base: Any, timeout: float) -> dict[str, object]:
    method = getattr(base, "restart_service_for_native_ui", None)
    if not callable(method):
        raise NativeUIJourneyError(
            "base adapter has no service-only process-loss preparation"
        )
    try:
        value = method(timeout)
    except Exception as error:
        raise NativeUIJourneyError(
            f"base adapter service-only process loss failed: {error}"
        ) from error
    if not isinstance(value, dict):
        raise NativeUIJourneyError(
            "base adapter service-only process loss returned an invalid result"
        )
    return value


def _positive_throughput(observations: dict[str, object]) -> bool:
    """Derive the shared throughput assertion from measured values."""
    values = tuple(observations.get(name) for name in (
        "latency_ms", "download_mbps", "upload_mbps"
    ))
    if all(isinstance(value, (int, float)) and not isinstance(value, bool) for value in values):
        return all(math.isfinite(float(value)) and float(value) > 0 for value in values)
    return False


def _record_native_observations(
    base: Any,
    checks: dict[str, object],
    timeout: float,
    *,
    prefix: str = "",
) -> None:
    """Run one independent tunnel/routing/stability/throughput observation set."""
    def key(name: str) -> str:
        return name if not prefix else f"{prefix}_{name}"

    tunnel = _base_execute(base, f"{prefix or 'native'}-tunnel", "observe_tunnel", timeout)
    checks[key("tunnel_interface")] = tunnel.get("tunnel_interface") is True
    routing = _base_execute(base, f"{prefix or 'native'}-routing", "observe_routing_identity", timeout)
    checks[key("routing_verified")] = routing.get("routing_verified") is True
    stability = _base_execute(base, f"{prefix or 'native'}-stability", "measure_stability", timeout)
    checks[key("stability_verified")] = stability.get("stability_verified") is True
    if "stability_sample_count" in stability:
        checks[key("stability_sample_count")] = stability["stability_sample_count"]
    throughput = _base_execute(base, f"{prefix or 'native'}-throughput", "measure_throughput", timeout)
    for name in ("latency_ms", "download_mbps", "upload_mbps"):
        if name in throughput:
            checks[key(name)] = throughput[name]
    checks[key("throughput_positive")] = _positive_throughput(throughput)


def _layout_rect(layout: dict[str, Any], name: str) -> tuple[float, float, float, float]:
    value = layout.get(name)
    if not isinstance(value, dict):
        raise NativeUIJourneyError(f"desktop layout did not report {name} bounds")
    coordinates: list[float] = []
    for key in ("x", "y", "width", "height"):
        raw = value.get(key)
        if isinstance(raw, bool) or not isinstance(raw, (int, float)) or not math.isfinite(raw):
            raise NativeUIJourneyError(f"desktop layout reported invalid {name} {key}: {raw!r}")
        coordinates.append(float(raw))
    x, y, width, height = coordinates
    if width <= 0 or height <= 0:
        raise NativeUIJourneyError(f"desktop layout reported empty {name} bounds")
    return x, y, width, height


def _rect_contains(container: tuple[float, float, float, float], item: tuple[float, float, float, float]) -> bool:
    cx, cy, cw, ch = container
    ix, iy, iw, ih = item
    tolerance = 1.0  # Accessibility frames can be fractional on scaled displays.
    return (
        ix >= cx - tolerance and iy >= cy - tolerance
        and ix + iw <= cx + cw + tolerance and iy + ih <= cy + ch + tolerance
    )


def _rects_overlap(left: tuple[float, float, float, float], right: tuple[float, float, float, float]) -> bool:
    lx, ly, lw, lh = left
    rx, ry, rw, rh = right
    return min(lx + lw, rx + rw) - max(lx, rx) > 1 and min(ly + lh, ry + rh) - max(ly, ry) > 1


def _assert_profile_list_layout(
    layout: object,
    *,
    edge: str,
    visible_action: str,
) -> None:
    if not isinstance(layout, dict) or layout.get("ready") is not True:
        raise NativeUIJourneyError("desktop profile-list layout evidence was not ready")
    if edge not in {"top", "bottom"}:
        raise ValueError(f"unsupported profile-list edge: {edge}")

    bounds = {name: _layout_rect(layout, name) for name in (
        "window", "controls", "profile_viewport", "connection_action", "logs",
    )}
    window = bounds["window"]
    controls = bounds["controls"]
    profile_viewport = bounds["profile_viewport"]
    connection_action = bounds["connection_action"]
    logs = bounds["logs"]
    for name, rectangle in bounds.items():
        if name != "window" and not _rect_contains(window, rectangle):
            raise NativeUIJourneyError(f"desktop {name} bounds escaped the native window")
    if not _rect_contains(controls, profile_viewport) or not _rect_contains(controls, connection_action):
        raise NativeUIJourneyError("desktop profile viewport or main action escaped Connection controls")
    if logs[2] < 160 or logs[3] < 120:
        raise NativeUIJourneyError(
            f"desktop logs pane was too small to remain usable with a long profile list: {logs[2]:.1f}x{logs[3]:.1f}"
        )
    if _rects_overlap(profile_viewport, connection_action) or _rects_overlap(profile_viewport, logs):
        raise NativeUIJourneyError("desktop profile viewport overlapped the main action or logs pane")

    scroll_position = layout.get("scroll_position")
    if (
        isinstance(scroll_position, bool)
        or not isinstance(scroll_position, (int, float))
        or not math.isfinite(scroll_position)
        or not 0 <= scroll_position <= 100
    ):
        raise NativeUIJourneyError(f"desktop profile-list scroll position was invalid: {scroll_position!r}")
    if edge == "top" and scroll_position > 5:
        raise NativeUIJourneyError(f"desktop profile list did not return to the top: {scroll_position!r}%")
    if edge == "bottom" and scroll_position < 95:
        raise NativeUIJourneyError(f"desktop profile list did not reach the bottom: {scroll_position!r}%")

    visible_actions = layout.get("visible_profile_actions")
    if not isinstance(visible_actions, list) or any(not isinstance(action, str) for action in visible_actions):
        raise NativeUIJourneyError("desktop profile-list layout did not report visible profile actions")
    if visible_action not in visible_actions:
        raise NativeUIJourneyError(f"{visible_action} was not reachable inside the bounded profile viewport")


def _exercise_long_profile_list_viewport(ui) -> None:
    """Prove the bottom synthetic profile remains reachable without using it."""

    try:
        bottom = ui.scroll_profile_list("bottom")
        _assert_profile_list_layout(bottom, edge="bottom", visible_action="Profile 24 action")

        rendered_logs = ui._call("logs")
        if rendered_logs.get("ready") is not True or not str(rendered_logs.get("text", "")).strip():
            raise NativeUIJourneyError("desktop log output was unavailable with the long profile list at its bottom")
        visible_log_position = ui._call("log-position")
        if ui.platform == "macos":
            visible_range_start = visible_log_position.get("visible_range_start")
            visible_range_end = visible_log_position.get("visible_range_end")
            if not (
                isinstance(visible_range_start, int)
                and isinstance(visible_range_end, int)
                and visible_range_end > visible_range_start
            ):
                raise NativeUIJourneyError("desktop logs had no readable viewport at the bottom of the long profile list")
        elif not str(visible_log_position.get("visible_first_record", "")).strip():
            raise NativeUIJourneyError("desktop logs had no visible record at the bottom of the long profile list")
    except BaseException as primary_error:
        try:
            top = ui.scroll_profile_list("top")
            _assert_profile_list_layout(top, edge="top", visible_action="Profile 1 action")
        except BaseException as restore_error:
            raise BaseExceptionGroup(
                "long profile-list verification and top restoration failed",
                [primary_error, restore_error],
            )
        raise
    else:
        top = ui.scroll_profile_list("top")
        _assert_profile_list_layout(top, edge="top", visible_action="Profile 1 action")


def _exercise_subscription_controls(ui, base, url: str, fixture, timeout: float) -> dict[str, bool]:
    initial = base._snapshot(min(timeout, 30), "NATIVE_SELECTION_STATUS_FAILED")
    if len(initial.get("profiles", [])) < 2:
        raise NativeUIJourneyError("Native switching qualification requires two supplied profiles")

    def stats() -> dict[str, int]:
        value = fixture.control_stats()
        if not all(type(value.get(name)) is int for name in (
            "subscription_gets", "last_subscription_get_started_at_unix_ms",
            "in_flight_gets", "max_in_flight_gets",
        )):
            raise NativeUIJourneyError("subscription fixture returned incomplete request counts")
        return value

    def wait_for_gets(count: int, message: str, *, idle: bool = True) -> dict[str, int]:
        deadline = time.monotonic() + timeout
        current = stats()
        while time.monotonic() < deadline:
            current = stats()
            if current["subscription_gets"] > count:
                raise NativeUIJourneyError(message + ": unexpected duplicate subscription request")
            if current["subscription_gets"] == count and (not idle or current["in_flight_gets"] == 0):
                return current
            time.sleep(0.05)
        raise NativeUIJourneyError(message)

    def wait_for_snapshot(predicate, message: str) -> dict:
        deadline = time.monotonic() + timeout
        current = initial
        while time.monotonic() < deadline:
            current = base._snapshot(min(30, max(0.1, deadline - time.monotonic())), "NATIVE_SELECTION_STATUS_FAILED")
            if predicate(current):
                return current
            if current.get("state") == "FAILED":
                raise NativeUIJourneyError(message)
            time.sleep(0.05)
        raise NativeUIJourneyError(message)

    def same_active_generation(current: dict, previous: dict) -> bool:
        return (
            current.get("state") == "CONNECTED"
            and current.get("generation") == previous.get("generation")
            and current.get("active_digest") == previous.get("active_digest")
            and current.get("active_mode") == previous.get("active_mode")
            and current.get("active_index") == previous.get("active_index")
        )

    def require_active_generation(current: dict, previous: dict, message: str) -> None:
        if not same_active_generation(current, previous):
            raise NativeUIJourneyError(message)

    def require_disconnect_control() -> dict:
        view = ui.snapshot()
        if view.get("status") != "Connected":
            raise NativeUIJourneyError("active connection status was not visible during subscription loading")
        controls = view.get("enabled_controls", [])
        if not any("Disconnect" in str(name) for name in controls):
            raise NativeUIJourneyError("the active connection had no enabled Disconnect control")
        return view

    def wait_for_logs(predicate, message: str) -> dict:
        deadline = time.monotonic() + timeout
        value = {}
        while time.monotonic() < deadline:
            value = ui._call("logs")
            if value.get("ready") is True and predicate(value.get("text", "")):
                return value
            time.sleep(0.1)
        raise NativeUIJourneyError(message)

    original_profile = fixture.profile_bytes
    initial_stats = stats()
    initial_requests = initial_stats["subscription_gets"]
    if initial_requests != 1:
        raise NativeUIJourneyError(
            f"native Paste did not load the disposable subscription exactly once (observed {initial_requests} GETs)"
        )
    if ui.platform in {"windows", "macos"}:
        paste_invoked_at = getattr(ui, "last_paste_invoked_at_unix_ms", None)
        get_started_at = initial_stats["last_subscription_get_started_at_unix_ms"]
        if type(paste_invoked_at) is not int or get_started_at < paste_invoked_at:
            raise NativeUIJourneyError(f"{ui.platform} Paste request timing was unavailable or preceded the button invocation")
        paste_delay_ms = get_started_at - paste_invoked_at
        if paste_delay_ms >= 400:
            raise NativeUIJourneyError(
                f"{ui.platform} Paste waited for the typed debounce before requesting the subscription ({paste_delay_ms} ms)"
            )
    checks: dict[str, bool] = {"native_paste_immediate": True}
    if "Retry" in ui.snapshot().get("labels", []):
        raise NativeUIJourneyError("Retry appeared before any subscription failure")
    if any(
        str(label).strip().casefold() in {"load", "load profiles", "load configuration"}
        for label in ui.snapshot().get("labels", [])
    ):
        raise NativeUIJourneyError("the connection page exposed a separate Load action")
    checks["no_separate_load_action"] = True

    def selected(previous: dict, index: int | None) -> dict:
        deadline = time.monotonic() + timeout
        mode = "AUTO_SELECT" if index is None else "PROFILE_INDEX"
        target_index = index
        transition_seen = False
        next_ui_check = 0.0
        while time.monotonic() < deadline:
            current = base._snapshot(min(30, max(0.1, deadline - time.monotonic())), "NATIVE_SELECTION_STATUS_FAILED")
            if current.get("state") == "FAILED":
                raise NativeUIJourneyError(f"Native profile selection failed: {current}")
            pending = current.get("pending_target")
            pending_matches = (
                isinstance(pending, dict)
                and pending.get("mode") == mode
                and (target_index is None or pending.get("index") == target_index)
            )
            selected_is_starting = (
                current.get("state") in {"PROBING", "PREPARING"}
                and current.get("active_mode") == mode
                and (target_index is None or current.get("active_index") == target_index)
                and current.get("active_digest") == current.get("digest")
            )
            if (pending_matches or selected_is_starting) and time.monotonic() >= next_ui_check:
                view = ui.snapshot()
                labels = set(view.get("labels", []))
                enabled = set(view.get("enabled_controls", []))
                if "Stop" in labels and "Stop" in enabled:
                    competing_actions = {
                        f"Profile {profile.get('index', offset) + 1} action"
                        for offset, profile in enumerate(current.get("profiles", []))
                        if index is None or profile.get("index") != index
                    }
                    if competing_actions & enabled:
                        raise NativeUIJourneyError("a competing profile Connect action remained enabled during Auto selection")
                    transition_seen = True
                next_ui_check = time.monotonic() + 0.15
            if (current.get("state") == "CONNECTED" and current.get("generation", 0) > previous.get("generation", 0)
                    and current.get("active_mode") == mode
                    and (index is None or current.get("active_profile", {}).get("index") == index)):
                if not transition_seen:
                    action = "Auto" if index is None else f"Profile {index + 1}"
                    raise NativeUIJourneyError(f"{action} selection did not expose its enabled Stop action while connecting")
                return current
            time.sleep(0.1)
        raise NativeUIJourneyError("Native selection did not reach its requested generation and profile")

    def switch_profile(previous: dict, index: int, competing_index: int, *, during_pending=None) -> dict:
        target = f"Profile {index + 1} action"
        competing = f"Profile {competing_index + 1} action"
        deadline = time.monotonic() + timeout
        # Begin polling immediately after invoking Connect, but allow the
        # frontend's in-flight Start response and snapshot refresh to render.
        ui.activate_profile(index)
        transition_seen = False
        next_ui_check = 0.0
        while time.monotonic() < deadline:
            current = base._snapshot(min(30, max(0.1, deadline - time.monotonic())), "NATIVE_SELECTION_STATUS_FAILED")
            pending = current.get("pending_target")
            pending_matches = (
                isinstance(pending, dict)
                and pending.get("mode") == "PROFILE_INDEX"
                and pending.get("index") == index
            )
            selected_is_starting = (
                current.get("state") in {"PROBING", "PREPARING"}
                and current.get("active_mode") == "PROFILE_INDEX"
                and current.get("active_index") == index
                and current.get("active_digest") == current.get("digest")
            )
            if (pending_matches or selected_is_starting) and time.monotonic() >= next_ui_check:
                view = ui.snapshot()
                labels = set(view.get("labels", []))
                enabled = set(view.get("enabled_controls", []))
                if "Stop" in labels and target in enabled:
                    if competing in enabled:
                        raise NativeUIJourneyError("a competing profile Connect action remained enabled during switching")
                    transition_seen = True
                    if during_pending is not None:
                        observe_pending = during_pending
                        during_pending = None
                        observe_pending(current)
                next_ui_check = time.monotonic() + 0.15
            if (current.get("state") == "CONNECTED" and current.get("generation", 0) > previous.get("generation", 0)
                    and current.get("active_mode") == "PROFILE_INDEX"
                    and current.get("active_profile", {}).get("index") == index):
                if not transition_seen:
                    raise NativeUIJourneyError("the native UI transition did not expose Stop and disable competing profile actions")
                return current
            if current.get("state") == "FAILED":
                raise NativeUIJourneyError(f"Native profile switch failed: {current}")
            time.sleep(0.05)
        raise NativeUIJourneyError("Native profile switch did not reach its requested profile")

    def cancel_switch_profile(previous: dict, index: int, competing_index: int) -> dict:
        target = f"Profile {index + 1} action"
        competing = f"Profile {competing_index + 1} action"
        deadline = time.monotonic() + timeout
        ui.activate_profile(index)
        next_ui_check = 0.0
        while time.monotonic() < deadline:
            current = base._snapshot(min(30, max(0.1, deadline - time.monotonic())), "NATIVE_SELECTION_STATUS_FAILED")
            pending = current.get("pending_target")
            pending_matches = (
                isinstance(pending, dict)
                and pending.get("mode") == "PROFILE_INDEX"
                and pending.get("index") == index
            )
            selected_is_starting = (
                current.get("state") in {"PROBING", "PREPARING"}
                and current.get("active_mode") == "PROFILE_INDEX"
                and current.get("active_index") == index
                and current.get("active_digest") == current.get("digest")
            )
            if (pending_matches or selected_is_starting) and time.monotonic() >= next_ui_check:
                view = ui.snapshot()
                labels = set(view.get("labels", []))
                enabled = set(view.get("enabled_controls", []))
                if "Stop" in labels and target in enabled:
                    if competing in enabled:
                        raise NativeUIJourneyError("a competing profile Connect action remained enabled during a cancellable switch")
                    ui._click(target)
                    canceled = wait_for_snapshot(
                        lambda value: value.get("state") in {"IDLE", "CONFIGURED"}
                        and value.get("pending_target") is None
                        and value.get("active_profile") is None,
                        "Stop did not cancel the selected profile switch",
                    )
                    if canceled.get("generation", 0) < previous.get("generation", 0):
                        raise NativeUIJourneyError("canceled profile switch moved the session to an older generation")
                    return canceled
                next_ui_check = time.monotonic() + 0.15
            if current.get("state") == "FAILED":
                raise NativeUIJourneyError(f"Native profile switch failed before Stop was available: {current}")
            if current.get("state") == "CONNECTED" and current.get("generation", 0) > previous.get("generation", 0):
                raise NativeUIJourneyError("profile switch completed before its Stop action could cancel it")
            time.sleep(0.05)
        raise NativeUIJourneyError("the selected profile never exposed a cancellable Stop action")

    first = 1 if initial["active_profile"]["index"] == 0 else 0
    canceled = cancel_switch_profile(initial, first, 0 if first == 1 else 1)
    ui.activate_profile(first)
    manual = selected(canceled, first)
    second = 0 if first == 1 else 1

    pending_import: dict[str, object] = {}

    def import_while_switching(pending: dict) -> None:
        expected = pending.get("pending_target")
        if not (
            isinstance(expected, dict)
            and expected.get("mode") == "PROFILE_INDEX"
            and expected.get("index") == second
        ):
            raise NativeUIJourneyError(
                "desktop import test did not begin from the selected profile's pending switch target"
            )
        current_url = ui.profile.read_text(encoding="utf-8").strip()
        separator = "&" if "?" in current_url else "?"
        imported_url = current_url + separator + "import-during-connect=1"
        before_gets = stats()["subscription_gets"]
        fixture.hold_responses()
        try:
            ui.dispatch_import_link(imported_url)
            deadline = time.monotonic() + timeout
            current_stats = stats()
            while time.monotonic() < deadline:
                current_stats = stats()
                if current_stats["subscription_gets"] > before_gets + 1:
                    raise NativeUIJourneyError("import during a pending profile switch issued duplicate requests")
                if current_stats["max_in_flight_gets"] > 1:
                    raise NativeUIJourneyError("import during a pending profile switch overlapped subscription requests")
                if current_stats["subscription_gets"] == before_gets + 1 and current_stats["in_flight_gets"] == 1:
                    break
                time.sleep(0.025)
            else:
                raise NativeUIJourneyError("desktop import did not begin a held subscription request during switching")

            loading = base._snapshot(min(30, timeout), "NATIVE_PENDING_IMPORT_STATUS_FAILED")
            pending_target = loading.get("pending_target")
            still_pending = (
                isinstance(pending_target, dict)
                and pending_target.get("mode") == "PROFILE_INDEX"
                and pending_target.get("index") == second
            )
            active_target = (
                loading.get("active_mode") == "PROFILE_INDEX"
                and loading.get("active_index") == second
            )
            active_profile = loading.get("active_profile")
            connected_target = (
                loading.get("state") == "CONNECTED"
                and isinstance(active_profile, dict)
                and active_profile.get("index") == second
            )
            if not (still_pending or active_target or connected_target):
                raise NativeUIJourneyError(
                    "subscription import interrupted or replaced the authoritative pending profile switch"
                )
            if loading.get("source_url") != pending.get("source_url"):
                raise NativeUIJourneyError("held subscription import changed the accepted URL before its response completed")
            pending_import.update({"url": imported_url, "before_gets": before_gets})
        finally:
            fixture.release_responses()

    switched = switch_profile(manual, second, first, during_pending=import_while_switching)
    if pending_import:
        imported = str(pending_import["url"])
        before_gets = int(pending_import["before_gets"])
        loaded_import = wait_for_snapshot(
            lambda value: value.get("source_url") == imported,
            "subscription import did not complete after the profile switch",
        )
        wait_for_gets(before_gets + 1, "import during profile switching did not complete exactly one request")
        require_active_generation(
            loaded_import, switched,
            "accepting the imported URL started, stopped, or replaced the selected profile connection",
        )
        if loaded_import.get("active_index") != second or loaded_import.get("digest") != switched.get("digest"):
            raise NativeUIJourneyError("import completion changed the profile switch target or active inventory")
        checks["import_during_connecting_preserves_request"] = True
    checks["profile_switch_transition_controls"] = True

    # Relaunch the frontend while a manual target is active. The same loaded
    # inventory and target must return without fetching or switching profiles.
    manual_reopen_requests = stats()["subscription_gets"]
    ui.close()
    ui.start()
    ui._wait(lambda: f"Profile {second + 1} action" in ui.snapshot()["labels"], "manual selection was not restored after frontend reopen")
    manual_reopened = base._snapshot(min(timeout, 30), "NATIVE_MANUAL_REOPEN_STATUS_FAILED")
    require_active_generation(manual_reopened, switched, "frontend reopen changed the manual active generation")
    if manual_reopened.get("active_mode") != "PROFILE_INDEX" or manual_reopened.get("active_index") != second:
        raise NativeUIJourneyError("frontend reopen did not preserve the active manual profile")
    manual_view = ui.snapshot()
    if f"Profile {second + 1} action" not in manual_view.get("enabled_controls", []) or not any(
        "Disconnect" in str(name) for name in manual_view.get("enabled_controls", [])
    ):
        raise NativeUIJourneyError("reopened manual profile did not expose its active Disconnect control")
    if stats()["subscription_gets"] != manual_reopen_requests:
        raise NativeUIJourneyError("reopening an already loaded manual inventory refetched it")
    checks["manual_state_reopened"] = True

    # Typed edits are debounced. Observe the fixture itself rather than
    # inferring the delay from a UI spinner that may already be stale.
    before_typed = stats()["subscription_gets"]
    typed_url = url + "?typed=debounce"
    request_seen = threading.Event()
    stop_monitor = threading.Event()
    request_observation: dict[str, object] = {"at": None, "error": None}

    def observe_typed_request() -> None:
        try:
            while not stop_monitor.is_set():
                current_stats = stats()
                if current_stats["subscription_gets"] > before_typed:
                    request_observation["at"] = time.monotonic()
                    request_seen.set()
                    return
                stop_monitor.wait(0.02)
        except BaseException as error:
            request_observation["error"] = error
            request_seen.set()

    monitor = threading.Thread(target=observe_typed_request, name="native-subscription-debounce", daemon=True)
    monitor.start()
    typed_at = time.monotonic()
    try:
        ui.type_source(typed_url)
        if not request_seen.wait(timeout):
            raise NativeUIJourneyError("typed subscription edit did not start a request")
    finally:
        stop_monitor.set()
        monitor.join(timeout=5)
    if request_observation["error"] is not None:
        raise NativeUIJourneyError("could not observe typed subscription request timing") from request_observation["error"]
    typed_started = request_observation["at"]
    if not isinstance(typed_started, float) or typed_started - typed_at < 0.38:
        raise NativeUIJourneyError("typed subscription request did not respect the debounce interval")
    if stats()["subscription_gets"] != before_typed + 1:
        raise NativeUIJourneyError("typed subscription edit caused duplicate requests")
    typed_loaded = wait_for_snapshot(lambda value: value.get("source_url") == typed_url, "typed subscription URL was not accepted")
    if typed_loaded.get("digest") != initial.get("digest"):
        raise NativeUIJourneyError("typing the same inventory changed its configuration digest")
    wait_for_gets(before_typed + 1, "typed subscription request did not finish")
    time.sleep(0.9)
    if stats()["subscription_gets"] != before_typed + 1:
        raise NativeUIJourneyError("unchanged snapshot polling refetched the subscription")
    checks["typed_debounce"] = True

    # Hold one URL response, then edit to the newest URL while it is in
    # flight. The stale response and latest response are held separately so
    # the stale result can be inspected before the current response completes.
    before_coalesced = stats()["subscription_gets"]
    stale_inventory = (
        b'[[Outline]]\nDescription = "Stale intermediate profile"\nServer = "127.0.0.1"\n'
        b'Port = 9\nPassword = "never-connect-stale"\n'
    )
    synthetic_inventory = (
        b'[[Outline]]\nDescription = ""\nServer = "127.0.0.1"\nPort = 9\nPassword = "never-connect-one"\n\n'
        b'[[Outline]]\nDescription = "Latest second profile"\nServer = "127.0.0.1"\nPort = 9\nPassword = "never-connect-two"\n'
    )
    fixture.replace_response(stale_inventory)
    fixture.hold_responses()
    older_url = url + "?coalesce=older"
    older_edit_at = time.monotonic()
    ui.type_source(older_url)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        current_stats = stats()
        if current_stats["subscription_gets"] == before_coalesced + 1 and current_stats["in_flight_gets"] == 1:
            break
        if current_stats["subscription_gets"] > before_coalesced + 1 or current_stats["max_in_flight_gets"] > 1:
            fixture.release_responses()
            raise NativeUIJourneyError("subscription requests overlapped or an intermediate edit was fetched")
        time.sleep(0.025)
    else:
        fixture.release_responses()
        raise NativeUIJourneyError("the held subscription request did not start")
    if time.monotonic() - older_edit_at < 0.38:
        fixture.release_responses()
        raise NativeUIJourneyError("typed subscription request did not respect the debounce interval")

    middle = base._snapshot(min(timeout, 30), "NATIVE_LOADING_STATUS_FAILED")
    require_active_generation(middle, switched, "an in-flight subscription changed the active connection")
    require_disconnect_control()
    log_probe = ui._call("logs")
    if log_probe.get("ready") is not True:
        fixture.release_responses()
        raise NativeUIJourneyError("log refresh did not respond during subscription loading")

    fixture.replace_response(synthetic_inventory)
    latest_url = url + "?coalesce=latest"
    ui.type_source(latest_url)
    time.sleep(0.45)
    held = stats()
    if held["subscription_gets"] != before_coalesced + 1 or held["in_flight_gets"] != 1 or held["max_in_flight_gets"] != 1:
        fixture.release_responses()
        raise NativeUIJourneyError("frontend did not serialize and coalesce the held subscription edits")
    require_active_generation(base._snapshot(min(timeout, 30), "NATIVE_LOADING_STATUS_FAILED"), switched, "editing the URL interrupted the active tunnel")
    loading_view = require_disconnect_control()
    if not any("Loading profiles" in str(label) for label in loading_view.get("labels", [])):
        fixture.release_responses()
        raise NativeUIJourneyError("the active subscription load did not expose its loading state")
    active_index = (switched.get("active_profile") or {}).get("index")
    enabled_rows = set(loading_view.get("enabled_controls", []))
    expected_disconnect = f"Profile {active_index + 1} action" if isinstance(active_index, int) else ""
    if any(name in enabled_rows for name in ("Profile 1 action", "Profile 2 action") if name != expected_disconnect):
        fixture.release_responses()
        raise NativeUIJourneyError("a stale Connect action remained enabled while a replacement inventory was loading")
    checks["loading_disables_stale_actions"] = True
    fixture.release_one_response()
    deadline = time.monotonic() + timeout
    stale_guard = {}
    while time.monotonic() < deadline:
        stale_response_stats = stats()
        if (stale_response_stats["subscription_gets"] == before_coalesced + 2
                and stale_response_stats["in_flight_gets"] == 1):
            stale_guard = base._snapshot(
                min(timeout, 30), "NATIVE_STALE_RESPONSE_STATUS_FAILED"
            )
            break
        if stale_response_stats["subscription_gets"] > before_coalesced + 2:
            fixture.release_responses()
            raise NativeUIJourneyError("stale response gate observed an unexpected duplicate subscription request")
        time.sleep(0.025)
    else:
        fixture.release_responses()
        raise NativeUIJourneyError("latest subscription did not remain held after releasing only the stale response")
    try:
        require_active_generation(stale_guard, switched, "the stale subscription response changed the active tunnel")
        if stale_guard.get("source_url") != typed_url or stale_guard.get("digest") != initial.get("digest"):
            raise NativeUIJourneyError(
                "the stale subscription response became the current backend inventory before the latest response completed"
            )
        stale_view = ui.snapshot()
        if any("Stale intermediate profile" in str(label) for label in stale_view.get("labels", [])):
            raise NativeUIJourneyError("the stale subscription response was rendered as the current profile inventory")
    finally:
        fixture.release_responses()
    latest = wait_for_snapshot(
        lambda value: value.get("source_url") == latest_url and value.get("digest") != initial.get("digest"),
        "the newest coalesced subscription was not accepted",
    )
    completed = wait_for_gets(before_coalesced + 2, "serialized subscription requests did not finish")
    if completed["max_in_flight_gets"] != 1:
        raise NativeUIJourneyError("subscription fixture observed concurrent frontend requests")
    if len(latest.get("profiles", [])) != 2 or latest["profiles"][0].get("description") != "" or latest["profiles"][1].get("description") != "Latest second profile":
        raise NativeUIJourneyError("new subscription inventory did not preserve its two source-ordered profile descriptions")
    require_active_generation(latest, switched, "loading a new inventory changed the old active profile generation")
    latest_ui = ui.snapshot()
    labels = latest_ui.get("labels", [])
    first_row = next((index for index, label in enumerate(labels) if "Profile 1" in str(label)), None)
    second_row = next((index for index, label in enumerate(labels) if "Latest second profile" in str(label)), None)
    if first_row is None or second_row is None or first_row >= second_row or not any(
        "outline" in str(label).casefold() for label in labels
    ):
        raise NativeUIJourneyError("rendered profile rows lost source order, protocol, or the empty-description fallback")
    if ui.platform == "windows":
        expected_windows_rows = {
            f"Profile 1 · {latest['profiles'][0].get('protocol', '')}",
            f"Latest second profile · {latest['profiles'][1].get('protocol', '')}",
        }
        if not expected_windows_rows.issubset(set(labels)):
            raise NativeUIJourneyError(
                "a rendered Windows profile row did not retain its own protocol label: "
                f"expected={sorted(expected_windows_rows)} labels={labels}"
            )
    if ui.platform == "macos":
        expected_macos_rows = {
            f"Profile 1 protocol · {latest['profiles'][0].get('protocol', '')}",
            f"Profile 2 protocol · {latest['profiles'][1].get('protocol', '')}",
        }
        if not expected_macos_rows.issubset(set(labels)):
            raise NativeUIJourneyError(
                "a rendered macOS profile row did not retain its own protocol label: "
                f"expected={sorted(expected_macos_rows)} labels={labels}"
            )
    if not {"Profile 1 action", "Profile 2 action"}.issubset(set(labels)):
        raise NativeUIJourneyError("rendered profile rows did not expose both manual Connect controls")
    active_description = (switched.get("active_profile") or {}).get("description", "")
    if active_description and not any(active_description in str(label) for label in labels):
        raise NativeUIJourneyError("the active profile disappeared from the connection summary after inventory replacement")
    require_disconnect_control()
    checks.update({
        "coalesced_latest_subscription": True,
        "loading_ui_responsive": True,
        "new_inventory_keeps_active_disconnectable": True,
        "profile_rows_source_order": True,
        "empty_description_fallback": True,
    })

    # Restore the real two-profile fixture and deliver the same warm import
    # twice. Both deliveries should produce one fetch and leave the connection
    # generation and active digest untouched.
    fixture.replace_response(original_profile)
    before_import = stats()["subscription_gets"]
    restored_url = url + "?restore=profiles"
    ui.import_link(restored_url)
    restored = wait_for_snapshot(lambda value: value.get("source_url") == restored_url, "warm import did not load the restored subscription")
    after_import_stats = wait_for_gets(before_import + 1, "repeated warm import fetched more than once")
    if after_import_stats["max_in_flight_gets"] != 1:
        raise NativeUIJourneyError("repeated warm import caused overlapping subscription requests")
    require_active_generation(restored, switched, "importing a subscription changed or interrupted the active connection")
    if restored.get("digest") != initial.get("digest"):
        raise NativeUIJourneyError("restored subscription did not recover the supplied profile inventory")
    before_bare = base._snapshot(min(timeout, 30), "NATIVE_BARE_LINK_STATUS_FAILED")
    bare_view = ui.bare_link()
    after_bare = base._snapshot(min(timeout, 30), "NATIVE_BARE_LINK_STATUS_FAILED")
    require_active_generation(after_bare, before_bare, "a bare deep link changed the active connection")
    if after_bare.get("source_url") != restored_url or bare_view.get("status") != "Connected":
        raise NativeUIJourneyError("a bare deep link changed the accepted source or hid the active connection")
    if stats()["subscription_gets"] != before_import + 1:
        raise NativeUIJourneyError("a bare or duplicate deep link started another subscription fetch")
    checks["duplicate_import_single_load"] = True
    checks["import_preserves_active_generation"] = True
    checks["bare_deep_link_native"] = True
    checks["warm_link_same_window"] = True

    if ui.platform in {"windows", "macos"}:
        invalid_links = (
            "dobbyvpn://import",
            "dobbyvpn://import?url=",
            "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fone&url=https%3A%2F%2Fexample.invalid%2Ftwo",
            "dobbyvpn://import?url=%ZZ",
            "dobbyvpn://import?url=http%3A%2F%2Fexample.invalid%2Fsubscription",
            "dobbyvpn://import?url=https%3A%2F%2F",
        )
        for link in invalid_links:
            before_invalid = base._snapshot(min(timeout, 30), "NATIVE_INVALID_IMPORT_STATUS_FAILED")
            before_invalid_gets = stats()["subscription_gets"]
            before_invalid_logs = ui._call("logs").get("text", "")
            ui.open_deep_link(link)
            wait_for_logs(
                lambda text: text != before_invalid_logs,
                "invalid native deep link did not produce a new rendered diagnostic",
            )
            ui._wait(
                lambda: any("Use dobbyvpn://import?url=" in str(label) for label in ui.snapshot()["labels"]),
                "invalid native deep link did not show actionable guidance",
            )
            time.sleep(0.45)
            after_invalid_stats = stats()
            if (after_invalid_stats["subscription_gets"] != before_invalid_gets
                    or after_invalid_stats["in_flight_gets"] != 0):
                raise NativeUIJourneyError(
                    f"invalid deep link started a subscription GET: {link}; stats={after_invalid_stats}"
                )
            after_invalid = base._snapshot(min(timeout, 30), "NATIVE_INVALID_IMPORT_STATUS_FAILED")
            require_active_generation(after_invalid, before_invalid, "an invalid deep link changed the active connection")
            if after_invalid.get("source_url") != before_invalid.get("source_url") or after_invalid.get("digest") != before_invalid.get("digest"):
                raise NativeUIJourneyError("an invalid deep link changed the accepted subscription")
        checks["deep_link_rejections_preserve_connection"] = True

    # A controlled one-shot HTTP failure proves that failure has no automatic
    # retry loop; the visible Retry action then performs exactly one recovery.
    fixture.fail_next_response()
    before_retry = stats()["subscription_gets"]
    retry_url = url + "?retry=once"
    ui.type_source(retry_url)
    ui._wait(lambda: "Retry" in ui.snapshot()["labels"], "failed subscription did not expose Retry")
    failed = wait_for_snapshot(lambda value: value.get("source_url") == restored_url, "failed load replaced the accepted subscription")
    failed_stats = wait_for_gets(before_retry + 1, "failed subscription did not produce one request")
    require_active_generation(failed, switched, "failed subscription loading interrupted the active tunnel")
    if ui.snapshot().get("status") != "Connected" or failed_stats["in_flight_gets"] != 0:
        raise NativeUIJourneyError("the active connection or failed-load view did not remain usable")
    time.sleep(1.6)
    if stats()["subscription_gets"] != before_retry + 1:
        raise NativeUIJourneyError("failed subscription entered an automatic retry loop")
    checks["retry_single_attempt"] = True
    ui.retry()
    retry_loaded = wait_for_snapshot(lambda value: value.get("source_url") == retry_url, "Retry did not accept the repaired subscription")
    wait_for_gets(before_retry + 2, "Retry did not perform exactly one additional request")
    require_active_generation(retry_loaded, switched, "Retry changed or interrupted the active connection")
    checks["retry_success_native"] = True

    ui.clear_logs()
    if not getattr(ui, "log_resize_verified", False):
        raise NativeUIJourneyError("desktop logs did not verify frozen reading position across window resize")
    checks["logs_freeze_resize_preserves_reading_position"] = True
    if ui.platform == "windows":
        checks["rendered_stderr_capture_label"] = True
    empty_logs = ui._call("logs")
    if empty_logs.get("ready") is not True or empty_logs.get("text", "").strip():
        raise NativeUIJourneyError("Clear did not leave an empty rendered log view")
    before_clear_import = base._snapshot(min(timeout, 30), "NATIVE_CLEAR_STATUS_FAILED")
    clear_import_requests = stats()["subscription_gets"]
    clear_source = url + "?clear-follow=event"
    ui.import_link(clear_source)
    followed = wait_for_logs(lambda value: "profile inventory loaded" in value, "log following did not show a new event after Clear")
    after_clear_import = base._snapshot(min(timeout, 30), "NATIVE_CLEAR_STATUS_FAILED")
    clear_stats = stats()
    if clear_stats["subscription_gets"] != clear_import_requests + 1 or clear_stats["in_flight_gets"] != 0:
        raise NativeUIJourneyError("post-Clear import did not produce exactly one subscription request")
    require_active_generation(after_clear_import, before_clear_import, "importing after Clear changed the active connection")
    if "profile inventory loaded" not in followed.get("text", ""):
        raise NativeUIJourneyError("new post-Clear log event was not rendered")
    checks["clear_resumes_following"] = True

    # The user-local Clear boundary survives reopening the native process,
    # while new records after Clear remain visible and loaded inventory is
    # reused without another subscription request.
    clear_boundary_record = getattr(ui, "cleared_record", None)
    reopen_gets = stats()["subscription_gets"]
    ui.close()
    ui.start()
    reopened_after_clear = wait_for_logs(
        lambda value: "profile inventory loaded" in value,
        "reopened desktop UI did not retain new post-Clear log records",
    )
    if clear_boundary_record and clear_boundary_record in reopened_after_clear.get("text", ""):
        raise NativeUIJourneyError("reopening the desktop UI restored a record hidden by Clear")
    if stats()["subscription_gets"] != reopen_gets:
        raise NativeUIJourneyError("reopening the desktop UI refetched an already loaded inventory")
    checks["clear_boundary_survives_frontend_reopen"] = True

    auto_connection = ui.connect_with_auto_stop()
    selected(switched, None)
    if auto_connection.get("auto_stop_observed") is not True:
        raise NativeUIJourneyError("Auto selection returned without proving its rendered Stop action")
    checks["auto_stop_during_auto_selection"] = True

    # Replace the visible inventory while Auto is active. The active profile
    # must remain identifiable even though it is absent from the new rows, and
    # its rendered Disconnect action must stop the real generation.
    before_absent_disconnect = base._snapshot(min(timeout, 30), "NATIVE_ABSENT_PROFILE_STATUS_FAILED")
    absent_disconnect_url = url + "?absent-active-disconnect=1"
    absent_disconnect_before_gets = stats()["subscription_gets"]
    fixture.replace_response(synthetic_inventory)
    ui.type_source(absent_disconnect_url)
    absent_disconnect = wait_for_snapshot(
        lambda value: value.get("source_url") == absent_disconnect_url
        and value.get("digest") != before_absent_disconnect.get("digest"),
        "a different inventory was not accepted while the previous profile remained active",
    )
    wait_for_gets(absent_disconnect_before_gets + 1, "absent-profile inventory did not complete one request")
    require_active_generation(
        absent_disconnect, before_absent_disconnect,
        "accepting a different inventory interrupted the active generation",
    )
    active_description = (absent_disconnect.get("active_profile") or {}).get("description", "")
    new_descriptions = {item.get("description", "") for item in absent_disconnect.get("profiles", [])}
    if not active_description or active_description in new_descriptions:
        raise NativeUIJourneyError("the active profile was not absent from the replacement inventory")
    absent_view = require_disconnect_control()
    if not any(active_description in str(label) for label in absent_view.get("labels", [])):
        raise NativeUIJourneyError("the active profile summary disappeared when its row was absent")
    ui._click("Disconnect")
    disconnected_absent = wait_for_snapshot(
        lambda value: value.get("state") in {"IDLE", "CONFIGURED"}
        and value.get("active_profile") is None and value.get("pending_target") is None,
        "Disconnect did not stop an active profile absent from the loaded inventory",
    )
    if ui.wait_status("Disconnected").get("status") != "Disconnected":
        raise NativeUIJourneyError("the frontend did not render Disconnected after stopping the absent profile")
    checks["absent_active_profile_disconnect"] = True

    ui._click("VPN connection action")
    active_again = selected(disconnected_absent, None)

    # Keep a subscription GET held while the user invokes Disconnect. This
    # proves Configure does not block the foreground action or log refresh.
    held_disconnect_url = url + "?disconnect-during-configure=1"
    held_disconnect_before = stats()["subscription_gets"]
    fixture.hold_responses()
    try:
        ui.type_source(held_disconnect_url)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            held_stats = stats()
            if held_stats["subscription_gets"] == held_disconnect_before + 1 and held_stats["in_flight_gets"] == 1:
                break
            if held_stats["subscription_gets"] > held_disconnect_before + 1 or held_stats["max_in_flight_gets"] > 1:
                raise NativeUIJourneyError("held Configure issued overlapping requests before Disconnect")
            time.sleep(0.025)
        else:
            raise NativeUIJourneyError("held Configure did not start before the Disconnect interaction")
        require_active_generation(
            base._snapshot(min(timeout, 30), "NATIVE_HELD_DISCONNECT_STATUS_FAILED"), active_again,
            "held Configure changed the active connection before Disconnect",
        )
        require_disconnect_control()
        ui.disconnect()
        disconnected_during_load = wait_for_snapshot(
            lambda value: value.get("state") in {"IDLE", "CONFIGURED"}
            and value.get("active_profile") is None and value.get("pending_target") is None,
            "Disconnect was blocked by a held Configure request",
        )
        if stats()["in_flight_gets"] != 1:
            raise NativeUIJourneyError("Disconnect unexpectedly canceled the independent subscription load")
        if ui._call("logs").get("ready") is not True:
            raise NativeUIJourneyError("log refresh did not respond while Configure was held and Disconnect completed")
    finally:
        fixture.release_responses()
    after_held_disconnect = wait_for_snapshot(
        lambda value: value.get("source_url") == held_disconnect_url,
        "the held subscription did not finish after Disconnect",
    )
    wait_for_gets(held_disconnect_before + 1, "held Configure did not finish exactly once")
    if after_held_disconnect.get("state") not in {"IDLE", "CONFIGURED"} or after_held_disconnect.get("active_profile") is not None:
        raise NativeUIJourneyError("the completed Configure restarted a connection after Disconnect")
    checks["disconnect_responsive_during_configure"] = True

    # A real native Paste containing HTTP text must show validation, perform no
    # subscription GET and leave the active generation untouched.
    ui._click("VPN connection action")
    pasted_connection = selected(disconnected_during_load, None)
    valid_source = ui.profile.read_text(encoding="utf-8").strip()
    before_http_paste = base._snapshot(min(timeout, 30), "NATIVE_HTTP_PASTE_STATUS_FAILED")
    http_paste_gets = stats()["subscription_gets"]
    logs_before_http_paste = ui._call("logs").get("text", "")
    ui.profile.write_text("http://example.invalid/not-a-subscription", encoding="utf-8")
    ui.paste_source()
    invalid_paste_logs = wait_for_logs(
        lambda value: "Paste an HTTPS subscription URL with a host" in value
        and value != logs_before_http_paste,
        "native Paste of HTTP text did not show actionable URL validation",
    )
    time.sleep(0.45)
    if stats()["subscription_gets"] != http_paste_gets or stats()["in_flight_gets"] != 0:
        raise NativeUIJourneyError("native Paste of HTTP text started a subscription request")
    after_http_paste = base._snapshot(min(timeout, 30), "NATIVE_HTTP_PASTE_STATUS_FAILED")
    require_active_generation(
        after_http_paste, before_http_paste,
        "invalid native Paste changed or interrupted the active connection",
    )
    if "Paste an HTTPS subscription URL with a host" not in invalid_paste_logs.get("text", ""):
        raise NativeUIJourneyError("native Paste validation record was not readable")
    checks["invalid_http_native_paste_no_fetch"] = True

    ui.profile.write_text(valid_source, encoding="utf-8")
    restore_before_gets = stats()["subscription_gets"]
    ui.type_source(valid_source)
    wait_for_gets(restore_before_gets + 1, "restoring the accepted URL after invalid Paste did not load once")
    restored_paste = wait_for_snapshot(
        lambda value: value.get("source_url") == valid_source,
        "restoring the accepted URL after invalid Paste failed",
    )
    require_active_generation(restored_paste, pasted_connection, "restoring the URL after Paste changed the active generation")

    # A long inventory must remain bounded to its profile viewport so the
    # independent diagnostics pane stays usable on desktop-sized windows.
    previous_inventory = fixture.profile_bytes
    large_inventory = b"\n\n".join(
        (
            b'[[Outline]]\nDescription = "' +
            (b"" if index == 0 else f"Layout profile {index + 1}".encode("utf-8")) +
            b'"\nServer = "127.0.0.1"\nPort = 9\nPassword = "never-connect-layout-' +
            str(index + 1).encode("ascii") + b'"'
        )
        for index in range(24)
    )
    fixture.replace_response(large_inventory)
    layout_source = valid_source + ("&" if "?" in valid_source else "?") + "layout=24-profiles"
    before_layout_gets = stats()["subscription_gets"]
    ui.import_link(layout_source)
    large_layout = wait_for_snapshot(
        lambda value: value.get("source_url") == layout_source,
        "the 24-profile layout fixture did not load",
    )
    wait_for_gets(before_layout_gets + 1, "the 24-profile layout fixture did not complete exactly one request")
    if len(large_layout.get("profiles", [])) != 24:
        raise NativeUIJourneyError("desktop did not load all 24 profiles in source order")
    require_active_generation(
        large_layout, restored_paste,
        "loading a long profile list changed or interrupted the active connection",
    )
    layout_view = ui.snapshot()
    expected_logs_control = "Backend logs" if ui.platform == "windows" else "Connection logs"
    if not {"Profile 1 action", "Profile 2 action", expected_logs_control}.issubset(set(layout_view.get("labels", []))):
        raise NativeUIJourneyError("the long profile list hid the top profile actions or desktop logs pane")
    _exercise_long_profile_list_viewport(ui)

    fixture.replace_response(previous_inventory)
    restore_layout_source = valid_source + ("&" if "?" in valid_source else "?") + "layout=restore"
    before_restore_layout_gets = stats()["subscription_gets"]
    ui.import_link(restore_layout_source)
    restored_layout = wait_for_snapshot(
        lambda value: value.get("source_url") == restore_layout_source,
        "the normal profile inventory did not return after long-list verification",
    )
    wait_for_gets(before_restore_layout_gets + 1, "restoring the normal inventory did not complete exactly one request")
    require_active_generation(
        restored_layout, restored_paste,
        "restoring the normal profile inventory changed or interrupted the active connection",
    )
    if restored_layout.get("digest") != restored_paste.get("digest"):
        raise NativeUIJourneyError("long-list verification did not restore the prior profile inventory")
    checks["long_profile_list_keeps_logs_accessible"] = True

    ui.capture("subscription-controls")
    checks.update({
        "manual_selection_native": True,
        "profile_switch_native": True,
        "failed_load_preserves_tunnel": True,
        "warm_import_native": True,
        "clear_logs_native": True,
    })
    return checks


def run_journey(args: argparse.Namespace) -> dict[str, object]:
    _ensure_directory(args.raw_log_dir)
    runner = SubprocessRunner(
        args.raw_log_dir,
    )
    base = adapter_for_platform(
        args.platform,
        cli=args.cli,
        profile=args.profile,
        runner=runner,
        local_mode=True,
        service_pid=args.service_pid,
        service_binary=args.service_binary,
        service_socket=Path(args.service_socket) if args.service_socket else None,
        service_pipe=args.service_pipe,
        service_library_path=args.service_library_path,
        service_pid_file=args.service_pid_file,
        service_identity_file=args.service_identity_file,
        network_interface=args.network_interface,
        routing_firewall_helper=args.routing_firewall_helper,
    )
    previous_log_directory = os.environ.get("DOBBYVPN_NATIVE_UI_LOG_DIR")
    os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = str(args.raw_log_dir)
    ui: Any | None = None
    subscription = None
    request_timeout = min(args.timeout, _REQUEST_TIMEOUT)
    checks: dict[str, object] = {}
    primary: BaseException | None = None
    try:
        from torturer_runner.subscription_fixture import SubscriptionFixture
        subscription = SubscriptionFixture(args.profile, args.profile.parent / "native-subscription-fixture", args.platform, certificate_helper=args.ui_helper)
        url = subscription.start()
        url_file = subscription.directory / "source.url"
        url_file.write_text(url, encoding="utf-8")
        ui = smoke.NativeUIController(
            args.platform,
            args.ui,
            url_file,
            _smoke_timeout(request_timeout),
            helper=args.ui_helper,
            screenshot_dir=args.raw_log_dir / "screenshots",
            expected_version=getattr(args, "candidate_version", None),
            expected_source_sha=getattr(args, "source_sha", None),
        )
        _start_native_ui(ui, request_timeout)
        configured = _native_ui_action(
            ui, "configure", "native-input", request_timeout,
            ui.configure, milestone="configured",
        )
        if configured.get("input_verified") is not True:
            raise NativeUIJourneyError("native UI did not verify the pasted configuration")
        checks["configure_native"] = True

        # The base adapter prepares its independent observation/proof state;
        # it never substitutes a CLI connect action for the native Connect
        # click.  Routing-enabled desktop adapters use this seam to install
        # their temporary probe firewall before the UI action.
        prepare = getattr(base, "prepare_native_connect", None)
        if not callable(prepare):
            raise NativeUIJourneyError("base adapter has no native connect preparation")
        prepare(args.timeout)
        _native_ui_action(
            ui, "connect", "visible-connect", request_timeout,
            ui.connect, milestone="connected",
        )
        active = base._snapshot(min(30.0, args.timeout), "NATIVE_CONNECT_STATUS_FAILED")
        if active.get("configured") is not True or not active.get("profiles"):
            raise NativeUIJourneyError("native Connect did not establish an accepted configuration")
        if active.get("active_profile") is None:
            raise NativeUIJourneyError("native UI Auto selection reported no active profile")
        checks["connect_native"] = True
        checks.update(_exercise_subscription_controls(ui, base, url, subscription, request_timeout))
        _record_native_observations(base, checks, args.timeout)

        _native_ui_action(
            ui, "disconnect", "visible-disconnect", request_timeout,
            ui.disconnect, milestone="disconnected",
        )
        checks["disconnect_native"] = True
        connected = getattr(base, "_connected", None)
        if not callable(connected) or connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after native Disconnect")
        cleanup_verified = getattr(base, "_cleanup_verified", None)
        if not callable(cleanup_verified) or not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("base adapter could not verify native Disconnect cleanup")
        checks["disconnect_clean"] = True

        # Distinguish inventory reuse from restoring a saved URL after Go has
        # restarted with no accepted inventory. The backend keeps the URL on
        # disk, while the frontend must issue one Configure request on reopen
        # and remain disconnected.
        saved_source = ui.profile.read_text(encoding="utf-8").strip()
        requests_before_restore = subscription.control_stats()["subscription_gets"]
        _native_ui_action(
            ui, "close-window", "close-before-inventory-restore", request_timeout,
            ui.close,
        )
        _restart_service_for_native_ui(base, args.timeout)
        unconfigured = base._snapshot(min(30.0, args.timeout), "NATIVE_RESTORE_EMPTY_SNAPSHOT_FAILED")
        if (
            unconfigured.get("configured") is not False
            or unconfigured.get("profiles")
            or unconfigured.get("source_url") != saved_source
        ):
            raise NativeUIJourneyError(
                "service restart did not leave the saved URL with an empty accepted inventory"
            )
        _native_ui_action(
            ui, "reopen-saved-url-without-inventory", "restore-saved-url", request_timeout,
            ui.start, milestone="restored-saved-url",
        )
        deadline = time.monotonic() + request_timeout
        restored_inventory = {}
        while time.monotonic() < deadline:
            restored_inventory = base._snapshot(
                min(30.0, max(0.1, deadline - time.monotonic())),
                "NATIVE_RESTORE_INVENTORY_STATUS_FAILED",
            )
            if restored_inventory.get("configured") and restored_inventory.get("source_url") == saved_source:
                break
            if restored_inventory.get("state") == "FAILED":
                raise NativeUIJourneyError(
                    "frontend failed to load the saved URL after backend inventory loss"
                )
            time.sleep(0.1)
        if restored_inventory.get("configured") is not True or restored_inventory.get("source_url") != saved_source:
            raise NativeUIJourneyError("reopened frontend did not restore its saved subscription inventory")
        if (
            len(restored_inventory.get("profiles", [])) < 2
            or restored_inventory.get("active_profile") is not None
            or restored_inventory.get("pending_target") is not None
        ):
            raise NativeUIJourneyError("saved inventory restoration changed profiles or started a connection")
        ui._wait(
            lambda: "Profile 1 action" in ui.snapshot()["labels"],
            "reopened frontend did not render the restored saved inventory",
        )
        deadline = time.monotonic() + request_timeout
        while time.monotonic() < deadline:
            stats_after_restore = subscription.control_stats()
            if stats_after_restore["subscription_gets"] > requests_before_restore + 1:
                raise NativeUIJourneyError("saved URL restoration fetched the subscription more than once")
            if (
                stats_after_restore["subscription_gets"] == requests_before_restore + 1
                and stats_after_restore["in_flight_gets"] == 0
            ):
                break
            time.sleep(0.05)
        else:
            raise NativeUIJourneyError("saved URL restoration did not issue exactly one completed request")
        if restored_inventory.get("state") not in {"IDLE", "CONFIGURED"}:
            raise NativeUIJourneyError("loading the saved inventory started a VPN connection")
        checks["inventory_restored_from_saved_url"] = True

        prepare(args.timeout)
        _native_ui_action(
            ui, "reconnect", "visible-reconnect", request_timeout,
            ui.connect, milestone="reconnected",
        )
        checks["reconnect_native"] = True
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe native reconnect")
        checks["reconnect_completed"] = True
        _record_native_observations(base, checks, args.timeout, prefix="reconnect")

        about = _native_ui_action(
            ui, "about", "about-window", request_timeout,
            ui.about,
        )
        checks["about_version"] = about.get("about_version") is True
        checks["about_source_commit"] = about.get("about_source_commit") is True

        # Keep the subsequent cold-link input deterministic after earlier
        # warm-import assertions changed the helper's source file.
        ui.profile.write_text(url, encoding="utf-8")
        before_close = base._snapshot(min(30.0, args.timeout), "NATIVE_REOPEN_STATUS_FAILED")
        requests_before_cold_import = subscription.control_stats()["subscription_gets"]
        _native_ui_action(
            ui, "close-window", "close-window", request_timeout,
            ui.close,
        )
        checks["close_window"] = True
        cold_url = url + "?cold=1"
        _native_ui_action(
            ui, "cold-scheme-open", "cold-scheme-open", request_timeout,
            lambda: ui.cold_deep_link(cold_url), milestone="reopened",
        )
        checks["reopen_connected"] = True
        checks["cold_os_scheme_launch"] = True
        cleared_record = getattr(ui, "cleared_record", None)
        if not isinstance(cleared_record, str) or not cleared_record:
            raise NativeUIJourneyError("the native Clear action did not preserve an identifiable prior log record")
        reopened_logs = ui._call("logs").get("text", "")
        if cleared_record in reopened_logs:
            raise NativeUIJourneyError("Clear restored a prior rendered record after frontend reopen")
        checks["clear_boundary_survives_reopen"] = True
        deadline = time.monotonic() + request_timeout
        while time.monotonic() < deadline:
            reopened = base._snapshot(min(30.0, max(0.1, deadline - time.monotonic())), "NATIVE_IMPORT_STATUS_FAILED")
            if reopened.get("source_url") == cold_url:
                checks["cold_import_native"] = True
                break
            time.sleep(0.1)
        if checks.get("cold_import_native") is not True:
            raise NativeUIJourneyError("Cold import did not automatically load the supplied URL")
        cold_deadline = time.monotonic() + request_timeout
        cold_stats = subscription.control_stats()
        while time.monotonic() < cold_deadline and cold_stats["in_flight_gets"] != 0:
            cold_stats = subscription.control_stats()
            time.sleep(0.05)
        if cold_stats["subscription_gets"] != requests_before_cold_import + 1 or cold_stats["in_flight_gets"] != 0:
            raise NativeUIJourneyError("cold OS deep link did not produce exactly one subscription request")
        if not same_active_generation(reopened, before_close):
            raise NativeUIJourneyError("cold deep-link import changed or interrupted the active connection")
        if not connected(args.timeout):
            raise NativeUIJourneyError("base adapter did not observe service continuity after UI reopen")

        # A second fresh frontend process with no link must reuse the loaded
        # backend inventory and restored URL without fetching it again.
        _native_ui_action(
            ui, "close-window", "close-window-after-import", request_timeout,
            ui.close,
        )
        _native_ui_action(
            ui, "reopen-saved-inventory", "window-reopen-saved-inventory", request_timeout,
            ui.start, milestone="reopened-saved-inventory",
        )
        ui._wait(lambda: "Profile 1 action" in ui.snapshot()["labels"], "reopened frontend did not reuse the loaded profile inventory")
        deadline = time.monotonic() + request_timeout
        reused = {}
        while time.monotonic() < deadline:
            reused = base._snapshot(min(30.0, max(0.1, deadline - time.monotonic())), "NATIVE_REOPEN_INVENTORY_STATUS_FAILED")
            if reused.get("source_url") == cold_url and reused.get("digest") == before_close.get("digest"):
                break
            if reused.get("state") == "FAILED":
                raise NativeUIJourneyError("reopened frontend failed to restore its saved inventory")
            time.sleep(0.1)
        if reused.get("source_url") != cold_url or reused.get("digest") != before_close.get("digest"):
            raise NativeUIJourneyError("reopened frontend did not restore its saved subscription URL")
        time.sleep(1.6)
        reused_stats = subscription.control_stats()
        if reused_stats["subscription_gets"] != requests_before_cold_import + 1:
            raise NativeUIJourneyError("unchanged snapshot polling refetched a reused inventory")
        if not same_active_generation(reused, before_close):
            raise NativeUIJourneyError("reopening the frontend changed the active connection generation")
        checks["inventory_reused_after_reopen"] = True

        loss = _restart_service_for_native_ui(base, args.timeout)
        checks.update(loss)
        prepare(args.timeout)
        recovery = _native_ui_action(
            ui, "process_loss_recovery", "process-loss-recovery", args.timeout,
            ui.recover_after_process_loss, milestone="process-recovered",
        )
        checks["ui_process_loss_recovered"] = recovery.get("status") == "Connected"
        checks["ui_reconnecting_observed"] = recovery.get("reconnecting_seen") is True
        if "reconnecting_probe_error" in recovery:
            checks["ui_reconnecting_probe_error"] = recovery["reconnecting_probe_error"]
        if not connected(args.timeout):
            raise NativeUIJourneyError(
                "base adapter did not observe native UI process-loss recovery"
            )
        _record_native_observations(base, checks, args.timeout, prefix="process_loss")

        _native_ui_action(
            ui, "disconnect", "visible-disconnect", request_timeout,
            ui.disconnect, milestone="disconnected",
        )
        checks["final_disconnect_native"] = True
        if connected(args.timeout):
            raise NativeUIJourneyError("base adapter still reports a tunnel after final native Disconnect")
        if not cleanup_verified(args.timeout):
            raise NativeUIJourneyError("final native Disconnect cleanup was not verified")
        checks["final_cleanup_verified"] = True
        if args.platform == "windows":
            log_path = os.environ.get("DOBBY_LOG_PATH")
            if not log_path:
                raise NativeUIJourneyError("Windows rendered palette test requires the run-scoped DOBBY_LOG_PATH")
            _exercise_windows_rendered_log_palette(
                ui,
                Path(log_path),
                request_timeout,
                checks,
            )
        _require_complete_checks(checks, args.platform)
    except BaseException as error:
        primary = error
        error.native_ui_checks = dict(checks)
        raise
    finally:
        cleanup_errors: list[str] = []
        if ui is not None:
            try:
                with ui.bounded_by(min(request_timeout, 15.0)):
                    ui.close_for_cleanup()
            except BaseException as error:
                cleanup_errors.append(f"native-ui: {_exception_details(error)}")
            try:
                ui.collect_diagnostics()
            except BaseException as error:
                cleanup_errors.append(f"native-ui-diagnostics: {_exception_details(error)}")
        try:
            base.reset(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-reset: {_exception_details(error)}")
        try:
            base.finalize(timeout_seconds=min(args.timeout, 30.0))
        except BaseException as error:
            cleanup_errors.append(f"base-finalize: {_exception_details(error)}")
        if subscription is not None:
            try:
                subscription.close()
            except BaseException as error:
                cleanup_errors.append(f"subscription-fixture: {_exception_details(error)}")
        if previous_log_directory is None:
            os.environ.pop("DOBBYVPN_NATIVE_UI_LOG_DIR", None)
        else:
            os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = previous_log_directory
        if cleanup_errors and primary is not None:
            for value in cleanup_errors:
                primary.add_note(value)
        elif cleanup_errors:
            failure = NativeUIJourneyError(
                "native journey cleanup failed: " + "; ".join(cleanup_errors)
            )
            failure.native_ui_checks = dict(checks)
            raise failure
    return {"platform": args.platform, "checks": checks, "complete": True}


def _exercise_auto_recovery_stop(ui: Any, base: Any, marker: Path, timeout: float) -> dict[str, object]:
    """Arm the tagged fault only after a rendered Auto connection carries traffic."""
    if marker.exists():
        raise NativeUIJourneyError("recovery Stop marker exists before real traffic verification")
    configured = ui.configure()
    if configured.get("input_verified") is not True:
        raise NativeUIJourneyError("recovery Stop case did not load through native Paste")
    base.prepare_native_connect(timeout)
    ui.connect()
    active = base._snapshot(min(timeout, 30), "NATIVE_RECOVERY_INITIAL_SNAPSHOT_FAILED")
    if active.get("state") != "CONNECTED" or active.get("active_mode") != "AUTO_SELECT":
        raise NativeUIJourneyError("recovery Stop case did not establish native Auto")
    checks: dict[str, object] = {}
    _record_native_observations(base, checks, timeout)
    if any(checks.get(name) is not True for name in (
        "tunnel_interface", "routing_verified", "stability_verified", "throughput_positive",
    )):
        raise NativeUIJourneyError("recovery fault cannot arm without verified real route and traffic")
    ui.capture("auto-recovery-connected")
    marker.write_text("armed after real route and traffic\n", encoding="utf-8")
    deadline = time.monotonic() + timeout
    recovering: dict[str, object] = {}
    while time.monotonic() < deadline:
        recovering = base._snapshot(min(30.0, max(0.1, deadline - time.monotonic())), "NATIVE_RECOVERY_SNAPSHOT_FAILED")
        if recovering.get("recovering") is True and recovering.get("generation", 0) > active["generation"]:
            break
        if recovering.get("state") == "FAILED":
            raise NativeUIJourneyError("Auto failed before recovery Stop could be exercised")
        time.sleep(0.1)
    else:
        raise NativeUIJourneyError("tagged health fault did not enter a subsequent recovery generation")

    def stop_is_rendered() -> bool:
        view = ui.snapshot()
        enabled = set(view.get("enabled_controls", []))
        if "Stop" not in view.get("labels", []) or "Stop" not in enabled:
            return False
        competing = [name for name in enabled if name.startswith("Profile ") and name.endswith(" action")]
        if competing:
            raise NativeUIJourneyError("recovery Stop exposed competing profile Connect actions: " + ", ".join(competing))
        return True

    ui._wait(stop_is_rendered, "Auto recovery did not render an enabled Stop control")
    ui.capture("auto-recovery-stop")
    if ui.platform == "macos":
        try:
            ui.connection_action_details()
        except Exception as error:
            print(
                "Read-only connection-action-details inspection failed before the unchanged Stop click: "
                + _exception_details(error),
                file=sys.stderr,
                flush=True,
            )
    ui._click("Stop" if ui.platform == "macos" else "VPN connection action")
    deadline = time.monotonic() + timeout
    stopped: dict[str, object] = {}
    while time.monotonic() < deadline:
        stopped = base._snapshot(min(30.0, max(0.1, deadline - time.monotonic())), "NATIVE_RECOVERY_STOP_SNAPSHOT_FAILED")
        if (stopped.get("state") in {"IDLE", "CONFIGURED"}
                and stopped.get("cleanup_complete") is True
                and stopped.get("recovering") is False
                and stopped.get("pending_target") is None
                and stopped.get("active_profile") is None):
            break
        time.sleep(0.1)
    else:
        raise NativeUIJourneyError("rendered Stop did not cancel recovery and finish cleanup")
    ui.wait_status("Disconnected")
    cleaned = _base_execute(base, "recovery-stop-cleanup", "inspect_cleanup", timeout)
    if cleaned.get("cleanup_verified") is not True:
        raise NativeUIJourneyError("recovery Stop left tunnel or routing resources")
    stable_deadline = time.monotonic() + 3.0
    while time.monotonic() < stable_deadline:
        current = base._snapshot(min(timeout, 30), "NATIVE_RECOVERY_STOP_STABLE_SNAPSHOT_FAILED")
        if (current.get("generation") != stopped.get("generation")
                or current.get("recovering") is not False
                or current.get("pending_target") is not None
                or current.get("active_profile") is not None
                or current.get("state") not in {"IDLE", "CONFIGURED"}
                or current.get("cleanup_complete") is not True):
            raise NativeUIJourneyError("a connection generation resumed after rendered recovery Stop")
        time.sleep(0.1)
    ui.capture("auto-recovery-stopped")
    return {
        **checks, "initial_mode": "AUTO_SELECT", "initial_generation": active["generation"],
        "recovery_generation": recovering["generation"], "stopped_generation": stopped["generation"],
        "rendered_stop": True, "competing_connect_disabled": True,
        "cleanup_verified": True, "pending_cleared": True, "no_later_generation": True,
        "build_local_test_seams": True,
    }


def run_native_cases(args: argparse.Namespace) -> dict[str, object]:
    """Run only selected diagnostic UI cases within the interactive app setup."""
    try:
        selected = validate_native_cases(args.platform, "full", args.native_cases)
    except ValueError as error:
        raise NativeUIJourneyError(str(error)) from error
    if selected not in {
        (WINDOWS_CONFIGURE_TREE_CASE,),
        (WINDOWS_CONFIGURE_TREE_NO_UIA_CASE,),
        (MACOS_CONFIGURE_STARTUP_CASE,),
        (AUTO_RECOVERY_STOP_CASE,),
    }:
        raise NativeUIJourneyError("unsupported desktop native case selection")

    _ensure_directory(args.raw_log_dir)
    previous_log_directory = os.environ.get("DOBBYVPN_NATIVE_UI_LOG_DIR")
    os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = str(args.raw_log_dir)
    ui: smoke.NativeUIController | None = None
    subscription = None
    base: Any | None = None
    primary: BaseException | None = None
    try:
        profile = args.profile
        if selected in {(MACOS_CONFIGURE_STARTUP_CASE,), (AUTO_RECOVERY_STOP_CASE,)}:
            from torturer_runner.subscription_fixture import SubscriptionFixture

            subscription = SubscriptionFixture(
                args.profile,
                args.profile.parent / "native-subscription-fixture",
                args.platform,
                certificate_helper=args.ui_helper,
            )
            url = subscription.start()
            profile = subscription.directory / "source.url"
            profile.write_text(url, encoding="utf-8")
            base = adapter_for_platform(
                args.platform,
                cli=args.cli,
                profile=args.profile,
                runner=SubprocessRunner(args.raw_log_dir),
                local_mode=True,
                service_pid=args.service_pid,
                service_binary=args.service_binary,
                service_socket=Path(args.service_socket) if args.service_socket else None,
                service_pipe=args.service_pipe,
                service_library_path=args.service_library_path,
                service_pid_file=args.service_pid_file,
                service_identity_file=args.service_identity_file,
                network_interface=args.network_interface,
                routing_firewall_helper=args.routing_firewall_helper,
            )
        ui = smoke.NativeUIController(
            args.platform,
            args.ui,
            profile,
            _smoke_timeout(args.timeout),
            helper=args.ui_helper,
            screenshot_dir=args.raw_log_dir / "screenshots",
            expected_version=getattr(args, "candidate_version", None),
            expected_source_sha=getattr(args, "source_sha", None),
        )
        if args.platform == "windows":
            ui.enable_windows_crash_diagnostics()
        with ui.bounded_by(_smoke_timeout(args.timeout)):
            if selected == (WINDOWS_CONFIGURE_TREE_NO_UIA_CASE,):
                startup = ui.start(windows_no_uia_hold_seconds=20)
            else:
                startup = ui.start(
                    windows_content_root_diagnostics=selected == (WINDOWS_CONFIGURE_TREE_CASE,),
                )
        content_root_probe: dict[str, object] | None = None
        post_probe_process: dict[str, object] | None = None
        external_point_probe: dict[str, object] | None = None
        if selected == (WINDOWS_CONFIGURE_TREE_CASE,):
            diagnostics = ui.windows_content_root_diagnostics
            if diagnostics is None:
                raise NativeUIJourneyError(
                    "Windows configure-tree did not retain its XAML content-root diagnostics"
                )
            if any(key in diagnostics for key in (
                "xaml_content_root_exception", "external_uia_point_exception", "post_probe_process_exception",
            )):
                details = json.dumps(diagnostics, sort_keys=True, separators=(",", ":"))
                raise NativeUIJourneyError(
                    "Windows XAML content-root diagnostic raised or lost its post-probe process check: "
                    + details
                )
            content_root_probe = diagnostics.get("xaml_content_root_peers")
            post_probe_process = diagnostics.get("post_probe_process")
            if not _valid_windows_content_root_probe(content_root_probe):
                details = json.dumps(diagnostics, sort_keys=True, separators=(",", ":"))
                raise NativeUIJourneyError(
                    "Windows XAML content-root direct editor inspection did not complete: "
                    + details
                )
            if not isinstance(post_probe_process, dict) or post_probe_process.get("alive") is not True:
                details = json.dumps(diagnostics, sort_keys=True, separators=(",", ":"))
                raise NativeUIJourneyError(
                    "Windows content-root inspection left the app process unavailable: " + details
                )
            external_point_probe = diagnostics.get("external_uia_point")
            if (not isinstance(external_point_probe, dict)
                    or external_point_probe.get("schema") != "dobbyvpn.windows-uia-point/v1"
                    or external_point_probe.get("fromPointCompleted") is not True
                    or external_point_probe.get("ready") is not True
                    or external_point_probe.get("matchesExpected", {}).get("all") is not True
                    or external_point_probe.get("processAliveAfterQuery") is not True):
                raise NativeUIJourneyError(
                    "Windows point query did not find the verified native SourceEditor: "
                    + json.dumps(diagnostics, sort_keys=True, separators=(",", ":"))
                )
        if selected == (AUTO_RECOVERY_STOP_CASE,):
            if base is None:
                raise NativeUIJourneyError("Auto recovery case has no independent platform adapter")
            case_result = _exercise_auto_recovery_stop(
                ui, base, args.raw_log_dir / "recovery-stop.arm", min(args.timeout, _REQUEST_TIMEOUT),
            )
        elif selected == (MACOS_CONFIGURE_STARTUP_CASE,):
            configured = ui.configure()
            if configured.get("input_verified") is not True:
                raise NativeUIJourneyError("macOS rendered Configure did not verify pasted input")
            if subscription is None:
                raise NativeUIJourneyError("macOS Configure case has no disposable subscription fixture")
            fixture_stats = subscription.control_stats()
            if (
                fixture_stats.get("subscription_gets") != 1
                or fixture_stats.get("in_flight_gets") != 0
                or fixture_stats.get("max_in_flight_gets") != 1
            ):
                raise NativeUIJourneyError(
                    "macOS rendered Configure did not complete exactly one fixture request"
                )
            if base is None or subscription is None:
                raise NativeUIJourneyError("macOS Configure case has no service adapter or disposable fixture")

            saved_source = profile.read_text(encoding="utf-8").strip()
            configured_snapshot = base._snapshot(
                min(30.0, args.timeout), "NATIVE_CASE_CONFIGURED_SNAPSHOT_FAILED"
            )

            def require_disconnected_without_generation(
                snapshot: dict[str, object], context: str
            ) -> None:
                if (
                    snapshot.get("state") not in {"IDLE", "CONFIGURED"}
                    or type(snapshot.get("generation")) is not int
                    or snapshot.get("generation") != 0
                    or snapshot.get("active_profile") is not None
                    or snapshot.get("pending_target") is not None
                    or snapshot.get("cleanup_complete") is not True
                ):
                    raise NativeUIJourneyError(
                        f"{context} did not remain disconnected without a connection generation"
                    )

            configured_profiles = configured_snapshot.get("profiles")
            if (
                configured_snapshot.get("configured") is not True
                or configured_snapshot.get("source_url") != saved_source
                or not isinstance(configured_profiles, list)
                or len(configured_profiles) < 2
            ):
                raise NativeUIJourneyError(
                    "macOS rendered Configure did not create the accepted disposable inventory"
                )
            require_disconnected_without_generation(
                configured_snapshot, "initial saved URL configuration"
            )

            requests_before_restore = fixture_stats["subscription_gets"]
            ui.close()
            restart = _restart_service_for_native_ui(base, args.timeout)
            if restart.get("process_loss_verified") is not True:
                raise NativeUIJourneyError(
                    "macOS service-only restart was not verified"
                )
            unconfigured = base._snapshot(
                min(30.0, args.timeout), "NATIVE_CASE_RESTORE_EMPTY_SNAPSHOT_FAILED"
            )
            if (
                unconfigured.get("configured") is not False
                or unconfigured.get("profiles")
                or unconfigured.get("source_url") != saved_source
            ):
                raise NativeUIJourneyError(
                    "service restart did not leave the saved URL with an empty accepted inventory"
                )
            require_disconnected_without_generation(
                unconfigured, "service restart with an empty accepted inventory"
            )

            # Relaunch without passing a URL or pressing Paste. The frontend
            # must restore the accepted source URL from the restarted service.
            with ui.bounded_by(_smoke_timeout(args.timeout)):
                ui.start()
            deadline = time.monotonic() + args.timeout
            restored_inventory: dict[str, object] = {}
            expected_requests = requests_before_restore + 1
            while time.monotonic() < deadline:
                restored_inventory = base._snapshot(
                    min(30.0, max(0.1, deadline - time.monotonic())),
                    "NATIVE_CASE_RESTORE_INVENTORY_STATUS_FAILED",
                )
                restore_stats = subscription.control_stats()
                if restore_stats.get("subscription_gets", 0) > expected_requests:
                    raise NativeUIJourneyError(
                        "saved URL restoration fetched the subscription more than once"
                    )
                if (
                    restored_inventory.get("configured") is True
                    and restored_inventory.get("source_url") == saved_source
                    and restore_stats.get("subscription_gets") == expected_requests
                    and restore_stats.get("in_flight_gets") == 0
                ):
                    break
                if restored_inventory.get("state") == "FAILED":
                    raise NativeUIJourneyError(
                        "frontend failed to load the saved URL after backend inventory loss"
                    )
                time.sleep(0.05)
            else:
                raise NativeUIJourneyError(
                    "reopened frontend did not restore its saved inventory with exactly one request"
                )
            restored_profiles = restored_inventory.get("profiles", [])
            if not isinstance(restored_profiles, list) or len(restored_profiles) < 2:
                raise NativeUIJourneyError(
                    "saved URL restoration did not return the disposable profiles"
                )
            require_disconnected_without_generation(
                restored_inventory, "saved URL restoration"
            )
            expected_rows = {
                f"Profile {profile_value.get('index', offset) + 1} action"
                for offset, profile_value in enumerate(restored_profiles)
                if isinstance(profile_value, dict)
            }
            if len(expected_rows) != len(restored_profiles):
                raise NativeUIJourneyError(
                    "saved URL restoration returned an invalid profile inventory"
                )
            ui._wait(
                lambda: expected_rows.issubset(set(ui.snapshot().get("labels", []))),
                "reopened frontend did not render the restored saved inventory",
            )

            # Keep both UI Snapshot polling and independent backend Snapshot
            # reads active long enough to detect an accidental refetch loop.
            polling_deadline = time.monotonic() + 1.6
            while time.monotonic() < polling_deadline:
                polled = base._snapshot(
                    min(30.0, max(0.1, polling_deadline - time.monotonic())),
                    "NATIVE_CASE_RESTORE_POLL_SNAPSHOT_FAILED",
                )
                poll_stats = subscription.control_stats()
                if (
                    polled.get("configured") is not True
                    or polled.get("source_url") != saved_source
                    or poll_stats.get("subscription_gets") != expected_requests
                    or poll_stats.get("in_flight_gets") != 0
                ):
                    raise NativeUIJourneyError(
                        "Snapshot polling changed the restored inventory or fetched it again"
                    )
                require_disconnected_without_generation(
                    polled, "Snapshot polling after saved URL restoration"
                )
                time.sleep(0.1)
            fixture_stats = subscription.control_stats()
            if (
                fixture_stats.get("subscription_gets") != expected_requests
                or fixture_stats.get("in_flight_gets") != 0
                or fixture_stats.get("max_in_flight_gets") != 1
            ):
                raise NativeUIJourneyError(
                    "Snapshot polling caused an extra or overlapping saved URL request"
                )
            case_result = {
                "startup_tree": startup,
                "configure": configured,
                "saved_url_service_restart": {
                    "service_restart": restart,
                    "empty_inventory_before_reopen": True,
                    "restored_automatically_without_paste": True,
                    "rendered_profiles": True,
                    "remained_disconnected_without_generation": True,
                    "snapshot_polling_did_not_refetch": True,
                },
                "fixture_requests": fixture_stats,
            }
        elif selected == (WINDOWS_CONFIGURE_TREE_NO_UIA_CASE,):
            case_result = {"windows_no_uia_hold": startup}
        else:
            case_result = {
                "configure_tree": {
                    "diagnostic_only": True,
                    "rendered_controls_queried": True,
                    "connection_actions_exercised": False,
                    "xaml_content_root_peers": content_root_probe,
                    "external_uia_point": external_point_probe,
                    "post_probe_process": post_probe_process,
                }
            }
            if ui.windows_content_root_diagnostics is not None:
                case_result["windows_content_root_diagnostics"] = ui.windows_content_root_diagnostics
        return {
            "platform": args.platform,
            "native_cases": list(selected),
            "coverage": {
                "platform": args.platform,
                "suite": "full",
                "kind": "native-cases",
                "native_cases": list(selected),
                "native_case_selection": "explicit",
            },
            "checks": {selected[0]: case_result},
            "complete": True,
        }
    except BaseException as error:
        primary = error
        raise
    finally:
        cleanup_errors: list[str] = []
        if ui is not None:
            try:
                with ui.bounded_by(min(args.timeout, 15.0)):
                    ui.close_for_cleanup()
            except BaseException as error:
                cleanup_errors.append(f"native-ui: {_exception_details(error)}")
            try:
                ui.collect_diagnostics()
            except BaseException as error:
                cleanup_errors.append(f"native-ui-diagnostics: {_exception_details(error)}")
            try:
                ui.restore_windows_crash_diagnostics()
            except BaseException as error:
                cleanup_errors.append(f"native-ui-wer-registry-cleanup: {_exception_details(error)}")
        if base is not None:
            try:
                base.reset(timeout_seconds=min(args.timeout, 30.0))
            except BaseException as error:
                cleanup_errors.append(f"base-reset: {_exception_details(error)}")
            try:
                base.finalize(timeout_seconds=min(args.timeout, 30.0))
            except BaseException as error:
                cleanup_errors.append(f"base-finalize: {_exception_details(error)}")
        if subscription is not None:
            try:
                subscription.close()
            except BaseException as error:
                cleanup_errors.append(f"subscription-fixture: {_exception_details(error)}")
        if previous_log_directory is None:
            os.environ.pop("DOBBYVPN_NATIVE_UI_LOG_DIR", None)
        else:
            os.environ["DOBBYVPN_NATIVE_UI_LOG_DIR"] = previous_log_directory
        if cleanup_errors and primary is not None:
            for value in cleanup_errors:
                primary.add_note(value)
        elif cleanup_errors:
            raise NativeUIJourneyError(
                "native case cleanup failed: " + "; ".join(cleanup_errors)
            )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("windows", "macos"), required=True)
    parser.add_argument("--cli", type=_path, required=True)
    parser.add_argument("--ui", type=_path, required=True)
    parser.add_argument("--ui-helper", type=_path, required=True)
    parser.add_argument("--profile", type=_path, required=True)
    parser.add_argument("--raw-log-dir", type=_path, required=True)
    parser.add_argument("--output", type=_path)
    parser.add_argument("--timeout", type=_timeout, default=900.0)
    parser.add_argument("--candidate-version")
    parser.add_argument("--source-sha")
    parser.add_argument("--native-case", action="append", dest="native_cases")
    parser.add_argument("--service-pid", type=int, required=True)
    parser.add_argument("--service-binary", type=_path, required=True)
    parser.add_argument("--service-socket", type=str)
    parser.add_argument("--service-pipe")
    parser.add_argument("--service-library-path", type=_path)
    parser.add_argument("--service-pid-file", type=_path)
    parser.add_argument("--service-identity-file", type=_path)
    parser.add_argument("--network-interface")
    parser.add_argument("--routing-firewall-helper", type=_path)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.native_cases is not None:
        try:
            validate_native_cases(args.platform, "full", args.native_cases)
        except ValueError as error:
            parser.error(str(error))
    if (args.platform == "windows" and (args.service_pipe != "DobbyVPN.Control" or args.service_socket)) or (
        args.platform == "macos" and (not args.service_socket or args.service_pipe)
    ):
        raise SystemExit("native UI journey requires the platform's configured local control endpoint")
    if (
        not args.cli.is_file()
        or not _ui_path_is_launchable(args.platform, args.ui)
        or not args.profile.is_file()
        or not args.ui_helper.is_file()
    ):
        raise SystemExit("native UI journey requires a CLI, launchable UI, prepared helper, and profile")
    try:
        result = run_native_cases(args) if args.native_cases is not None else run_journey(args)
    except Exception as error:
        rendered_error = _exception_details(error)
        if args.output is not None:
            operation = getattr(error, "operation", None)
            stage = getattr(error, "stage", None)
            failure = {
                "suite": "native-case" if args.native_cases is not None else "full",
                "action_driver": "native-window",
                "platform": args.platform,
                "complete": False,
                "error": rendered_error,
                "notes": list(getattr(error, "__notes__", ())),
            }
            if args.native_cases is not None:
                failure["native_cases"] = args.native_cases
                failure["coverage"] = {
                    "platform": args.platform,
                    "suite": "full",
                    "kind": "native-cases",
                    "native_cases": args.native_cases,
                    "native_case_selection": "explicit",
                }
            checks = getattr(error, "native_ui_checks", None)
            if isinstance(checks, dict):
                failure["checks"] = checks
            if isinstance(operation, str) and operation:
                failure["operation"] = operation
            if isinstance(stage, str) and stage:
                failure["stage"] = stage
            try:
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps(failure, sort_keys=True) + "\n", encoding="utf-8")
            except OSError as output_error:
                error.add_note(
                    "native_ui_failure_result_write_error="
                    f"{type(output_error).__name__}: {output_error}"
                )
        print(f"native-ui-journey failed: {rendered_error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(
                "native-ui-journey note: " + note,
                file=sys.stderr,
            )
        return 1
    result = {
        "suite": "native-case" if args.native_cases is not None else "full",
        "action_driver": "native-window",
        **result,
    }
    if args.output is not None:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps(result, sort_keys=True, separators=(",", ":")), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
