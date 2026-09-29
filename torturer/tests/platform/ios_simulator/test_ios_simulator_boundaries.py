from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_runner import ios_simulator_app


class IOSSimulatorBoundaryTests(unittest.TestCase):
    def test_install_timeout_reports_elapsed_time_and_preserves_cleanup_window(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
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
            subprocess.TimeoutExpired(("command",), 1.0, output=b"started\nterm\n"),
            subprocess.TimeoutExpired(("command",), 1.0, output=b"started\nterm\nkill\n"),
            subprocess.TimeoutExpired(
                ("command",), 1.0, output=b"started\nterm\nkill\nfinal\n",
                stderr=b"child stderr\n",
            ),
        ]
        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch.object(ios_simulator_app, "_signal_process_group"),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(
                    ("command",), timeout_seconds=0.1,
                )

        self.assertEqual(process.communicate.call_count, 4)
        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("started\nterm\nkill\nfinal\n", notes)
        self.assertIn("child stderr", notes)
        self.assertIn("pipes remained open", notes)
        process.stdout.close.assert_called_once()
        process.stderr.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
