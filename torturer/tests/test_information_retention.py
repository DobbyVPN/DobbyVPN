from __future__ import annotations

from contextlib import redirect_stderr
from io import BytesIO, StringIO
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_checks import ios_simulator_app
from torturer_checks.hosted import android, macos, native_ui
from torturer_checks.windows_job import (
    WindowsJobCloseResult,
    _close_job,
)
from torturer_contract.functional.engine import ScenarioExecutionError


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

    def test_macos_restore_streams_are_forwarded_before_scratch_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            adapter = object.__new__(macos.MacOSHostedAdapter)
            adapter.local_mode = True
            adapter.network_interface = "en0"
            adapter.network_transition_helper = root / "network-transition"
            adapter.raw_directory = root

            def allocate(directory: Path, prefix: str, suffix: str) -> Path:
                path = directory / f"{prefix}{suffix}"
                path.touch()
                return path

            def command(arguments, _timeout, _failure_code):
                if arguments[3] == "arm":
                    Path(arguments[5]).write_text("restore_status=1\n", encoding="ascii")
                    Path(arguments[6]).write_bytes(b"restore stdout\x00\xff\n")
                    Path(arguments[7]).write_bytes(b"restore stderr\n")
                    raise ScenarioExecutionError("NETWORK_DOWN_FAILED")
                return SimpleNamespace(stdout_text="", stderr_text="")

            adapter._network_command = command
            forwarded = BinaryStderr()
            with mock.patch.object(
                macos, "_allocate_scratch_path", side_effect=allocate
            ):
                with redirect_stderr(forwarded):
                    with self.assertRaises(ScenarioExecutionError):
                        adapter._network_transition(5)

            output = forwarded.buffer.getvalue()
            self.assertIn(b"restore stdout\x00\xff", output)
            self.assertIn(b"restore stderr", output)
            self.assertEqual(list(root.iterdir()), [])

    def test_macos_restore_streams_survive_delivery_failure(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            adapter = object.__new__(macos.MacOSHostedAdapter)
            adapter.local_mode = True
            adapter.network_interface = "en0"
            adapter.network_transition_helper = root / "network-transition"
            adapter.raw_directory = root

            def allocate(directory: Path, prefix: str, suffix: str) -> Path:
                path = directory / f"{prefix}{suffix}"
                path.touch()
                return path

            def command(arguments, _timeout, _failure_code):
                if arguments[3] == "arm":
                    Path(arguments[5]).write_text("restore_status=1\n", encoding="ascii")
                    Path(arguments[6]).write_bytes(b"restore stdout\x00\xff\n")
                    Path(arguments[7]).write_bytes(b"restore stderr\n")
                    raise ScenarioExecutionError("NETWORK_DOWN_FAILED")
                return SimpleNamespace(stdout_text="", stderr_text="")

            adapter._network_command = command
            with (
                mock.patch.object(macos, "_allocate_scratch_path", side_effect=allocate),
                mock.patch.object(macos, "emit_streams", side_effect=OSError("output pipe closed")),
            ):
                with self.assertRaises(ScenarioExecutionError) as caught:
                    adapter._network_transition(5)

            stdout = root / "macos-network-repair.stdout.tmp"
            stderr = root / "macos-network-repair.stderr.tmp"
            self.assertEqual(stdout.read_bytes(), b"restore stdout\x00\xff\n")
            self.assertEqual(stderr.read_bytes(), b"restore stderr\n")
            self.assertIn(str(stdout), "\n".join(caught.exception.__notes__))
            self.assertIn(str(stderr), "\n".join(caught.exception.__notes__))

    def test_native_ui_failure_result_keeps_completed_checks(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            cli = root / "cli"
            ui = root / "ui"
            profile = root / "profile"
            smoke = root / "smoke"
            output = root / "native-ui.json"
            raw_logs = root / "logs"
            raw_logs.mkdir()
            for path in (cli, ui, profile, smoke):
                path.write_text("test", encoding="utf-8")
            args = SimpleNamespace(
                platform="windows",
                service_pipe="DobbyVPN.Control",
                service_socket=None,
                cli=cli,
                ui=ui,
                profile=profile,
                smoke_script=smoke,
                raw_log_dir=raw_logs,
                output=output,
                service_pid=123,
                service_binary=cli,
                service_library_path=None,
                service_pid_file=None,
                service_identity_file=None,
                network_interface=None,
                routing_firewall_helper=None,
                network_transition_helper=None,
                timeout=5,
            )
            base = mock.Mock()
            base.discover_connections.return_value = (object(),)
            native_process = mock.Mock()

            def request(operation, **_kwargs):
                if operation == "configure":
                    return {}
                raise native_ui.NativeUIJourneyError("connect failed")

            native_process.request.side_effect = request

            with (
                mock.patch.object(native_ui, "_ensure_directory"),
                mock.patch.object(native_ui, "SubprocessRunner"),
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "_NativeUIProcess", return_value=native_process),
            ):
                with self.assertRaises(native_ui.NativeUIJourneyError) as caught:
                    native_ui.run_journey(args)

            failure = caught.exception
            self.assertEqual(failure.native_ui_checks, {"configure_native": True})

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
            subprocess.TimeoutExpired(("command",), 1.0, output=final_stdout,
                                      stderr=final_stderr),
            (final_stdout, final_stderr),
        ]
        forwarded = BinaryStderr()

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch.object(ios_simulator_app, "_signal_process_group"),
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

    def test_android_composite_keeps_child_details_without_stream_duplication(self) -> None:
        child = android.HostedAdapterError("CHILD_OPERATION_FAILED")
        child.code = "ANDROID_CHILD_CODE"
        child.add_note("provider detail=control service rejected reset")
        child.add_note("adb_stdout:\nlarge output already emitted")
        aggregate = None
        try:
            android._composite_failure("reset", [("gui-auto", child)])
        except android.HostedAdapterError as error:
            aggregate = error

        self.assertIsNotNone(aggregate)
        assert aggregate is not None
        notes = "\n".join(aggregate.__notes__)
        self.assertIn("ANDROID_CHILD_CODE", notes)
        self.assertIn("control service rejected reset", notes)
        self.assertNotIn("large output already emitted", notes)

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
