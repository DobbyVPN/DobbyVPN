"""Source-level contracts for the disposable iOS rendered subscription lane."""

from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

from torturer_runner import ios_simulator, ios_simulator_app, local_vm_ios
from torturer_runner.native_cases import IOS_SUBSCRIPTION_FIXTURE_CASE
from torturer_runner import subscription_fixture


_UDID = "01234567-89ab-cdef-0123-456789abcdef"


class IOSSubscriptionFixtureRunnerTests(unittest.TestCase):
    def test_default_and_selected_fixture_test_receive_run_owned_fixture(self) -> None:
        fixture = SimpleNamespace(
            url="https://127.0.0.1:49123/subscription",
        )
        environment = ios_simulator_app._subscription_fixture_test_environment(fixture)
        self.assertEqual(
            environment,
            {
                "DOBBY_IOS_TEST_SUBSCRIPTION_URL": fixture.url,
                "DOBBY_IOS_TEST_FIXTURE_REQUIRED": "1",
                "DOBBY_SIMULATOR_TEST_SEED_STDERR_CAPTURE": "1",
            },
        )
        command = ios_simulator.xcodebuild_ui_test_without_building_command(
            _UDID,
            Path("candidate/iosApp.xcodeproj"),
            Path("work/derived-data"),
            architecture="arm64",
            native_cases=[IOS_SUBSCRIPTION_FIXTURE_CASE],
            test_environment=environment,
        )
        self.assertEqual(command[0], "/usr/bin/env")
        self.assertEqual(
            command[1:4],
            [
                "DOBBY_IOS_TEST_FIXTURE_REQUIRED=1",
                "DOBBY_IOS_TEST_SUBSCRIPTION_URL=" + fixture.url,
                "DOBBY_SIMULATOR_TEST_SEED_STDERR_CAPTURE=1",
            ],
        )
        self.assertIn(
            "-only-testing:iosAppUITests/NativeSubscriptionFixtureInteractionTests/"
            "testAutomaticProfilesAndFixtureRequestCounts",
            command,
        )
        self.assertEqual(command[-1], "test-without-building")
        self.assertIn(
            "iosAppUITests/NativeSubscriptionFixtureInteractionTests/"
            "testAutomaticProfilesAndFixtureRequestCounts",
            ios_simulator.ui_test_selection(),
            "Default CI should run the fixture case with disposable fixture setup",
        )
        self.assertTrue(ios_simulator_app._requires_subscription_fixture(None))
        self.assertTrue(
            ios_simulator_app._requires_subscription_fixture(
                [IOS_SUBSCRIPTION_FIXTURE_CASE]
            )
        )
        self.assertFalse(
            ios_simulator_app._requires_subscription_fixture(["logs-freeze-resume"])
        )

    def test_host_checks_failed_paste_retry_and_cold_link_gets_after_xctest(self) -> None:
        fixture = SimpleNamespace(
            control_stats=lambda: {
                "subscription_gets": 3,
                "in_flight_gets": 0,
                "max_in_flight_gets": 1,
            }
        )
        self.assertEqual(
            ios_simulator_app._assert_subscription_fixture_request_count(
                fixture, expected_gets=3
            ),
            fixture.control_stats(),
        )
        for count in (2, 4):
            fixture.control_stats = lambda count=count: {
                "subscription_gets": count,
                "in_flight_gets": 0,
                "max_in_flight_gets": 1,
            }
            with self.assertRaisesRegex(
                ios_simulator_app.IOSSimulatorAppContractError,
                "expected 3 completed",
            ):
                ios_simulator_app._assert_subscription_fixture_request_count(
                    fixture, expected_gets=3
                )

    def test_subscription_fixture_profile_contains_a_blank_description_for_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as scratch:
            profile = ios_simulator_app._write_disposable_subscription_profile(
                Path(scratch)
            )
            sections = profile.read_text(encoding="utf-8").strip().split("\n\n")
        self.assertEqual(len(sections), 12)
        self.assertIn('Description = "Simulator fixture profile 11"', sections[10])
        self.assertNotIn("Description", sections[11])

    def test_fixture_trust_is_limited_to_and_removed_from_temporary_simulator(self) -> None:
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            profile = root / "profile.toml"
            profile.write_text('[[Outline]]\nDescription = "synthetic"\n', encoding="utf-8")
            fixture = subscription_fixture.SubscriptionFixture(
                profile,
                root / "fixture",
                "ios_simulator",
                simulator_udid=_UDID,
                simulator_temporary=True,
            )
            real_command = subscription_fixture.command
            trust_commands: list[list[str]] = []

            def command(arguments, **kwargs):
                arguments = list(arguments)
                if arguments[:4] == ["xcrun", "simctl", "list", "devices"]:
                    inventory = {"devices": {"runtime": [{"udid": _UDID, "state": "Booted"}]}}
                    return json.dumps(inventory).encode()
                if arguments[:4] == ["xcrun", "simctl", "keychain", _UDID]:
                    trust_commands.append(arguments)
                    return b""
                return real_command(arguments, **kwargs)

            try:
                with patch.object(subscription_fixture, "command", side_effect=command):
                    self.assertTrue(fixture.start().startswith("https://127.0.0.1:"))
                    fixture.close()
            finally:
                if fixture.directory.exists():
                    with patch.object(subscription_fixture, "command", side_effect=command):
                        subscription_fixture.SubscriptionFixture.cleanup_interrupted(fixture.directory)
            self.assertEqual(
                trust_commands,
                [
                    ["xcrun", "simctl", "keychain", _UDID, "add-root-cert", str(fixture.certificate)],
                    ["xcrun", "simctl", "keychain", _UDID, "reset"],
                ],
            )
            self.assertFalse(fixture.directory.exists())

    def test_fixture_rejects_reusable_simulator_trust(self) -> None:
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            profile = root / "profile.toml"
            profile.write_text('[[Outline]]\nDescription = "synthetic"\n', encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "disposable Simulator"):
                subscription_fixture.SubscriptionFixture(
                    profile,
                    root / "fixture",
                    "ios_simulator",
                    simulator_udid=_UDID,
                    simulator_temporary=False,
                )
            self.assertFalse((root / "fixture").exists())

    def test_interrupted_temporary_simulator_is_deleted_and_fixture_marker_cleaned(self) -> None:
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            run_dir = root / "run"
            run_dir.mkdir()
            logs = root / "logs"
            runtime = {
                "udid": _UDID,
                "bundle_id": "vpn.dobby.app",
                "temporary": True,
                "created": True,
                "deleted": False,
                "installed": False,
            }
            state = {"runtime": dict(runtime)}
            calls: list[list[str]] = []
            saved: list[dict] = []
            inventory = json.dumps({
                "devices": {"runtime": [{"udid": _UDID, "state": "Booted"}]},
            })

            def run_logged(arguments, **kwargs):
                del kwargs
                calls.append(list(arguments))
                return SimpleNamespace(stdout=inventory if arguments[2:4] == ["list", "devices"] else "")

            with (
                patch("torturer_runner.local_vm._run_logged", side_effect=run_logged),
                patch("torturer_runner.local_vm._read_state", return_value=state),
                patch("torturer_runner.local_vm._write_json", side_effect=lambda path, value: saved.append(value)),
                patch.object(subscription_fixture.SubscriptionFixture, "cleanup_interrupted") as clean_fixture,
            ):
                local_vm_ios.cleanup(run_dir, runtime, logs, 30)

            operations = [command[2:4] for command in calls]
            self.assertEqual(operations, [["list", "devices"], ["shutdown", _UDID], ["delete", _UDID]])
            self.assertEqual(clean_fixture.call_count, 2)
            self.assertTrue(saved[-1]["runtime"]["deleted"])

    def test_fake_session_client_is_simulator_only_and_keeps_default_tls_validation(self) -> None:
        project = (
            Path(__file__).parents[4]
            / "ui/apple/ios/iosApp.xcodeproj/project.pbxproj"
        ).read_text(encoding="utf-8")
        sources = project.split("/* Begin PBXSourcesBuildPhase section */", 1)[1]
        production_sources = sources.split("F30000000000000000000002 /* Sources */", 1)[1].split(
            "F30000000000000000000008 /* Sources */", 1
        )[0]
        simulator_sources = sources.split("F30000000000000000000008 /* Sources */", 1)[1].split(
            "/* End PBXSourcesBuildPhase section */", 1
        )[0]
        self.assertNotIn("IOSSimulatorTestSessionClient.swift", production_sources)
        self.assertIn("IOSSimulatorTestSessionClient.swift", simulator_sources)
        self.assertIn("NativeSubscriptionFixtureInteractionTests.swift", project)
        self.assertEqual(
            project.count('SWIFT_ACTIVE_COMPILATION_CONDITIONS = "DOBBY_SIMULATOR_TEST $(inherited)";'),
            2,
        )
        client = (
            Path(__file__).parents[4]
            / "ui/apple/ios/app/IOSSimulatorTestSessionClient.swift"
        ).read_text(encoding="utf-8")
        self.assertIn("URLSession.shared.dataTask", client)
        self.assertNotIn("serverTrust", client)
        self.assertNotIn("didReceive challenge", client)
        for key in (
            "session_id",
            "sequence",
            "generation",
            "configure_requests",
            "request_count",
            "start_requests",
            "stop_requests",
        ):
            self.assertIn(f'"{key}"', client)
        self.assertIn("DOBBY_IOS_TEST_FIXTURE_REQUIRED", client)
        self.assertIn('accessibilityIdentifier("Simulator test session state")',
                      (Path(__file__).parents[4] / "ui/apple/ios/app/NativeApp.swift").read_text(encoding="utf-8"))
        rendered_test = (
            Path(__file__).parents[4]
            / "ui/apple/ios/tests/NativeSubscriptionFixtureInteractionTests.swift"
        ).read_text(encoding="utf-8")
        self.assertIn("throw XCTSkip(", rendered_test)
        self.assertNotIn("Torturer did not provide the disposable HTTPS", rendered_test)
        self.assertIn('app.buttons["Retry"]', rendered_test)
        self.assertIn("assertFailedLoadWithoutConnection", rendered_test)
        self.assertIn("assertRetriedLoadWithoutConnection", rendered_test)
        self.assertIn("pasteElapsed, 1.5", rendered_test)
        self.assertIn("Tunnel stderr · tunnel", rendered_test)
        self.assertIn("Stderr capture initialized", rendered_test)
        self.assertIn("paste.tap()", rendered_test)
        self.assertIn("app.open(deepLink)", rendered_test)
        self.assertIn("XCUIDevice.shared.system.open(deepLink)", rendered_test)
        self.assertIn("for _ in 0..<2", rendered_test)
        runner = (
            Path(__file__).parent / "run_app_contract.py"
        ).read_text(encoding="utf-8")
        self.assertIn('parser.add_argument("--native-case", action="append")', runner)
        self.assertIn("validate_native_cases(", runner)


if __name__ == "__main__":
    unittest.main()
