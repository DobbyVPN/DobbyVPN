from __future__ import annotations

import tempfile
import unittest
from contextlib import nullcontext
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from torturer_runner import subscription_fixture
from torturer_runner.ui import journey

WINDOWS_NATIVE_UI_HELPER = Path(__file__).resolve().parents[1] / "native_ui" / "windows" / "Program.cs"
MACOS_NATIVE_UI_HELPER = Path(__file__).resolve().parents[1] / "native_ui" / "macos.swift"


class NativeUICaseFixtureTests(unittest.TestCase):
    class FakeClock:
        def __init__(self):
            self.now = 0.0

        def monotonic(self):
            return self.now

        def sleep(self, seconds):
            self.now += seconds

    def recovery_fakes(self, marker, *, platform="macos", traffic_ok=True,
                       competing_connect=False, later_generation=False):
        events = []
        clock = self.FakeClock()

        class UI:
            def __init__(self):
                self.base = None
                self.platform = platform

            def configure(self):
                events.append("configure")
                return {"input_verified": True}

            def connect(self):
                events.append("connect")

            def capture(self, name):
                events.append(f"capture:{name}")

            def connection_action_details(self):
                events.append("connection-action-details")
                return {"ready": True, "matches": []}

            def snapshot(self):
                events.append("ui-snapshot")
                enabled = ["Stop"]
                if competing_connect:
                    enabled.append("Profile 1 action")
                return {"labels": ["Stop"], "enabled_controls": enabled}

            def _wait(self, predicate, message):
                if not predicate():
                    raise AssertionError(message)

            def _click(self, name):
                events.append(f"click:{name}")
                self.base.stop_requested = True

            def wait_status(self, expected):
                events.append(f"status:{expected}")
                return {"status": expected}

        class Base:
            def __init__(self):
                self.snapshot_count = 0
                self.stop_requested = False
                self.stopped_snapshots = 0
                self.stop_generation = 8
                self.cleanup_verified = True

            def prepare_native_connect(self, _timeout):
                events.append("prepare-connect")

            def _snapshot(self, _timeout, _failure):
                self.snapshot_count += 1
                if self.snapshot_count == 1:
                    events.append("snapshot-connected")
                    return {
                        "state": "CONNECTED", "active_mode": "AUTO_SELECT",
                        "generation": 7,
                    }
                if not self.stop_requested:
                    events.append("snapshot-recovering")
                    return {
                        "state": "CONNECTED", "recovering": True,
                        "generation": 8,
                    }
                self.stopped_snapshots += 1
                events.append("snapshot-stopped")
                generation = self.stop_generation
                if later_generation and self.stopped_snapshots >= 3:
                    generation += 1
                return {
                    "state": "IDLE", "recovering": False,
                    "generation": generation, "cleanup_complete": True,
                    "pending_target": None, "active_profile": None,
                }

            def execute(self, step):
                events.append(step.operation)
                if step.operation == "observe_tunnel":
                    return {"tunnel_interface": True}
                if step.operation == "observe_routing_identity":
                    return {"routing_verified": True}
                if step.operation == "measure_stability":
                    return {"stability_verified": True}
                if step.operation == "measure_throughput":
                    return {
                        "latency_ms": 1 if traffic_ok else 0,
                        "download_mbps": 2 if traffic_ok else 0,
                        "upload_mbps": 3 if traffic_ok else 0,
                    }
                if step.operation == "inspect_cleanup":
                    return {"cleanup_verified": self.cleanup_verified}
                raise AssertionError(f"unexpected base operation: {step.operation}")

        ui = UI()
        base = Base()
        ui.base = base
        return ui, base, events, clock

    def test_auto_recovery_stop_arms_after_route_and_traffic_then_stays_idle(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, events, clock = self.recovery_fakes(marker)
            original_write_text = Path.write_text

            def record_marker_write(path, data, *args, **kwargs):
                if path == marker:
                    events.append("marker")
                return original_write_text(path, data, *args, **kwargs)

            with (
                patch.object(journey, "time", clock),
                patch.object(Path, "write_text", new=record_marker_write),
            ):
                result = journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

            marker_index = events.index("marker")
            for operation in (
                "observe_tunnel", "observe_routing_identity", "measure_stability",
                "measure_throughput",
            ):
                self.assertLess(events.index(operation), marker_index)
            self.assertLess(marker_index, events.index("snapshot-recovering"))
            self.assertEqual(marker.read_text(encoding="utf-8"), "armed after real route and traffic\n")
            self.assertTrue(result["rendered_stop"])
            self.assertTrue(result["competing_connect_disabled"])
            self.assertTrue(result["cleanup_verified"])
            self.assertTrue(result["pending_cleared"])
            self.assertTrue(result["no_later_generation"])
            self.assertEqual(result["initial_generation"], 7)
            self.assertEqual(result["recovery_generation"], 8)
            self.assertEqual(result["stopped_generation"], 8)
            self.assertEqual(base.stopped_snapshots, 31)
            self.assertEqual(events.count("inspect_cleanup"), 1)
            self.assertIn("click:VPN connection action", events)
            self.assertEqual(events.count("connection-action-details"), 1)
            self.assertLess(events.index("connection-action-details"), events.index("click:VPN connection action"))
            self.assertNotIn("click:Stop", events)

    def test_auto_recovery_stop_keeps_windows_action_identifier(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, events, clock = self.recovery_fakes(marker, platform="windows")
            with patch.object(journey, "time", clock):
                journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

            self.assertIn("click:VPN connection action", events)
            self.assertNotIn("click:Stop", events)
            self.assertNotIn("connection-action-details", events)

    def test_connection_action_details_wrapper_is_read_only_operation(self):
        controller = journey.smoke.NativeUIController.__new__(journey.smoke.NativeUIController)
        response = {"ready": True, "match_count": 1}
        with patch.object(controller, "_call", return_value=response) as call:
            self.assertIs(controller.connection_action_details(), response)
        call.assert_called_once_with("connection-action-details")

    def windows_cancel_switch_response(self):
        target, competing = "Profile 2 action", "Profile 1 action"
        def state(automation_id, name, enabled):
            return {
                "found": True, "automation_id": automation_id, "name": name,
                "control_type": "ControlType.Button", "is_control_element": True,
                "enabled": enabled, "offscreen": False,
            }
        return {
            "ready": True,
            "target_automation_id": target,
            "competing_automation_id": competing,
            "connect_invoked_at_utc": "2026-10-08T12:00:00.0000000+00:00",
            "stop_observed_at_utc": "2026-10-08T12:00:00.2500000+00:00",
            "stop_invoked_at_utc": "2026-10-08T12:00:00.2600000+00:00",
            "target_at_stop": state(target, "Stop", True),
            "competing_at_stop": state(competing, "Connect", False),
            "connection_action_at_stop": {
                "found": True, "automation_id": "VPN connection action", "name": "Auto connect",
                "control_type": "ControlType.Button", "is_control_element": True,
                "enabled": False, "offscreen": False,
            },
        }

    def test_windows_cancel_switch_wrapper_accepts_only_observed_stop_and_disabled_connects(self):
        controller = journey.smoke.NativeUIController.__new__(journey.smoke.NativeUIController)
        controller.platform = "windows"
        response = self.windows_cancel_switch_response()
        with patch.object(controller, "_call", return_value=response) as call:
            self.assertIs(controller.cancel_profile_switch(1, 0), response)
        call.assert_called_once_with(
            "cancel-profile-switch", target="Profile 2 action", competing="Profile 1 action",
        )

        for allowed_name in ("Stop", "Disconnect"):
            with self.subTest(allowed_main_action=allowed_name):
                allowed = self.windows_cancel_switch_response()
                allowed["connection_action_at_stop"].update(name=allowed_name, enabled=True)
                with patch.object(controller, "_call", return_value=allowed):
                    self.assertIs(controller.cancel_profile_switch(1, 0), allowed)

        for mutation in (
            lambda value: value["target_at_stop"].update(name="Disconnect"),
            lambda value: value["competing_at_stop"].update(enabled=True),
            lambda value: value["connection_action_at_stop"].update(enabled=True),
            lambda value: value["connection_action_at_stop"].update(found=False),
            lambda value: value["connection_action_at_stop"].update(automation_id="Connection controls"),
            lambda value: value["connection_action_at_stop"].update(control_type="ControlType.Text"),
            lambda value: value["connection_action_at_stop"].update(is_control_element=False),
            lambda value: value["connection_action_at_stop"].update(name=""),
            lambda value: value["connection_action_at_stop"].update(name="Connecting"),
            lambda value: value["connection_action_at_stop"].update(enabled=None),
            lambda value: value["connection_action_at_stop"].update(enabled=0),
            lambda value: value["connection_action_at_stop"].update(offscreen=None),
            lambda value: value["connection_action_at_stop"].update(offscreen=True),
            lambda value: value.update(stop_invoked_at_utc="2026-10-08T11:59:59+00:00"),
        ):
            with self.subTest(mutation=mutation), patch.object(controller, "_call") as call:
                invalid = self.windows_cancel_switch_response()
                mutation(invalid)
                call.return_value = invalid
                with self.assertRaises(journey.smoke.NativeUISmokeError):
                    controller.cancel_profile_switch(1, 0)

    def test_windows_switch_import_dispatches_only_after_fresh_stop_observation(self):
        controller = journey.smoke.NativeUIController.__new__(journey.smoke.NativeUIController)
        controller.platform = "windows"
        controller.pid = 42
        controller.identity = "candidate-ui-instance"
        url = "https://127.0.0.1:49152/subscription?import-during-connect=1"
        uri = "dobbyvpn://import?url=https%3A%2F%2F127.0.0.1%3A49152%2Fsubscription%3Fimport-during-connect%3D1"

        def dispatch_response():
            response = self.windows_cancel_switch_response()
            response.pop("stop_invoked_at_utc")
            response.update({
                "pid": 42,
                "identity": "candidate-ui-instance",
                "window_handle": "0x100",
                "protocol_uri": uri,
                "protocol_dispatch_started_at_utc": "2026-10-08T12:00:00.2600000+00:00",
                "protocol_dispatch_returned_at_utc": "2026-10-08T12:00:00.3100000+00:00",
                "shell_execute_result": 33,
            })
            return response

        response = dispatch_response()
        with patch.object(controller, "_call", return_value=response) as call:
            self.assertIs(controller.switch_profile_and_dispatch_import(1, 0, url), response)
        call.assert_called_once_with(
            "profile-switch-import",
            target="Profile 2 action",
            competing="Profile 1 action",
            uri=uri,
        )

        mutations = (
            lambda value: value["target_at_stop"].update(name="Disconnect"),
            lambda value: value["competing_at_stop"].update(enabled=True),
            lambda value: value.update(protocol_uri="dobbyvpn://"),
            lambda value: value.update(window_handle="not-a-window"),
            lambda value: value.update(shell_execute_result=32),
            lambda value: value.update(protocol_dispatch_started_at_utc="2026-10-08T11:59:59+00:00"),
        )
        for mutation in mutations:
            with self.subTest(mutation=mutation), patch.object(controller, "_call") as call:
                invalid = dispatch_response()
                mutation(invalid)
                call.return_value = invalid
                with self.assertRaises(journey.smoke.NativeUISmokeError):
                    controller.switch_profile_and_dispatch_import(1, 0, url)

    def test_snapshot_preserves_readback_of_native_subscription_editor(self):
        controller = journey.smoke.NativeUIController.__new__(journey.smoke.NativeUIController)
        controller.platform = "macos"
        controller.window_id = None
        controller.reconnecting_seen = False
        response = {
            "ready": True,
            "window_count": 1,
            "window_id": "window-1",
            "labels": ["Connected"],
            "enabled_controls": [],
            "source_text": "https://example.invalid/latest",
        }
        with patch.object(controller, "_call", return_value=response) as call:
            snapshot = controller.snapshot()
        call.assert_called_once_with("tree")
        self.assertEqual(snapshot["source_text"], "https://example.invalid/latest")

    def test_macos_connection_action_requires_exact_enabled_native_press(self):
        source = MACOS_NATIVE_UI_HELPER.read_text(encoding="utf-8")
        press_start = source.index("func pressConnectionAction(")
        press_end = source.index("\nfunc key(", press_start)
        press_operation = source[press_start:press_end]
        self.assertIn("try find(nodes, expectedIdentifier)", press_operation)
        self.assertIn("actualIdentifier == expectedIdentifier", press_operation)
        self.assertIn("kAXEnabledAttribute) as? Bool", press_operation)
        self.assertIn("AXUIElementCopyActionNames(element, &rawActionNames)", press_operation)
        self.assertIn("AXActionNames", press_operation)
        self.assertIn("actionNames.contains(kAXPressAction as String)", press_operation)
        self.assertIn("try press(element)", press_operation)

        click_start = source.index('case "click":')
        click_end = source.index('case "type":', click_start)
        click_operation = source[click_start:click_end]
        self.assertIn('if target == "VPN connection action"', click_operation)
        self.assertIn("try pressConnectionAction(nodes, identifier: target)", click_operation)
        self.assertIn('label($0, kAXRoleAttribute) == kAXButtonRole', click_operation)
        self.assertIn("try press(find(buttons, target))", click_operation)

    def test_windows_text_size_settings_wrapper_keeps_inspection_read_only_and_apply_guarded(self):
        controller = journey.smoke.NativeUIController.__new__(journey.smoke.NativeUIController)
        controller.platform = "windows"
        response = {"ready": False, "available": False}
        with patch.object(controller, "_call", return_value=response) as call:
            self.assertIs(controller.inspect_windows_text_size_settings(), response)
        call.assert_called_once_with(
            "settings-text-size",
            unbound=True,
            action="inspect",
            uri=journey.smoke._WINDOWS_TEXT_SIZE_SETTINGS_URI,
        )

        with patch.object(controller, "_call", return_value=response) as call:
            self.assertIs(controller.apply_windows_text_size(150, 100), response)
        call.assert_called_once_with(
            "settings-text-size",
            unbound=True,
            action="apply",
            uri=journey.smoke._WINDOWS_TEXT_SIZE_SETTINGS_URI,
            target=150,
            expectedCurrent=100,
        )

        with patch.object(controller, "_call") as call:
            for target, current in ((0, 100), (150, 0), (150.0, 100), (150, True)):
                with self.subTest(target=target, current=current):
                    with self.assertRaises(ValueError):
                        controller.apply_windows_text_size(target, current)
            call.assert_not_called()

        controller.platform = "macos"
        with patch.object(controller, "_call") as call:
            with self.assertRaisesRegex(ValueError, "only available on Windows"):
                controller.inspect_windows_text_size_settings()
        call.assert_not_called()

    def test_windows_text_size_dispatch_rejects_mutation_and_scopes_before_app_resolution(self):
        source = WINDOWS_NATIVE_UI_HELPER.read_text(encoding="utf-8")
        dispatch = source.index('if (operation == "settings-text-size")')
        product_resolution = source.index('var expected = Path.GetFullPath(Text("executable"));')
        validation = source.index("private static void ValidateSettingsTextSizeRequest")
        snapshot_start = source.index("private static Dictionary<string, object?> CaptureSettingsWindowAfterClose(")
        inspection = source.index("private static int OperateWindowsTextSizeSettings")
        inspection_end = source.index("private static string DescribeElement", inspection)

        self.assertLess(dispatch, product_resolution)
        self.assertLess(validation, inspection)
        self.assertIn('if (action == "inspect")', source[validation:inspection])
        self.assertIn('if (action != "apply")', source[validation:inspection])
        self.assertIn('request.GetProperty("uri").GetString() != WindowsTextSizeSettingsUri', source[validation:inspection])
        self.assertIn('request.TryGetProperty("target", out _) || request.TryGetProperty("expectedCurrent", out _)', source[validation:inspection])

        operation = source[inspection:inspection_end]
        self.assertIn('Process.Start(new ProcessStartInfo(WindowsTextSizeSettingsUri)', operation)
        ownership_phase = operation.index("settings-text-size-new-window-owned")
        uia_query = operation.index("var slider = FindSettingsTextSizeControl(")
        self.assertLess(ownership_phase, uia_query)
        self.assertIn("WindowsTextSizeSliderAutomationId", operation)
        self.assertIn("RangeValuePattern.Pattern", operation)
        self.assertIn("WindowsTextSizeApplyAutomationId", operation)
        self.assertIn('if (action == "apply")', operation)
        self.assertNotIn(".SetValue(", operation)
        self.assertNotIn(".Invoke()", operation)
        self.assertNotIn("Walk(", operation)
        self.assertIn("windowsBefore.Contains(window)", operation)
        self.assertIn("owner.StartTime.ToUniversalTime().Ticks != ownerStart", operation)
        self.assertIn("else if (activationAttempted && window == IntPtr.Zero)", operation)
        self.assertIn('response["newSettingsWindowClosed"] = false', operation)
        self.assertIn("cleanup of a potentially new Settings window cannot be verified", operation)
        self.assertIn('response["error"] = primaryError', operation)
        self.assertIn('response["cleanupErrors"] = cleanupErrors', operation)

        apply_start = source.index("private static void ApplyWindowsTextSize(")
        apply_end = source.index("private static int OperateWindowsTextSizeSettings", apply_start)
        apply_operation = source[apply_start:apply_end]
        for assertion in (
            "WindowsTextSizeApplyAutomationId",
            "expectedCurrent",
            "before.IsReadOnly",
            "before.Value",
            "before.Minimum",
            "before.Maximum",
            "range.SetValue(target)",
            "((InvokePattern)invokePattern).Invoke()",
            "WaitFor(",
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, apply_operation)
        self.assertIn("!currentApply.Current.IsEnabled", apply_operation)

        close_start = operation.index("if (window != IntPtr.Zero && !windowsBefore.Contains(window))")
        close_cleanup = operation[close_start:operation.index(
            "else if (activationAttempted && window == IntPtr.Zero)", close_start)]
        self.assertTrue(all(fragment in close_cleanup for fragment in (
            '"New Settings window did not close"', "cleanupErrors.Add(error.ToString())", "finally",
            'response["postCloseWindowSnapshot"] = CaptureSettingsWindowAfterClose(',
        )))

        snapshot = source[snapshot_start:inspection]
        self.assertTrue(all(fragment in snapshot for fragment in (
            'snapshot["window"] = windowContext', 'windowContext["isWindowVisible"]',
            "GetAncestor(window, GaRootOwner)", 'snapshot["rootOwner"]', "DescribeWindowContext(",
        )))

        smoke_source = Path(journey.smoke.__file__).read_text(encoding="utf-8")
        operation_limit = smoke_source.index("operation_limit = 30.0")
        operation_timeout = smoke_source.index("operation_timeout = min", operation_limit)
        self.assertIn('"settings-text-size"', smoke_source[operation_limit:operation_timeout])

    def test_auto_recovery_stop_does_not_arm_when_throughput_is_not_positive(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, events, clock = self.recovery_fakes(marker, traffic_ok=False)
            with patch.object(journey, "time", clock):
                with self.assertRaisesRegex(
                    journey.NativeUIJourneyError,
                    "cannot arm without verified real route and traffic",
                ):
                    journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

            self.assertFalse(marker.exists())
            self.assertNotIn("snapshot-recovering", events)
            self.assertNotIn("click:VPN connection action", events)
            self.assertNotIn("inspect_cleanup", events)

    def test_auto_recovery_stop_rejects_competing_profile_connect(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, events, clock = self.recovery_fakes(marker, competing_connect=True)
            with patch.object(journey, "time", clock):
                with self.assertRaisesRegex(
                    journey.NativeUIJourneyError,
                    "exposed competing profile Connect actions",
                ):
                    journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

            self.assertTrue(marker.exists())
            self.assertNotIn("click:VPN connection action", events)
            self.assertNotIn("inspect_cleanup", events)

    def test_auto_recovery_stop_rejects_a_later_connection_generation(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, _events, clock = self.recovery_fakes(marker, later_generation=True)
            with patch.object(journey, "time", clock):
                with self.assertRaisesRegex(
                    journey.NativeUIJourneyError,
                    "connection generation resumed after rendered recovery Stop",
                ):
                    journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

    @staticmethod
    def rendered_profile_rows():
        return [
            {
                "index": index,
                "name": "Profile 1" if index == 1 else f"Layout profile {index}",
                "protocol": "OUTLINE",
                "action": "Connect",
            }
            for index in range(1, 25)
        ]

    @staticmethod
    def profile_layout(scroll_position, visible_actions, row_indexes=None):
        all_rows = NativeUICaseFixtureTests.rendered_profile_rows()
        rows = all_rows if row_indexes is None else [all_rows[index - 1] for index in row_indexes]
        return {
            "ready": True,
            "window": {"x": 0, "y": 0, "width": 1000, "height": 800},
            "controls": {"x": 10, "y": 10, "width": 470, "height": 400},
            "profile_viewport": {"x": 20, "y": 120, "width": 440, "height": 180},
            "connection_action": {"x": 20, "y": 20, "width": 440, "height": 80},
            "logs": {"x": 500, "y": 10, "width": 480, "height": 760},
            "scroll_position": scroll_position,
            "profile_rows": rows,
            "visible_profile_actions": visible_actions,
        }

    def windows_text_size_fakes(
        self,
        *,
        apply_enabled=False,
        layout_fails=False,
        external_change_before_restore=False,
        apply_mode="normal",
    ):
        events = []
        state = {"value": 100.0, "apply_enabled": apply_enabled, "inspections": 0}

        def response(*, ready=True, apply_invoked=None, readback=None):
            result = {
                "ready": ready,
                "available": ready,
                "slider": {
                    "enabled": True,
                    "offscreen": False,
                    "range": {
                        "value": state["value"], "minimum": 100.0, "maximum": 225.0,
                        "isReadOnly": False,
                    },
                },
                "apply": {"enabled": state["apply_enabled"], "offscreen": False},
            }
            if apply_invoked is not None:
                result["applyInvoked"] = apply_invoked
            if readback is not None:
                result["setValueReadback"] = readback
            return result

        class UI:
            platform = "windows"

            def bounded_by(self, _timeout):
                return nullcontext()

            def inspect_windows_text_size_settings(self):
                state["inspections"] += 1
                events.append("inspect")
                if external_change_before_restore and state["inspections"] == 2:
                    state["value"] = 125.0
                    state["apply_enabled"] = False
                return response()

            def apply_windows_text_size(self, target, expected_current):
                events.append(f"apply:{target}:expected:{expected_current}")
                if abs(state["value"] - expected_current) > 0.01:
                    return response(ready=False, apply_invoked=False)
                if target == 150 and apply_mode == "partial-no-change":
                    return response(ready=False, apply_invoked=False)
                if target == 150 and apply_mode == "partial-set-value":
                    state["value"] = 150.0
                    state["apply_enabled"] = True
                    return response(ready=False, apply_invoked=False, readback=150)
                state["value"] = float(target)
                state["apply_enabled"] = False
                return response(apply_invoked=True, readback=target)

            def close(self):
                events.append("close")
                return {"closed": True}

            def close_for_cleanup(self):
                events.append("close-for-cleanup")

            def start(self):
                events.append("start")
                return {"started": True}

            def capture(self, name):
                return {"path": f"{name}.png"}

            def snapshot(self):
                return {"labels": ["Profile 1 action", "Profile 2 action", "Backend logs"]}

            def profile_list_layout(self):
                events.append("layout")
                if layout_fails:
                    raise journey.NativeUIJourneyError("150% profile layout failed")
                return NativeUICaseFixtureTests.profile_layout(0, ["Profile 1 action"])

            def scroll_profile_list(self, position):
                scroll = 0 if position == "top" else 100 if position == "bottom" else float(position)
                actions = ["Profile 1 action"] if position == "top" else (
                    ["Profile 24 action"] if position == "bottom" else []
                )
                return NativeUICaseFixtureTests.profile_layout(scroll, actions)

            def _call(self, operation):
                if operation == "logs":
                    return {"ready": True, "text": "INFO · fixture log"}
                if operation == "log-position":
                    return {"visible_first_record": "INFO · fixture log"}
                raise AssertionError(f"unexpected operation: {operation}")

        class Base:
            def _snapshot(self, _timeout, _failure):
                return {
                    "configured": True,
                    "profiles": [object()] * 24,
                    "state": "CONNECTED",
                    "generation": 7,
                    "active_digest": "profile-digest",
                    "active_mode": "AUTO_SELECT",
                    "active_index": 0,
                }

        return UI(), Base(), state, events

    def run_windows_text_size_journey(self, ui, base):
        journey._exercise_windows_text_size_layout(
            ui,
            base,
            3.0,
            {
                "state": "CONNECTED", "generation": 7,
                "active_digest": "profile-digest", "active_mode": "AUTO_SELECT", "active_index": 0,
            },
            {},
        )

    def test_windows_text_size_does_not_commit_a_dirty_preexisting_apply(self):
        ui, base, state, events = self.windows_text_size_fakes(apply_enabled=True)

        with self.assertRaisesRegex(journey.NativeUIJourneyError, "Apply was already enabled"):
            self.run_windows_text_size_journey(ui, base)

        self.assertEqual(state["inspections"], 1)
        self.assertEqual(state["value"], 100.0)
        self.assertTrue(state["apply_enabled"])
        self.assertEqual(events, ["inspect"])

    def test_windows_text_size_restores_original_after_layout_failure(self):
        ui, base, state, events = self.windows_text_size_fakes(layout_fails=True)

        with self.assertRaisesRegex(journey.NativeUIJourneyError, "150% profile layout failed"):
            self.run_windows_text_size_journey(ui, base)

        self.assertEqual(state["value"], 100.0)
        self.assertFalse(state["apply_enabled"])
        self.assertEqual(
            [event for event in events if event.startswith("apply:")],
            ["apply:150:expected:100", "apply:100:expected:150"],
        )
        self.assertIn("close-for-cleanup", events)
        self.assertEqual(events.count("start"), 2)

    def test_windows_text_size_partial_setter_failure_restores_or_accepts_noop(self):
        for mode, expected_apply_calls, expected_value in (
            ("partial-no-change", ["apply:150:expected:100"], 100.0),
            ("partial-set-value", ["apply:150:expected:100", "apply:100:expected:150"], 100.0),
        ):
            with self.subTest(mode=mode):
                ui, base, state, events = self.windows_text_size_fakes(apply_mode=mode)
                with self.assertRaises(journey.NativeUIJourneyError):
                    self.run_windows_text_size_journey(ui, base)
                self.assertEqual(state["value"], expected_value)
                self.assertFalse(state["apply_enabled"])
                self.assertEqual([event for event in events if event.startswith("apply:")], expected_apply_calls)

    def test_windows_text_size_refuses_to_overwrite_a_changed_current_value(self):
        ui, base, state, events = self.windows_text_size_fakes(external_change_before_restore=True)

        with self.assertRaisesRegex(journey.NativeUIJourneyError, "could not restore its original setting") as caught:
            self.run_windows_text_size_journey(ui, base)

        self.assertEqual(state["value"], 125.0)
        self.assertEqual([event for event in events if event.startswith("apply:")], ["apply:150:expected:100"])
        self.assertTrue(any("refusing to overwrite it" in note for note in caught.exception.__notes__))

    def test_desktop_profile_metadata_accumulates_realized_rows_in_source_order(self):
        rows = self.rendered_profile_rows()
        accumulated = {}
        journey._accumulate_rendered_profile_rows(rows[:3], accumulated)
        journey._accumulate_rendered_profile_rows(rows[2:6], accumulated)
        journey._accumulate_rendered_profile_rows(rows[5:9], accumulated)
        journey._accumulate_rendered_profile_rows(rows[8:12], accumulated)
        journey._accumulate_rendered_profile_rows(rows[11:15], accumulated)
        journey._accumulate_rendered_profile_rows(rows[14:18], accumulated)
        journey._accumulate_rendered_profile_rows(rows[17:21], accumulated)
        journey._accumulate_rendered_profile_rows(rows[20:], accumulated)
        journey._assert_complete_rendered_profile_rows(accumulated)

        missing = {}
        journey._accumulate_rendered_profile_rows(rows[:3], missing)
        journey._accumulate_rendered_profile_rows(rows[3:6], missing)
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "did not realize all 24"):
            journey._assert_complete_rendered_profile_rows(missing)

        reordered = list(rows[1:3]) + list(rows[:1])
        wrong_name = [dict(rows[0]), dict(rows[1])]
        wrong_name[0]["name"] = "Layout profile 1"
        wrong_protocol = [dict(rows[7])]
        wrong_protocol[0]["protocol"] = "XRAY"
        wrong_action = [dict(rows[23])]
        wrong_action[0]["action"] = "Stop"

        for evidence, message in (
            (reordered, "out of source order"),
            (wrong_name, "had name="),
            (wrong_protocol, "had protocol="),
            (wrong_action, "had action="),
        ):
            with self.subTest(message=message):
                with self.assertRaisesRegex(journey.NativeUIJourneyError, message):
                    journey._assert_rendered_profile_rows(evidence)

        changed = {3: {"index": 3, "name": "Layout profile 3", "protocol": "OUTLINE", "action": "Stop"}}
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "changed metadata"):
            journey._accumulate_rendered_profile_rows(rows[2:3], changed)
        out_of_order_union = {}
        journey._accumulate_rendered_profile_rows(rows[4:6], out_of_order_union)
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "after a later source row"):
            journey._accumulate_rendered_profile_rows(rows[2:4], out_of_order_union)

    def test_long_profile_list_reaches_twenty_fourth_action_and_restores_top(self):
        events = []
        visible_rows = {
            "top": [1, 2, 3],
            "10": [3, 4, 5],
            "20": [5, 6, 7],
            "30": [7, 8, 9],
            "40": [9, 10, 11],
            "50": [11, 12, 13],
            "60": [13, 14, 15],
            "70": [15, 16, 17],
            "80": [17, 18, 19],
            "90": [19, 20, 21],
            "bottom": [22, 23, 24],
        }

        class UI:
            platform = "macos"

            def scroll_profile_list(self, position):
                events.append(f"scroll:{position}")
                scroll = 0 if position == "top" else 100 if position == "bottom" else float(position)
                actions = ["Profile 1 action"] if position == "top" else (
                    ["Profile 24 action"] if position == "bottom" else []
                )
                return NativeUICaseFixtureTests.profile_layout(scroll, actions, visible_rows[position])

            def _call(self, operation):
                events.append(operation)
                if operation == "logs":
                    return {"ready": True, "text": "INFO · desktop fixture log"}
                if operation == "log-position":
                    return {"visible_range_start": 0, "visible_range_end": 20}
                raise AssertionError(f"unexpected UI operation: {operation}")

        journey._exercise_long_profile_list_viewport(UI())
        self.assertEqual(
            events,
            ["scroll:top", "scroll:10", "scroll:20", "scroll:30", "scroll:40", "scroll:50",
             "scroll:60", "scroll:70", "scroll:80", "scroll:90", "scroll:bottom", "logs",
             "log-position", "scroll:top"],
        )
        self.assertFalse(any(event.startswith("click:") for event in events))

    def test_long_profile_list_restores_top_if_bottom_profile_is_not_reachable(self):
        events = []
        visible_rows = {
            "top": [1, 2, 3],
            "10": [3, 4, 5],
            "20": [5, 6, 7],
            "30": [7, 8, 9],
            "40": [9, 10, 11],
            "50": [11, 12, 13],
            "60": [13, 14, 15],
            "70": [15, 16, 17],
            "80": [17, 18, 19],
            "90": [19, 20, 21],
            "bottom": [22, 23, 24],
        }

        class UI:
            platform = "windows"

            def scroll_profile_list(self, position):
                events.append(f"scroll:{position}")
                if position == "bottom":
                    actions = ["Profile 1 action"]
                elif position == "top":
                    actions = ["Profile 1 action"]
                else:
                    actions = []
                scroll = 0 if position == "top" else 100 if position == "bottom" else float(position)
                return NativeUICaseFixtureTests.profile_layout(scroll, actions, visible_rows[position])

            def _call(self, operation):
                events.append(operation)
                raise AssertionError("logs should not be queried after a failed reachability assertion")

        with self.assertRaisesRegex(journey.NativeUIJourneyError, "Profile 24 action was not reachable"):
            journey._exercise_long_profile_list_viewport(UI())
        self.assertEqual(
            events,
            ["scroll:top", "scroll:10", "scroll:20", "scroll:30", "scroll:40", "scroll:50",
             "scroll:60", "scroll:70", "scroll:80", "scroll:90", "scroll:bottom", "scroll:top"],
        )

    def test_long_profile_list_preserves_failure_when_top_restoration_also_fails(self):
        top_calls = 0
        visible_rows = {
            "top": [1, 2, 3],
            "10": [3, 4, 5],
            "20": [5, 6, 7],
            "30": [7, 8, 9],
            "40": [9, 10, 11],
            "50": [11, 12, 13],
            "60": [13, 14, 15],
            "70": [15, 16, 17],
            "80": [17, 18, 19],
            "90": [19, 20, 21],
            "bottom": [22, 23, 24],
        }

        class UI:
            platform = "windows"

            def scroll_profile_list(self, position):
                nonlocal top_calls
                if position == "top":
                    top_calls += 1
                    if top_calls > 1:
                        raise journey.NativeUIJourneyError("top restoration failed")
                    return NativeUICaseFixtureTests.profile_layout(0, ["Profile 1 action"], visible_rows[position])
                if position not in {"top", "bottom"}:
                    return NativeUICaseFixtureTests.profile_layout(
                        float(position), [], visible_rows[position]
                    )
                if position == "bottom":
                    return NativeUICaseFixtureTests.profile_layout(100, ["Profile 1 action"], visible_rows[position])
                raise AssertionError("unexpected scroll position")

            def _call(self, _operation):
                raise AssertionError("logs should not be queried after a failed reachability assertion")

        with self.assertRaises(BaseExceptionGroup) as caught:
            journey._exercise_long_profile_list_viewport(UI())
        self.assertEqual(len(caught.exception.exceptions), 2)
        self.assertIn("Profile 24 action was not reachable", str(caught.exception.exceptions[0]))
        self.assertIn("top restoration failed", str(caught.exception.exceptions[1]))

    def test_long_profile_layout_rejects_a_logs_pane_that_is_too_small(self):
        layout = self.profile_layout(100, ["Profile 24 action"])
        layout["logs"] = {"x": 500, "y": 10, "width": 120, "height": 100}
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "logs pane was too small"):
            journey._assert_profile_list_layout(layout, edge="bottom", visible_action="Profile 24 action")

    def test_long_profile_layout_rejects_viewport_overlapping_the_main_action(self):
        layout = self.profile_layout(100, ["Profile 24 action"])
        layout["profile_viewport"] = {"x": 20, "y": 80, "width": 440, "height": 180}
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "overlapped the main action"):
            journey._assert_profile_list_layout(layout, edge="bottom", visible_action="Profile 24 action")

    def test_macos_configure_case_uses_disposable_https_fixture(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "owner-profile"
            profile.write_bytes(b"fixture subscription profile bytes")
            raw_logs = root / "logs"
            captured: dict[str, object] = {}

            class Fixture:
                def __init__(self, source, directory, platform, *, certificate_helper):
                    captured["fixture_arguments"] = (source, directory, platform, certificate_helper)
                    self.directory = directory
                    self.stats = {
                        "subscription_gets": 0,
                        "in_flight_gets": 0,
                        "max_in_flight_gets": 1,
                    }
                    captured["fixture"] = self

                def start(self):
                    self.directory.mkdir(parents=True)
                    return "https://127.0.0.1:49152/subscription"

                def control_stats(self):
                    return dict(self.stats)

                def close(self):
                    captured["fixture_closed"] = True

            class UI:
                def __init__(self, platform, binary, source, timeout, *, helper, screenshot_dir, **_kwargs):
                    captured["ui_arguments"] = (platform, source)
                    self.profile = Path(source)
                    self.launches = 0
                    self.profiles_rendered = False
                    self.operations = []

                def bounded_by(self, _timeout):
                    return nullcontext()

                def start(
                    self,
                    *,
                    windows_content_root_diagnostics=False,
                ):
                    self.windows_content_root_diagnostics = windows_content_root_diagnostics
                    self.launches += 1
                    self.operations.append("start-tree")
                    if self.launches == 2:
                        fixture = captured["fixture"]
                        fixture.stats["subscription_gets"] += 1
                        self.profiles_rendered = True
                    return {"labels": ["Connection configuration"]}

                def configure(self):
                    fixture = captured["fixture"]
                    fixture.stats["subscription_gets"] += 1
                    self.profiles_rendered = True
                    self.operations.append("rendered-configure")
                    return {"input_verified": True}

                def close(self):
                    self.operations.append("close-before-service-restart")

                def snapshot(self):
                    labels = ["Connection configuration"]
                    if self.profiles_rendered:
                        labels.extend(("Profile 1 action", "Profile 2 action"))
                    return {"labels": labels}

                def _wait(self, predicate, message):
                    if not predicate():
                        raise AssertionError(message)

                def close_for_cleanup(self):
                    self.operations.append("close-for-cleanup")
                    captured["ui_closed"] = True

                def collect_diagnostics(self):
                    captured["diagnostics_collected"] = True

                def restore_windows_crash_diagnostics(self):
                    captured["windows_crash_diagnostics_restored"] = True

            class Base:
                def __init__(self):
                    self.restarted = False
                    self.actions = []

                @staticmethod
                def _configured_snapshot():
                    return {
                        "configured": True,
                        "profiles": [{"index": 0}, {"index": 1}],
                        "source_url": "https://127.0.0.1:49152/subscription",
                        "state": "CONFIGURED",
                        "generation": 0,
                        "active_profile": None,
                        "pending_target": None,
                        "cleanup_complete": True,
                    }

                def _snapshot(self, _timeout, _failure):
                    self.actions.append("snapshot")
                    if not self.restarted or captured.get("reopened"):
                        return self._configured_snapshot()
                    return {
                        "configured": False,
                        "profiles": [],
                        "source_url": "https://127.0.0.1:49152/subscription",
                        "state": "IDLE",
                        "generation": 0,
                        "active_profile": None,
                        "pending_target": None,
                        "cleanup_complete": True,
                    }

                def restart_service_for_native_ui(self, _timeout):
                    self.actions.append("restart-service-only")
                    self.restarted = True
                    return {"process_loss_verified": True}

                def reset(self, *, timeout_seconds):
                    self.actions.append(("reset", timeout_seconds))

                def finalize(self, *, timeout_seconds):
                    self.actions.append(("finalize", timeout_seconds))

            base = Base()
            native_ui = None

            def ui_factory(*args, **kwargs):
                nonlocal native_ui
                native_ui = UI(*args, **kwargs)
                original_start = native_ui.start

                def start(**kwargs):
                    result = original_start(**kwargs)
                    if native_ui.launches == 2:
                        captured["reopened"] = True
                    return result

                native_ui.start = start
                return native_ui

            args = SimpleNamespace(
                platform="macos",
                ui=Path("DobbyVPN.app"),
                ui_helper=Path("native-helper"),
                profile=profile,
                cli=Path("dobbyvpn"),
                raw_log_dir=raw_logs,
                timeout=30.0,
                service_pid=42,
                service_binary=Path("dobbyvpn-service"),
                service_socket="/var/run/dobbyvpn/control.sock",
                service_pipe=None,
                service_library_path=None,
                service_pid_file=None,
                service_identity_file=None,
                network_interface=None,
                routing_firewall_helper=None,
            )
            with (
                patch.object(subscription_fixture, "SubscriptionFixture", Fixture),
                patch.object(journey.smoke, "NativeUIController", side_effect=ui_factory),
                patch.object(journey, "adapter_for_platform", return_value=base),
            ):
                result = journey.run_native_cases(
                    SimpleNamespace(**vars(args), native_cases=["configure-startup"])
                )

            fixture_args = captured["fixture_arguments"]
            self.assertEqual(fixture_args[0], profile)
            self.assertEqual(fixture_args[2], "macos")
            self.assertEqual(fixture_args[3], Path("native-helper"))
            ui_arguments = captured["ui_arguments"]
            self.assertEqual(ui_arguments[0], "macos")
            self.assertEqual(
                Path(ui_arguments[1]).read_text(encoding="utf-8"),
                "https://127.0.0.1:49152/subscription",
            )
            case = result["checks"]["configure-startup"]
            self.assertEqual(case["fixture_requests"]["subscription_gets"], 2)
            self.assertEqual(case["fixture_requests"]["in_flight_gets"], 0)
            self.assertTrue(case["saved_url_service_restart"]["empty_inventory_before_reopen"])
            self.assertTrue(case["saved_url_service_restart"]["restored_automatically_without_paste"])
            self.assertTrue(case["saved_url_service_restart"]["rendered_profiles"])
            self.assertTrue(case["saved_url_service_restart"]["remained_disconnected_without_generation"])
            self.assertTrue(case["saved_url_service_restart"]["snapshot_polling_did_not_refetch"])
            self.assertEqual(native_ui.launches, 2)
            self.assertEqual(native_ui.operations[:3], [
                "start-tree", "rendered-configure", "close-before-service-restart",
            ])
            self.assertEqual(native_ui.operations.count("rendered-configure"), 1)
            self.assertEqual(base.actions.count("restart-service-only"), 1)
            self.assertNotIn("connect", base.actions)
            self.assertIn("reset", [action[0] for action in base.actions if isinstance(action, tuple)])
            self.assertIn("finalize", [action[0] for action in base.actions if isinstance(action, tuple)])
            self.assertTrue(captured["fixture_closed"])
            self.assertTrue(captured["ui_closed"])
            self.assertTrue(captured["diagnostics_collected"])
            self.assertTrue(captured["windows_crash_diagnostics_restored"])
            self.assertEqual(result["coverage"]["native_cases"], ["configure-startup"])


if __name__ == "__main__":
    unittest.main()
