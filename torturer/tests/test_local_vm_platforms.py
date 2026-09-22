from __future__ import annotations

import hashlib
import json
import plistlib
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest import mock
import zlib

from torturer_checks import local_vm, local_vm_android, local_vm_macos, local_vm_windows


def _png_chunk(kind: bytes, payload: bytes) -> bytes:
    return (
        struct.pack(">I", len(payload))
        + kind
        + payload
        + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
    )


def _test_png() -> bytes:
    width, height = 2, 2
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    row = b"\x00" + (b"\xff\x00\x00\xff" * width)
    return (
        b"\x89PNG\r\n\x1a\n"
        + _png_chunk(b"IHDR", ihdr)
        + _png_chunk(b"IDAT", zlib.compress(row * height))
        + _png_chunk(b"IEND", b"")
    )


class LocalVMPlatformTests(unittest.TestCase):
    def _state(self, root: Path, platform: str) -> None:
        (root / "platform.json").write_text(json.dumps({
            "platform": platform,
        }), encoding="utf-8")

    def test_macos_local_ui_is_staged_as_disposable_release_shape_bundle(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            executable = root / "Dobby Vpn"
            executable.write_bytes(b"candidate")
            executable.chmod(0o755)

            bundle = local_vm_macos.stage_native_ui_bundle(root, executable)

            target = bundle / "Contents" / "MacOS" / "Dobby Vpn"
            self.assertEqual(bundle.name, "Dobby VPN.app")
            self.assertEqual(target.read_bytes(), b"candidate")
            self.assertTrue(target.stat().st_mode & 0o111)
            with (bundle / "Contents" / "Info.plist").open("rb") as stream:
                info = plistlib.load(stream)
            self.assertEqual(info["CFBundleExecutable"], "Dobby Vpn")
            self.assertEqual(info["CFBundlePackageType"], "APPL")
            self.assertEqual(info["CFBundleIdentifier"], "com.dobby.vpn")

    def test_macos_release_bundle_path_is_reused_without_copying(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bundle = Path(temporary) / "Dobby VPN.app"
            executable = bundle / "Contents" / "MacOS" / "Dobby Vpn"
            executable.parent.mkdir(parents=True)
            executable.write_bytes(b"release")
            with (bundle / "Contents" / "Info.plist").open("wb") as stream:
                plistlib.dump({"CFBundleExecutable": "Dobby Vpn"}, stream)

            self.assertEqual(
                local_vm_macos.stage_native_ui_bundle(Path(temporary), executable),
                bundle.resolve(),
            )

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

    def test_windows_release_stops_only_the_msi_owned_service(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(local_vm_windows, "_powershell") as powershell:
                powershell.return_value = subprocess.CompletedProcess([], 0, b"", b"")
                local_vm_windows.stop_installed_service(root, root / "logs", 120)
            powershell.assert_called_once()
            script = powershell.call_args.args[0]
            self.assertIn('Get-Service -Name "DobbyVPN Server"', script)
            self.assertIn("Stop-Service -InputObject $service -Force", script)
            self.assertEqual(powershell.call_args.kwargs["label"], "release-stop-service")

    def test_windows_full_mesa_fixture_stages_exact_ui_and_dll_members(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            ui = root / "Dobby Vpn.exe"
            ui.write_bytes(b"production-ui")
            command = ["python.exe", "native_ui.py", "--ui", str(ui)]
            archive_bytes = b"synthetic-msvc-archive"
            calls: list[list[str]] = []

            def fake_command(command: list[str], **_kwargs):
                calls.append(command)
                if command[0] == "curl.exe":
                    archive = Path(command[command.index("--output") + 1])
                    archive.write_bytes(archive_bytes)
                else:
                    extraction = Path(command[command.index("-C") + 1])
                    members = extraction / "x64"
                    members.mkdir(parents=True)
                    (members / "opengl32.dll").write_bytes(b"opengl")
                    (members / "libgallium_wgl.dll").write_bytes(b"gallium")
                return subprocess.CompletedProcess(command, 0, b"", b"")

            with (
                mock.patch.object(
                    local_vm_windows,
                    "_MESA_LLVMPIPE_SHA256",
                    hashlib.sha256(archive_bytes).hexdigest(),
                ),
                mock.patch.object(
                    local_vm_windows,
                    "_run_mesa_fixture_command",
                    side_effect=fake_command,
                ),
            ):
                staged, staging = local_vm_windows._prepare_mesa_llvmpipe_fixture(
                    command, run_dir=root, timeout=10,
                )

            self.assertEqual(staging, root / "native-ui-staging")
            self.assertEqual(
                staged[staged.index("--ui") + 1],
                str(root / "native-ui-staging" / "Dobby Vpn.exe"),
            )
            self.assertEqual((staging / "Dobby Vpn.exe").read_bytes(), b"production-ui")
            self.assertEqual((staging / "opengl32.dll").read_bytes(), b"opengl")
            self.assertEqual((staging / "libgallium_wgl.dll").read_bytes(), b"gallium")
            self.assertFalse((staging / local_vm_windows._MESA_LLVMPIPE_ARCHIVE_NAME).exists())
            self.assertFalse((staging / ".extract").exists())
            self.assertEqual(ui.read_bytes(), b"production-ui")
            self.assertEqual(calls[0][0], "curl.exe")
            self.assertIn(local_vm_windows._MESA_LLVMPIPE_URL, calls[0])
            self.assertEqual(calls[1][0], "tar.exe")
            self.assertEqual(
                calls[1][-2:], list(local_vm_windows._MESA_LLVMPIPE_MEMBERS),
            )
            wrapper = local_vm_windows._native_ui_wrapper(
                staged,
                cwd=root,
                environment={"GALLIUM_DRIVER": "llvmpipe"},
                stdout=root / "stdout.log",
                stderr=root / "stderr.log",
                pid=root / "controller.pid",
                child_pid=root / "child.pid",
                exit_code=root / "exit",
            )
            self.assertIn("GALLIUM_DRIVER", wrapper)
            self.assertIn("llvmpipe", wrapper)

            local_vm_windows._remove_mesa_llvmpipe_staging(root)
            self.assertFalse(staging.exists())

    def test_windows_full_mesa_fixture_hash_failure_skips_extraction_and_cleans(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            ui = root / "Dobby Vpn.exe"
            ui.write_bytes(b"production-ui")
            command = ["python.exe", "native_ui.py", "--ui", str(ui)]
            calls: list[list[str]] = []

            def fake_command(command: list[str], **_kwargs):
                calls.append(command)
                archive = Path(command[command.index("--output") + 1])
                archive.write_bytes(b"wrong-archive")
                return subprocess.CompletedProcess(command, 0, b"", b"")

            with mock.patch.object(
                local_vm_windows, "_run_mesa_fixture_command", side_effect=fake_command,
            ):
                with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                    local_vm_windows._prepare_mesa_llvmpipe_fixture(
                        command, run_dir=root, timeout=10,
                    )

            self.assertEqual([call[0] for call in calls], ["curl.exe"])
            self.assertFalse((root / "native-ui-staging").exists())

    def test_windows_mesa_fixture_failure_keeps_stdout_and_stderr(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(
                local_vm_windows.subprocess,
                "run",
                return_value=subprocess.CompletedProcess(
                    ["tar.exe"], 9, b"fixture stdout\n", b"fixture stderr\n",
                ),
            ):
                with self.assertRaisesRegex(
                    local_vm.LocalVMError,
                    "(?s)fixture stdout.*fixture stderr",
                ):
                    local_vm_windows._run_mesa_fixture_command(
                        ["tar.exe"], cwd=root, timeout=10,
                    )

    def test_windows_mesa_fixture_success_forwards_stdout_and_stderr(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            result = subprocess.CompletedProcess(
                ["tar.exe"], 0, b"fixture stdout\xff", b"fixture stderr\xfe",
            )
            with (
                mock.patch.object(local_vm_windows.subprocess, "run", return_value=result),
                mock.patch.object(local_vm_windows, "emit_streams") as emit,
            ):
                self.assertIs(
                    local_vm_windows._run_mesa_fixture_command(
                        ["tar.exe"], cwd=root, timeout=10,
                    ),
                    result,
                )
            emit.assert_called_once_with("native-ui-mesa", result.stdout, result.stderr)

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
                if kwargs["label"] == "native-ui-preflight":
                    return subprocess.CompletedProcess([], 0, b"ready|1|1\n", b"")
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
                "--service-pid-file", r"C:\candidate\service.pid",
                "--service-identity-file", r"C:\candidate\service.identity",
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
                [
                    "native-ui-preflight", "native-ui-access",
                    "native-ui-task-register", "native-ui-task-cleanup",
                ],
            )
            preflight_script = calls[0][1]
            access_script = calls[1][1]
            register_script = calls[2][1]
            cleanup_script = calls[3][1]
            self.assertIn('Get-Process -Name "explorer"', preflight_script)
            self.assertIn("SessionId", preflight_script)
            self.assertIn("Add-Type -TypeDefinition", preflight_script)
            self.assertIn("WTSGetActiveConsoleSessionId", preflight_script)
            self.assertIn("Select-Object -ExpandProperty SessionId -Unique", preflight_script)
            self.assertIn("$sessions.Count -ne 1", preflight_script)
            self.assertIn("System32\\tscon.exe", preflight_script)
            self.assertIn('"/dest:console"', preflight_script)
            self.assertIn("$observedSession -eq $session", preflight_script)
            self.assertIn("$activeConsole -eq [uint32]$session", preflight_script)
            self.assertNotIn("quser", preflight_script.casefold())
            self.assertNotIn("$shortName", preflight_script)
            self.assertIn("icacls.exe", access_script)
            self.assertIn("(OI)(CI)RX", access_script)
            self.assertIn("(OI)(CI)M", access_script)
            self.assertIn("Permission", access_script)
            self.assertIn("'(OI)(CI)RX' $true", access_script)
            self.assertIn("'RX' $false", access_script)
            self.assertNotIn("'RX' true", access_script)
            self.assertNotIn("'RX' false", access_script)
            self.assertIn(
                "Grant-Access 'C:\\candidate\\service.pid' 'M' $false",
                access_script,
            )
            self.assertIn(
                "Grant-Access 'C:\\candidate\\service.identity' 'M' $false",
                access_script,
            )
            self.assertIn("-LogonType Interactive -RunLevel Highest", register_script)
            self.assertNotIn("-RunLevel Limited", register_script)
            self.assertNotIn("InteractiveToken", register_script)
            self.assertIn(r"TEST\dobby", register_script)
            self.assertIn("Register-ScheduledTask", register_script)
            self.assertIn("Get-ScheduledTask -TaskName", register_script)
            self.assertIn("Get-ScheduledTaskInfo", register_script)
            self.assertIn("$task.State -eq 'Running'", register_script)
            self.assertNotIn("$info.State", register_script)
            self.assertIn("LastTaskResult", register_script)
            self.assertIn("scheduled native UI task failed before launch", register_script)
            self.assertNotIn("New-ScheduledTaskTrigger", register_script)
            self.assertNotIn("-Trigger", register_script)
            self.assertIn("Unregister-ScheduledTask", cleanup_script)
            self.assertEqual(len(wrapper_text), 1)
            self.assertIn("DOBBYVPN_CONTROL_TOKEN_USER", wrapper_text[0])
            self.assertIn("EnvironmentVariables.Clear()", wrapper_text[0])
            # The native controller must not request CREATE_NO_WINDOW.  That
            # startup mode can leave a GUI child created from the controller
            # with hidden/minimized GLFW windows in an interactive task.
            self.assertNotIn("CreateNoWindow", wrapper_text[0])
            self.assertIn("USERPROFILE", wrapper_text[0])
            self.assertIn(r"C:\Windows\System32;C:\Windows", wrapper_text[0])
            self.assertIn("StartTime.ToUniversalTime().Ticks", wrapper_text[0])
            self.assertIn("$process.Id) + '|' +", wrapper_text[0])
            self.assertNotIn("PRIVATE_TEST_VALUE", wrapper_text[0])
            self.assertFalse((root / "native-ui-task.ps1").exists())

    def test_windows_native_ui_timeout_kills_recorded_tree_and_unregisters(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            labels: list[str] = []
            scripts: dict[str, str] = {}
            environments: dict[str, dict[str, str]] = {}

            def fake_powershell(script: str, **kwargs):
                label = kwargs["label"]
                labels.append(label)
                scripts[label] = script
                environments[label] = kwargs.get("environment", {})
                if kwargs["label"] == "native-ui-preflight":
                    return subprocess.CompletedProcess([], 0, b"ready|1|1\n", b"")
                if kwargs["label"] == "native-ui-task-register":
                    (root / "native-ui.pid").write_text(
                        "431|638000000000000431", encoding="ascii",
                    )
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
                [
                    "native-ui-preflight", "native-ui-access",
                    "native-ui-task-register", "native-ui-kill", "native-ui-task-cleanup",
                ],
            )
            self.assertFalse((root / "native-ui.pid").exists())
            self.assertIn("Get-CimInstance Win32_Process", scripts["native-ui-kill"])
            self.assertIn("ExecutablePath", scripts["native-ui-kill"])
            self.assertIn("GetOwner", scripts["native-ui-kill"])
            self.assertIn("S-1-5-18", scripts["native-ui-kill"])
            self.assertIn("$creationDelta -ge 10", scripts["native-ui-kill"])
            self.assertIn("/PID $pidValue /T /F", scripts["native-ui-kill"])
            self.assertEqual(
                environments["native-ui-kill"]["DOBBYVPN_NATIVE_UI_CONTROLLER_BINARY"],
                r"C:\Python\python.exe",
            )
            self.assertEqual(
                environments["native-ui-kill"]["DOBBYVPN_NATIVE_UI_CONTROLLER_OWNER"],
                "dobby",
            )

    def test_windows_native_ui_timeout_rejects_pid_only_controller_marker(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            labels: list[str] = []

            def fake_powershell(_script: str, **kwargs):
                label = kwargs["label"]
                labels.append(label)
                if label == "native-ui-preflight":
                    return subprocess.CompletedProcess([], 0, b"ready|1|1\n", b"")
                if label == "native-ui-task-register":
                    (root / "native-ui.pid").write_text("431", encoding="ascii")
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with self.assertRaisesRegex(
                    local_vm.LocalVMError,
                    "controller identity marker is invalid",
                ):
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
                [
                    "native-ui-preflight", "native-ui-access",
                    "native-ui-task-register", "native-ui-task-cleanup",
                ],
            )

    def test_windows_native_ui_controller_exit_kills_exact_ui_child_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            labels: list[str] = []
            scripts: dict[str, str] = {}
            environments: dict[str, dict[str, str]] = {}

            def fake_powershell(script: str, **kwargs):
                label = kwargs["label"]
                labels.append(label)
                scripts[label] = script
                environments[label] = kwargs.get("environment", {})
                if label == "native-ui-preflight":
                    return subprocess.CompletedProcess([], 0, b"ready|1|1\n", b"")
                if label == "native-ui-task-register":
                    (root / "native-ui.pid").write_text(
                        "431|638000000000000431", encoding="ascii",
                    )
                    (root / "native-ui-child.pid").write_text(
                        "733|638000000000000733", encoding="ascii"
                    )
                    (root / "native-ui.exit").write_text("1", encoding="ascii")
                return subprocess.CompletedProcess([], 0, b"", b"")

            command = [
                r"C:\Python\python.exe",
                r"C:\candidate\native_ui.py",
                "--ui", r"C:\candidate\Dobby Vpn.exe",
            ]
            staged_command = list(command)
            staged_command[staged_command.index("--ui") + 1] = (
                str(root / "native-ui-staging" / "Dobby Vpn.exe")
            )
            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with mock.patch.object(
                    local_vm_windows,
                    "_prepare_mesa_llvmpipe_fixture",
                    return_value=(staged_command, root / "native-ui-staging"),
                ):
                    result = local_vm_windows.run_interactive_ui(
                        command,
                        run_dir=root,
                        cwd=cwd,
                        logs=logs,
                        timeout=1,
                        environment={"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
                    )

            self.assertEqual(result.returncode, 1)
            self.assertEqual(
                labels,
                [
                    "native-ui-preflight", "native-ui-access",
                    "native-ui-task-register", "native-ui-child-kill",
                    "native-ui-task-cleanup",
                ],
            )
            self.assertIn("Get-CimInstance Win32_Process", scripts["native-ui-child-kill"])
            self.assertIn("ExecutablePath", scripts["native-ui-child-kill"])
            self.assertIn("GetOwner", scripts["native-ui-child-kill"])
            self.assertIn("S-1-5-18", scripts["native-ui-child-kill"])
            self.assertIn("$creationDelta -ge 10", scripts["native-ui-child-kill"])
            self.assertIn("/PID $pidValue /T /F", scripts["native-ui-child-kill"])
            self.assertEqual(
                environments["native-ui-child-kill"]["DOBBYVPN_NATIVE_UI_CHILD_OWNER"],
                "dobby",
            )
            self.assertEqual(
                root.joinpath("native-ui-child.pid").exists(), False,
            )

    def test_windows_native_ui_creation_ticks_allow_only_submicrosecond_loss(self) -> None:
        expected = 639255020798658834
        self.assertTrue(
            local_vm_windows._creation_ticks_match(expected, expected - 4)
        )
        self.assertTrue(
            local_vm_windows._creation_ticks_match(expected, expected + 9)
        )
        self.assertFalse(
            local_vm_windows._creation_ticks_match(expected, expected + 10)
        )
        self.assertFalse(
            local_vm_windows._creation_ticks_match(expected, expected - 10)
        )

    def test_windows_native_ui_without_explorer_is_explicitly_unavailable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            cwd = root / "source"
            cwd.mkdir()
            labels: list[str] = []

            def fake_powershell(_script: str, **kwargs):
                labels.append(kwargs["label"])
                if kwargs["label"] == "native-ui-preflight":
                    return subprocess.CompletedProcess(
                        [], 3, b"", b"no Explorer desktop session for TEST\\dobby\n",
                    )
                raise AssertionError("the native task must not be registered")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with self.assertRaises(
                    local_vm_windows.WindowsInteractiveDesktopUnavailable,
                ) as raised:
                    local_vm_windows.run_interactive_ui(
                        [r"C:\Python\python.exe", r"C:\candidate\smoke.py"],
                        run_dir=root,
                        cwd=cwd,
                        logs=logs,
                        timeout=900,
                        environment={"DOBBYVPN_CONTROL_TOKEN_USER": r"TEST\dobby"},
                    )

            self.assertEqual(
                str(raised.exception),
                "WINDOWS_INTERACTIVE_DESKTOP_UNAVAILABLE: "
                "no usable Explorer desktop session for configured interactive user",
            )
            self.assertNotIn("TEST\\dobby", str(raised.exception))
            self.assertEqual(labels, ["native-ui-preflight"])
            self.assertFalse((root / "native-ui-task.ps1").exists())

    def test_macos_native_ui_without_aqua_is_unavailable_before_launch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(
                    command,
                    0,
                    b"<dictionary> {\n"
                    b"  kCGSSessionUserNameKey : root\n"
                    b"  kCGSSessionUserIDKey : 0\n"
                    b"}\n",
                    b"",
                )

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="root"),
            ):
                with self.assertRaises(local_vm_macos.MacOSInteractiveDesktopUnavailable) as raised:
                    local_vm_macos.run_interactive_ui(
                        ["python", "native_ui.py"],
                        run_dir=root,
                        cwd=root,
                        logs=logs,
                        timeout=900,
                        environment={},
                    )

            self.assertEqual(
                str(raised.exception),
                "MACOS_AQUA_SESSION_UNAVAILABLE: no logged-in Aqua console user",
            )
            self.assertEqual(calls, [["scutil"]])

    def test_macos_native_ui_aqua_preflight_passes_through_after_probes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            logs = root / "logs"
            calls: list[list[str]] = []
            call_kwargs: list[dict[str, object]] = []
            expected = subprocess.CompletedProcess(["native-ui"], 0, b"passed", b"")

            def fake_probe(command, **_kwargs):
                calls.append(command)
                call_kwargs.append(_kwargs)
                if command[0] == "scutil":
                    return subprocess.CompletedProcess(
                        command,
                        0,
                        b"<dictionary> {\n"
                        b"  kCGSSessionUserNameKey : alice\n"
                        b"  kCGSSessionUserIDKey : 501\n"
                        b"  kCGSSessionOnConsoleKey : TRUE\n"
                        b"}\n",
                        b"",
                    )
                if command[:2] == ["launchctl", "print"]:
                    return subprocess.CompletedProcess(command, 0, b"gui session\n", b"")
                if command[0] == "ioreg":
                    return subprocess.CompletedProcess(
                        command, 0, b'"CGSSessionScreenIsLocked" = No\n', b""
                    )
                if command[0] == "osascript":
                    return subprocess.CompletedProcess(command, 0, b"Finder\n", b"")
                if command[:4] == ["sudo", "-n", "launchctl", "asuser"]:
                    return expected
                return expected

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="alice"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
            ):
                result = local_vm_macos.run_interactive_ui(
                    ["native-ui"],
                    run_dir=root,
                    cwd=root,
                    logs=logs,
                    timeout=900,
                    environment={
                        "PATH": "/usr/bin",
                        "HOME": "/Users/alice",
                        "DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock",
                        "CI_PRIVATE_ENDPOINT": "https://private.invalid",
                    },
                )

            self.assertIs(result, expected)
            self.assertEqual(call_kwargs[0]["input_data"], b"show State:/Users/ConsoleUser\nquit\n")
            self.assertEqual(
                calls,
                [
                    ["scutil"],
                    ["launchctl", "print", "gui/501"],
                    ["ioreg", "-l", "-n", "Root", "-d", "1", "-w", "0"],
                    ["osascript", "-e", local_vm_macos._MACOS_ACCESSIBILITY_PROBE],
                    [
                        "sudo", "-n", "launchctl", "asuser", "501",
                        "sudo", "-n", "-u", "alice", "--", "/usr/bin/env",
                        "DOBBYVPN_CONTROL_SOCKET=/var/run/dobbyvpn/control.sock",
                        "HOME=/Users/alice", "PATH=/usr/bin", "native-ui",
                    ],
                ],
            )
            self.assertEqual(
                call_kwargs[-1],
                {
                    "cwd": root,
                    "logs": logs,
                    "label": "native-ui",
                    "timeout": 900,
                    "environment": {
                        "DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock",
                        "HOME": "/Users/alice",
                        "PATH": "/usr/bin",
                    },
                    "check": False,
                },
            )

    def test_macos_native_ui_malformed_console_state_is_unavailable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(command, 0, b"not a ConsoleUser dictionary\n", b"")

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
            ):
                with self.assertRaises(local_vm_macos.MacOSInteractiveDesktopUnavailable) as raised:
                    local_vm_macos.run_interactive_ui(
                        ["native-ui"],
                        run_dir=root,
                        cwd=root,
                        logs=root / "logs",
                        timeout=900,
                        environment={},
                    )

            self.assertEqual(
                str(raised.exception),
                "MACOS_AQUA_SESSION_UNAVAILABLE: no logged-in Aqua console user",
            )
            self.assertNotIn("ConsoleUser", str(raised.exception))
            self.assertEqual(calls, [["scutil"]])

    def test_macos_native_ui_locked_screen_is_unavailable_before_launch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                if command[0] == "scutil":
                    return subprocess.CompletedProcess(
                        command,
                        0,
                        b"kCGSSessionUserNameKey : alice\n"
                        b"kCGSSessionUserIDKey : 501\n",
                        b"",
                    )
                if command[:2] == ["launchctl", "print"]:
                    return subprocess.CompletedProcess(command, 0, b"gui session\n", b"")
                if command[0] == "ioreg":
                    return subprocess.CompletedProcess(
                        command, 0, b'"CGSSessionScreenIsLocked" = Yes\n', b""
                    )
                if command[0] == "osascript":
                    return subprocess.CompletedProcess(command, 0, b"Finder\n", b"")
                raise AssertionError("native UI must not launch while the screen is locked")

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="alice"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
            ):
                with self.assertRaises(
                    local_vm_macos.MacOSInteractiveDesktopUnavailable,
                ) as raised:
                    local_vm_macos.run_interactive_ui(
                        ["native-ui"],
                        run_dir=root,
                        cwd=root,
                        logs=root / "logs",
                        timeout=900,
                        environment={},
                    )

            self.assertEqual(
                str(raised.exception),
                "MACOS_AQUA_SESSION_UNAVAILABLE: Aqua console screen is locked",
            )
            self.assertEqual(
                calls,
                [
                    ["scutil"],
                    ["launchctl", "print", "gui/501"],
                    ["ioreg", "-l", "-n", "Root", "-d", "1", "-w", "0"],
                    ["osascript", "-e", local_vm_macos._MACOS_ACCESSIBILITY_PROBE],
                ],
            )

    def test_macos_native_ui_absent_lock_key_requires_finder_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                if command[0] == "scutil":
                    return subprocess.CompletedProcess(
                        command,
                        0,
                        b"kCGSSessionUserNameKey : alice\n"
                        b"kCGSSessionUserIDKey : 501\n",
                        b"",
                    )
                if command[:2] == ["launchctl", "print"]:
                    return subprocess.CompletedProcess(command, 0, b"gui session\n", b"")
                if command[0] == "ioreg":
                    return subprocess.CompletedProcess(command, 0, b"+-o Root\n", b"")
                if command[0] == "osascript":
                    return subprocess.CompletedProcess(command, 0, b"Finder\n", b"")
                raise AssertionError(command)

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="alice"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
            ):
                self.assertEqual(
                    local_vm_macos.preflight_interactive_desktop(
                        run_dir=root,
                        logs=root / "logs",
                        timeout=900,
                    ),
                    ("alice", 501),
                )

            self.assertEqual(
                calls,
                [
                    ["scutil"],
                    ["launchctl", "print", "gui/501"],
                    ["ioreg", "-l", "-n", "Root", "-d", "1", "-w", "0"],
                    ["osascript", "-e", local_vm_macos._MACOS_ACCESSIBILITY_PROBE],
                ],
            )

    def test_macos_native_ui_accessibility_is_unavailable_before_launch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                if command[0] == "scutil":
                    return subprocess.CompletedProcess(
                        command,
                        0,
                        b"kCGSSessionUserNameKey : alice\n"
                        b"kCGSSessionUserIDKey : 501\n",
                        b"",
                    )
                if command[:2] == ["launchctl", "print"]:
                    return subprocess.CompletedProcess(command, 0, b"gui session\n", b"")
                if command[0] == "ioreg":
                    return subprocess.CompletedProcess(
                        command, 0, b'"CGSSessionScreenIsLocked" = No\n', b""
                    )
                if command[0] == "osascript":
                    return subprocess.CompletedProcess(command, 1, b"", b"not authorized")
                raise AssertionError(command)

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="alice"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
            ):
                with self.assertRaisesRegex(
                    local_vm_macos.MacOSInteractiveDesktopUnavailable,
                    "System Events accessibility",
                ):
                    local_vm_macos.run_interactive_ui(
                        ["python", "native_ui.py"],
                        run_dir=root,
                        cwd=root,
                        logs=root / "logs",
                        timeout=900,
                        environment={},
                    )

            self.assertEqual(len(calls), 4)
            self.assertEqual(calls[-1][0], "osascript")

    def test_macos_screen_state_parser_fails_conflicts_and_malformed_values_closed(self) -> None:
        self.assertTrue(
            local_vm_macos._screen_is_unlocked(
                b"+-o Root\n", finder_accessible=True
            )
        )
        self.assertFalse(
            local_vm_macos._screen_is_unlocked(
                b'"CGSSessionScreenIsLocked" = Yes\n'
                b'"CGSSessionScreenIsLocked" = No\n',
                finder_accessible=True,
            )
        )
        self.assertFalse(
            local_vm_macos._screen_is_unlocked(
                b'"CGSSessionScreenIsLocked" = Maybe\n',
                finder_accessible=True,
            )
        )
        self.assertFalse(
            local_vm_macos._screen_is_unlocked(
                b"+-o Root\n", finder_accessible=False
            )
        )

    def test_macos_screen_state_parser_accepts_complete_large_probe_output(self) -> None:
        # The command runner already retains the complete bounded command
        # stream; parser size caps would silently discard valid diagnostics.
        root_output = b"+-o Root\n" + b" " * (64 * 1024 - len(b"+-o Root\n"))
        self.assertTrue(
            local_vm_macos._screen_is_unlocked(root_output, finder_accessible=True)
        )
        self.assertTrue(
            local_vm_macos._screen_is_unlocked(
                root_output + b" ", finder_accessible=True
            )
        )

    def test_macos_native_ui_worker_identity_mismatch_is_unavailable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            calls: list[list[str]] = []

            def fake_probe(command, **_kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(
                    command,
                    0,
                    b"kCGSSessionUserNameKey : alice\n"
                    b"kCGSSessionUserIDKey : 501\n",
                    b"",
                )

            with (
                mock.patch.object(local_vm_macos.host_platform, "system", return_value="Darwin"),
                mock.patch.object(local_vm_macos, "_run_logged", side_effect=fake_probe),
                mock.patch.object(local_vm_macos.getpass, "getuser", return_value="runner"),
                mock.patch.object(local_vm_macos.os, "getuid", return_value=501),
            ):
                with self.assertRaises(local_vm_macos.MacOSInteractiveDesktopUnavailable) as raised:
                    local_vm_macos.run_interactive_ui(
                        ["native-ui"],
                        run_dir=root,
                        cwd=root,
                        logs=root / "logs",
                        timeout=900,
                        environment={},
                    )

            self.assertEqual(
                str(raised.exception),
                "MACOS_AQUA_SESSION_UNAVAILABLE: current process does not own the Aqua console session",
            )
            self.assertNotIn("alice", str(raised.exception))
            self.assertNotIn("runner", str(raised.exception))
            self.assertEqual(calls, [["scutil"]])

    def test_windows_native_ui_preflight_failure_is_bounded(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            cwd = root / "source"
            cwd.mkdir()
            logs = root / "logs"

            def fake_powershell(_script: str, **kwargs):
                self.assertEqual(kwargs["label"], "native-ui-preflight")
                return subprocess.CompletedProcess([], 17, b"", b"raw script and username")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                with self.assertRaises(
                    local_vm_windows.WindowsInteractiveDesktopUnavailable,
                ) as raised:
                    local_vm_windows.run_interactive_ui(
                        [r"C:\Python\python.exe", r"C:\candidate\smoke.py"],
                        run_dir=root,
                        cwd=cwd,
                        logs=logs,
                        timeout=900,
                        environment={"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
                    )

            self.assertEqual(
                str(raised.exception),
                "WINDOWS_INTERACTIVE_DESKTOP_UNAVAILABLE: "
                "Explorer desktop preflight exited with status 17",
            )
            self.assertNotIn("raw script", str(raised.exception))


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
                "environment": {"DOBBYVPN_CONTROL_TOKEN_USER": r"TEST\dobby"},
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
            self.assertEqual(
                calls[0][1]["environment"]["DOBBYVPN_SERVICE_INTERACTIVE_OWNER"],
                r"TEST\dobby",
            )
            self.assertIn("S-1-5-18", calls[0][0])
            self.assertIn("NTAccount", calls[0][0])
            self.assertIn("CreationDate.ToUniversalTime().Ticks", calls[0][0])
            self.assertIn("ExecutablePath", calls[0][0])
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
                "environment": {"DOBBYVPN_CONTROL_TOKEN_USER": r".\dobby"},
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
                "environment": {"DOBBYVPN_CONTROL_TOKEN_USER": "dobby"},
            }
            calls: list[dict] = []

            def fake_powershell(_script, **kwargs):
                calls.append(kwargs)
                return subprocess.CompletedProcess([], 0, b"", b"")

            with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                local_vm_windows.cleanup(root, runtime, root / "logs", 10)

            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_PID"], "777")
            self.assertEqual(calls[0]["environment"]["DOBBYVPN_SERVICE_IDENTITY"], "777|638000000000000777")

    def test_windows_cleanup_rejects_unsafe_interactive_owner_configuration(self) -> None:
        for configured in ("", "SYSTEM", r"NT AUTHORITY\SYSTEM"):
            with self.subTest(configured=configured), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                binary = root / "windows_grpcvpnserver.exe"
                binary.write_bytes(b"candidate")
                identity = root / "service.identity"
                identity.write_text("431|638000000000000000\n", encoding="ascii")
                runtime = {
                    "pid": 431,
                    "binary": str(binary),
                    "pid_file": str(root / "service.pid"),
                    "identity_file": str(identity),
                    "environment": {"DOBBYVPN_CONTROL_TOKEN_USER": configured},
                }
                labels: list[str] = []

                def fake_powershell(_script, **kwargs):
                    labels.append(kwargs["label"])
                    return subprocess.CompletedProcess([], 0, b"", b"")

                with mock.patch.object(local_vm_windows, "_powershell", side_effect=fake_powershell):
                    with self.assertRaisesRegex(
                        local_vm.LocalVMError,
                        "cleanup-service: LocalVMError: Windows cleanup interactive owner",
                    ):
                        local_vm_windows.cleanup(root, runtime, root / "logs", 10)

                self.assertEqual(labels, ["cleanup-routing"])

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

    def test_android_native_ui_uses_instrumentation_without_staging_profile(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            environment = {
                "ADB_SERVER_SOCKET": "localfilesystem:/run/redroid/adb.sock",
                "ANDROID_SERIAL": "redroid-1",
            }
            calls: list[tuple[list[str], dict]] = []

            def fake_logged(command, **kwargs):
                calls.append((command, kwargs))
                label = kwargs["label"]
                payload = _test_png()
                if label == "android-native-ui":
                    markers = []
                    for frame in ("startup", "failure-state", "reopened"):
                        markers.append(
                            (
                                f"DOBBY_UI_SCREENSHOT label={frame} "
                                "path=/data/user/0/com.dobby.vpn/cache/"
                                f"dobbyvpn-rendered-screenshots/{frame}.png "
                                f"bytes={len(payload)} sha256={hashlib.sha256(payload).hexdigest()} "
                                "width=2 height=2\n"
                            ).encode()
                        )
                    output = b"".join(markers) + b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n"
                elif label == "android-complete-throwable-self-test":
                    output = (
                        b"DOBBY_COMPLETE_THROWABLE_BEGIN id=1 chunks=1 bytes=1 chars=1\n"
                        b"DOBBY_COMPLETE_THROWABLE_CHUNK id=1 sequence=1/1\n"
                        b"x\n"
                        b"DOBBY_COMPLETE_THROWABLE_END id=1 chunks=1 bytes=1 chars=1\n"
                        b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n"
                    )
                elif label.startswith("android-screenshot-"):
                    Path(command[-1]).write_bytes(payload)
                    output = b"1 file pulled\n"
                else:
                    output = b""
                return subprocess.CompletedProcess(command, 0, output, b"")

            with (
                mock.patch.dict(local_vm_android.os.environ, environment, clear=True),
                mock.patch.object(local_vm_android, "_run_logged", side_effect=fake_logged),
            ):
                result = local_vm_android.run_ui(
                    root,
                    {"adb": "/sdk/adb", "serial": "redroid-1"},
                    root / "logs",
                    10,
                )

            self.assertEqual(result.returncode, 0)
            commands = [command for command, _ in calls]
            labels = [kwargs["label"] for _, kwargs in calls]
            instrument = next(
                command
                for command in commands
                if any("GoUiInstrumentedTest" in item for item in command)
            )
            reporter_instrument = next(
                command
                for command in commands
                if any("CompleteThrowableReporterTest" in item for item in command)
            )
            cold_start = next(
                command for command, kwargs in calls
                if kwargs["label"] == "android-native-ui-cold-start"
            )
            self.assertEqual(cold_start[-3:], ["am", "force-stop", "com.dobby.vpn"])
            self.assertLess(
                labels.index("android-native-ui-cold-start"),
                labels.index("android-native-ui"),
            )
            self.assertLess(
                labels.index("android-complete-throwable-self-test"),
                labels.index("android-native-ui"),
            )
            self.assertIn("com.dobby.GoUiInstrumentedTest", instrument)
            self.assertIn("com.dobby.CompleteThrowableReporterTest", reporter_instrument)
            self.assertNotIn("dobby.ui_profile", instrument)
            self.assertFalse(any("push" in command or "chmod" in command for command in commands))


if __name__ == "__main__":
    unittest.main()
