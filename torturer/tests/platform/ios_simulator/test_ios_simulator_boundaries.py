from __future__ import annotations

import json
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import ios_simulator_app, local_vm, local_vm_ios


class IOSSimulatorBoundaryTests(unittest.TestCase):
    def test_run_reactivates_prepared_screenshot_decoder_without_installing(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            screenshot_python = run_dir / "screenshot-python"
            fake_pil = screenshot_python / "PIL"
            fake_pil.mkdir(parents=True)
            (fake_pil / "__init__.py").write_text("prepared_decoder = True\n", encoding="utf-8")
            logs = run_dir / "logs"
            logs.mkdir()
            work = run_dir / "work" / "ios"
            contract = ios_simulator_app.public_ios_simulator_app_contract("arm64")
            app_path = contract.app_path(work)
            app_path.mkdir(parents=True)
            descriptor = {
                "mode": "ios-simulator",
                "app": str(app_path),
                "architecture": "arm64",
                "screenshot_python": str(screenshot_python),
            }
            loaded_from: list[Path] = []

            def run_contract(**_kwargs):
                import PIL

                self.assertTrue(PIL.prepared_decoder)
                loaded_from.append(Path(PIL.__file__).resolve())
                return SimpleNamespace(
                    simulator=SimpleNamespace(udid="01234567-89ab-cdef-0123-456789abcdef")
                )

            original_path = sys.path[:]
            saved_pil_modules = {
                name: sys.modules.pop(name)
                for name in tuple(sys.modules)
                if name == "PIL" or name.startswith("PIL.")
            }
            sys.path[:] = [entry for entry in sys.path if entry != str(screenshot_python)]
            try:
                with (
                    mock.patch.object(local_vm, "_read_state", return_value={"runtime": {"udid": "0123"}}),
                    mock.patch.object(local_vm, "_write_json"),
                    mock.patch.object(local_vm, "_install_screenshot_decoder", side_effect=AssertionError("run must not install")) as install,
                    mock.patch.object(local_vm_ios.ios, "run_ios_simulator_app_contract", side_effect=run_contract),
                    mock.patch.object(local_vm_ios.ios, "retain_ios_diagnostics"),
                ):
                    local_vm_ios.run(run_dir, descriptor, logs, timeout=30)
                install.assert_not_called()
            finally:
                sys.path[:] = original_path
                for name in tuple(sys.modules):
                    if name == "PIL" or name.startswith("PIL."):
                        sys.modules.pop(name)
                sys.modules.update(saved_pil_modules)

            self.assertEqual(loaded_from, [fake_pil / "__init__.py"])

    def test_ui_test_build_and_run_share_products_without_rebuilding(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
        contract = ios_simulator_app.PUBLIC_IOS_SIMULATOR_APP_CONTRACT
        project = Path("candidate/iosApp.xcodeproj")
        work_dir = Path("work/ios")
        derived_data = Path("work/ios/derived-data")
        result_bundle = Path("work/ios/xctest-results.xcresult")

        build = ios_simulator_app.xcodebuild_app_command(
            contract, work_dir=work_dir
        )
        run = ios_simulator_app.xcodebuild_ui_test_without_building_command(
            udid, project, derived_data, result_bundle,
            architecture=contract.architecture,
        )
        intel_run = ios_simulator_app.xcodebuild_ui_test_without_building_command(
            udid, project, derived_data, result_bundle, architecture="amd64"
        )

        self.assertEqual(build[1:3], ["scripts/package_ios_app.sh", "iossimulator"])
        self.assertEqual(build[3], str(contract.app_path(work_dir)))
        self.assertEqual(run[-1], "test-without-building")
        self.assertEqual(
            run[run.index("-destination") + 1],
            f"platform=iOS Simulator,id={udid.upper()},arch=arm64",
        )
        self.assertEqual(
            intel_run[intel_run.index("-destination") + 1],
            f"platform=iOS Simulator,id={udid.upper()},arch=x86_64",
        )
        self.assertEqual(run[run.index("-scheme") + 1], "iosAppUITests")
        self.assertIn(str(derived_data), build[3])
        self.assertEqual(run[run.index("-derivedDataPath") + 1], str(derived_data))
        self.assertEqual(
            run[run.index("-resultBundlePath") + 1], str(result_bundle)
        )

    def test_install_timeout_reports_elapsed_time_and_preserves_cleanup_window(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
        open_command = [
            "/usr/bin/open", "-a", "Simulator", "--args",
            "-CurrentDeviceUDID", udid.upper(),
        ]
        clock = [0.0]
        commands: list[tuple[list[str], float | None]] = []

        class Runner:
            def run(self, command, *, cwd=None, timeout_seconds=None):
                del cwd
                arguments = list(command)
                commands.append((arguments, timeout_seconds))
                if arguments[:4] == ["xcrun", "simctl", "list", "devices"]:
                    inventory = {
                        "devices": {
                            "com.apple.CoreSimulator.SimRuntime.iOS-17-5": [{
                                "isAvailable": True,
                                "name": "iPhone 15",
                                "udid": udid,
                            }],
                        },
                    }
                    return ios_simulator_app.CommandResult(0, json.dumps(inventory), "")
                if arguments[:3] == ["xcrun", "--sdk", "iphonesimulator"]:
                    return ios_simulator_app.CommandResult(0, "17.5", "")
                if arguments == open_command:
                    self.open_simulator_timeout = timeout_seconds
                    return ios_simulator_app.CommandResult(0, "", "")
                if arguments[:3] == ["xcrun", "simctl", "install"]:
                    self.asserted_install_timeout = timeout_seconds
                    clock[0] += (timeout_seconds or 0) + ios_simulator_app.COMMAND_TERMINATION_RESERVE_SECONDS
                    error = ios_simulator_app.IOSSimulatorAppContractError(
                        "iOS command timed out after 180s"
                    )
                    error.stdout = b"install stdout\x00\xff"
                    error.stderr = b"install stderr\n"
                    raise error
                if arguments[:3] == ["xcrun", "simctl", "shutdown"]:
                    self.shutdown_timeout = timeout_seconds
                    return ios_simulator_app.CommandResult(0, "", "")
                if arguments[:3] in (
                    ["xcrun", "simctl", "boot"],
                    ["xcrun", "simctl", "bootstatus"],
                ):
                    return ios_simulator_app.CommandResult(0, "", "")
                raise AssertionError(f"unexpected command: {arguments}")

        runner = Runner()
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            work_dir = root / "work"
            contract = ios_simulator_app.PUBLIC_IOS_SIMULATOR_APP_CONTRACT
            contract.app_path(work_dir).mkdir(parents=True)
            (root / "candidate" / "ui" / "apple" / "ios" / "iosApp.xcodeproj").mkdir(parents=True)
            budget = ios_simulator_app.RunBudget(
                max_seconds=345,
                cleanup_reserve_seconds=120,
                clock=lambda: clock[0],
            )
            with mock.patch.object(
                ios_simulator_app, "_read_simulator_hardware_keyboard_override", return_value=False
            ):
                with self.assertRaises(ios_simulator_app.IOSSimulatorStageError) as caught:
                    ios_simulator_app.run_ios_simulator_app_contract(
                        candidate_root=root / "candidate",
                        work_dir=work_dir,
                        runner=runner,
                        budget=budget,
                    )

        failure = caught.exception
        self.assertEqual(failure.stage, "install")
        self.assertEqual(failure.timeout_seconds, 180)
        self.assertIsNotNone(failure.elapsed_seconds)
        self.assertIn("elapsed", str(failure))
        notes = "\n".join(failure.__notes__)
        self.assertIn("install stdout\x00\\xff", notes)
        self.assertIn("install stderr", notes)
        # The remaining lane budget still caps the five-minute stage limit.
        self.assertEqual(runner.asserted_install_timeout, 180)
        self.assertEqual(runner.open_simulator_timeout, 30)
        command_arguments = [arguments for arguments, _ in commands]
        open_index = command_arguments.index(open_command)
        install_index = command_arguments.index(
            [
                "xcrun", "simctl", "install", udid.upper(),
                str(contract.app_path(work_dir)),
            ]
        )
        self.assertLess(open_index, install_index)
        # The simulated install and its bounded process-group stop consume 225s
        # of a 345s run. Cleanup still receives the reserved 120s; the shutdown
        # command leaves its own 45s process-stop bound inside that window.
        self.assertEqual(clock[0], 225)
        self.assertEqual(budget.cleanup_timeout(), 120)
        self.assertEqual(runner.shutdown_timeout, 75)

    def test_stalled_process_pipe_drain_is_bounded_and_keeps_all_available_output(self) -> None:
        process = mock.Mock()
        process.pid = 123
        process.stdout = mock.Mock()
        process.stderr = mock.Mock()
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(("command",), 0.1, output=b"started\n"),
            subprocess.TimeoutExpired(
                ("command",), 1.0, output=b"started\nterm\n",
            ),
            subprocess.TimeoutExpired(
                ("command",), 1.0, output=b"started\nterm\nkill\nfinal\n",
                stderr=b"child stderr\n",
            ),
        ]
        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch("bounded_process.terminate_process_group"),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(
                    ("command",), timeout_seconds=0.1,
                )

        self.assertEqual(process.communicate.call_count, 3)
        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("started\nterm\nkill\nfinal\n", notes)
        self.assertIn("child stderr", notes)
        self.assertIn("pipes remained open", notes)
        process.stdout.close.assert_called_once()
        process.stderr.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
