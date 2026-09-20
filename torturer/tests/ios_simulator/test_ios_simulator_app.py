from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time
import unittest

from torturer_checks.ios_simulator_app import (
    CLEANUP_RESERVE_SECONDS,
    IOS_GO_UI_BUILD_TIMEOUT_SECONDS,
    IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS,
    MAX_RUN_SECONDS,
    CommandResult,
    IOSSimulatorAppContractError,
    RunBudget,
    STAGE_TIMEOUT_SECONDS,
    SubprocessCommandRunner,
    _app_container,
    prepare_ios_simulator_candidate,
    public_ios_simulator_app_contract,
    run_ios_simulator_app_contract,
    select_available_iphone,
    xcodebuild_app_command,
)


UDID = "A12B34C5-1234-5678-9ABC-123456789ABC"
BUNDLE = "vpn.dobby.app"


def inventory() -> dict[str, object]:
    return {"devices": {
        "com.apple.CoreSimulator.SimRuntime.iOS-18-5": [{
            "isAvailable": True, "name": "iPhone 16", "udid": "00000000-0000-0000-0000-000000000001",
        }],
        "com.apple.CoreSimulator.SimRuntime.iOS-26-2": [{
            "isAvailable": True, "name": "iPhone 17", "udid": UDID,
        }],
        "com.apple.CoreSimulator.SimRuntime.iOS-26-2-unavailable": [{
            "isAvailable": False, "name": "iPhone 99", "udid": "00000000-0000-0000-0000-000000000002",
        }],
        "com.apple.CoreSimulator.SimRuntime.tvOS-26-2": [{
            "isAvailable": True, "name": "Apple TV", "udid": "00000000-0000-0000-0000-000000000003",
        }],
    }}


class FakeRunner:
    def __init__(
        self,
        root: Path,
        *,
        fail: dict[tuple[str, ...], CommandResult | BaseException] | None = None,
        write_startup_marker: bool = True,
        app_logs: bytes = b'{"message":"old run"}\n',
        app_data_output: str | None = None,
    ) -> None:
        self.root = root
        self.fail = fail or {}
        self.write_startup_marker = write_startup_marker
        self.app_data_output = app_data_output
        self.commands: list[list[str]] = []
        self.calls: list[tuple[list[str], Path | None, float | None]] = []
        self.data_container = root / "simulator-data"
        self.container = self.data_container / "tmp"
        self.log_at_launch: bytes | None = None
        self.container.mkdir(parents=True, exist_ok=True)
        (self.container / "app_logs.txt").write_bytes(app_logs)
        (self.container / "go_app_logs.jsonl").write_bytes(b"go log\n")

    def run(self, command, *, cwd=None, timeout_seconds=None):
        command = list(command)
        self.commands.append(command)
        self.calls.append((command, cwd, timeout_seconds))
        for prefix, result in self.fail.items():
            if command[:len(prefix)] == list(prefix):
                if isinstance(result, BaseException):
                    raise result
                return result
        if command == ["xcrun", "simctl", "list", "devices", "available", "-j"]:
            app = self.root / "work" / "derived-data" / "Build" / "Products" / "Release-iphonesimulator" / "Dobby-Vpn.app"
            app.mkdir(parents=True, exist_ok=True)
            return CommandResult(0, json.dumps(inventory()))
        if command[:3] == ["xcrun", "simctl", "get_app_container"]:
            return CommandResult(
                0,
                self.app_data_output
                if self.app_data_output is not None
                else f"{self.data_container}\n",
            )
        if command[:1] == ["xcodebuild"]:
            self.log_at_launch = (self.container / "app_logs.txt").read_bytes()
            if self.write_startup_marker:
                marker = b"startup.ui_attached mode=normal"
                with (self.container / "app_logs.txt").open("ab") as output:
                    output.write(b'{"message":"' + marker + b'"}\n')
            return CommandResult(0)
        if command[:2] == ["/usr/bin/defaults", "read"]:
            return CommandResult(1, stderr="The domain/default pair does not exist")
        if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"]:
            (Path(cwd) / "DobbyVPNRuntime.xcframework").mkdir(parents=True, exist_ok=True)
            return CommandResult(0)
        if command[:2] == ["/bin/bash", "scripts/package_ios_app.sh"]:
            Path(command[3]).mkdir(parents=True, exist_ok=True)
            return CommandResult(0)
        if command and command[0] == "/bin/rm":
            target = Path(command[-1])
            if target.exists():
                shutil.rmtree(target)
            return CommandResult(0)
        if command and command[0] == "/usr/bin/ditto":
            source, destination = Path(command[1]), Path(command[2])
            destination.parent.mkdir(parents=True, exist_ok=True)
            if source.is_dir():
                shutil.copytree(source, destination, dirs_exist_ok=True)
            return CommandResult(0)
        if command[:3] == ["xcrun", "simctl", "boot"]:
            return CommandResult(0)
        return CommandResult(0)


class IOSSimulatorSimplificationTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.candidate = self.root / "candidate"
        for name in ("go_module", "swift_module"):
            (self.candidate / name).mkdir(parents=True, exist_ok=True)
        (self.candidate / "swift_module" / "iosApp.xcodeproj").mkdir(parents=True, exist_ok=True)
        self.contract = public_ios_simulator_app_contract("amd64")

    def test_run_budget_reserves_cleanup(self) -> None:
        self.assertEqual(MAX_RUN_SECONDS, 1800)
        self.assertEqual(CLEANUP_RESERVE_SECONDS, 120)
        clock_values = iter((0.0, 0.0, 7.0, 8.1))
        budget = RunBudget(max_seconds=10, cleanup_reserve_seconds=2, clock=lambda: next(clock_values))
        self.assertEqual(budget.operation_timeout(), 8)
        self.assertEqual(budget.operation_timeout(), 1)
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "functional budget"):
            budget.operation_timeout()

    def test_bootstatus_allows_one_cold_data_migration(self) -> None:
        self.assertEqual(STAGE_TIMEOUT_SECONDS["bootstatus"], 360)
        budget = RunBudget()
        self.assertEqual(
            budget.operation_timeout(STAGE_TIMEOUT_SECONDS["bootstatus"]),
            360,
        )

    def test_device_selection_and_packaging_command_are_small_and_deterministic(self) -> None:
        selected = select_available_iphone(json.dumps(inventory()))
        self.assertEqual((selected.name, selected.udid, selected.runtime), (
            "iPhone 17", UDID, "com.apple.CoreSimulator.SimRuntime.iOS-26-2",
        ))
        command = xcodebuild_app_command(
            self.contract, candidate_root=self.candidate, device_udid=UDID,
            work_dir=self.root / "work",
        )
        self.assertEqual(command[:4], ["/bin/bash", "scripts/package_ios_app.sh", "iossimulator", str(self.root / "work" / "derived-data" / "Build" / "Products" / "Release-iphonesimulator" / "Dobby-Vpn.app")])
        self.assertEqual(command[-1], "amd64")

    def test_simulator_build_is_single_contract_and_native_arch(self) -> None:
        command = xcodebuild_app_command(
            self.contract, candidate_root=self.candidate, device_udid=UDID,
            work_dir=self.root / "work",
        )
        self.assertEqual(command[-1], "amd64")
        self.assertNotIn("kmp_module", command)

    def test_mini_runs_real_xctest_ui_contract_and_cleans_up(self) -> None:
        work = self.root / "work"
        runner = FakeRunner(self.root)
        diagnostics = self.root / "logs" / "ios"
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate,
            work_dir=work,
            runner=runner,
            contract=self.contract,
            diagnostic_dir=diagnostics,
        )
        self.assertTrue(evidence.app.app_path.is_dir())
        self.assertFalse(any(command[:2] == ["swift", "-e"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "install"] for command in runner.commands))
        self.assertFalse(any(command[:3] == ["xcrun", "simctl", "launch"] for command in runner.commands))
        self.assertTrue(any(command[:1] == ["xcodebuild"] for command in runner.commands))
        self.assertIn(
            [
                "/usr/bin/defaults", "write", "com.apple.iphonesimulator",
                "ConnectHardwareKeyboard", "-bool", "false",
            ],
            runner.commands,
        )
        self.assertIn(
            [
                "/usr/bin/defaults", "delete", "com.apple.iphonesimulator",
                "ConnectHardwareKeyboard",
            ],
            runner.commands,
        )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        terminate_timeout = next(
            timeout for command, _, timeout in runner.calls
            if command[:3] == ["xcrun", "simctl", "terminate"]
        )
        self.assertEqual(terminate_timeout, STAGE_TIMEOUT_SECONDS["terminate"])
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))
        self.assertIn(b"startup.ui_attached mode=normal", (diagnostics / "app_logs.txt").read_bytes())
        self.assertEqual((diagnostics / "go_app_logs.jsonl").read_bytes(), b"go log\n")

    def test_ui_test_failure_still_shuts_down(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={("xcodebuild",): CommandResult(1, stderr="XCTest accessibility failure")},
        )
        diagnostics = self.root / "diagnostics"
        with self.assertRaisesRegex(IOSSimulatorAppContractError, r"(?s)stage 'xctest-ui'.*XCTest accessibility failure"):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                contract=self.contract, diagnostic_dir=diagnostics,
            )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))
        self.assertIn("stage 'xctest-ui'", (diagnostics / "failure.txt").read_text())
        self.assertEqual((diagnostics / "failure-stage.txt").read_text(), "xctest-ui\n")

    def test_ui_test_failure_accepts_already_terminated_app_cleanup(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={
                ("xcodebuild",): CommandResult(1, stderr="XCTest accessibility failure"),
                ("xcrun", "simctl", "terminate"): CommandResult(
                    3,
                    stderr='Simulator device failed to terminate vpn.dobby.app. found nothing to terminate',
                ),
            },
        )
        diagnostics = self.root / "diagnostics"
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"(?s)stage 'xctest-ui'.*XCTest accessibility failure",
        ) as raised:
            run_ios_simulator_app_contract(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=runner,
                contract=self.contract,
                diagnostic_dir=diagnostics,
            )
        self.assertNotIn("cleanup also failed", str(raised.exception))
        self.assertTrue(any(
            command[:3] == ["xcrun", "simctl", "shutdown"]
            for command in runner.commands
        ))

    def test_xctest_timeout_is_stage_specific_without_retry(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={
                ("xcodebuild",): IOSSimulatorAppContractError(
                    "iOS command timed out after 600s; stdout=compile complete; stderr=runner hung"
                )
            },
        )
        diagnostics = self.root / "diagnostics"
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"stage 'xctest-ui' timed out after 600s.*runner hung",
        ):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=runner,
                contract=self.contract,
                diagnostic_dir=diagnostics,
            )
        self.assertEqual(sum(command[:1] == ["xcodebuild"] for command in runner.commands), 1)
        xctest_timeout = next(timeout for command, _, timeout in runner.calls if command[:1] == ["xcodebuild"])
        self.assertEqual(xctest_timeout, 600)
        self.assertIn("stage 'xctest-ui'", (diagnostics / "failure.txt").read_text())
        self.assertEqual((diagnostics / "failure-stage.txt").read_text(), "xctest-ui\n")

    def test_keyboard_preference_read_failure_is_not_treated_as_unset(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={
                ("/usr/bin/defaults", "read"): CommandResult(
                    1, stderr="preference domain is unreadable"
                )
            },
        )
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            "stage 'read-hardware-keyboard'.*exit code 1",
        ):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=runner,
                contract=self.contract,
            )
        self.assertFalse(
            any(command[:2] == ["/usr/bin/defaults", "write"] for command in runner.commands)
        )

    def test_contract_builds_launches_and_checks_rendered_ui_target(self) -> None:
        runner = FakeRunner(self.root)
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            contract=self.contract,
        )
        self.assertEqual(runner.commands[0][:5], ["xcrun", "simctl", "list", "devices", "available"])
        self.assertTrue(evidence.app.app_path.is_dir())
        self.assertFalse(any(command[:2] == ["/bin/bash", "scripts/package_ios_app.sh"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "install"] for command in runner.commands))
        self.assertFalse(any(command[:3] == ["xcrun", "simctl", "launch"] for command in runner.commands))
        self.assertTrue(any(command[:1] == ["xcodebuild"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_xctest_failure_shuts_down_simulator(self) -> None:
        runner = FakeRunner(self.root, fail={
            ("xcodebuild",): CommandResult(1, stderr="app failed to launch"),
        })
        with self.assertRaisesRegex(IOSSimulatorAppContractError, r"(?s)stage 'xctest-ui'.*app failed to launch"):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                contract=self.contract,
            )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_ui_test_is_not_satisfied_by_a_stale_startup_marker(self) -> None:
        runner = FakeRunner(
            self.root,
            write_startup_marker=False,
            app_logs=b'{"message":"startup.ui_attached mode=normal"}\n',
        )
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            contract=self.contract,
        )
        self.assertTrue(any(command[:1] == ["xcodebuild"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_ui_test_does_not_require_app_log_container(self) -> None:
        runner = FakeRunner(self.root, fail={
            ("xcrun", "simctl", "get_app_container"): CommandResult(1, stderr="not available"),
        })
        run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            contract=self.contract,
            diagnostic_dir=self.root / "diagnostics",
        )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_app_log_lookup_uses_simctl_data_container_kind(self) -> None:
        runner = FakeRunner(self.root)
        run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            contract=self.contract,
        )
        lookup = next(command for command in runner.commands if command[:3] == ["xcrun", "simctl", "get_app_container"])
        self.assertEqual(lookup[-1], "data")

    def test_app_log_lookup_uses_the_data_container_temporary_directory(self) -> None:
        runner = FakeRunner(
            self.root,
            app_data_output=f"{self.root / 'simulator-data'}\n",
        )
        self.assertEqual(
            _app_container(runner, UDID, budget=RunBudget()),
            self.root / "simulator-data" / "tmp",
        )

    def test_prepare_builds_the_go_runtime_and_packages_the_fyne_app(self) -> None:
        runner = FakeRunner(self.root)
        work = self.root / "work"
        prepare_ios_simulator_candidate(
            candidate_root=self.candidate, work_dir=work, runner=runner,
            contract=self.contract, budget=RunBudget(),
        )
        go_build = next(command for command in runner.commands if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"])
        self.assertEqual(go_build[-2:], ["--simulator-architecture", "amd64"])
        package = next(command for command in runner.commands if command[:2] == ["/bin/bash", "scripts/package_ios_app.sh"])
        self.assertEqual(package[-1], "amd64")
        timeout = next(timeout for command, _, timeout in runner.calls if command == package)
        self.assertEqual(timeout, IOS_GO_UI_BUILD_TIMEOUT_SECONDS)
        framework_timeout = next(timeout for command, _, timeout in runner.calls if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"])
        self.assertEqual(framework_timeout, IOS_NATIVE_FRAMEWORK_BUILD_TIMEOUT_SECONDS)
        self.assertFalse(any(command and command[0] == "./gradlew" for command in runner.commands))
        self.assertFalse(any(command[:3] == ["xcrun", "simctl", "list"] for command in runner.commands))

    @unittest.skipUnless(os.name == "posix", "process-group cleanup requires POSIX")
    def test_command_runner_timeout_kills_group_and_keeps_no_sidecars(self) -> None:
        pid_file = self.root / "child.pid"
        child = (
            "import os, signal, subprocess, sys, time; "
            f"p=subprocess.Popen([sys.executable,'-c',\"import os,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); "
            f"open({str(pid_file)!r},'w').write(str(os.getpid())); time.sleep(30)\"], "
            "stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL); time.sleep(30)"
        )
        runner = SubprocessCommandRunner()
        self.assertFalse(hasattr(runner, "raw_directory"))
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "timed out"):
            # Allow both interpreters to start even on a busy CI runner before
            # exercising the timeout cleanup path.
            runner.run((sys.executable, "-c", child), timeout_seconds=1.0)
        child_pid = int(pid_file.read_text())
        for _ in range(40):
            proc_stat = Path(f"/proc/{child_pid}/stat")
            if not proc_stat.exists() or proc_stat.read_text().split(") ")[-1].startswith("Z"):
                break
            time.sleep(0.05)
        else:
            self.fail("timed-out iOS command child survived its process-group cleanup")

    def test_command_runner_keeps_stdout_stderr_separate(self) -> None:
        runner = SubprocessCommandRunner()
        result = runner.run((
            sys.executable, "-c",
            "import sys; print('stdout'); print('stderr', file=sys.stderr)",
        ))
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "stdout\n")
        self.assertEqual(result.stderr, "stderr\n")


if __name__ == "__main__":
    unittest.main()
