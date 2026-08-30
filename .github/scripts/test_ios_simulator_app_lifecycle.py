import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


SOURCE = Path(__file__).with_name("run_ios_simulator_app_lifecycle.py")
SPEC = importlib.util.spec_from_file_location("ios_lifecycle", SOURCE)
assert SPEC and SPEC.loader
LIFECYCLE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LIFECYCLE)


class IosSimulatorAppLifecycleTests(unittest.TestCase):
    def test_script_requires_every_agreed_lifecycle_and_retained_data_step(self) -> None:
        source = SOURCE.read_text(encoding="utf-8")
        for required in (
            '"uninstall"',
            '"install"',
            '"--terminate-running-process"',
            "SETTINGS_BUNDLE_ID",
            '"terminate"',
            '"get_app_container"',
            '"retained_data_reinstall"',
            '"ios-app-cold-start.png"',
            '"ios-app-foreground-return.png"',
            '"repeated_start_process_reused"',
            '"foreground_process_reused"',
            '"background_process_survived"',
            '"data_container_relocated_on_reinstall"',
        ):
            self.assertIn(required, source)
        self.assertNotIn("repeated launch unexpectedly replaced", source)
        self.assertNotIn("backgrounded app did not resume its original process", source)
        workflow = SOURCE.parents[1] / "workflows" / "test.yml"
        self.assertNotIn("--screenshots", workflow.read_text(encoding="utf-8"))

    def test_public_simulator_output_is_retained_without_public_echo(self) -> None:
        completed = subprocess.CompletedProcess(
            ["xcrun", "simctl", "list"], 0,
            stdout="private simulator device path\n",
            stderr="private simulator diagnostic\n",
        )
        with tempfile.TemporaryDirectory() as temporary:
            stdout = io.StringIO()
            diagnostics = io.StringIO()
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                    clear=False,
                ),
                mock.patch.object(LIFECYCLE.subprocess, "run", return_value=completed),
                mock.patch.object(LIFECYCLE.sys, "stdout", stdout),
                mock.patch.object(LIFECYCLE.sys, "stderr", diagnostics),
            ):
                result = LIFECYCLE.run("list")
            self.assertEqual(result.returncode, 0)
            self.assertNotIn("private simulator device path", stdout.getvalue())
            self.assertNotIn("private simulator device path", diagnostics.getvalue())
            self.assertNotIn("private simulator diagnostic", stdout.getvalue())
            self.assertNotIn("private simulator diagnostic", diagnostics.getvalue())
            self.assertIn("dobbyvpn_diagnostic kind=ios-simulator", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            self.assertIn(b"private simulator device path", retained[0].read_bytes())
            self.assertIn(b"private simulator diagnostic", retained[0].read_bytes())

    def test_screenshot_is_nonempty_private_and_digest_bound(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "frame.png"

            def fake_run(*args: str, **_: object) -> None:
                Path(args[-1]).write_bytes(b"png")

            with mock.patch.object(LIFECYCLE, "run", side_effect=fake_run):
                record = LIFECYCLE.screenshot("device", target)

            self.assertEqual(record["bytes"], 3)
            self.assertEqual(record["subject"], "app")
            self.assertEqual(record["state"], "frame")
            self.assertEqual(len(record["sha256"]), 64)
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)

    def test_launch_requires_a_live_simulator_process(self) -> None:
        launch = subprocess.CompletedProcess(
            ["xcrun", "simctl", "launch"], 0, stdout="vpn.dobby.app: 1234\n",
        )
        live = subprocess.CompletedProcess(
            ["xcrun", "simctl", "spawn"], 0, stdout="",
        )
        with (
            mock.patch.object(LIFECYCLE, "run", side_effect=[launch, live]) as runner,
            mock.patch.object(LIFECYCLE.time, "sleep") as sleeper,
        ):
            self.assertEqual(LIFECYCLE.launch_app("device", "vpn.dobby.app"), 1234)
        self.assertEqual(runner.call_args_list[-1].args, ("spawn", "device", "/bin/kill", "-0", "1234"))
        sleeper.assert_called_once_with(35)

    def test_lifecycle_accepts_simulator_process_replacement_and_checks_current_background_pid(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            app = root / "DobbyVPN.app"
            app.mkdir()
            with (app / "Info.plist").open("wb") as output:
                import plistlib
                plistlib.dump({"CFBundleIdentifier": "vpn.dobby.app"}, output)
            data = root / "data"
            data.mkdir()

            def fake_run(*args: str, **_: object) -> subprocess.CompletedProcess[str]:
                stdout = str(data) + "\n" if args[0] == "get_app_container" else ""
                return subprocess.CompletedProcess(["xcrun", "simctl", *args], 0, stdout=stdout)

            with (
                mock.patch.object(LIFECYCLE, "run", side_effect=fake_run) as runner,
                mock.patch.object(LIFECYCLE, "launch_app", side_effect=[100, 101, 102, 103, 104]),
            ):
                result = LIFECYCLE.lifecycle("device", app, None)

        self.assertTrue(result["repeated_start"])
        self.assertFalse(result["repeated_start_process_reused"])
        self.assertTrue(result["background_foreground"])
        self.assertTrue(result["background_process_survived"])
        self.assertFalse(result["foreground_process_reused"])
        self.assertIn(
            mock.call("spawn", "device", "/bin/kill", "-0", "101", check=False),
            runner.call_args_list,
        )

    def test_background_termination_is_recorded_but_foreground_relaunch_can_pass(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            app = root / "DobbyVPN.app"
            app.mkdir()
            with (app / "Info.plist").open("wb") as output:
                import plistlib
                plistlib.dump({"CFBundleIdentifier": "vpn.dobby.app"}, output)
            data = root / "data"
            data.mkdir()

            def fake_run(*args: str, **kwargs: object) -> subprocess.CompletedProcess[str]:
                stdout = str(data) + "\n" if args[0] == "get_app_container" else ""
                returncode = 3 if args[:4] == ("spawn", "device", "/bin/kill", "-0") and kwargs.get("check") is False else 0
                return subprocess.CompletedProcess(["xcrun", "simctl", *args], returncode, stdout=stdout)

            with (
                mock.patch.object(LIFECYCLE, "run", side_effect=fake_run),
                mock.patch.object(LIFECYCLE, "launch_app", side_effect=[100, 101, 102, 103, 104]),
            ):
                result = LIFECYCLE.lifecycle("device", app, None)

        self.assertFalse(result["background_process_survived"])
        self.assertTrue(result["background_foreground"])

    def test_retained_data_is_checked_in_relocated_post_install_container(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            app = root / "DobbyVPN.app"
            app.mkdir()
            with (app / "Info.plist").open("wb") as output:
                import plistlib
                plistlib.dump({"CFBundleIdentifier": "vpn.dobby.app"}, output)
            original = root / "data-before"
            relocated = root / "data-after"
            original.mkdir()
            relocated.mkdir()
            container_queries = iter((original, relocated))

            def fake_run(*args: str, **_: object) -> subprocess.CompletedProcess[str]:
                if args[0] == "get_app_container":
                    current = next(container_queries)
                    if current == relocated:
                        source = original / "Library" / "Application Support" / "dobby-retained-install-marker"
                        target = relocated / "Library" / "Application Support" / source.name
                        target.parent.mkdir(parents=True)
                        target.write_bytes(source.read_bytes())
                    stdout = str(current) + "\n"
                else:
                    stdout = ""
                return subprocess.CompletedProcess(["xcrun", "simctl", *args], 0, stdout=stdout)

            with (
                mock.patch.object(LIFECYCLE, "run", side_effect=fake_run),
                mock.patch.object(LIFECYCLE, "launch_app", side_effect=[100, 100, 100, 101, 102]),
            ):
                result = LIFECYCLE.lifecycle("device", app, None)

        self.assertTrue(result["retained_data_reinstall"])
        self.assertTrue(result["data_container_relocated_on_reinstall"])

    def test_terminate_running_option_precedes_device_and_bundle(self) -> None:
        launch = subprocess.CompletedProcess(
            ["xcrun", "simctl", "launch"], 0, stdout="vpn.dobby.app: 1234\n",
        )
        live = subprocess.CompletedProcess(
            ["xcrun", "simctl", "spawn"], 0, stdout="",
        )
        with (
            mock.patch.object(LIFECYCLE, "run", side_effect=[launch, live]) as runner,
            mock.patch.object(LIFECYCLE.time, "sleep"),
        ):
            LIFECYCLE.launch_app(
                "00000000-0000-0000-0000-000000000001",
                "vpn.dobby.app",
                terminate_running=True,
            )
        self.assertEqual(
            runner.call_args_list[0].args,
            (
                "launch",
                "--terminate-running-process",
                "00000000-0000-0000-0000-000000000001",
                "vpn.dobby.app",
            ),
        )

    def test_launch_rejects_missing_process_identifier(self) -> None:
        launch = subprocess.CompletedProcess(
            ["xcrun", "simctl", "launch"], 0, stdout="unexpected output\n",
        )
        with mock.patch.object(LIFECYCLE, "run", return_value=launch):
            with self.assertRaises(RuntimeError):
                LIFECYCLE.launch_app("device", "vpn.dobby.app")


if __name__ == "__main__":
    unittest.main()
