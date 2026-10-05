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

                def start(self):
                    self.directory.mkdir(parents=True)
                    self.stats["subscription_gets"] = 1
                    return "https://127.0.0.1:49152/subscription"

                def control_stats(self):
                    return dict(self.stats)

                def close(self):
                    captured["fixture_closed"] = True

            class UI:
                def __init__(self, platform, binary, source, timeout, *, helper, screenshot_dir, native_cases):
                    captured["ui_arguments"] = (platform, source, native_cases)
                    self.native_case_results = {}

                def bounded_by(self, _timeout):
                    return nullcontext()

                def start(self):
                    return {"labels": ["Connection configuration"]}

                def configure(self):
                    return {"input_verified": True}

                def close_for_cleanup(self):
                    captured["ui_closed"] = True

                def collect_diagnostics(self):
                    captured["diagnostics_collected"] = True

            args = SimpleNamespace(
                platform="macos",
                ui=Path("DobbyVPN.app"),
                ui_helper=Path("native-helper"),
                profile=profile,
                raw_log_dir=raw_logs,
                timeout=30.0,
            )
            with (
                patch.object(subscription_fixture, "SubscriptionFixture", Fixture),
                patch.object(journey.smoke, "NativeUIController", UI),
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
            self.assertEqual(ui_arguments[2], ("configure-startup",))
            self.assertEqual(
                Path(ui_arguments[1]).read_text(encoding="utf-8"),
                "https://127.0.0.1:49152/subscription",
            )
            self.assertTrue(captured["fixture_closed"])
            self.assertTrue(captured["ui_closed"])
            self.assertTrue(captured["diagnostics_collected"])
            self.assertEqual(result["coverage"]["native_cases"], ["configure-startup"])


if __name__ == "__main__":
    unittest.main()
