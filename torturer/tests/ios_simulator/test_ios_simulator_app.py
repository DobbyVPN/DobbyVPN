from __future__ import annotations

import json
import os
import plistlib
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
    _validate_runtime_framework,
    validate_runtime_framework,
    IOSSimulatorStageError,
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
        lipo_arches: str = "arm64 x86_64",
        sdk_version: str = "26.2",
    ) -> None:
        self.root = root
        self.fail = fail or {}
        self.sdk_version = sdk_version
        # Framework fixtures intentionally contain non-Mach-O bytes. The
        # injected runner is the unit-test seam for macOS `xcrun lipo -archs`;
        # production validation never treats these bytes as proof.
        self.lipo_arches = lipo_arches
        self.commands: list[list[str]] = []
        self.calls: list[tuple[list[str], Path | None, float | None]] = []

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
        if command == ["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"]:
            return CommandResult(0, f"{self.sdk_version}\n")
        if command[:3] == ["xcrun", "lipo", "-archs"]:
            return CommandResult(0, self.lipo_arches)
        if command[:1] == ["xcodebuild"]:
            return CommandResult(0)
        if command[:2] == ["/usr/bin/defaults", "read"]:
            return CommandResult(1, stderr="The domain/default pair does not exist")
        if command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"]:
            architecture = "arm64" if command[-1] == "arm64" else "x86_64"
            framework = Path(cwd) / "DobbyVPNRuntime.xcframework"
            slice_path = framework / f"ios-{architecture}-simulator" / "DobbyVPNRuntime.framework"
            slice_path.mkdir(parents=True, exist_ok=True)
            (slice_path / "DobbyVPNRuntime").write_bytes(b"synthetic framework binary")
            with (framework / "Info.plist").open("wb") as output:
                plistlib.dump(
                    {
                        "AvailableLibraries": [{
                            "LibraryIdentifier": f"ios-{architecture}-simulator",
                            "LibraryPath": "DobbyVPNRuntime.framework",
                            "SupportedArchitectures": [architecture],
                            "SupportedPlatform": "ios",
                            "SupportedPlatformVariant": "simulator",
                        }],
                    },
                    output,
                )
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

    def make_runtime_framework(self, *, architectures: list[str] | None = None) -> Path:
        architectures = architectures or ["x86_64"]
        framework = self.root / "artifact" / "DobbyVPNRuntime.xcframework"
        slice_path = framework / "ios-x86_64-simulator" / "DobbyVPNRuntime.framework"
        slice_path.mkdir(parents=True, exist_ok=True)
        # This is a path fixture only. The FakeRunner's mocked lipo result,
        # not these bytes, supplies the Mach-O architecture proof.
        (slice_path / "DobbyVPNRuntime").write_bytes(b"synthetic framework binary")
        with (framework / "Info.plist").open("wb") as output:
            plistlib.dump(
                {
                    "AvailableLibraries": [{
                        "LibraryIdentifier": "ios-x86_64-simulator",
                        "LibraryPath": "DobbyVPNRuntime.framework",
                        "SupportedArchitectures": architectures,
                        "SupportedPlatform": "ios",
                        "SupportedPlatformVariant": "simulator",
                    }],
                },
                output,
            )
        return framework

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
        selected = select_available_iphone(json.dumps(inventory()), "26.2")
        self.assertEqual((selected.name, selected.udid, selected.runtime), (
            "iPhone 17", UDID, "com.apple.CoreSimulator.SimRuntime.iOS-26-2",
        ))
        command = xcodebuild_app_command(
            self.contract, candidate_root=self.candidate,
            work_dir=self.root / "work",
        )
        self.assertEqual(command[:4], ["/bin/bash", "scripts/package_ios_app.sh", "iossimulator", str(self.root / "work" / "derived-data" / "Build" / "Products" / "Release-iphonesimulator" / "Dobby-Vpn.app")])
        self.assertEqual(command[-1], "amd64")

    def test_device_selection_requires_the_sdk_major_minor(self) -> None:
        mismatched = {
            "devices": {
                "com.apple.CoreSimulator.SimRuntime.iOS-26-3": [{
                    "isAvailable": True,
                    "name": "iPhone 18",
                    "udid": UDID,
                }],
            },
        }
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            "no available iPhone Simulator matches active iphonesimulator SDK 26.2",
        ):
            select_available_iphone(json.dumps(mismatched), "26.2")

    def test_contract_queries_active_sdk_before_device_selection(self) -> None:
        runner = FakeRunner(self.root)
        run_ios_simulator_app_contract(
            candidate_root=self.candidate,
            work_dir=self.root / "work",
            runner=runner,
            contract=self.contract,
        )
        sdk_query = next(
            (command, timeout)
            for command, _, timeout in runner.calls
            if command == ["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"]
        )
        self.assertEqual(sdk_query[1], STAGE_TIMEOUT_SECONDS["read-sdk-version"])

    def test_simulator_build_is_single_contract_and_native_arch(self) -> None:
        command = xcodebuild_app_command(
            self.contract, candidate_root=self.candidate,
            work_dir=self.root / "work",
        )
        self.assertEqual(command[-1], "amd64")
        self.assertNotIn("kmp_module", command)

    def test_mini_runs_real_xctest_ui_contract_and_cleans_up(self) -> None:
        work = self.root / "work"
        runner = FakeRunner(self.root)
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate,
            work_dir=work,
            runner=runner,
            contract=self.contract,
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
        self.assertFalse((self.root / "logs").exists())

    def test_ui_test_failure_still_shuts_down(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={("xcodebuild",): CommandResult(1, stderr="XCTest accessibility failure")},
        )
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"stage 'xctest-ui'.*exit code 1",
        ) as raised:
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                contract=self.contract,
            )
        self.assertIn("command_stderr:\nXCTest accessibility failure", raised.exception.__notes__)
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

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
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"stage 'xctest-ui'.*exit code 1",
        ) as raised:
            run_ios_simulator_app_contract(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=runner,
                contract=self.contract,
            )
        self.assertNotIn("cleanup also failed", str(raised.exception))
        self.assertIn("command_stderr:\nXCTest accessibility failure", raised.exception.__notes__)
        self.assertTrue(any(
            command[:3] == ["xcrun", "simctl", "shutdown"]
            for command in runner.commands
        ))

    def test_xctest_timeout_is_stage_specific_without_retry(self) -> None:
        runner = FakeRunner(
            self.root,
            fail={
                ("xcodebuild",): IOSSimulatorAppContractError(
                    "iOS command timed out after 900s; stdout=compile complete; stderr=runner hung"
                )
            },
        )
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"stage 'xctest-ui' timed out after 900s",
        ):
            run_ios_simulator_app_contract(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=runner,
                contract=self.contract,
            )
        self.assertEqual(sum(command[:1] == ["xcodebuild"] for command in runner.commands), 1)
        xctest_timeout = next(timeout for command, _, timeout in runner.calls if command[:1] == ["xcodebuild"])
        self.assertEqual(xctest_timeout, 900)

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
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"stage 'xctest-ui'.*exit code 1",
        ) as raised:
            run_ios_simulator_app_contract(
                candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
                contract=self.contract,
            )
        self.assertIn("command_stderr:\napp failed to launch", raised.exception.__notes__)
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_ui_test_does_not_collect_app_logs(self) -> None:
        runner = FakeRunner(self.root)
        evidence = run_ios_simulator_app_contract(
            candidate_root=self.candidate, work_dir=self.root / "work", runner=runner,
            contract=self.contract,
        )
        self.assertTrue(evidence.app.app_path.is_dir())
        self.assertTrue(any(command[:1] == ["xcodebuild"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "terminate"] for command in runner.commands))
        self.assertTrue(any(command[:3] == ["xcrun", "simctl", "shutdown"] for command in runner.commands))

    def test_shared_runtime_validator_accepts_a_physical_ios_slice(self) -> None:
        framework = self.root / "device-artifact" / "DobbyVPNRuntime.xcframework"
        slice_path = framework / "ios-arm64" / "DobbyVPNRuntime.framework"
        slice_path.mkdir(parents=True)
        (slice_path / "DobbyVPNRuntime").write_bytes(b"synthetic framework binary")
        with (framework / "Info.plist").open("wb") as output:
            plistlib.dump(
                {
                    "AvailableLibraries": [{
                        "LibraryIdentifier": "ios-arm64",
                        "LibraryPath": "DobbyVPNRuntime.framework",
                        "SupportedArchitectures": ["arm64"],
                        "SupportedPlatform": "ios",
                    }],
                },
                output,
            )
        self.assertEqual(
            validate_runtime_framework(
                framework,
                "arm64",
                platform_variant="device",
                runner=FakeRunner(self.root),
            ),
            framework.resolve(),
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

    def test_prepare_reuses_a_valid_hosted_framework_without_rebuilding(self) -> None:
        runner = FakeRunner(self.root)
        runtime_framework = self.make_runtime_framework()
        prepare_ios_simulator_candidate(
            candidate_root=self.candidate,
            work_dir=self.root / "work",
            runner=runner,
            contract=self.contract,
            budget=RunBudget(),
            runtime_framework=runtime_framework,
        )
        self.assertFalse(any(
            command[:2] == ["/bin/bash", "scripts/build_ios_xcframework.sh"]
            for command in runner.commands
        ))
        package = next(
            command for command in runner.commands
            if command[:2] == ["/bin/bash", "scripts/package_ios_app.sh"]
        )
        self.assertEqual(package[4], str(runtime_framework.resolve()))
        self.assertTrue((self.root / "work" / "derived-data" / "Build" / "Products" / "Release-iphonesimulator" / "Dobby-Vpn.app").is_dir())

    def test_supplied_framework_must_contain_requested_simulator_architecture(self) -> None:
        runtime_framework = self.make_runtime_framework(architectures=["arm64"])
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"validate-ios-framework.*x86_64",
        ):
            prepare_ios_simulator_candidate(
                candidate_root=self.candidate,
                work_dir=self.root / "work",
                runner=FakeRunner(self.root),
                contract=self.contract,
                budget=RunBudget(),
                runtime_framework=runtime_framework,
            )

    def test_supplied_framework_must_contain_real_binary_architecture(self) -> None:
        runtime_framework = self.make_runtime_framework()
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            r"does not contain the required x86_64 architecture",
        ):
            _validate_runtime_framework(
                runtime_framework,
                "amd64",
                runner=FakeRunner(self.root, lipo_arches="arm64"),
            )

    def test_supplied_framework_rejects_binary_symlink_escape(self) -> None:
        runtime_framework = self.make_runtime_framework()
        binary = (
            runtime_framework
            / "ios-x86_64-simulator"
            / "DobbyVPNRuntime.framework"
            / "DobbyVPNRuntime"
        )
        outside = self.root / "outside-binary"
        outside.write_bytes(b"not a Mach-O")
        binary.unlink()
        binary.symlink_to(outside)
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            "framework binary escapes",
        ):
            _validate_runtime_framework(
                runtime_framework, "amd64", runner=FakeRunner(self.root)
            )

    def test_supplied_framework_rejects_metadata_path_traversal(self) -> None:
        runtime_framework = self.make_runtime_framework()
        with (runtime_framework / "Info.plist").open("wb") as output:
            plistlib.dump(
                {
                    "AvailableLibraries": [{
                        "LibraryIdentifier": "ios-x86_64-simulator",
                        "LibraryPath": "../outside/DobbyVPNRuntime.framework",
                        "SupportedArchitectures": ["x86_64"],
                        "SupportedPlatform": "ios",
                        "SupportedPlatformVariant": "simulator",
                    }],
                },
                output,
            )
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "safe relative path"):
            _validate_runtime_framework(
                runtime_framework, "amd64", runner=FakeRunner(self.root)
            )

    def test_supplied_framework_wraps_symlink_resolution_errors(self) -> None:
        runtime_framework = self.make_runtime_framework()
        slice_path = runtime_framework / "ios-x86_64-simulator"
        framework_path = slice_path / "DobbyVPNRuntime.framework"
        shutil.rmtree(framework_path)
        framework_path.symlink_to(slice_path / "loop-a")
        (slice_path / "loop-a").symlink_to(framework_path)
        with self.assertRaisesRegex(
            IOSSimulatorAppContractError,
            "could not be resolved",
        ):
            _validate_runtime_framework(
                runtime_framework, "amd64", runner=FakeRunner(self.root)
            )

    def test_runtime_framework_validation_requires_xcframework_metadata(self) -> None:
        runtime_framework = self.root / "missing-metadata.xcframework"
        runtime_framework.mkdir()
        with self.assertRaisesRegex(IOSSimulatorAppContractError, "metadata"):
            _validate_runtime_framework(runtime_framework, "amd64")

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
