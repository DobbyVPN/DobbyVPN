from __future__ import annotations

import json
from pathlib import Path
import socket
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_checks import local_vm
from torturer_checks.functional import build_parser as functional_parser


class LocalVMTests(unittest.TestCase):
    def test_macos_route_output_has_indented_interface(self):
        result = subprocess.CompletedProcess([], 0, b"   route to: default\n  interface: en0\n", b"")
        with mock.patch.object(local_vm, "_run_logged", return_value=result):
            self.assertEqual(local_vm._discover_network_interface(Path("."), Path("."), 10, "macos", None), "en0")

    def test_macos_cleanup_accepts_absent_job_but_not_permission_failure(self):
        for code, message, failed in ((3, b"Boot-out failed: 3: No such process", False), (1, b"Operation not permitted", True)):
            with self.subTest(code=code):
                errors = []
                result = subprocess.CompletedProcess([], code, b"", message)
                with mock.patch.object(local_vm, "_run_logged", return_value=result):
                    local_vm._cleanup_launchd(["launchctl", "bootout", "system/com.dobby.vpnservice"], cwd=Path("."), logs=Path("."), timeout=10, errors=errors)
                self.assertEqual(bool(errors), failed)

    def test_ready_service_uses_privileged_identity_probe(self):
        with tempfile.TemporaryDirectory() as temporary:
            service = Path(temporary) / "service"
            control = Path(temporary) / "control"
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as listener:
                listener.bind(str(control))
                listener.listen()
                with mock.patch.object(local_vm, "_pid_matches", return_value=True) as probe:
                    local_vm._wait_linux_service(42, service, control, 1800)
                probe.assert_called_once_with(42, str(service.resolve()))

    def test_service_startup_does_not_wait_for_entire_suite_timeout(self):
        with mock.patch.object(local_vm.time, "monotonic", side_effect=[0, 31]):
            with self.assertRaisesRegex(local_vm.LocalVMError, "did not become ready"):
                local_vm._wait_linux_service(42, Path("/service"), Path("/control"), 1800)

    def test_capability_service_executable_is_probed_with_sudo(self):
        with mock.patch.object(local_vm, "_pid_alive", return_value=True), mock.patch.object(local_vm.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "/tmp/service\n", "")) as probe:
            self.assertTrue(local_vm._pid_matches(42, "/tmp/service"))
        self.assertEqual(probe.call_args.args[0], ["sudo", "-n", "readlink", "-f", "/proc/42/exe"])

    def test_unreadable_live_service_is_not_treated_as_stopped(self):
        with mock.patch.object(local_vm, "_pid_alive", return_value=True), mock.patch.object(local_vm.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "sudo refused")):
            with self.assertRaisesRegex(local_vm.LocalVMError, "sudo readlink"):
                local_vm._pid_matches(42, "/tmp/service")

    def test_stop_pid_signals_the_sudo_checked_process_group(self):
        with (
            mock.patch.object(local_vm, "_pid_alive", return_value=True),
            mock.patch.object(local_vm, "_pid_matches", side_effect=[True, False]),
            mock.patch.object(
                local_vm.subprocess,
                "run",
                side_effect=[
                    subprocess.CompletedProcess([], 0, "306\n", ""),
                    subprocess.CompletedProcess([], 0, "", ""),
                ],
            ) as run,
        ):
            self.assertTrue(local_vm._stop_pid(42, "/tmp/service", 1))
        self.assertEqual(run.call_args_list[0].args[0], [
            "sudo", "-n", "ps", "-o", "pgid=", "-p", "42",
        ])
        self.assertEqual(run.call_args_list[1].args[0], [
            "sudo", "-n", "kill", "-TERM", "--", "-306",
        ])

    def test_stop_pid_signals_the_sudo_checked_process_group(self):
        with (
            mock.patch.object(local_vm, "_pid_alive", return_value=True),
            mock.patch.object(local_vm, "_pid_matches", side_effect=[True, False]),
            mock.patch.object(
                local_vm.subprocess,
                "run",
                side_effect=[
                    subprocess.CompletedProcess([], 0, "306\n", ""),
                    subprocess.CompletedProcess([], 0, "", ""),
                ],
            ) as run,
        ):
            self.assertTrue(local_vm._stop_pid(42, "/tmp/service", 1))
        self.assertEqual(run.call_args_list[0].args[0], [
            "sudo", "-n", "ps", "-o", "pgid=", "-p", "42",
        ])
        self.assertEqual(run.call_args_list[1].args[0], [
            "sudo", "-n", "kill", "-TERM", "--", "-306",
        ])

    def test_parser_requires_absolute_run_dir_and_allows_repeated_scenarios(self) -> None:
        with self.assertRaises(SystemExit):
            local_vm.build_parser().parse_args(
                ["run", "--platform", "linux", "--run-dir", "relative", "--timeout", "10"]
            )
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "linux", "--run-dir", "/tmp/dobby-run",
            "--timeout", "10", "--scenario", "connect", "--scenario", "process_loss",
        ])
        self.assertEqual(args.scenarios, ["connect", "process_loss"])

    def _run_directory(self) -> tuple[Path, dict[str, object]]:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        (root / "source" / "torturer").mkdir(parents=True)
        (root / "profile").write_text("synthetic", encoding="utf-8")
        for name in ("dobby-cli", "ubuntu_grpcvpnserver"):
            (root / "source" / name).write_text("candidate", encoding="utf-8")
        descriptor = {
            "cli": str(root / "source" / "dobby-cli"),
            "service": str(root / "source" / "ubuntu_grpcvpnserver"),
            "network": str(root / "socket"),
        }
        return root, descriptor

    def test_run_uses_functional_json_and_keeps_candidate_running(self) -> None:
        root, descriptor = self._run_directory()
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "linux", "--run-dir", str(root), "--timeout", "30",
            "--network-interface", "eth0",
        ])
        captured: list[list[str]] = []

        def fake_prepare(*_args, **_kwargs):
            (root / "candidate.json").write_text(json.dumps(descriptor), encoding="utf-8")

        def fake_logged(command, **kwargs):
            captured.append(command)
            return mock.Mock(returncode=0, stdout=b"", stderr=b"")

        with (
            mock.patch.object(local_vm, "_prepare_candidate", side_effect=fake_prepare),
            mock.patch.object(local_vm, "_start_linux", return_value={
                "pid": 42, "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
                "socket": str(root / "socket"), "launcher": "launcher",
            }),
            mock.patch.object(local_vm, "_run_logged", side_effect=fake_logged),
        ):
            self.assertEqual(local_vm.run(args), 0)

        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertEqual(state["status"], "functional-complete")
        self.assertTrue(any("--output" in command and "functional.json" in " ".join(command) for command in captured))
        self.assertNotIn("cleanup", [command[0] for command in captured])

    def test_run_failure_is_retained_for_supervisor_cleanup(self) -> None:
        root, descriptor = self._run_directory()
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "linux", "--run-dir", str(root), "--timeout", "30",
            "--network-interface", "eth0",
        ])

        def fake_prepare(*_args, **_kwargs):
            (root / "candidate.json").write_text(json.dumps(descriptor), encoding="utf-8")

        with (
            mock.patch.object(local_vm, "_prepare_candidate", side_effect=fake_prepare),
            mock.patch.object(local_vm, "_start_linux", return_value={"pid": 42}),
            mock.patch.object(local_vm, "_run_logged", side_effect=local_vm.LocalVMError("functional failed")),
        ):
            self.assertEqual(local_vm.run(args), 1)
        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertEqual(state["status"], "failed")
        self.assertNotEqual(state.get("status"), "cleaned")

    def test_linux_start_records_service_pid_returned_by_root_service(self) -> None:
        root, descriptor = self._run_directory()
        logs = root / "logs"
        result = subprocess.CompletedProcess([], 0, b"321\n", b"")
        with (
            mock.patch.object(local_vm, "_run_logged", return_value=result) as command,
            mock.patch.object(local_vm, "_wait_linux_service"),
        ):
            runtime = local_vm._start_linux(root, descriptor, logs, 10, "eth0")
        self.assertEqual(runtime["pid"], 321)
        self.assertEqual(runtime["environment"]["DOBBYVPN_CONTROL_SOCKET"], runtime["socket"])
        self.assertEqual((root / "service.pid").read_text(encoding="ascii"), "321\n")
        argv = command.call_args.args[0]
        self.assertEqual(argv[:4], ["sudo", "-n", "sh", "-c"])
        self.assertIn("setsid", argv[4])

    def test_macos_start_uses_candidate_installer_hook(self) -> None:
        root, descriptor = self._run_directory()
        (root / "source" / "installer" / "macos").mkdir(parents=True)
        (root / "source" / "installer" / "macos" / "postinstall.sh").write_text("#!/bin/sh\n", encoding="utf-8")
        (root / "source" / "installer" / "macos" / "vpnservice.plist").write_text("plist", encoding="utf-8")
        descriptor["service"] = str(root / "source" / "macos_grpcvpnserver")
        descriptor["network"] = str(root / "socket")
        (root / "source" / "macos_grpcvpnserver").write_text("candidate", encoding="utf-8")
        calls: list[list[str]] = []

        def fake_logged(command, **_kwargs):
            self.assertTrue((root / "logs" / "service.log").is_file())
            calls.append(command)
            return subprocess.CompletedProcess(command, 0, b"pid = 654\n", b"")

        with mock.patch.object(local_vm, "_run_logged", side_effect=fake_logged):
            runtime = local_vm._start_macos(root, descriptor, root / "logs", 10, "en0")
        self.assertEqual(runtime["pid"], 654)
        self.assertEqual(Path(runtime["pid_file"]).read_text(), "654\n")
        self.assertEqual(runtime["environment"]["DOBBYVPN_CONTROL_SOCKET"], runtime["socket"])
        self.assertIn("installer/macos/postinstall.sh", " ".join(calls[0]))
        self.assertIn("DOBBYVPN_SERVICE_RESOURCES=", " ".join(calls[0]))

    def test_generated_functional_commands_match_the_canonical_parser(self) -> None:
        root, descriptor = self._run_directory()
        linux_runtime = {
            "pid": 42,
            "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
            "socket": str(root / "socket"),
            "library_path": str(root / "source"),
            "launcher": str(root / "source" / "launcher"),
            "pid_file": str(root / "service.pid"),
            "identity_file": str(root / "service.identity"),
            "network_interface": "eth0",
        }
        command = local_vm._functional_command(
            root, descriptor | {"runtime": linux_runtime}, "linux", 30, ["connect"]
        )
        parsed = functional_parser().parse_args(command[3:])
        self.assertEqual(parsed.platform, "linux")
        self.assertEqual(parsed.network_interface, "eth0")
        self.assertEqual(parsed.output, root / "logs" / "functional.json")

        android_command = local_vm._functional_command(
            root, {"runtime": {"adb": "/usr/bin/adb"}}, "android", 30, None
        )
        self.assertEqual(functional_parser().parse_args(android_command[3:]).platform, "android")

        windows_runtime = {
            "pid": 42, "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
            "socket": "127.0.0.1:50051", "pid_file": str(root / "service.pid"),
            "identity_file": str(root / "service.identity"), "network_interface": "7",
        }
        windows_command = local_vm._functional_command(
            root, descriptor | {"runtime": windows_runtime}, "windows", 30, None
        )
        windows_args = functional_parser().parse_args(windows_command[3:])
        self.assertEqual(windows_args.network_interface, "7")
        self.assertEqual(windows_command[3:].count("--network-interface"), 1)

        windows_without_discovery = local_vm._functional_command(
            root, descriptor | {"runtime": {}}, "windows", 30, None
        )
        windows_fallback_args = functional_parser().parse_args(windows_without_discovery[3:])
        self.assertIsNone(windows_fallback_args.network_interface)

    def test_command_runner_streams_output_into_retained_logs(self) -> None:
        root, _ = self._run_directory()
        result = local_vm._run_logged(
            ["python3", "-c", "print('probe-output')"], cwd=root,
            logs=root / "logs", label="probe", timeout=10,
        )
        self.assertEqual(result.stdout, b"probe-output\n")
        self.assertEqual((root / "logs" / "probe.stdout.log").read_bytes(), result.stdout)

    def test_linux_cleanup_prefers_pid_file_updated_by_process_loss(self) -> None:
        root, _ = self._run_directory()
        (root / "service.pid").write_text("987\n", encoding="ascii")
        local_vm._write_json(root / "platform.json", {
            "platform": "linux",
            "runtime": {
                "pid": 42,
                "pid_file": str(root / "service.pid"),
                "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
                "network_interface": "eth0",
            },
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "linux", "--run-dir", str(root), "--timeout", "10",
        ])
        with (
            mock.patch.object(local_vm, "_cleanup_logged") as cleanup_command,
            mock.patch.object(local_vm, "_stop_pid", return_value=True) as stop,
        ):
            self.assertEqual(local_vm.cleanup(args), 0)
        self.assertEqual(stop.call_args.args[:2], (987, str(root / "source" / "ubuntu_grpcvpnserver")))
        self.assertTrue(any(call.args[0][-4:] == ["set", "dev", "eth0", "up"] for call in cleanup_command.call_args_list))

    def test_linux_cleanup_removes_private_root_owned_runtime_state(self) -> None:
        root, _ = self._run_directory()
        runtime_dir = root / ".dobbyvpn-run"
        runtime_dir.mkdir()
        socket_path = runtime_dir / "s"
        socket_path.touch()
        local_vm._write_json(root / "platform.json", {
            "platform": "linux",
            "runtime": {
                "pid": 42,
                "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
                "socket": str(socket_path),
                "network_interface": "eth0",
            },
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "linux", "--run-dir", str(root), "--timeout", "10",
        ])
        with (
            mock.patch.object(local_vm, "_cleanup_logged") as cleanup_command,
            mock.patch.object(local_vm, "_stop_pid", return_value=True),
        ):
            self.assertEqual(local_vm.cleanup(args), 0)
        commands = [call.args[0] for call in cleanup_command.call_args_list]
        self.assertIn(["sudo", "-n", "unlink", "--", str(socket_path)], commands)
        self.assertIn(["sudo", "-n", "rmdir", "--", str(runtime_dir)], commands)

    def test_linux_cleanup_rejects_runtime_state_outside_run_directory(self) -> None:
        root, _ = self._run_directory()
        local_vm._write_json(root / "platform.json", {
            "platform": "linux",
            "runtime": {"socket": str(root.parent / "outside" / "s")},
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "linux", "--run-dir", str(root), "--timeout", "10",
        ])
        with mock.patch.object(local_vm, "_cleanup_logged"):
            self.assertEqual(local_vm.cleanup(args), 1)

    def test_cleanup_without_state_is_idempotent(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "android", "--run-dir", str(root), "--timeout", "10",
        ])
        self.assertEqual(local_vm.cleanup(args), 0)

if __name__ == "__main__":
    unittest.main()
