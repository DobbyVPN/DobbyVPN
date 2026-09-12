from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import torturer_checks.hosted.cli as hosted_cli
from torturer_checks.hosted.cli import HostedAdapterError, SubprocessRunner
from torturer_checks.windows_job import (
    WindowsJobCleanup,
    WindowsJobCloseDiagnostics,
    WindowsJobError,
)


class _FakeProcess:
    pid = 9137
    returncode = 0

    def __init__(self, *, timeout: bool = False) -> None:
        self._timeout = timeout
        self.communicate_calls = 0

    def communicate(self, **_kwargs):
        self.communicate_calls += 1
        if self._timeout and self.communicate_calls == 1:
            raise subprocess.TimeoutExpired(
                ("synthetic-command",),
                0.1,
                output=b"timeout-stdout\x00",
                stderr=b"timeout-stderr\x00",
            )
        return b"timeout-stdout\x00-final", b"timeout-stderr\x00-final"


class HostedWindowsJobRunnerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)

    def test_subprocess_runner_retains_exact_streams(self) -> None:
        raw = Path(self.directory.name) / "raw"
        result = SubprocessRunner(raw).run(
            (
                sys.executable,
                "-c",
                "import sys; sys.stdout.buffer.write(b'out\\x00\\xff'); "
                "sys.stderr.buffer.write(b'err\\x00\\xfe')",
            ),
            timeout_seconds=5,
        )

        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b"out\x00\xff")
        self.assertEqual(result.stderr, b"err\x00\xfe")
        retained = (raw / "command-001.raw.log").read_bytes()
        self.assertIn(b"stdout-begin\nout\x00\xff\nstdout-end", retained)
        self.assertIn(b"stderr-begin\nerr\x00\xfe\nstderr-end", retained)

    def test_subprocess_runner_does_not_synthesize_an_application_log(self) -> None:
        raw = Path(self.directory.name) / "application-log-raw"
        raw.mkdir()
        app_log = raw / "app.log"
        app_log.write_bytes(b"product-owned application event\n")
        executable = Path(sys.executable).resolve()

        result = SubprocessRunner(
            raw,
        ).run(
            (
                str(executable),
                "-c",
                "import sys; sys.stdout.buffer.write(b'outerr'); "
                "sys.stderr.buffer.write(b'')",
            ),
            timeout_seconds=5,
        )

        self.assertEqual(result.returncode, 0)
        self.assertEqual(app_log.read_bytes(), b"product-owned application event\n")
        retained = (raw / "command-001.raw.log").read_bytes()
        self.assertIn(b"stdout-begin\nouterr\nstdout-end", retained)

    def test_timeout_uses_windows_job_cleanup_and_retains_output(self) -> None:
        raw = Path(self.directory.name) / "timeout-raw"
        process = _FakeProcess(timeout=True)
        terminate = mock.Mock(
            return_value=WindowsJobCleanup(
                True,
                0,
                ("api=TerminateJobObject winerror=0",),
            )
        )
        close = mock.Mock(return_value=WindowsJobCloseDiagnostics())
        with (
            mock.patch.object(hosted_cli.os, "name", "nt"),
            mock.patch.object(hosted_cli, "popen_with_windows_job", return_value=process),
            mock.patch.object(hosted_cli, "windows_job_for", return_value=object()),
            mock.patch.object(hosted_cli, "terminate_windows_job", terminate),
            mock.patch.object(hosted_cli, "close_windows_job", close),
        ):
            with self.assertRaisesRegex(HostedAdapterError, "COMMAND_TIMEOUT"):
                SubprocessRunner(raw).run(("synthetic-command",), timeout_seconds=1.0)

        terminate.assert_called_once()
        close.assert_called_once()
        retained = (raw / "command-001.raw.log").read_bytes()
        self.assertIn(b"timeout-stdout\x00-final", retained)
        self.assertIn(b"timeout-stderr\x00-final", retained)
        self.assertIn(b"api=TerminateJobObject winerror=0", retained)

    def test_timeout_retains_primary_and_cleanup_errors(self) -> None:
        raw = Path(self.directory.name) / "cleanup-error-raw"
        process = _FakeProcess(timeout=True)
        with (
            mock.patch.object(hosted_cli.os, "name", "nt"),
            mock.patch.object(hosted_cli, "popen_with_windows_job", return_value=process),
            mock.patch.object(hosted_cli, "windows_job_for", return_value=object()),
            mock.patch.object(
                hosted_cli,
                "terminate_windows_job",
                side_effect=OSError("synthetic terminate failure"),
            ),
            mock.patch.object(
                hosted_cli,
                "close_windows_job",
                side_effect=OSError("synthetic close failure"),
            ),
        ):
            with self.assertRaisesRegex(HostedAdapterError, "COMMAND_TIMEOUT") as caught:
                SubprocessRunner(raw).run(("synthetic-command",), timeout_seconds=1.0)

        self.assertTrue(any("termination" in note for note in caught.exception.__notes__))
        self.assertTrue(any("close" in note for note in caught.exception.__notes__))
        retained = (raw / "command-001.raw.log").read_bytes()
        self.assertIn(b"primary_timeout_type=TimeoutExpired", retained)
        self.assertIn(b"termination_error_type=OSError", retained)
        self.assertIn(b"close_error_type=OSError", retained)
        self.assertIn(b"timeout-stdout\x00-final", retained)
        self.assertIn(b"timeout-stderr\x00-final", retained)

    def test_windows_job_setup_failure_retains_original_output_and_diagnostics(self) -> None:
        raw = Path(self.directory.name) / "setup-failure-raw"
        error = WindowsJobError(
            "hosted-cli-command",
            ("api=AssignProcessToJobObject winerror=5",),
            stdout=b"setup-stdout\x00\xff",
            stderr=b"setup-stderr\x00\xfe",
        )
        with (
            mock.patch.object(hosted_cli.os, "name", "nt"),
            mock.patch.object(hosted_cli, "popen_with_windows_job", side_effect=error),
        ):
            with self.assertRaisesRegex(
                HostedAdapterError, "PROCESS_CONTAINMENT_UNAVAILABLE"
            ):
                SubprocessRunner(raw).run(("synthetic-command",), timeout_seconds=1.0)

        retained = (raw / "command-001.raw.log").read_bytes()
        self.assertIn(b"setup-stdout\x00\xff", retained)
        self.assertIn(b"setup-stderr\x00\xfe", retained)
        self.assertIn(b"api=AssignProcessToJobObject winerror=5", retained)


if __name__ == "__main__":
    unittest.main()
