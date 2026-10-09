from __future__ import annotations

import importlib.util
import io
import json
from pathlib import Path
import contextlib
import sys
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import ios_simulator_app, local_vm, local_vm_ios
from torturer_runner.native_cases import (
    IOS_LOGS_FREEZE_RESUME_CASE,
    IOS_RENDERER_SEVERITY_CASE,
)

_ENTRYPOINT_PATH = Path(__file__).with_name("run_app_contract.py")
_ENTRYPOINT_SPEC = importlib.util.spec_from_file_location(
    "ios_simulator_run_app_contract", _ENTRYPOINT_PATH
)
assert _ENTRYPOINT_SPEC is not None and _ENTRYPOINT_SPEC.loader is not None
IOS_RUNNER_ENTRYPOINT = importlib.util.module_from_spec(_ENTRYPOINT_SPEC)
_ENTRYPOINT_SPEC.loader.exec_module(IOS_RUNNER_ENTRYPOINT)


class IOSSimulatorBoundaryTests(unittest.TestCase):
    def test_named_text_diagnostics_remain_compatible_with_screenshot_collection(self) -> None:
        from PIL import Image

        screenshot_buffer = io.BytesIO()
        Image.new("RGB", (1, 1), color="white").save(screenshot_buffer, format="PNG")
        screenshot_bytes = screenshot_buffer.getvalue()

        class ExportRunner:
            def run(self, command, *, cwd=None, timeout_seconds=None):
                del cwd, timeout_seconds
                output_dir = Path(command[command.index("--output-path") + 1])
                screenshot_name = "dobbyvpn-ui-scroll-check_0_01234567-89AB-CDEF-0123-456789ABCDEF.png"
                text_name = "dobbyvpn-ui-scroll-diagnostics_0_01234567-89AB-CDEF-0123-456789ABCDEF.txt"
                (output_dir / screenshot_name).write_bytes(screenshot_bytes)
                (output_dir / text_name).write_text("scroll geometry", encoding="utf-8")
                (output_dir / "manifest.json").write_text(
                    json.dumps([{
                        "testName": "NativeUIInteractionTests/testLogsFreezeAndResumeAtBottom",
                        "attachments": [
                            {
                                "suggestedHumanReadableName": screenshot_name,
                                "exportedFileName": screenshot_name,
                            },
                            {
                                "suggestedHumanReadableName": text_name,
                                "exportedFileName": text_name,
                            },
                        ],
                    }]),
                    encoding="utf-8",
                )
                return ios_simulator_app.CommandResult(0, "", "")

        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            result_bundle = root / "xctest-results.xcresult"
            result_bundle.mkdir()
            work_dir = root / "work"
            work_dir.mkdir()
            retained = ios_simulator_app._retain_xctest_screenshots(
                ExportRunner(),
                result_bundle,
                work_dir,
                budget=ios_simulator_app.RunBudget(),
            )
            self.assertEqual(retained.name, "ui-screenshots")
            self.assertEqual((retained / "scroll-check.png").read_bytes(), screenshot_bytes)
            self.assertEqual(
                sorted(path.name for path in retained.iterdir()), ["scroll-check.png"]
            )

    def test_disposable_simulator_creation_uses_unique_name_and_selected_device_type(self) -> None:
        selected = ios_simulator_app.AvailableSimulator(
            udid="89ABCDEF-0123-4567-89AB-CDEF01234567",
            name="iPhone 17",
            runtime="com.apple.CoreSimulator.SimRuntime.iOS-26-2",
            device_type_identifier="com.apple.CoreSimulator.SimDeviceType.iPhone-17",
        )
        runner = mock.Mock()
        runner.run.return_value = ios_simulator_app.CommandResult(
            0, "01234567-89ab-cdef-0123-456789abcdef\n", ""
        )

        simulator = ios_simulator_app._create_disposable_simulator(
            selected,
            runner=runner,
            budget=ios_simulator_app.RunBudget(
                max_seconds=300,
                cleanup_reserve_seconds=120,
            ),
        )

        command = runner.run.call_args.args[0]
        self.assertEqual(command[:3], ["xcrun", "simctl", "create"])
        self.assertRegex(command[3], r"\ADobbyVPN Torturer [0-9a-f]{12}\Z")
        self.assertEqual(command[4:], [selected.device_type_identifier, selected.runtime])
        self.assertEqual(simulator.udid, "01234567-89AB-CDEF-0123-456789ABCDEF")
        self.assertTrue(simulator.temporary)

    def test_ci_entrypoint_passes_candidate_sha_into_build_and_ui_test(self) -> None:
        source_sha = "a" * 40
        contract = object()
        runner = object()
        budget = object()
        evidence = SimpleNamespace(
            simulator=SimpleNamespace(name="iPhone", runtime="iOS 18"),
            native_log_collection_error=None,
        )
        args = [
            "--candidate-root", "candidate",
            "--work-dir", "work",
            "--source-sha", source_sha,
        ]

        with (
            mock.patch.object(IOS_RUNNER_ENTRYPOINT, "public_ios_simulator_app_contract", return_value=contract),
            mock.patch.object(IOS_RUNNER_ENTRYPOINT, "SubprocessCommandRunner", return_value=runner),
            mock.patch.object(IOS_RUNNER_ENTRYPOINT, "RunBudget", return_value=budget),
            mock.patch.object(IOS_RUNNER_ENTRYPOINT, "prepare_ios_simulator_candidate") as prepare,
            mock.patch.object(IOS_RUNNER_ENTRYPOINT, "run_ios_simulator_app_contract", return_value=evidence) as run,
            contextlib.redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(IOS_RUNNER_ENTRYPOINT.main(args), 0)

        self.assertEqual(prepare.call_args.kwargs["source_sha"], source_sha)
        self.assertEqual(run.call_args.kwargs["source_sha"], source_sha)
        self.assertEqual(prepare.call_args.kwargs["contract"], contract)
        workflow = (_ENTRYPOINT_PATH.parents[4] / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        invocation = workflow.split("python3 torturer/tests/platform/ios_simulator/run_app_contract.py", 1)[1].split("\n\n", 1)[0]
        self.assertIn('--source-sha "$GITHUB_SHA"', invocation)

    def test_ci_entrypoint_retains_early_failure_in_uploaded_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            work_dir = Path(name) / "work"
            failure = ios_simulator_app.IOSSimulatorStageError("package-ios-app", "xcodebuild failed")
            stderr = io.StringIO()
            args = [
                "--candidate-root", str(Path(name) / "candidate"),
                "--work-dir", str(work_dir),
                "--source-sha", "b" * 40,
            ]
            with (
                mock.patch.object(IOS_RUNNER_ENTRYPOINT, "prepare_ios_simulator_candidate", side_effect=failure),
                mock.patch.object(IOS_RUNNER_ENTRYPOINT, "run_ios_simulator_app_contract") as run,
                contextlib.redirect_stderr(stderr),
            ):
                self.assertEqual(IOS_RUNNER_ENTRYPOINT.main(args), 1)

            report = work_dir / "diagnostics/ios-simulator/runner-failure.txt"
            self.assertTrue(report.is_file())
            self.assertIn("xcodebuild failed", report.read_text(encoding="utf-8"))
            self.assertIn("xcodebuild failed", stderr.getvalue())
            run.assert_not_called()
            workflow = (_ENTRYPOINT_PATH.parents[4] / ".github/workflows/ci.yml").read_text(encoding="utf-8")
            self.assertIn("if: always()", workflow)
            self.assertIn("ios-simulator-mini-contract/diagnostics", workflow)

    def test_early_failure_report_is_nonempty_and_keeps_exception_notes(self) -> None:
        failure = ios_simulator_app.IOSSimulatorStageError("install", "simctl failed")
        failure.add_note("command_stdout:\ninstall stdout")
        failure.add_note("command_stderr:\ninstall stderr")

        with tempfile.TemporaryDirectory() as name:
            work_dir = Path(name)
            report = ios_simulator_app.retain_ios_failure_diagnostic(work_dir, failure)
            content = report.read_text(encoding="utf-8")
            retained = ios_simulator_app.retain_ios_diagnostics(
                work_dir, work_dir / "collected"
            )

        self.assertTrue(content.strip())
        self.assertIn(
            "IOSSimulatorStageError: iOS Simulator stage 'install' failed: simctl failed",
            content,
        )
        self.assertIn("command_stdout:\ninstall stdout", content)
        self.assertIn("command_stderr:\ninstall stderr", content)
        self.assertEqual([path.name for path in retained], ["runner-failure.txt"])

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
        self.assertIn(
            "-only-testing:iosAppUITests/NativeUIInteractionTests",
            run,
        )
        self.assertIn(
            "-only-testing:iosAppUITests/NativeRendererInteractionTests/"
            "testSeverityColorsResolveForLightAndDarkAppearances",
            run,
        )
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

    def test_ios_native_case_selectors_filter_to_exact_tests(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
        command = ios_simulator_app.xcodebuild_ui_test_without_building_command(
            udid,
            Path("candidate/iosApp.xcodeproj"),
            Path("work/derived-data"),
            architecture="arm64",
            native_cases=[IOS_LOGS_FREEZE_RESUME_CASE],
        )
        self.assertEqual(command[0], "xcodebuild")
        filters = [argument for argument in command if argument.startswith("-only-testing:")]
        self.assertEqual(filters, [
            "-only-testing:iosAppUITests/NativeUIInteractionTests/"
            "testLogsFreezeAndResumeAtBottom",
            "-only-testing:iosAppUITests/NativeUIInteractionTests/"
            "testNativeConnectionAboutAndLogs",
        ])
        severity = ios_simulator_app.xcodebuild_ui_test_without_building_command(
            udid,
            Path("candidate/iosApp.xcodeproj"),
            Path("work/derived-data"),
            architecture="arm64",
            native_cases=[IOS_RENDERER_SEVERITY_CASE],
        )
        self.assertEqual(
            [argument for argument in severity if argument.startswith("-only-testing:")],
            ["-only-testing:iosAppUITests/NativeRendererInteractionTests/"
             "testSeverityColorsResolveForLightAndDarkAppearances"],
        )

    def test_ios_test_bundle_embeds_candidate_source_commit_for_about_assertion(self) -> None:
        project = (
            Path(__file__).parents[4]
            / "ui/apple/ios/iosApp.xcodeproj/project.pbxproj"
        ).read_text(encoding="utf-8")
        ui_test = (
            Path(__file__).parents[4]
            / "ui/apple/ios/tests/NativeUIInteractionTests.swift"
        ).read_text(encoding="utf-8")
        self.assertEqual(project.count("INFOPLIST_FILE = tests/Info.plist;"), 2)
        test_info = (
            Path(__file__).parents[4] / "ui/apple/ios/tests/Info.plist"
        ).read_text(encoding="utf-8")
        self.assertIn("<key>DobbyTestSourceCommit</key>", test_info)
        self.assertIn("$(DOBBY_SOURCE_COMMIT)", test_info)
        self.assertIn('object(forInfoDictionaryKey: "DobbyTestSourceCommit")', ui_test)

    def test_install_timeout_reports_elapsed_time_and_preserves_cleanup_window(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
        open_command = [
            "/usr/bin/open", "-a", "Simulator", "--args",
            "-CurrentDeviceUDID", udid.upper(),
        ]

        class Runner:
            def __init__(self, clock, commands, diagnostic_mode, simlaunch_arch):
                self.clock = clock
                self.commands = commands
                self.diagnostic_mode = diagnostic_mode
                self.simlaunch_arch = simlaunch_arch

            def run(self, command, *, cwd=None, timeout_seconds=None):
                del cwd
                arguments = list(command)
                self.commands.append((arguments, timeout_seconds))
                if arguments[:4] == ["xcrun", "simctl", "list", "devices"]:
                    inventory = {"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-17-5": [{
                        "isAvailable": True,
                        "name": "iPhone 15",
                        "state": "Booted",
                        "udid": udid,
                    }]}}
                    return ios_simulator_app.CommandResult(0, json.dumps(inventory), "")
                if arguments[:2] == ["/bin/df", "-Pk"]:
                    return ios_simulator_app.CommandResult(0, "disk0 100 40 60 40%\n", "")
                if arguments[:2] == ["/usr/bin/du", "-sk"]:
                    return ios_simulator_app.CommandResult(0, "15360\tDobby-Vpn-Simulator.app\n", "")
                if arguments[:3] == ["xcrun", "--sdk", "iphonesimulator"]:
                    return ios_simulator_app.CommandResult(0, "17.5", "")
                if arguments == open_command:
                    self.open_simulator_timeout = timeout_seconds
                    return ios_simulator_app.CommandResult(0, "", "")
                if arguments[:3] == ["xcrun", "simctl", "install"]:
                    self.install_timeout = timeout_seconds
                    self.clock[0] += (timeout_seconds or 0) + ios_simulator_app.COMMAND_TERMINATION_RESERVE_SECONDS
                    error = ios_simulator_app.IOSSimulatorAppContractError(
                        f"iOS command timed out after {timeout_seconds:g}s"
                    )
                    error.stdout = b"install stdout\x00\xff"
                    error.stderr = b"install stderr\n"
                    raise error
                if arguments[:2] == ["/bin/ps", "-A"]:
                    self.clock[0] += timeout_seconds or 0
                    if self.diagnostic_mode == "ps-failure":
                        return ios_simulator_app.CommandResult(
                            7, "process snapshot\x00\xff\n", "ps diagnostic stderr\n"
                        )
                    simulator_service = (
                        "17145 1 S 08:18 /Library/Developer/PrivateFrameworks/CoreSimulator.framework/Versions/A/XPCServices/com.apple.CoreSimulator.CoreSimulatorService.xpc/Contents/MacOS/com.apple.CoreSimulator.CoreSimulatorService",
                    )
                    if self.diagnostic_mode == "ambiguous":
                        simulator_service += (
                            "17146 1 S 08:18 /Library/Developer/PrivateFrameworks/CoreSimulator.framework/Versions/A/XPCServices/com.apple.CoreSimulator.CoreSimulatorService.xpc/Contents/MacOS/com.apple.CoreSimulator.CoreSimulatorService",
                        )
                    return ios_simulator_app.CommandResult(
                        0,
                        "\n".join((
                            "  PID PPID STAT ELAPSED COMM",
                            *simulator_service,
                            f"19487 1 S 07:17 /Library/Developer/PrivateFrameworks/CoreSimulator.framework/Versions/A/XPCServices/SimLaunchHost.{self.simlaunch_arch}.xpc/Contents/MacOS/SimLaunchHost.{self.simlaunch_arch}",
                            "20765 19864 Ss 05:44 /Library/Developer/CoreSimulator/Volumes/iOS_23C54/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.2.simruntime/Contents/Resources/RuntimeRoot/usr/libexec/installd",
                            "20792 19864 Ss 05:43 /Library/Developer/CoreSimulator/Volumes/iOS_23C54/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.2.simruntime/Contents/Resources/RuntimeRoot/System/Library/PrivateFrameworks/MobileInstallation.framework/XPCServices/com.apple.MobileInstallationHelperService.xpc/com.apple.MobileInstallationHelperService",
                            "606 1 Ss 19:48 /System/Library/PrivateFrameworks/PackageKit.framework/Resources/installd",
                        )),
                        "ps diagnostic stderr\n",
                    )
                if arguments[:1] == ["/usr/bin/sample"]:
                    self.clock[0] += timeout_seconds or 0
                    if self.diagnostic_mode == "sample-denied" and arguments[1] == "19487":
                        return ios_simulator_app.CommandResult(
                            7, "sample denied stdout\x00\xff", "sample denied stderr\n"
                        )
                    return ios_simulator_app.CommandResult(
                        0, f"sample stdout pid={arguments[1]}\x00\xff", "sample stderr\n"
                    )
                if arguments[:3] == ["/usr/bin/log", "show", "--style"]:
                    self.clock[0] += timeout_seconds or 0
                    return ios_simulator_app.CommandResult(
                        0, "installd diagnostic log\x00\n", "unified log stderr\n"
                    )
                if arguments[:3] == ["xcrun", "simctl", "shutdown"]:
                    self.shutdown_timeout = timeout_seconds
                    return ios_simulator_app.CommandResult(0, "", "")
                if arguments[:3] in (
                    ["xcrun", "simctl", "boot"],
                    ["xcrun", "simctl", "bootstatus"],
                ):
                    return ios_simulator_app.CommandResult(0, "", "")
                raise AssertionError(f"unexpected command: {arguments}")

        for lane_seconds, expected_install_timeout, diagnostic_mode, simlaunch_arch in (
            (345, 180, "budget-clamped", "arm64"),
            (600, 300, "samples", "arm64"),
            (600, 300, "sample-denied", "arm64"),
            (600, 300, "ambiguous", "arm64"),
            (600, 300, "samples", "x86_64"),
            (600, 300, "ps-failure", "arm64"),
        ):
            with self.subTest(
                mode=diagnostic_mode,
                architecture=simlaunch_arch,
                lane_seconds=lane_seconds,
            ):
                clock = [0.0]
                commands: list[tuple[list[str], float | None]] = []
                runner = Runner(clock, commands, diagnostic_mode, simlaunch_arch)
                with tempfile.TemporaryDirectory() as name:
                    root = Path(name)
                    work_dir = root / "work"
                    contract = ios_simulator_app.PUBLIC_IOS_SIMULATOR_APP_CONTRACT
                    contract.app_path(work_dir).mkdir(parents=True)
                    (root / "candidate" / "ui" / "apple" / "ios" / "iosApp.xcodeproj").mkdir(parents=True)
                    budget = ios_simulator_app.RunBudget(
                        max_seconds=lane_seconds,
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
                                native_cases=[IOS_LOGS_FREEZE_RESUME_CASE],
                            )
                    failure_report = ios_simulator_app.retain_ios_failure_diagnostic(
                        work_dir, caught.exception
                    )
                    report = failure_report.read_text(encoding="utf-8")

                failure = caught.exception
                self.assertEqual(failure.stage, "install")
                self.assertEqual(failure.timeout_seconds, expected_install_timeout)
                self.assertEqual(failure.__cause__.stdout, b"install stdout\x00\xff")
                self.assertEqual(failure.__cause__.stderr, b"install stderr\n")
                self.assertIsNotNone(failure.elapsed_seconds)
                self.assertIn("elapsed", str(failure))
                notes = "\n".join(failure.__notes__)
                for expected in (
                    "install stdout\x00\\xff",
                    "install stderr",
                    "15360\tDobby-Vpn-Simulator.app",
                    f"install_preflight_simulator_state_{udid.upper()}_stdout:",
                    "install_preflight_disk_free_stdout:",
                ):
                    self.assertIn(expected, notes)
                self.assertEqual(runner.install_timeout, expected_install_timeout)
                self.assertEqual(runner.open_simulator_timeout, 30)

                command_arguments = [arguments for arguments, _ in commands]
                def index(prefix):
                    return next(
                        i for i, arguments in enumerate(command_arguments)
                        if arguments[:len(prefix)] == prefix
                    )
                self.assertLess(index(open_command), index(["/bin/df", "-Pk"]))
                self.assertLess(index(["/bin/df", "-Pk"]), index(["/usr/bin/du", "-sk"]))
                self.assertLess(index(["/usr/bin/du", "-sk"]), index(["xcrun", "simctl", "list", "devices", "-j"]))
                self.assertLess(index(["xcrun", "simctl", "list", "devices", "-j"]), index(["xcrun", "simctl", "install"]))
                if diagnostic_mode != "budget-clamped":
                    self.assertLess(index(["xcrun", "simctl", "install"]), index(["/bin/ps", "-A"]))
                    self.assertLess(index(["/bin/ps", "-A"]), index(["/usr/bin/log", "show"]))
                    sample_calls = [
                        (arguments, timeout)
                        for arguments, timeout in commands
                        if arguments[:1] == ["/usr/bin/sample"]
                    ]
                    self.assertTrue(all(timeout == 5 for _, timeout in sample_calls))
                    self.assertIn("install_interval_utc=", notes)
                    self.assertIn("ps diagnostic stderr", notes)
                    self.assertIn("ps diagnostic stderr", report)
                    self.assertIn("unified log stderr", notes)
                    self.assertIn("unified log stderr", report)
                    self.assertIn("installd diagnostic log", report)
                    self.assertLess(index(["/usr/bin/log", "show"]), index(["xcrun", "simctl", "shutdown"]))
                    if diagnostic_mode == "ps-failure":
                        self.assertIn("process snapshot\x00ÿ", notes)
                        self.assertIn("process snapshot\x00ÿ", report)
                        self.assertIn("ps diagnostic stderr", notes)
                        self.assertIn("install_failure_processes_error=IOSSimulatorStageError", notes)
                        self.assertIn("exit code 7", notes)
                        self.assertIn("process snapshot was unavailable", notes)
                        self.assertFalse(sample_calls)
                        self.assertEqual(clock[0], 360)
                    else:
                        expected_pids = {"17145", "19487", "20765", "20792"}
                        if diagnostic_mode == "ambiguous":
                            expected_pids.remove("17145")
                            self.assertIn("expected one exact process; matches=", notes)
                            self.assertIn("17145", notes)
                            self.assertIn("17146", notes)
                        self.assertEqual(len(sample_calls), len(expected_pids))
                        self.assertEqual({arguments[1] for arguments, _ in sample_calls}, expected_pids)
                        self.assertEqual(len({arguments[1] for arguments, _ in sample_calls}), len(sample_calls))
                        self.assertTrue(all(arguments[2:] == ["1", "1", "-file", "/dev/stdout"] for arguments, _ in sample_calls))
                        self.assertLess(index(["/bin/ps", "-A"]), index(["/usr/bin/sample"]))
                        self.assertLess(index(["/usr/bin/sample"]), index(["/usr/bin/log", "show"]))
                        self.assertIn(f"SimLaunchHost.{simlaunch_arch}", notes)
                        if diagnostic_mode == "sample-denied":
                            self.assertIn("sample denied stdout\x00ÿ", notes)
                            self.assertIn("sample denied stderr", notes)
                            self.assertIn("exit code 7", notes)
                            self.assertIn("sample denied stdout\x00ÿ", report)
                            self.assertIn("sample denied stderr", report)
                            sampled_successfully = expected_pids - {"19487"}
                        else:
                            self.assertIn("sample stderr", notes)
                            self.assertIn("install_failure_sample_target_simulator-installd: pid=20765 command=", notes)
                            sampled_successfully = expected_pids
                        for pid in sampled_successfully:
                            self.assertIn(f"sample stdout pid={pid}\x00ÿ", notes)
                            self.assertIn(f"sample stdout pid={pid}\x00ÿ", report)
                        self.assertEqual(clock[0], 380 if diagnostic_mode != "ambiguous" else 375)
                else:
                    self.assertNotIn("sample stdout", notes)
                    self.assertFalse(any(arguments[0] == "/bin/ps" for arguments in command_arguments))
                    self.assertFalse(any(arguments[0] == "/usr/bin/sample" for arguments in command_arguments))
                    self.assertFalse(any(arguments[0] == "/usr/bin/log" for arguments in command_arguments))
                    self.assertIn("functional budget before cleanup reserve", notes)
                    self.assertEqual(clock[0], 225)
                self.assertIn("install stdout", report)
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
