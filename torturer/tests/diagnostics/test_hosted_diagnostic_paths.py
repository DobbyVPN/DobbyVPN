from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.engine import ScenarioExecutionError
from torturer_runner import lane as run
from torturer_runner.adapters import linux
from torturer_runner.adapters.cli import CommandResult, CLIAdapter


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


def _exception_streams(message: str, stdout: bytes, stderr: bytes) -> RuntimeError:
    error = RuntimeError(message)
    error.stdout = stdout
    error.stderr = stderr
    error.add_note("original secondary note")
    return error


class RestartedServiceLogTests(unittest.TestCase):
    def test_forwards_exact_log_bytes_while_file_still_exists(self) -> None:
        payload = b'credential="service-profile"\x00\xff\nlast-line'
        with tempfile.TemporaryDirectory() as name:
            service_log = Path(name) / "service.log"
            service_log.write_bytes(payload)
            controller = linux.LinuxServiceProcessController.__new__(
                linux.LinuxServiceProcessController
            )
            controller.service_log = service_log
            controller._restart_number = 1
            original_emit = linux.emit_streams
            forwarded = BinaryStderr()

            def emit_before_delete(label, stdout, stderr):
                self.assertEqual(label, "restarted-service")
                self.assertTrue(service_log.is_file())
                self.assertEqual(stdout, payload)
                return original_emit(label, stdout, stderr)

            with mock.patch.object(linux, "emit_streams", side_effect=emit_before_delete):
                with redirect_stderr(forwarded):
                    controller.cleanup_scratch()

            self.assertFalse(service_log.exists())
            self.assertIn(b"[restarted-service stdout]\n" + payload, forwarded.buffer.getvalue())

    def test_collection_failure_is_reported_after_cleanup_is_attempted(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            service_log = Path(name) / "missing-service.log"
            controller = linux.LinuxServiceProcessController.__new__(
                linux.LinuxServiceProcessController
            )
            controller.service_log = service_log
            controller._restart_number = 1

            with self.assertRaises(linux.ScenarioExecutionError) as caught:
                controller.cleanup_scratch()

            self.assertEqual(caught.exception.reason_code, "SERVICE_LOG_COLLECTION_FAILED")
            self.assertTrue(any("FileNotFoundError" in note for note in caught.exception.__notes__))
            self.assertFalse(service_log.exists())

    def test_collection_and_unlink_failures_are_both_retained(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            service_log = Path(name) / "service-directory"
            service_log.mkdir()
            controller = linux.LinuxServiceProcessController.__new__(
                linux.LinuxServiceProcessController
            )
            controller.service_log = service_log
            controller._restart_number = 1

            with self.assertRaises(linux.ScenarioExecutionError) as caught:
                controller.cleanup_scratch()

            self.assertEqual(caught.exception.reason_code, "SERVICE_LOG_COLLECTION_FAILED")
            details = "\n".join(caught.exception.__notes__)
            self.assertIn("service_log_collection_error=IsADirectoryError", details)
            self.assertIn("service_scratch_cleanup_error=ScenarioExecutionError", details)
            self.assertIn("service_log_cleanup_error=IsADirectoryError", details)
            self.assertTrue(service_log.is_dir())


class HostedMeasurementDiagnosticsTests(unittest.TestCase):
    @staticmethod
    def adapter_for(result: CommandResult) -> tuple[CLIAdapter, mock.Mock]:
        adapter = CLIAdapter.__new__(CLIAdapter)
        runner = mock.Mock()
        runner.run.return_value = result
        adapter.runner = runner
        return adapter, runner

    @staticmethod
    def response(body: bytes, *, seconds: str, byte_count: str, status: str) -> bytes:
        return (
            body
            + b"\nDOBBYVPN_CURL_METRIC\t"
            + f"{seconds}\t{byte_count}\t{status}".encode("ascii")
        )

    def test_successful_measurement_captures_body_and_preserves_metric(self) -> None:
        body = b"measurement-response-body\x00\xff"
        result = CommandResult(
            command=("curl",),
            returncode=0,
            stdout=self.response(body, seconds="2", byte_count="500000", status="200"),
            stderr=b"curl diagnostic stream\n",
        )
        adapter, runner = self.adapter_for(result)

        metrics = adapter._curl_metric("https://measurement.invalid/down", 8, upload=False)

        self.assertEqual(metrics, (2000.0, 2.0))
        command = runner.run.call_args.args[0]
        self.assertNotIn("--output", command)
        self.assertIn("--write-out", command)
        self.assertNotIn("/dev/null", command)
        self.assertIn(body, result.stdout)
        self.assertIn(b"curl diagnostic stream", result.stderr)

    def test_non_2xx_retains_body_and_both_command_streams(self) -> None:
        body = b"measurement-service-error-body\x00\xff"
        stdout = self.response(body, seconds="1", byte_count="256", status="503")
        stderr = b"upstream diagnostic detail\n"
        result = CommandResult(
            command=("curl",), returncode=0, stdout=stdout, stderr=stderr
        )
        adapter, _runner = self.adapter_for(result)
        forwarded = BinaryStderr()

        with redirect_stderr(forwarded):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._curl_metric("https://measurement.invalid/down", 8, upload=False)

        self.assertEqual(
            getattr(caught.exception, "reason_code", None),
            "MEASUREMENT_SERVICE_UNAVAILABLE",
        )
        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("measurement-service-error-body", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("upstream diagnostic detail", notes)
        emitted = forwarded.buffer.getvalue()
        self.assertIn(body, emitted)
        self.assertIn(stderr, emitted)


class HostedStabilityTests(unittest.TestCase):
    def test_stability_uses_five_lightweight_identity_requests(self) -> None:
        adapter = CLIAdapter.__new__(CLIAdapter)
        adapter.identity_url = "https://identity.invalid"
        adapter.download_url = "https://measurement.invalid/down?bytes=1048576"
        adapter.runner = mock.Mock()
        adapter.runner.run.return_value = CommandResult(
            command=("curl",),
            returncode=0,
            stdout=b"198.51.100.17",
        )
        adapter._connected = mock.Mock(return_value=True)

        with mock.patch("torturer_runner.adapters.cli.time.sleep"):
            observation = adapter._stability(15)

        self.assertEqual(observation["stability_verified"], True)
        self.assertEqual(observation["stability_sample_count"], 5)
        self.assertEqual(adapter.runner.run.call_count, 5)
        for call in adapter.runner.run.call_args_list:
            command = call.args[0]
            self.assertEqual(command[-1], adapter.identity_url)
            self.assertNotIn(adapter.download_url, command)


class HostedSecondaryFailureTests(unittest.TestCase):
    def test_scenario_cleanup_error_is_fully_attached_to_primary(self) -> None:
        primary = RuntimeError("scenario operation failed")
        cleanup_error = _exception_streams(
            "reset lost the original session", b"cleanup stdout\x00\xff", b"cleanup stderr\n"
        )

        class Adapter:
            capabilities = frozenset()

            def set_progress_sink(self, _sink):
                pass

            def reset(self, *, timeout_seconds):
                raise cleanup_error

        class Engine:
            def run(self, *_args, **_kwargs):
                raise primary

        scenario = SimpleNamespace(
            id="diagnostic-scenario",
            required_capabilities=frozenset(),
        )
        connection = SimpleNamespace(index=0, protocol="outline")
        provenance = SimpleNamespace(platform="linux")
        progress = StringIO()

        with redirect_stdout(progress):
            with self.assertRaises(RuntimeError) as caught:
                run._run_scenarios(
                    Engine(), [scenario], Adapter(), provenance, connection
                )

        self.assertIs(caught.exception, primary)
        notes = "\n".join(primary.__notes__)
        self.assertIn("reset lost the original session", notes)
        self.assertIn("original secondary note", notes)
        self.assertIn("cleanup stdout", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("cleanup stderr", notes)
        self.assertIn('"outcome": "error"', progress.getvalue())

    def test_adapter_finalization_error_is_fully_attached_to_primary(self) -> None:
        primary = RuntimeError("connection discovery failed")
        finalization_error = _exception_streams(
            "finalizer could not release service", b"finalize stdout\x00\xff", b"finalize stderr\n"
        )

        class Adapter:
            def discover_connections(self, *, timeout_seconds):
                raise primary

            def select_connection(self, _connection):
                pass

            def finalize(self, *, timeout_seconds, deadline):
                raise finalization_error

        progress = StringIO()
        with redirect_stdout(progress):
            with self.assertRaises(RuntimeError) as caught:
                run._execute_lane(
                    object(), [], Adapter(), SimpleNamespace(platform="linux"), deadline=None
                )

        self.assertIs(caught.exception, primary)
        notes = "\n".join(primary.__notes__)
        self.assertIn("finalizer could not release service", notes)
        self.assertIn("original secondary note", notes)
        self.assertIn("finalize stdout", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("finalize stderr", notes)
        self.assertIn('"finalized": false', progress.getvalue())


if __name__ == "__main__":
    unittest.main()
