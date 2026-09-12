from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from torturer_checks.ios_simulator_app import (
    CLEANUP_RESERVE_SECONDS,
    IOS_KMP_BUILD_TIMEOUT_SECONDS,
    MAX_RUN_SECONDS,
    CommandResult,
    IOSSimulatorAppContractError,
    RunBudget,
    SubprocessCommandRunner,
    _ios_test_tasks,
    metal_probe_command,
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
        fail: dict[tuple[str, ...], CommandResult] | None = None,
        startup_mode: str = "mini",
        write_startup_marker: bool = True,
        app_logs: bytes = b'{"message":"old run"}\n',
    ) -> None:
        self.root = root
        self.fail = fail or {}
        self.startup_mode = startup_mode
        self.write_startup_marker = write_startup_marker
        self.commands: list[list[str]] = []
        self.calls: list[tuple[list[str], Path | None, float | None]] = []
        self.container = root / "simulator-app-group"
        self.container.mkdir(parents=True, exist_ok=True)
        (self.container / "app_logs.txt").write_bytes(app_logs)
        (self.container / "go_app_logs.jsonl").write_bytes(b"go log\n")

    def run(self, command, *, cwd=None, timeout_seconds=None):
        command = list(command)
        self.commands.append(command)
        self.calls.append((command, cwd, timeout_seconds))
        for prefix, result in self.fail.items():
            if command[:len(prefix)] == list(prefix):
                return result
        if command == ["xcrun", "simctl", "list", "devices", "available", "-j"]:
            return CommandResult(0, json.dumps(inventory()))
        if command[:2] == ["swift", "-e"]:
            return CommandResult(0, "true\n")
        if command[:3] == ["xcrun", "simctl", "get_app_container"]:
            return CommandResult(0, f"{self.container}\n")
        if command[:3] == ["xcrun", "simctl", "launch"]:
            if self.write_startup_marker:
                marker = (
                    b"startup.initialized mode=mini"
                    if self.startup_mode == "mini"
                    else b"startup.ui_attached mode=normal"
                )
                with (self.container / "app_logs.txt").open("ab") as output:
                    output.write(b'{"message":"' + marker + b'"}\n')
            return CommandResult(0, f"{BUNDLE}: 1001\n")
        if command[:2] == ["xcodebuild", "build"]:
            derived_data = Path(command[command.index("-derivedDataPath") + 1])
            app = derived_data / "Build" / "Products" / "Debug-iphonesimulator" / "doBBYVPN.app"
            app.mkdir(parents=True, exist_ok=True)
            return CommandResult(0)
        if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"]:
            (Path(cwd) / "DobbyVPNRuntime.xcframework").mkdir(parents=True, exist_ok=True)
            return CommandResult(0)
        if command and command[0] == "./gradlew":
            framework = Path(cwd) / "app" / "build" / "bin" / "iosX64" / "debugFramework" / "app.framework"
            framework.mkdir(parents=True, exist_ok=True)
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
        for name in ("go_module", "kmp_module", "swift_module"):
            (self.candidate / name).mkdir(parents=True, exist_ok=True)
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

    def test_device_selection_and_architecture_tasks_are_small_and_deterministic(self) -> None:
        selected = select_available_iphone(json.dumps(inventory()))
        self.assertEqual((selected.name, selected.udid, selected.runtime), (
            "iPhone 17", UDID, "com.apple.CoreSimulator.SimRuntime.iOS-26-2",
        ))
        self.assertEqual(_ios_test_tasks("amd64"), (":app:linkDebugFrameworkIosX64", ":app:iosX64Test"))
        self.assertEqual(_ios_test_tasks("arm64"), (
            ":app:linkDebugFrameworkIosSimulatorArm64", ":app:iosSimulatorArm64Test",
        ))

    def test_mini_build_is_explicitly_non_metal_and_native_arch(self) -> None:
        command = xcodebuild_app_command(
            self.contract, candidate_root=self.candidate, device_udid=UDID,
            work_dir=self.root / "work", mode="mini",
        )
        self.assertIn("ARCHS=x86_64", command)
        self.assertIn("SWIFT_ACTIVE_COMPILATION_CONDITIONS=$(inherited) DOBBY_SIMULATOR_MINI", command)

    def test_mini_requires_fresh_startup_marker_and_cleans_up(self) -> None:
        work = self.root / "work"
        runner = FakeRunner(self.root)
        diagnostics = self.root / "logs" / "ios"
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate,
            work_dir=work,
            runner=runner,
            mode="mini",
            contract=self.contract,
            diagnostic_dir=diagnostics,
        )
        self.assertEqual(evidence.mode, "mini")
        self.assertTrue(evidence.app.app_path.is_dir())
        self.assertNotIn(metal_probe_command(), runner.commands)
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "install"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "launch"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))
        self.assertIn(b"startup.initialized mode=mini", (diagnostics / "app_logs.txt").read_bytes())
        self.assertEqual((diagnostics / "go_app_logs.jsonl").read_bytes(), b"go log\n")

    def test_mini_fails_without_new_startup_marker_but_still_shuts_down(self) -> None:
        runner = FakeRunner(
            self.root,
            write_startup_marker=False,
            app_logs=b'{"message":"startup.initialized mode=mini"}\n',
        )
        with patch("torturer_checks.ios_simulator_app._STARTUP_WAIT_SECONDS", 0.01):
            with self.assertRaisesRegex(IOSSimulatorAppContractError, "did not write startup.initialized"):
                run_ios_simulator_app_contract(
                    candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                    mode="mini", contract=self.contract,
                )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_metal_contract_builds_launches_and_checks_view_attachment(self) -> None:
        runner = FakeRunner(self.root, startup_mode="metal")
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            mode="metal", contract=self.contract,
        )
        self.assertEqual(evidence.mode, "metal")
        self.assertEqual(runner.commands[0], metal_probe_command())
        self.assertTrue(evidence.app.app_path.is_dir())
        self.assertTrue(any(command[:2] == ["xcodebuild", "build"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "install"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "launch"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_metal_capability_failure_stops_before_simulator_setup(self) -> None:
        runner = FakeRunner(self.root, fail={
            ("swift", "-e"): CommandResult(0, "false\n"),
        })
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "usable Metal"):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                mode="metal", contract=self.contract,
            )
        self.assertEqual(runner.commands, [metal_probe_command()])

    def test_metal_launch_failure_shuts_down_simulator(self) -> None:
        runner = FakeRunner(self.root, fail={
            ("xcrun", "simctl", "launch"): CommandResult(1, stderr="app failed to launch"),
        }, startup_mode="metal")
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "app failed to launch"):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                mode="metal", contract=self.contract,
            )
        self.assertFalse(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_metal_requires_fresh_view_attachment_marker(self) -> None:
        runner = FakeRunner(
            self.root,
            startup_mode="metal",
            write_startup_marker=False,
            app_logs=b'{"message":"startup.ui_attached mode=normal"}\n',
        )
        with patch("torturer_checks.ios_simulator_app._STARTUP_WAIT_SECONDS", 0.01):
            with self.assertRaisesRegex(IOSSimulatorAppContractError, "did not attach its main view"):
                run_ios_simulator_app_contract(
                    candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                    mode="metal", contract=self.contract,
                )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_metal_startup_check_requires_app_log_container(self) -> None:
        runner = FakeRunner(self.root, startup_mode="metal", fail={
            ("xcrun", "simctl", "get_app_container"): CommandResult(1, stderr="not available"),
        })
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "not available"):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                mode="metal", contract=self.contract,
                diagnostic_dir=self.root / "diagnostics",
            )
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_prepare_builds_native_frameworks_and_runs_kmp_tests(self) -> None:
        runner = FakeRunner(self.root)
        work = self.root / "work"
        prepare_ios_simulator_candidate(
            candidate_root=self.candidate, work_dir=work, runner=runner,
            contract=self.contract, budget=RunBudget(),
        )
        go_build = next(command for command in runner.commands if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"])
        self.assertEqual(go_build[-2:], ["--simulator-architecture", "amd64"])
        gradle = next(command for command in runner.commands if command and command[0] == "./gradlew")
        self.assertIn(":app:iosX64Test", gradle)
        timeout = next(timeout for command, _, timeout in runner.calls if command == gradle)
        self.assertEqual(timeout, IOS_KMP_BUILD_TIMEOUT_SECONDS)
        self.assertFalse(any(command[:2] == ["xcodebuild", "build"] for command in runner.commands))
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
            runner.run((sys.executable, "-c", child), timeout_seconds=0.1)
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
