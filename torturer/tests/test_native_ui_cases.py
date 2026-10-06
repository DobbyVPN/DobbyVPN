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

                def start(self, *, windows_uia_diagnostics=False):
                    self.windows_uia_diagnostics = windows_uia_diagnostics
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
