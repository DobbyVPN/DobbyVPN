from __future__ import annotations

from pathlib import Path
import socket
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_runner import local_vm, local_vm_macos
from torturer_runner.adapters.cli import CommandResult
from torturer_runner.adapters.macos import MacOSAdapter


class MacOSPreflightTests(unittest.TestCase):
    def test_native_ui_forwards_prepared_pythonpath_and_filters_other_loader_vars(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            completed = subprocess.CompletedProcess(["native-ui"], 0, b"", b"")
            allowed = {
                "PATH": "/usr/bin",
                "HOME": str(root / "ui-home"),
                "PYTHONPATH": str(root / "screenshot-python"),
                "DOBBYVPN_CONTROL_SOCKET": str(root / "control.sock"),
            }
            environment = {
                **allowed,
                "DYLD_LIBRARY_PATH": str(root / "developer-libraries"),
                "UNRELATED_SECRET": "must-not-forward",
            }
            with (
                mock.patch.object(
                    local_vm_macos,
                    "preflight_interactive_desktop",
                    return_value=("tester", 501),
                ),
                mock.patch.object(local_vm_macos, "_run_logged", return_value=completed) as logged,
            ):
                result = local_vm_macos.run_interactive_ui(
                    ["native-ui"],
                    run_dir=root,
                    cwd=root,
                    logs=root / "logs",
                    timeout=30,
                    environment=environment,
                )

        self.assertIs(result, completed)
        self.assertEqual(logged.call_args.kwargs["environment"], allowed)
        command = logged.call_args.args[0]
        self.assertEqual(
            command[:11],
            ["sudo", "-n", "launchctl", "asuser", "501", "sudo", "-n", "-u",
             "tester", "--", "/usr/bin/env"],
        )
        self.assertEqual(
            command[11:-1],
            [f"{key}={value}" for key, value in sorted(allowed.items())],
        )
        self.assertEqual(command[-1], "native-ui")

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

        adapter = object.__new__(MacOSAdapter)
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
                    plist = run_dir / "source" / "ui/apple/macos/installer/vpnservice.plist"
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


if __name__ == "__main__":
    unittest.main()
