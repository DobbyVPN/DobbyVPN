from __future__ import annotations

import json
import hashlib
import stat
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

    def test_windows_native_ui_is_delegated_to_interactive_session_adapter(self):
        expected = subprocess.CompletedProcess(["python"], 0, b"", b"")
        with mock.patch("torturer_checks.local_vm_windows.run_interactive_ui", return_value=expected) as run:
            result = local_vm._run_native_ui(
                ["python", "native_ui_smoke.py"],
                platform="windows",
                run_dir=Path("/tmp/run"),
                cwd=Path("/tmp/run/source"),
                logs=Path("/tmp/run/logs"),
                timeout=10,
                environment={"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
            )
        self.assertIs(result, expected)
        run.assert_called_once_with(
            ["python", "native_ui_smoke.py"],
            run_dir=Path("/tmp/run"),
            cwd=Path("/tmp/run/source"),
            logs=Path("/tmp/run/logs"),
            timeout=10,
            environment={"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
        )

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

    def test_local_full_rejects_linux_before_candidate_setup(self) -> None:
        root, _ = self._run_directory()
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "linux", "--run-dir", str(root),
            "--timeout", "30", "--suite", "full",
        ])
        with mock.patch.object(local_vm, "_prepare_candidate") as prepare:
            with self.assertRaisesRegex(local_vm.LocalVMError, "full is unsupported"):
                local_vm.run(args)
        prepare.assert_not_called()

    def test_local_full_rejects_focused_scenario_before_candidate_setup(self) -> None:
        root, _ = self._run_directory()
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "windows", "--run-dir", str(root),
            "--timeout", "30", "--suite", "full",
            "--scenario", "functional.core-connection",
        ])
        with mock.patch.object(local_vm, "_prepare_candidate") as prepare:
            with self.assertRaisesRegex(
                local_vm.LocalVMError, "cannot select focused scenarios"
            ):
                local_vm.run(args)
        prepare.assert_not_called()

    def _run_directory(self) -> tuple[Path, dict[str, object]]:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        (root / "source" / "torturer").mkdir(parents=True)
        (root / "profile").write_text("synthetic", encoding="utf-8")
        for name in ("dobby-cli", "dobby-vpn-ui-test", "ubuntu_grpcvpnserver"):
            (root / "source" / name).write_text("candidate", encoding="utf-8")
        descriptor = {
            "cli": str(root / "source" / "dobby-cli"),
            "ui_test": str(root / "source" / "dobby-vpn-ui-test"),
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
            mock.patch.object(local_vm, "_prepare_release_candidate", side_effect=AssertionError("release install used")),
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

    def test_windows_release_run_stops_msi_service_before_direct_candidate_start(self) -> None:
        from torturer_checks import local_vm_windows

        root, descriptor = self._run_directory()
        ui = root / "source" / "Dobby Vpn.exe"
        ui.write_bytes(b"ui")
        descriptor.update({"ui": str(ui), "ui_test": str(root / "source" / "dobby-vpn-ui-test")})
        manifest_path = root / "release-manifest.json"
        manifest_path.write_text("synthetic", encoding="utf-8")
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "windows", "--run-dir", str(root), "--timeout", "30",
            "--suite", "full", "--release-manifest", str(manifest_path),
        ])
        order: list[str] = []

        def fake_prepare(*_args, **_kwargs):
            local_vm._write_json(root / "platform.json", {
                "platform": "windows", "suite": "full", "status": "candidate-prepared",
                "candidate": descriptor,
                "release": {
                    "mode": "release-package", "package_path": str(root / "release.msi"),
                    "installed": True, "install_attempted": True,
                },
            })
            return descriptor

        def fake_stop(*_args, **_kwargs):
            order.append("stop")

        def fake_start(*_args, **_kwargs):
            order.append("start")
            return {
                "pid": 42, "binary": descriptor["service"],
                "socket": "127.0.0.1:50051", "network_interface": "7",
            }

        with (
            mock.patch.object(local_vm, "_prepare_release_candidate", side_effect=fake_prepare),
            mock.patch.object(local_vm_windows, "stop_installed_service", side_effect=fake_stop),
            mock.patch.object(local_vm, "_start_windows", side_effect=fake_start),
            mock.patch.object(local_vm, "_run_logged", return_value=subprocess.CompletedProcess([], 0, b"", b"")),
            mock.patch.object(local_vm, "_native_ui_command", return_value=["native-ui"]),
            mock.patch.object(local_vm, "_run_native_ui", return_value=subprocess.CompletedProcess([], 0, b"", b"")),
        ):
            self.assertEqual(local_vm.run(args), 0)
        self.assertEqual(order, ["stop", "start"])
        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertTrue(state["release"]["msi_service_stopped"])

    def test_windows_full_records_unavailable_native_window_without_passing(self) -> None:
        from torturer_checks import local_vm_windows

        root, descriptor = self._run_directory()
        ui = root / "source" / "Dobby Vpn.exe"
        ui.write_bytes(b"ui")
        descriptor.update({"ui": str(ui), "ui_test": str(root / "source" / "dobby-vpn-ui-test")})
        args = local_vm.build_parser().parse_args([
            "run", "--platform", "windows", "--run-dir", str(root), "--timeout", "30",
            "--suite", "full",
        ])

        def fake_prepare(*_args, **_kwargs):
            (root / "candidate.json").write_text(json.dumps(descriptor), encoding="utf-8")

        unavailable = local_vm_windows.WindowsInteractiveDesktopUnavailable(
            "no Explorer desktop session for TEST\\dobby"
        )
        with (
            mock.patch.object(local_vm, "_prepare_candidate", side_effect=fake_prepare),
            mock.patch.object(local_vm, "_start_windows", return_value={
                "pid": 42, "binary": descriptor["service"],
                "socket": "127.0.0.1:50051", "network_interface": "7",
            }),
            mock.patch.object(
                local_vm, "_run_logged",
                return_value=subprocess.CompletedProcess([], 0, b"", b""),
            ),
            mock.patch.object(local_vm, "_native_ui_command", return_value=["native-ui"]),
            mock.patch.object(local_vm, "_run_native_ui", side_effect=unavailable),
        ):
            self.assertEqual(local_vm.run(args), 1)

        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertEqual(state["functional_exit_code"], 0)
        self.assertEqual(state["native_ui_status"], "unavailable")
        self.assertEqual(state["status"], "native-ui-unavailable")
        self.assertEqual(state["native_ui_exit_code"], 1)
        native_result = json.loads((root / "logs" / "native-ui.json").read_text(encoding="utf-8"))
        self.assertFalse(native_result["complete"])
        self.assertEqual(native_result["availability"], "unavailable")
        self.assertEqual(native_result["reason_code"], "WINDOWS_INTERACTIVE_DESKTOP_UNAVAILABLE")

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
            root, descriptor | {"runtime": linux_runtime}, "linux", 30, ["connect"], "mini"
        )
        parsed = functional_parser().parse_args(command[3:])
        self.assertEqual(parsed.platform, "linux")
        self.assertEqual(parsed.network_interface, "eth0")
        self.assertEqual(parsed.output, root / "logs" / "functional.json")

        android_command = local_vm._functional_command(
            root, {"runtime": {"adb": "/usr/bin/adb"}}, "android", 30, None, "mini"
        )
        self.assertEqual(functional_parser().parse_args(android_command[3:]).platform, "android")

        windows_runtime = {
            "pid": 42, "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
            "socket": "127.0.0.1:50051", "pid_file": str(root / "service.pid"),
            "identity_file": str(root / "service.identity"), "network_interface": "7",
        }
        windows_command = local_vm._functional_command(
            root, descriptor | {"runtime": windows_runtime}, "windows", 30, None, "mini"
        )
        windows_args = functional_parser().parse_args(windows_command[3:])
        self.assertEqual(windows_args.network_interface, "7")
        self.assertEqual(windows_command[3:].count("--network-interface"), 1)

        windows_without_discovery = local_vm._functional_command(
            root, descriptor | {"runtime": {}}, "windows", 30, None, "mini"
        )
        windows_fallback_args = functional_parser().parse_args(windows_without_discovery[3:])
        self.assertIsNone(windows_fallback_args.network_interface)

    def test_native_full_command_uses_base_adapter_journey_module_and_runtime(self) -> None:
        root, descriptor = self._run_directory()
        ui = root / "source" / "dobby-vpn-ui.exe"
        smoke = root / "source" / ".github" / "scripts"
        smoke.mkdir(parents=True)
        (smoke / "native_ui_smoke.py").write_text("# candidate\n", encoding="utf-8")
        native_module = root / "source" / "torturer" / "torturer_checks" / "hosted"
        native_module.mkdir(parents=True)
        (native_module / "native_ui.py").write_text("# candidate\n", encoding="utf-8")
        ui.write_text("candidate", encoding="utf-8")
        descriptor["cli"] = str(root / "source" / "dobby-cli")
        descriptor["ui"] = str(ui)
        runtime = {
            "pid": 42,
            "binary": str(root / "source" / "ubuntu_grpcvpnserver"),
            "socket": "127.0.0.1:50051",
            "pid_file": str(root / "service.pid"),
            "identity_file": str(root / "service.identity"),
            "network_interface": "7",
        }
        command = local_vm._native_ui_command(root, descriptor, runtime, "windows", 120)
        self.assertIn("torturer_checks.hosted.native_ui", command)
        self.assertIn("--smoke-script", command)
        self.assertIn("--service-pid", command)
        self.assertIn("42", command)
        self.assertIn("--service-socket", command)
        self.assertIn("127.0.0.1:50051", command)
        self.assertIn("--service-pid-file", command)
        self.assertIn(str(root / "service.pid"), command)
        self.assertIn("--service-identity-file", command)
        self.assertIn(str(root / "service.identity"), command)

    def test_release_manifest_rejects_hash_mismatch_before_install(self) -> None:
        root, _ = self._run_directory()
        (root / "source-identity.json").write_text(json.dumps({
            "schema": 1, "repository": "DobbyVPN/DobbyVPN",
            "source_sha": "a" * 40, "release_run_id": 42, "platform": "windows",
        }), encoding="utf-8")
        release = root / "release" / "windows"
        release.mkdir(parents=True)
        package = release / "dobbyVPN-windows-amd64.msi"
        ui_test = release / "dobby-vpn-ui-test.exe"
        package.write_bytes(b"package")
        ui_test.write_bytes(b"ui")
        manifest = {
            "schema": 1, "mode": "release-package", "repository": "DobbyVPN/DobbyVPN",
            "workflow": "Release", "workflow_path": ".github/workflows/release.yml",
            "run_id": 42, "run_attempt": 1, "branch": "main", "source_sha": "a" * 40,
            "platform": "windows", "artifacts": [
                {"artifact_name": "dobbyVPN-windows-amd64.msi", "file_name": package.name,
                 "role": "package", "architecture": "amd64",
                 "relative_path": "release/windows/dobbyVPN-windows-amd64.msi",
                 "sha256": "0" * 64},
                {"artifact_name": "dobby-vpn-ui-test-windows", "file_name": ui_test.name,
                 "role": "ui-test", "architecture": "amd64",
                 "relative_path": "release/windows/dobby-vpn-ui-test.exe",
                 "sha256": hashlib.sha256(ui_test.read_bytes()).hexdigest()},
            ],
        }
        manifest_path = root / "release-manifest.json"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(local_vm.LocalVMError, "hash mismatch"):
            local_vm._validate_release_inputs(root, root / "source", manifest_path)

    def test_release_manifest_rejects_wrong_staging_directory(self) -> None:
        root, _ = self._run_directory()
        (root / "source-identity.json").write_text(json.dumps({
            "schema": 1, "repository": "DobbyVPN/DobbyVPN",
            "source_sha": "a" * 40, "release_run_id": 42, "platform": "windows",
        }), encoding="utf-8")
        release = root / "release" / "other"
        release.mkdir(parents=True)
        package = release / "dobbyVPN-windows-amd64.msi"
        ui_test = release / "dobby-vpn-ui-test.exe"
        package.write_bytes(b"package")
        ui_test.write_bytes(b"ui")
        manifest = {
            "schema": 1, "mode": "release-package", "repository": "DobbyVPN/DobbyVPN",
            "workflow": "Release", "workflow_path": ".github/workflows/release.yml",
            "run_id": 42, "run_attempt": 1, "branch": "main", "source_sha": "a" * 40,
            "platform": "windows", "artifacts": [
                {"artifact_name": "dobbyVPN-windows-amd64.msi", "file_name": package.name,
                 "role": "package", "architecture": "amd64",
                 "relative_path": "release/other/dobbyVPN-windows-amd64.msi",
                 "sha256": hashlib.sha256(package.read_bytes()).hexdigest()},
                {"artifact_name": "dobby-vpn-ui-test-windows", "file_name": ui_test.name,
                 "role": "ui-test", "architecture": "amd64",
                 "relative_path": "release/other/dobby-vpn-ui-test.exe",
                 "sha256": hashlib.sha256(ui_test.read_bytes()).hexdigest()},
            ],
        }
        manifest_path = root / "release-manifest.json"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(local_vm.LocalVMError, "staging path"):
            local_vm._validate_release_inputs(root, root / "source", manifest_path)

    def test_release_manifest_mac_selects_guest_architecture_pair(self) -> None:
        root, _ = self._run_directory()
        (root / "source-identity.json").write_text(json.dumps({
            "schema": 1, "repository": "DobbyVPN/DobbyVPN",
            "source_sha": "a" * 40, "release_run_id": 42, "platform": "macos",
        }), encoding="utf-8")
        artifacts = []
        for arch, package_name, package_artifact, ui_artifact in (
            ("arm64", "dobbyVPN-macos-aarch64.pkg", "dobbyVPN-macos-aarch64.pkg", "dobby-vpn-ui-test-macos-arm64"),
            ("amd64", "dobbyVPN-macos-amd64.pkg", "dobbyVPN-macos-amd64.pkg", "dobby-vpn-ui-test-macos-amd64"),
        ):
            directory = root / "release" / arch
            directory.mkdir(parents=True, exist_ok=True)
            package = directory / package_name
            ui_test = directory / "dobby-vpn-ui-test"
            package.write_bytes(arch.encode())
            ui_test.write_bytes((arch + "-ui").encode())
            for role, artifact, path in (
                ("package", package_artifact, package), ("ui-test", ui_artifact, ui_test),
            ):
                artifacts.append({
                    "artifact_name": artifact, "file_name": path.name, "role": role,
                    "architecture": arch, "relative_path": str(path.relative_to(root)),
                    "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                })
        manifest = {
            "schema": 1, "mode": "release-package", "repository": "DobbyVPN/DobbyVPN",
            "workflow": "Release", "workflow_path": ".github/workflows/release.yml",
            "run_id": 42, "run_attempt": 1, "branch": "main", "source_sha": "a" * 40,
            "platform": "macos", "artifacts": artifacts,
        }
        manifest_path = root / "release-manifest.json"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        _, selected = local_vm._validate_release_inputs(root, root / "source", manifest_path)
        self.assertEqual(set(selected), {
            ("package", "arm64"), ("package", "amd64"),
            ("ui-test", "arm64"), ("ui-test", "amd64"),
        })
        self.assertTrue(all(
            stat.S_IMODE(path.stat().st_mode) == 0o700
            for key, path in selected.items() if key[0] == "ui-test"
        ))

    def test_desktop_full_refreshes_replaced_service_pid_from_sidecar(self) -> None:
        root, _ = self._run_directory()
        pid_file = root / "service.pid"
        pid_file.write_text("777\n", encoding="ascii")
        runtime = {"pid": 42, "pid_file": str(pid_file), "binary": "/service"}
        refreshed = local_vm._refresh_desktop_runtime_after_headless(runtime)
        self.assertEqual(refreshed["pid"], 777)
        self.assertEqual(runtime["pid"], 42)

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

    def test_windows_partial_release_install_uses_exact_msi_uninstaller_and_reports_cleanup_error(self) -> None:
        from torturer_checks import local_vm_windows

        root, _ = self._run_directory()
        package = root / "release" / "windows" / "dobbyVPN-windows-amd64.msi"
        package.parent.mkdir(parents=True)
        package.write_bytes(b"package")
        local_vm._write_json(root / "platform.json", {
            "platform": "windows", "suite": "full", "runtime": {},
            "release": {
                "mode": "release-package", "package_path": str(package),
                "installed": False, "install_attempted": True,
            },
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "windows", "--run-dir", str(root), "--timeout", "10",
        ])
        calls: list[tuple[list[str], str]] = []

        def fake_cleanup_logged(command, *, label, errors, **kwargs):
            calls.append((command, label))
            if label == "cleanup-package":
                errors.append("cleanup-package: simulated uninstall failure")

        with mock.patch.object(local_vm_windows, "cleanup"), \
                mock.patch.object(local_vm, "_cleanup_logged", side_effect=fake_cleanup_logged):
            self.assertEqual(local_vm.cleanup(args), 1)
        package_commands = [command for command, label in calls if label == "cleanup-package"]
        self.assertEqual(len(package_commands), 1)
        self.assertEqual(package_commands[0][:4], [
            "msiexec.exe", "/x", str(package), "/qn",
        ])
        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertEqual(state["status"], "cleanup-failed")
        self.assertIn("simulated uninstall failure", state["cleanup_errors"][0])

    def test_windows_release_cleanup_rejects_msi_path_outside_exact_staging(self) -> None:
        from torturer_checks import local_vm_windows

        root, _ = self._run_directory()
        local_vm._write_json(root / "platform.json", {
            "platform": "windows", "suite": "full", "runtime": {},
            "release": {
                "mode": "release-package", "package_path": str(root.parent / "outside.msi"),
                "installed": True, "install_attempted": True,
            },
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "windows", "--run-dir", str(root), "--timeout", "10",
        ])
        with mock.patch.object(local_vm_windows, "cleanup"), \
                mock.patch.object(local_vm, "_cleanup_logged") as cleanup_command:
            self.assertEqual(local_vm.cleanup(args), 1)
        self.assertFalse(any(call.args[1] == "cleanup-package" for call in cleanup_command.call_args_list))
        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertIn("outside the exact Release staging path", state["cleanup_errors"][0])

    def test_macos_partial_release_install_uses_package_uninstaller_contract(self) -> None:
        root, _ = self._run_directory()
        package = root / "release" / "dobbyVPN-macos-aarch64.pkg"
        package.parent.mkdir()
        package.write_bytes(b"package")
        local_vm._write_json(root / "platform.json", {
            "platform": "macos", "suite": "full", "runtime": {},
            "release": {
                "mode": "release-package", "package_path": str(package),
                "installed": False, "install_attempted": True,
            },
        })
        args = local_vm.build_parser().parse_args([
            "cleanup", "--platform", "macos", "--run-dir", str(root), "--timeout", "10",
        ])
        calls: list[tuple[list[str], str]] = []

        def fake_cleanup_logged(command, *, label, errors, **kwargs):
            calls.append((command, label))
            if label == "cleanup-package":
                errors.append("cleanup-package: simulated uninstall failure")

        with mock.patch.object(local_vm, "_cleanup_logged", side_effect=fake_cleanup_logged):
            self.assertEqual(local_vm.cleanup(args), 1)
        package_commands = [command for command, label in calls if label == "cleanup-package"]
        self.assertEqual(package_commands, [[
            "sudo", "-n", "/usr/local/libexec/dobbyvpn-uninstall",
        ]])
        state = json.loads((root / "platform.json").read_text(encoding="utf-8"))
        self.assertEqual(state["status"], "cleanup-failed")
        self.assertIn("simulated uninstall failure", state["cleanup_errors"][0])

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
