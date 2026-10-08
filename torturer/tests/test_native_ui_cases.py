from __future__ import annotations

import tempfile
import unittest
from contextlib import nullcontext
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from torturer_runner import subscription_fixture
from torturer_runner.ui import journey


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
            self.assertIn("click:Stop", events)
            self.assertNotIn("click:VPN connection action", events)

    def test_auto_recovery_stop_keeps_windows_action_identifier(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "recovery-stop.arm"
            ui, base, events, clock = self.recovery_fakes(marker, platform="windows")
            with patch.object(journey, "time", clock):
                journey._exercise_auto_recovery_stop(ui, base, marker, 1.0)

            self.assertIn("click:VPN connection action", events)
            self.assertNotIn("click:Stop", events)

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
    def profile_layout(scroll_position, visible_actions):
        return {
            "ready": True,
            "window": {"x": 0, "y": 0, "width": 1000, "height": 800},
            "controls": {"x": 10, "y": 10, "width": 470, "height": 400},
            "profile_viewport": {"x": 20, "y": 120, "width": 440, "height": 180},
            "connection_action": {"x": 20, "y": 20, "width": 440, "height": 80},
            "logs": {"x": 500, "y": 10, "width": 480, "height": 760},
            "scroll_position": scroll_position,
            "visible_profile_actions": visible_actions,
        }

    def test_long_profile_list_reaches_twenty_fourth_action_and_restores_top(self):
        events = []

        class UI:
            platform = "macos"

            def scroll_profile_list(self, position):
                events.append(f"scroll:{position}")
                if position == "bottom":
                    return NativeUICaseFixtureTests.profile_layout(100, ["Profile 24 action"])
                return NativeUICaseFixtureTests.profile_layout(0, ["Profile 1 action", "Profile 2 action"])

            def _call(self, operation):
                events.append(operation)
                if operation == "logs":
                    return {"ready": True, "text": "INFO · desktop fixture log"}
                if operation == "log-position":
                    return {"visible_range_start": 0, "visible_range_end": 20}
                raise AssertionError(f"unexpected UI operation: {operation}")

        journey._exercise_long_profile_list_viewport(UI())
        self.assertEqual(events, ["scroll:bottom", "logs", "log-position", "scroll:top"])
        self.assertFalse(any(event.startswith("click:") for event in events))

    def test_long_profile_list_restores_top_if_bottom_profile_is_not_reachable(self):
        events = []

        class UI:
            platform = "windows"

            def scroll_profile_list(self, position):
                events.append(f"scroll:{position}")
                actions = ["Profile 1 action"] if position == "bottom" else ["Profile 1 action"]
                return NativeUICaseFixtureTests.profile_layout(100 if position == "bottom" else 0, actions)

            def _call(self, operation):
                events.append(operation)
                raise AssertionError("logs should not be queried after a failed reachability assertion")

        with self.assertRaisesRegex(journey.NativeUIJourneyError, "Profile 24 action was not reachable"):
            journey._exercise_long_profile_list_viewport(UI())
        self.assertEqual(events, ["scroll:bottom", "scroll:top"])

    def test_long_profile_list_preserves_failure_when_top_restoration_also_fails(self):
        class UI:
            platform = "windows"

            def scroll_profile_list(self, position):
                if position == "bottom":
                    return NativeUICaseFixtureTests.profile_layout(100, ["Profile 1 action"])
                raise journey.NativeUIJourneyError("top restoration failed")

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
