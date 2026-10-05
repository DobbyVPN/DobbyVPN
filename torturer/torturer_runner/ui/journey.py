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
import json
import math
import os
from pathlib import Path
import sys
import threading
import time
import traceback
from typing import Any

from torturer_contract.scenarios import ScenarioStep

from ..adapters.cli import SubprocessRunner, _ensure_directory
from ..adapters.factory import adapter_for_platform
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
    "clear_resumes_following",
    "inventory_reused_after_reopen",
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


def _require_complete_checks(checks: dict[str, object]) -> None:
    """Fail closed when an evidence-producing step returned false/missing data."""
    failed = sorted(key for key in _REQUIRED_TRUE_CHECKS if checks.get(key) is not True)
    if failed:
        raise NativeUIJourneyError(
            "required native UI checks did not pass: " + ", ".join(failed)
        )


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
    if ui.platform == "windows":
        paste_invoked_at = getattr(ui, "last_paste_invoked_at_unix_ms", None)
        get_started_at = initial_stats["last_subscription_get_started_at_unix_ms"]
        if type(paste_invoked_at) is not int or get_started_at < paste_invoked_at:
            raise NativeUIJourneyError("Windows Paste request timing was unavailable or preceded the button invocation")
        paste_delay_ms = get_started_at - paste_invoked_at
        if paste_delay_ms >= 400:
            raise NativeUIJourneyError(
                f"Windows Paste waited for the typed debounce before requesting the subscription ({paste_delay_ms} ms)"
            )
    if "Retry" in ui.snapshot().get("labels", []):
        raise NativeUIJourneyError("Retry appeared before any subscription failure")
    checks: dict[str, bool] = {"native_paste_immediate": True}

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

    def switch_profile(previous: dict, index: int, competing_index: int) -> dict:
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
    switched = switch_profile(manual, second, first)
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

    ui.connect()
    selected(switched, None)

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
        _require_complete_checks(checks)
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
    args = build_parser().parse_args(argv)
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
        result = run_journey(args)
    except Exception as error:
        rendered_error = _exception_details(error)
        if args.output is not None:
            operation = getattr(error, "operation", None)
            stage = getattr(error, "stage", None)
            failure = {
                "suite": "full",
                "action_driver": "native-window",
                "platform": args.platform,
                "complete": False,
                "error": rendered_error,
                "notes": list(getattr(error, "__notes__", ())),
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
        "suite": "full",
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
