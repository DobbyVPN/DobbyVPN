from __future__ import annotations

import importlib.util
from contextlib import redirect_stdout
from io import StringIO
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from torturer_checks import ios_simulator_app, local_vm, local_vm_macos
from torturer_checks.hosted.cli import CommandResult
from torturer_checks.hosted.macos import MacOSHostedAdapter


PRODUCT_ROOT = Path(__file__).resolve().parents[2]


def _load_smoke_script():
    path = PRODUCT_ROOT / ".github/scripts/desktop/native_ui_smoke.py"
    spec = importlib.util.spec_from_file_location("dobbyvpn_native_ui_smoke_apple_test", path)
    if spec is None or spec.loader is None:
        raise AssertionError(f"could not load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class ApplePreflightTests(unittest.TestCase):
    def test_hosted_macos_discovers_uplink_for_routing_proof(self) -> None:
        commands: list[tuple[str, ...]] = []

        class Runner:
            def run(self, command, *, timeout_seconds):
                self.asserted_timeout = timeout_seconds
                arguments = tuple(command)
                commands.append(arguments)
                if arguments[0] == "/usr/bin/dscacheutil":
                    return CommandResult(arguments, 0, b"name: api.ipify.org\nip_address: 104.26.12.205\n")
                if arguments[0] == "/sbin/route":
                    return CommandResult(arguments, 0, b"route to: 104.26.12.205\ninterface: en0\n")
                raise AssertionError(arguments)

        adapter = object.__new__(MacOSHostedAdapter)
        adapter.runner = Runner()
        adapter.identity_url = "https://api.ipify.org"
        adapter.network_interface = None
        adapter._resolve_routing_probe(5.0)
        self.assertEqual(adapter.network_interface, "en0")
        self.assertEqual(commands[-1], ("/sbin/route", "-n", "get", "104.26.12.205"))

    def test_accessibility_probe_gets_longer_timeout_than_other_aqua_probes(self) -> None:
        responses = {
            "scutil": b"kCGSSessionUserNameKey : tester\nkCGSSessionUserIDKey : 501\n",
            "launchctl": b"gui session ready\n",
            "ioreg": b"Root\n    | |   \"CGSSessionScreenIsLocked\" = No\n",
            "osascript": b"Finder\n",
        }

        def run(command, **kwargs):
            return subprocess.CompletedProcess(
                command,
                0,
                responses[command[0]],
                b"",
            )

        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="tester"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=run) as logged,
            ):
                self.assertEqual(
                    local_vm_macos.preflight_interactive_desktop(
                        run_dir=root,
                        logs=root / "logs",
                        timeout=300,
                    ),
                    ("tester", 501),
                )

        self.assertEqual(
            [call.kwargs["label"] for call in logged.call_args_list],
            [
                "macos-ui-console",
                "macos-ui-session",
                "macos-ui-screen",
                "macos-ui-accessibility",
            ],
        )
        self.assertEqual(
            [call.kwargs["timeout"] for call in logged.call_args_list],
            [5.0, 5.0, 5.0, 30.0],
        )

    def test_macos_service_waits_for_connectable_socket_and_confirms_launchd_pid(self) -> None:
        completed = subprocess.CompletedProcess(
            ["launchctl", "print"], 0, b"pid = 418\n", b"",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            control_socket = root / "control.sock"
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as listener:
                listener.bind(str(control_socket))
                listener.listen(1)
                listener.settimeout(1)
                with (
                    mock.patch.object(local_vm, "_run_logged", return_value=completed) as run,
                    mock.patch.object(Path, "is_socket", side_effect=PermissionError("lstat denied")),
                ):
                    local_vm._wait_macos_service(
                        418, control_socket, root, root / "logs", timeout=1,
                    )
                connection, _ = listener.accept()
                connection.close()

        self.assertEqual(run.call_args.args[0], ["launchctl", "print", "system/com.dobby.vpnservice"])
        self.assertEqual(run.call_args.kwargs["label"], "service-ready")

    def test_macos_service_readiness_timeout_keeps_last_socket_diagnostic(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with mock.patch.object(local_vm, "_run_logged") as run:
                with self.assertRaises(local_vm.LocalVMError) as caught:
                    local_vm._wait_macos_service(
                        418, root / "missing.sock", root, root / "logs", timeout=0.02,
                    )

        self.assertIn("did not become ready", str(caught.exception))
        self.assertIn("FileNotFoundError", str(caught.exception))
        run.assert_not_called()

    def test_source_and_release_macos_start_wait_for_service_readiness(self) -> None:
        completed = subprocess.CompletedProcess(
            ["launchctl", "print"], 0, b"pid = 418\n", b"",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            for release in (False, True):
                run_dir = root / ("release" if release else "source")
                logs = run_dir / "logs"
                service = run_dir / "candidate" / "dobbyvpn-backend"
                service.parent.mkdir(parents=True)
                if release:
                    service.touch()
                else:
                    plist = run_dir / "source" / "apple/installer/vpnservice.plist"
                    plist.parent.mkdir(parents=True)
                    plist.touch()
                descriptor = {
                    "service": str(service),
                    "network": str(run_dir / "candidate" / "network"),
                }
                start = local_vm._start_macos_release if release else local_vm._start_macos
                with (
                    mock.patch.object(local_vm, "_run_logged", return_value=completed),
                    mock.patch.object(local_vm, "_wait_macos_service") as wait,
                ):
                    runtime = start(run_dir, descriptor, logs, 12, "en0")

                self.assertEqual(runtime["pid"], 418)
                wait.assert_called_once_with(
                    418, Path("/var/run/dobbyvpn/control.sock"), run_dir, logs, 12,
                )

    def test_appkit_compile_is_bounded_at_sixty_seconds(self) -> None:
        smoke = _load_smoke_script()
        completed = subprocess.CompletedProcess(
            ["clang"], 1, stdout=b"compiler stdout\n", stderr=b"compiler stderr\n",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "probe.m"
            source.write_text("// synthetic test source\n", encoding="utf-8")
            with (
                mock.patch.object(smoke, "_MACOS_REFERENCE_EVENT_HELPER_SOURCE", source),
                mock.patch.object(smoke.shutil, "which", return_value="/usr/bin/clang"),
                mock.patch.object(smoke, "_native_run", return_value=completed) as run,
            ):
                with self.assertRaises(smoke.NativeUISmokeError) as caught:
                    smoke._macos_build_reference_event_helper(root / "temporary")

        self.assertEqual(run.call_args.kwargs["timeout"], 60)
        self.assertIn("compiler stdout", str(caught.exception))
        self.assertIn("compiler stderr", str(caught.exception))

    def test_native_smoke_preflight_defaults_to_ninety_seconds(self) -> None:
        smoke = _load_smoke_script()
        with (
            mock.patch.object(smoke, "preflight_macos_capabilities", return_value="ready") as preflight,
            redirect_stdout(StringIO()),
        ):
            self.assertEqual(
                smoke.main(["--platform", "macos", "--preflight-only"]),
                0,
            )

        preflight.assert_called_once_with(90.0)

    def test_capability_compile_failure_keeps_non_aqua_classification_and_streams(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            script = root / ".github" / "scripts" / "desktop" / "native_ui_smoke.py"
            script.parent.mkdir(parents=True)
            script.write_text("# test\n", encoding="utf-8")
            stdout = b"preflight stdout\n"
            stderr = b"AppKit compile timed out; complete compiler diagnostic\n"
            completed = subprocess.CompletedProcess(["python"], 1, stdout, stderr)
            with mock.patch.object(local_vm_macos, "_run_logged", return_value=completed) as run:
                with self.assertRaises(
                    local_vm_macos.MacOSNativeCapabilityPreflightFailed
                ) as caught:
                    local_vm_macos.preflight_native_ui_capabilities(
                        script, run_dir=root, logs=root / "logs", timeout=30,
                    )

        self.assertEqual(
            caught.exception.reason_code,
            "MACOS_NATIVE_UI_CAPABILITY_PREFLIGHT_FAILED",
        )
        self.assertNotEqual(
            caught.exception.reason_code,
            local_vm_macos.MacOSInteractiveDesktopUnavailable.reason_code,
        )
        self.assertIn(stderr.decode(), str(caught.exception))
        self.assertIn(stdout.decode(), "\n".join(caught.exception.__notes__))
        self.assertEqual(run.call_args.kwargs["timeout"], 90)
        self.assertEqual(run.call_args.args[0][-1], "90.0")

    def test_outer_preflight_timeout_keeps_original_diagnostic_notes(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            script = root / ".github" / "scripts" / "desktop" / "native_ui_smoke.py"
            script.parent.mkdir(parents=True)
            script.write_text("# test\n", encoding="utf-8")
            original = local_vm_macos.LocalVMError("command timed out")
            original.add_note("preflight_stdout:\npartial native probe output\n")
            with mock.patch.object(local_vm_macos, "_run_logged", side_effect=original):
                with self.assertRaises(
                    local_vm_macos.MacOSNativeCapabilityPreflightFailed
                ) as caught:
                    local_vm_macos.preflight_native_ui_capabilities(
                        script, run_dir=root, logs=root / "logs", timeout=30,
                    )

        self.assertIn("partial native probe output", str(caught.exception))
        self.assertEqual(
            caught.exception.reason_code,
            "MACOS_NATIVE_UI_CAPABILITY_PREFLIGHT_FAILED",
        )

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
            (root / "candidate" / "apple" / "iosApp.xcodeproj").mkdir(parents=True)
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
