from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_checks import local_vm, local_vm_android, local_vm_windows


class LocalVMPlatformTests(unittest.TestCase):
    def _state(self, root: Path, platform: str) -> None:
        (root / "platform.json").write_text(json.dumps({
            "platform": platform,
        }), encoding="utf-8")

    def test_windows_start_records_identity_and_network_before_returning(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            self._state(root, "windows")
            descriptor = {"service": str(binary)}
            process = mock.Mock(pid=431)
            with (
                mock.patch.dict(local_vm_windows.os.environ, {
                    "DOBBYVPN_CONTROL_TOKEN_USER": "test-user",
                }, clear=False),
                mock.patch.object(local_vm_windows, "_discover_network_interface", return_value="17") as discover,
                mock.patch.object(local_vm_windows, "_query_identity", return_value="431|638000000000000000"),
                mock.patch.object(local_vm_windows, "_wait_ready"),
                mock.patch.object(local_vm_windows.subprocess, "Popen", return_value=process) as popen,
            ):
                runtime = local_vm_windows.start(root, descriptor, logs, 10)

            self.assertEqual(runtime["network_interface"], "17")
            self.assertEqual(runtime["identity"], "431|638000000000000000")
            self.assertEqual(runtime["pid_file"], str(root / "service.pid"))
            self.assertEqual((root / "service.pid").read_text(), "431\n")
            self.assertEqual((root / "service.identity").read_text(), "431|638000000000000000\n")
            command = popen.call_args.args[0]
            self.assertEqual(command, [str(binary), "-port", "50051"])
            discover.assert_called_once()
            state = json.loads((root / "platform.json").read_text())
            self.assertEqual(state["runtime"]["network_interface"], "17")

    def test_windows_setup_failure_leaves_pre_side_effect_cleanup_state(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            self._state(root, "windows")
            descriptor = {"service": str(binary)}
            with (
                mock.patch.dict(local_vm_windows.os.environ, {
                    "DOBBYVPN_CONTROL_TOKEN_USER": "test-user",
                }, clear=False),
                mock.patch.object(local_vm_windows, "_discover_network_interface", side_effect=RuntimeError("no uplink")),
            ):
                with self.assertRaisesRegex(RuntimeError, "no uplink"):
                    local_vm_windows.start(root, descriptor, root / "logs", 10)
            state = json.loads((root / "platform.json").read_text())
            self.assertEqual(state["status"], "starting")
            self.assertIsNone(state["runtime"]["network_interface"])
            self.assertNotIn("pid", state["runtime"])

    def test_windows_native_ui_uses_interactive_token_and_cleans_task(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            calls: list[tuple[str, str, dict]] = []
            wrapper_text: list[str] = []

            def fake_powershell(script: str, **kwargs):
                calls.append((kwargs["label"], script, kwargs))
                if kwargs["label"] == "native-ui-task-register":
                    wrapper_text.append((root / "native-ui-task.ps1").read_text(encoding="utf-8"))
                    exit_marker = root / "native-ui.exit"
                    (logs / "native-ui.stdout.log").write_bytes(b"ui output\n")
                    (logs / "native-ui.stderr.log").write_bytes(b"")
                    exit_marker.write_text("0", encoding="ascii")
                return subprocess.CompletedProcess([], 0, b"", b"")

            command = [
                r"C:\Python\python.exe",
                r"C:\candidate\native_ui_smoke.py",
                "--platform", "windows",
            ]
            environment = {
                "PROGRAMDATA": r"C:\candidate\ProgramData",
                "DOBBYVPN_CONTROL_ADDRESS": "127.0.0.1:50051",
                "DOBBYVPN_CONTROL_TOKEN_USER": r"TEST\dobby",
                "DOBBY_LOG_PATH": r"C:\candidate\logs\service.log",
                "DOBBY_LOG_ROOT": r"C:\candidate\logs",
                "DOBBY_LOG_PRECREATED": "1",
                "GODEBUG": "asyncpreemptoff=1",
                "PATH": r"C:\Windows\System32;C:\Windows",
                "PRIVATE_TEST_VALUE": "must-not-be-copied",
            }
            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                result = local_vm_windows.run_interactive_ui(
                    command,
                    run_dir=root,
                    cwd=cwd,
                    logs=logs,
                    timeout=1,
                    environment=environment,
                )

            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stdout, b"ui output\n")
            self.assertEqual((logs / "native-ui.stdout.log").read_bytes(), b"ui output\n")
            self.assertEqual((logs / "native-ui.stderr.log").read_bytes(), b"")
            self.assertEqual(
                [label for label, _, _ in calls],
                ["native-ui-task-register", "native-ui-task-cleanup"],
            )
            register_script = calls[0][1]
            cleanup_script = calls[1][1]
            self.assertIn("-LogonType Interactive -RunLevel Limited", register_script)
            self.assertNotIn("InteractiveToken", register_script)
            self.assertIn(r"TEST\dobby", register_script)
            self.assertIn("Register-ScheduledTask", register_script)
            self.assertNotIn("New-ScheduledTaskTrigger", register_script)
            self.assertNotIn("-Trigger", register_script)
            self.assertIn("Unregister-ScheduledTask", cleanup_script)
            self.assertEqual(len(wrapper_text), 1)
            self.assertIn("DOBBYVPN_CONTROL_TOKEN_USER", wrapper_text[0])
            self.assertIn("EnvironmentVariables.Clear()", wrapper_text[0])
            self.assertIn("USERPROFILE", wrapper_text[0])
            self.assertIn(r"C:\Windows\System32;C:\Windows", wrapper_text[0])
            self.assertNotIn("PRIVATE_TEST_VALUE", wrapper_text[0])
            self.assertFalse((root / "native-ui-task.ps1").exists())

    def test_windows_native_ui_timeout_kills_recorded_tree_and_unregisters(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            labels: list[str] = []

            def fake_powershell(_script: str, **kwargs):
                labels.append(kwargs["label"])
                if kwargs["label"] == "native-ui-task-register":
                    (root / "native-ui.pid").write_text("431", encoding="ascii")
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with self.assertRaisesRegex(local_vm.LocalVMError, "task timed out"):
                    local_vm_windows.run_interactive_ui(
                        [r"C:\Python\python.exe", r"C:\candidate\smoke.py"],
                        run_dir=root,
                        cwd=cwd,
                        logs=logs,
                        timeout=0.01,
                        environment={"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
                    )

            self.assertEqual(
                labels,
                ["native-ui-task-register", "native-ui-kill", "native-ui-task-cleanup"],
            )
            self.assertFalse((root / "native-ui.pid").exists())


    def test_windows_cleanup_uses_exact_identity_and_firewall_rule(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            identity = root / "service.identity"
            identity.write_text("431|638000000000000000\n", encoding="ascii")
            runtime = {
                "pid": 431,
                "binary": str(binary),
                "pid_file": str(root / "service.pid"),
                "identity_file": str(identity),
                "network_interface": "17",
            }
            calls: list[tuple[str, dict]] = []

            def fake_powershell(script, **kwargs):
                calls.append((script, kwargs))
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                local_vm_windows.cleanup(root, runtime, logs, 10)
            # Ownership sidecars survive intermediate cleanup so a retry can
            # prove the same process (or safely observe it already gone).
            self.assertTrue(identity.exists())
            self.assertEqual(len(calls), 3)
            self.assertEqual(
                [kwargs["label"] for _, kwargs in calls],
                ["cleanup-service", "cleanup-routing", "cleanup-network-interface"],
            )
            self.assertIn("431|638000000000000000", calls[0][1]["environment"]["DOBBYVPN_SERVICE_IDENTITY"])
            self.assertEqual(calls[2][1]["environment"]["DOBBYVPN_NETWORK_INTERFACE"], "17")

    def test_windows_cleanup_prefers_current_sidecars_over_stale_runtime(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            pid_file = root / "service.pid"
            identity_file = root / "service.identity"
            pid_file.write_text("777\n", encoding="ascii")
            identity_file.write_text("777|638000000000000777\n", encoding="ascii")
            runtime = {
                "pid": 431,
                "identity": "431|638000000000000431",
                "binary": str(binary),
                "pid_file": str(pid_file),
                "identity_file": str(identity_file),
            }
            calls: list[dict] = []

            def fake_powershell(_script, **kwargs):
                calls.append(kwargs)
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                local_vm_windows.cleanup(root, runtime, root / "logs", 10)

            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_PID"], "777")
            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_IDENTITY"], "777|638000000000000777")
            self.assertTrue(pid_file.exists())
            self.assertTrue(identity_file.exists())

    def test_windows_cleanup_recovers_from_saved_identity_when_sidecars_are_missing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            runtime = {
                # The old runtime PID is intentionally stale; the saved
                # identity is the complete, validated ownership record.
                "pid": 431,
                "identity": "777|638000000000000777",
                "binary": str(binary),
                "pid_file": str(root / "service.pid"),
                "identity_file": str(root / "service.identity"),
            }
            calls: list[dict] = []

            def fake_powershell(_script, **kwargs):
                calls.append(kwargs)
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                local_vm_windows.cleanup(root, runtime, root / "logs", 10)

            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_PID"], "777")
            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_IDENTITY"], "777|638000000000000777")

    def test_windows_partial_setup_without_identity_does_not_attempt_kill(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "windows_grpcvpnserver.exe"
            binary.write_bytes(b"candidate")
            runtime = {
                "pid": 431,
                "binary": str(binary),
                "network_interface": "17",
            }
            calls: list[dict] = []

            def fake_powershell(_script, **kwargs):
                calls.append(kwargs)
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with self.assertRaisesRegex(local_vm.LocalVMError, "identity files are unavailable"):
                    local_vm_windows.cleanup(root, runtime, root / "logs", 10)

            self.assertEqual([call["label"] for call in calls], ["cleanup-routing", "cleanup-network-interface"])

    def _android_fixture(self, root: Path) -> dict:
        app = root / "dobbyvpn-release.apk"
        companion = root / "dobbyvpn-test-companion.apk"
        app.write_bytes(b"app")
        companion.write_bytes(b"companion")
        return {"app": str(app), "test_companion": str(companion)}

    def test_android_start_uses_redroid_namespace_and_installs_both_owned_apks(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self._state(root, "android")
            descriptor = self._android_fixture(root)
            environment = {
                "ADB_SERVER_SOCKET": "localfilesystem:/run/redroid/adb.sock",
                "ANDROID_SERIAL": "redroid-1",
            }
            calls: list[tuple[list[str], dict]] = []

            def fake_logged(command, **kwargs):
                calls.append((command, kwargs))
                label = kwargs["label"]
                if label == "android-state":
                    output = b"device\n"
                elif label == "android-identity":
                    output = b"0\n"
                elif label.startswith("android-verify"):
                    output = b"package:/data/app/base.apk\n"
                else:
                    output = b""
                return subprocess.CompletedProcess(command, 0, output, b"")

            with (
                mock.patch.object(local_vm_android, "_adb_and_environment", return_value=("/sdk/adb", "redroid-1", environment)),
                mock.patch.object(local_vm_android, "_run_logged", side_effect=fake_logged),
            ):
                runtime = local_vm_android.start(root, descriptor, root / "logs", 10)

            self.assertEqual(runtime["serial"], "redroid-1")
            self.assertEqual(runtime["installed_packages"], ["com.dobby.vpn", "com.dobby.vpn.test"])
            installs = [command for command, _ in calls if "install" in command]
            self.assertEqual(len(installs), 2)
            for command in installs:
                self.assertEqual(command[1:3], ["-s", "redroid-1"])
                self.assertIn("--no-incremental", command)
                self.assertIn("-r", command)
                self.assertIn("-t", command)
            self.assertFalse(any("reboot" in command or "systemctl" in command for command, _ in calls))

    def test_android_partial_install_is_recorded_for_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self._state(root, "android")
            descriptor = self._android_fixture(root)
            environment = {
                "ADB_SERVER_SOCKET": "localfilesystem:/run/redroid/adb.sock",
                "ANDROID_SERIAL": "redroid-1",
            }

            def fake_logged(command, **kwargs):
                label = kwargs["label"]
                if label == "android-state":
                    output = b"device\n"
                elif label == "android-identity":
                    output = b"0\n"
                elif label == "android-install-companion":
                    raise RuntimeError("companion rejected")
                elif label.startswith("android-verify"):
                    output = b"package:/data/app/base.apk\n"
                else:
                    output = b"Success\n"
                return subprocess.CompletedProcess(command, 0, output, b"")

            with (
                mock.patch.object(local_vm_android, "_adb_and_environment", return_value=("/sdk/adb", "redroid-1", environment)),
                mock.patch.object(local_vm_android, "_run_logged", side_effect=fake_logged),
            ):
                with self.assertRaisesRegex(RuntimeError, "companion rejected"):
                    local_vm_android.start(root, descriptor, root / "logs", 10)
            state = json.loads((root / "platform.json").read_text())
            self.assertEqual(state["runtime"]["installed_packages"], ["com.dobby.vpn", "com.dobby.vpn.test"])


if __name__ == "__main__":
    unittest.main()
