from __future__ import annotations

from contextlib import nullcontext, redirect_stderr
from io import BytesIO, StringIO
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import ios_simulator_app
from torturer_runner.adapters import macos
from torturer_runner.ui import journey as native_ui
from torturer_runner.adapters.cli import CommandResult
from torturer_runner.windows_job import (
    WindowsJobCloseResult,
    _close_job,
)
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

    def test_native_ui_failure_result_keeps_completed_checks(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            cli = root / "cli"
            ui = root / "ui"
            profile = root / "profile"
            output = root / "native-ui.json"
            raw_logs = root / "logs"
            raw_logs.mkdir()
            for path in (cli, ui, profile):
                path.write_text("test", encoding="utf-8")
            args = SimpleNamespace(
                platform="windows",
                service_pipe="DobbyVPN.Control",
                service_socket=None,
                cli=cli,
                ui=ui,
                profile=profile,
                raw_log_dir=raw_logs,
                output=output,
                service_pid=123,
                service_binary=cli,
                service_library_path=None,
                service_pid_file=None,
                service_identity_file=None,
                network_interface=None,
                routing_firewall_helper=None,
                timeout=5,
            )
            base = mock.Mock()
            base._snapshot.return_value = {"configured": True, "profiles": [{"index": 0}]}
            controller = mock.Mock()
            controller.bounded_by.side_effect = lambda _timeout: nullcontext()
            controller.configure.return_value = {"input_verified": True}
            args.ui_helper = root / "native-helper"
            controller.connect.side_effect = native_ui.NativeUIJourneyError("connect failed")
            controller.capture.return_value = {}
            smoke = SimpleNamespace(
                NativeUIController=mock.Mock(return_value=controller),
            )

            with (
                mock.patch.object(native_ui, "_ensure_directory"),
                mock.patch.object(native_ui, "SubprocessRunner"),
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "smoke", smoke),
            ):
                with self.assertRaises(native_ui.NativeUIJourneyError) as caught:
                    native_ui.run_journey(args)

            failure = caught.exception
            self.assertEqual(failure.native_ui_checks, {"configure_native": True})
            base.discover_connections.assert_not_called()
            controller.connect.assert_called_once()
            base._snapshot.assert_not_called()
            controller.close_for_cleanup.assert_called_once()

            with (
                mock.patch.object(native_ui, "build_parser", return_value=SimpleNamespace(
                    parse_args=lambda _argv: args,
                )),
                mock.patch.object(native_ui, "_ui_path_is_launchable", return_value=True),
                mock.patch.object(native_ui, "run_journey", side_effect=failure),
                redirect_stderr(StringIO()),
            ):
                self.assertEqual(native_ui.main([]), 1)

            self.assertEqual(
                json.loads(output.read_text(encoding="utf-8"))["checks"],
                {"configure_native": True},
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

    def test_windows_job_close_result_has_explicit_outcome_and_diagnostics(self) -> None:
        class FakeJob:
            closed = False
            diagnostics: list[str] = []

            def close(self, *, deadline=None):
                self.diagnostics.append("api=CloseHandle winerror=5")

        result = _close_job(FakeJob(), stage="test")

        self.assertIsInstance(result, WindowsJobCloseResult)
        self.assertFalse(isinstance(result, tuple))
        self.assertTrue(result.failed)
        self.assertIn("api=CloseHandle winerror=5", result.diagnostics)
        self.assertTrue(any("job-still-attached" in item for item in result.diagnostics))


if __name__ == "__main__":
    unittest.main()
