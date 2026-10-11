from __future__ import annotations

from contextlib import redirect_stderr
from io import BytesIO
import subprocess
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from torturer_runner import ios_simulator_app
from torturer_runner.adapters import macos
from torturer_runner.adapters.cli import CommandResult
from torturer_contract.engine import ScenarioExecutionError


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class InformationRetentionTests(unittest.TestCase):

    def test_simulator_runner_forwards_exact_success_streams_once(self) -> None:
        stdout = b"success stdout\x00\xff"
        stderr = b"success stderr\xfe"
        process = mock.Mock()
        process.returncode = 0
        process.communicate.return_value = (stdout, stderr)
        forwarded = BinaryStderr()

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            redirect_stderr(forwarded),
        ):
            result = ios_simulator_app.SubprocessCommandRunner().run(("command",))

        self.assertEqual(result.stdout, "success stdout\x00\\xff")
        self.assertEqual(result.stderr, "success stderr\\xfe")
        self.assertEqual(forwarded.buffer.getvalue().count(stdout), 1)
        self.assertEqual(forwarded.buffer.getvalue().count(stderr), 1)

    def test_simulator_forwarding_failure_keeps_original_stream_bytes(self) -> None:
        stdout = b"success stdout\x00\xff"
        stderr = b"success stderr\xfe"
        process = mock.Mock()
        process.returncode = 0
        process.communicate.return_value = (stdout, stderr)

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch.object(ios_simulator_app, "emit_streams", side_effect=OSError("stderr closed")),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(("command",))

        self.assertEqual(caught.exception.stdout, stdout)
        self.assertEqual(caught.exception.stderr, stderr)
        self.assertIn("stderr closed", "\n".join(caught.exception.__notes__))

    def test_macos_routing_firewall_failures_retain_command_diagnostics(self) -> None:
        adapter = object.__new__(macos.MacOSAdapter)
        adapter.routing_firewall_helper = Path("/tmp/routing-firewall")
        adapter.network_interface = "en0"
        adapter._routing_probe_address = "198.51.100.9"
        adapter.runner = mock.Mock()

        for action in ("block", "remove"):
            with self.subTest(action=action):
                stdout = f"{action} helper stdout\x00\xff".encode("latin-1")
                stderr = f"{action} helper stderr\xfe".encode("latin-1")
                argv = ["sudo", "-n", str(adapter.routing_firewall_helper), f"routing-{action}"]
                if action == "block":
                    argv.extend((adapter.network_interface, adapter._routing_probe_address))
                result = CommandResult(
                    command=tuple(argv), returncode=1, stdout=stdout, stderr=stderr
                )
                adapter.runner.run.return_value = result
                with mock.patch("torturer_runner.adapters.cli.emit_streams") as emit:
                    with self.assertRaises(ScenarioExecutionError) as caught:
                        adapter._firewall(action, 5)

                self.assertEqual(
                    caught.exception.reason_code,
                    f"ROUTING_FIREWALL_{action.upper()}_FAILED",
                )
                notes = "\n".join(caught.exception.__notes__)
                self.assertIn(stdout.decode("utf-8", errors="backslashreplace"), notes)
                self.assertIn(stderr.decode("utf-8", errors="backslashreplace"), notes)
                emit.assert_called_once_with("command", stdout, stderr)
                adapter.runner.run.assert_called_with(argv, timeout_seconds=5)

    def test_macos_missing_socket_retains_service_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            log_payloads = {
                "service.log": b'backend log\x00\xff\n',
                "service.stdout.log": b'backend stdout\x01\xfe\n',
                "service.stderr.log": b'backend stderr\x02\xfd\n',
            }
            for filename, payload in log_payloads.items():
                (root / filename).write_bytes(payload)

            cli_failure = CommandResult(
                command=("dobby-cli", "snapshot"),
                returncode=1,
                stdout=b"",
                stderr=(
                    b"dobby-cli: operation failed error=dial unix "
                    b"/var/run/dobbyvpn/control.sock: connect: no such file or directory\n"
                ),
            )
            launchd = CommandResult(
                command=("launchctl", "print", "system/com.dobby.vpnservice"),
                returncode=0,
                stdout=b"state = running\nruns = 2\nlast exit code = 15\n",
                stderr=b"launchd diagnostic stderr\n",
            )
            runner = mock.Mock()
            runner.run.side_effect = (cli_failure, launchd)

            service = object.__new__(macos.MacOSServiceProcessController)
            service.runner = runner
            service.raw_directory = root
            service.control_socket = root / "control.sock"
            adapter = object.__new__(macos.MacOSAdapter)
            adapter.cli = Path("dobby-cli")
            adapter.runner = runner
            adapter.service = service

            forwarded = BinaryStderr()
            with redirect_stderr(forwarded):
                with self.assertRaises(ScenarioExecutionError) as caught:
                    adapter._command(("snapshot",), 5.0, "STATUS_FAILED")

            self.assertEqual(caught.exception.reason_code, "STATUS_FAILED")
            notes = "\n".join(caught.exception.__notes__)
            self.assertIn("no such file or directory", notes)
            self.assertIn("macos_launchd_print_returncode=0 timed_out=False", notes)
            self.assertIn("state = running\nruns = 2", notes)
            self.assertIn("macos_control_socket_lstat", notes)
            self.assertIn("exists=false", notes)
            labels = (
                "macos_backend_log_stdout",
                "macos_backend_stdout_stdout",
                "macos_backend_stderr_stderr",
            )
            for label, payload in zip(labels, log_payloads.values()):
                rendered = payload.decode("utf-8", errors="backslashreplace")
                self.assertIn(f"{label}:\n{rendered}", notes)
                self.assertIn(payload, forwarded.buffer.getvalue())
            self.assertIn(b"launchd diagnostic stderr", notes.encode())
            self.assertEqual(runner.run.call_count, 2)
            self.assertEqual(
                runner.run.call_args_list[1].args[0],
                macos._MACOS_LAUNCHD_PRINT,
            )
            self.assertEqual(
                runner.run.call_args_list[1].kwargs["timeout_seconds"],
                macos._MACOS_DIAGNOSTIC_TIMEOUT_SECONDS,
            )

    def test_simulator_second_timeout_streams_reach_primary_failure(self) -> None:
        initial_stdout = b"initial\x00\xff"
        terminated_stdout = initial_stdout + b" after terminate"
        final_stdout = terminated_stdout + b" after cleanup"
        final_stderr = b"cleanup stderr"
        process = mock.Mock()
        process.pid = 123
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(("command",), 0.1, output=initial_stdout),
            subprocess.TimeoutExpired(("command",), 1.0, output=terminated_stdout),
            subprocess.TimeoutExpired(
                ("command",), 1.0, output=final_stdout, stderr=final_stderr
            ),
        ]
        forwarded = BinaryStderr()

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch("bounded_process.terminate_process_group"),
            redirect_stderr(forwarded),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(
                    ("command",), timeout_seconds=0.1
                )

        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("after cleanup", notes)
        self.assertIn("cleanup stderr", notes)
        self.assertIn(final_stdout, forwarded.buffer.getvalue())
        self.assertIn(final_stderr, forwarded.buffer.getvalue())

    def test_ios_ui_log_collection_preserves_raw_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            container = root / "container"
            (container / "tmp").mkdir(parents=True)
            (container / "tmp/app_logs.txt").write_bytes(b"native lifecycle\n")
            payload = b"UI diagnostic \xff\x00\r\n"
            retained_names = ("ui_diagnostics.jsonl", "app_logs.txt.previous", "go_app_logs.jsonl.stderr", "go_app_logs.jsonl.stderr.previous")
            for filename in retained_names:
                (container / "tmp" / filename).write_bytes(payload)
            with mock.patch.object(ios_simulator_app, "_require_success", return_value=SimpleNamespace(stdout=str(container))):
                ios_simulator_app._collect_ios_native_log(
                    mock.Mock(), SimpleNamespace(udid="11111111-1111-1111-1111-111111111111"),
                    SimpleNamespace(bundle_identifier="vpn.dobby.app"), root / "work", budget=mock.Mock(),
                )
            ios_simulator_app.retain_ios_diagnostics(root / "work", root / "collected")
            for filename in retained_names:
                self.assertEqual((root / "collected" / filename).read_bytes(), payload)

    def test_ios_ui_log_collection_reports_absent_native_log_without_placeholder(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            container = root / "container"
            temporary = container / "tmp"
            temporary.mkdir(parents=True)
            payloads = {
                "ui_diagnostics.jsonl": b'{"event":"ui.failure"}\n',
                "go_tunnel_logs.jsonl.stderr": b'{"event":"stderr.capture"}\n',
            }
            for filename, payload in payloads.items():
                (temporary / filename).write_bytes(payload)

            with mock.patch.object(
                ios_simulator_app,
                "_require_success",
                return_value=SimpleNamespace(stdout=str(container)),
            ):
                native_log = ios_simulator_app._collect_ios_native_log(
                    mock.Mock(),
                    SimpleNamespace(udid="11111111-1111-1111-1111-111111111111"),
                    SimpleNamespace(bundle_identifier="vpn.dobby.app"),
                    root / "work",
                    budget=mock.Mock(),
                )

            collected = root / "work/diagnostics/ios-simulator"
            self.assertIsNone(native_log)
            self.assertFalse((collected / "app-native.log").exists())
            for filename, payload in payloads.items():
                self.assertEqual((collected / filename).read_bytes(), payload)


if __name__ == "__main__":
    unittest.main()
