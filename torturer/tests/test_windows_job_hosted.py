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

    def test_subprocess_runner_returns_exact_streams_without_retaining_logs(self) -> None:
        scratch = Path(self.directory.name) / "scratch"
        result = SubprocessRunner(scratch).run(
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
        self.assertEqual(tuple(scratch.iterdir()), ())

    def test_subprocess_runner_does_not_synthesize_an_application_log(self) -> None:
        scratch = Path(self.directory.name) / "application-log-scratch"
        scratch.mkdir()
        app_log = scratch / "app.log"
        app_log.write_bytes(b"product-owned application event\n")
        executable = Path(sys.executable).resolve()

        result = SubprocessRunner(
            scratch,
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
        self.assertEqual(tuple(scratch.iterdir()), (app_log,))

    def test_timeout_uses_windows_job_cleanup(self) -> None:
        scratch = Path(self.directory.name) / "timeout-scratch"
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
            with self.assertRaisesRegex(HostedAdapterError, "COMMAND_TIMEOUT") as caught:
                SubprocessRunner(scratch).run(("synthetic-command",), timeout_seconds=1.0)

        terminate.assert_called_once()
        close.assert_called_once()
        self.assertIn("command_returncode=124", caught.exception.__notes__)
        self.assertIn("command_timed_out=True", caught.exception.__notes__)

    def test_timeout_preserves_primary_and_cleanup_errors(self) -> None:
        scratch = Path(self.directory.name) / "cleanup-error-scratch"
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
                SubprocessRunner(scratch).run(("synthetic-command",), timeout_seconds=1.0)

        self.assertTrue(any("termination" in note for note in caught.exception.__notes__))
        self.assertTrue(any("close" in note for note in caught.exception.__notes__))

    def test_windows_job_setup_failure_reports_containment_code(self) -> None:
        scratch = Path(self.directory.name) / "setup-failure-scratch"
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
                SubprocessRunner(scratch).run(("synthetic-command",), timeout_seconds=1.0)


if __name__ == "__main__":
    unittest.main()
