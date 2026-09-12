"""Native Windows Job Object integration tests for hosted CI."""

from __future__ import annotations

import io
import os
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

from torturer_checks.windows_job import (
    close_for,
    job_for,
    popen_with_windows_job,
    terminate_and_prove_empty,
    wait_for_empty,
)
from torturer_checks.hosted.cli import SubprocessRunner
from torturer_checks.hosted.windows import WindowsServiceProcessController


_WINDOWS_PROCESS_IDENTITY_PROBE_TIMEOUT_SECONDS = 30.0


def _exception_stream_bytes(error: BaseException, stream: str) -> bytes:
    """Keep every stream byte exposed by a failed subprocess call."""

    values = ("output", "stdout") if stream == "stdout" else (stream,)
    combined = b""
    for name in values:
        value = getattr(error, name, None)
        if isinstance(value, str):
            value = value.encode("utf-8", errors="replace")
        if not isinstance(value, bytes) or not value:
            continue
        if not combined or combined.startswith(value) or value.startswith(combined):
            combined = value if len(value) > len(combined) else combined
        else:
            combined += value
    return combined


def _identity_probe_failure(error: BaseException) -> str:
    """Format safe probe metadata while preserving complete output bytes."""

    error_type = type(error).__name__
    if not error_type.isidentifier():
        error_type = "Exception"
    errno = getattr(error, "errno", 0)
    if isinstance(errno, bool) or not isinstance(errno, int):
        errno = 0
    timeout = getattr(error, "timeout", None)
    stdout = _exception_stream_bytes(error, "stdout")
    stderr = _exception_stream_bytes(error, "stderr")
    return (
        "process identity PowerShell probe failed\n"
        f"exception_type={error_type} errno={errno} timeout={timeout!r}\n"
        f"stdout={stdout!r}\n"
        f"stderr={stderr!r}\n"
    )


@unittest.skipUnless(os.name == "nt", "native Windows Job Objects require Windows")
class WindowsJobIntegrationTests(unittest.TestCase):
    """Exercise the same native boundary used by hosted ordinary commands."""

    def _process_identity(self, pid: int) -> str:
        # ``powershell.exe -Command <script> <arg>`` does not bind the final
        # token to ``$args`` consistently across Windows PowerShell versions:
        # it can execute it as a second command instead.  Embed this
        # test-created integer in the script so the probe cannot accidentally
        # query PID 0 and append the argument as an extra output line.
        process_id = int(pid)
        script = (
            '$ErrorActionPreference="Stop"; '
            f'$processId={process_id}; '
            '$p=Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $processId); '
            'if ($null -eq $p -or $null -eq $p.CreationDate) { exit 2 }; '
            'Write-Output ([string]$p.ProcessId + "|" + '
            '[string]$p.CreationDate.ToUniversalTime().Ticks)'
        )
        command = (
            "powershell.exe",
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            script,
        )
        try:
            # WMI can take longer than 10 seconds on a cold Windows hosted
            # runner.  Keep this bounded at 30 seconds; the integration job
            # itself has a five-minute limit and all lifecycle assertions stay
            # unchanged.
            result = subprocess.run(
                command,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=_WINDOWS_PROCESS_IDENTITY_PROBE_TIMEOUT_SECONDS,
                check=False,
            )
        except (subprocess.TimeoutExpired, OSError) as error:
            diagnostics = _identity_probe_failure(error)
            print(diagnostics, file=sys.stderr, end="")
            self.fail(diagnostics.rstrip("\n"))
        self.assertEqual(result.returncode, 0, result.stderr.decode("utf-8", "replace"))
        identity = result.stdout.decode("ascii").strip()
        self.assertRegex(identity, rf"^{pid}\|[1-9][0-9]+$")
        return identity

    @staticmethod
    def _unused_tcp_port() -> int:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
            listener.bind(("127.0.0.1", 0))
            return int(listener.getsockname()[1])

    def _launch(self, code: str, *, stage: str):
        return popen_with_windows_job(
            subprocess.Popen,
            [sys.executable, "-c", code],
            stdin=None,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            stage=stage,
            deadline=time.monotonic() + 30.0,
        )

    def _close_process(self, process) -> None:
        failures: list[str] = []
        job = job_for(process)
        if job is not None:
            teardown_deadline = time.monotonic() + 10.0
            active = job.query_active_processes(deadline=teardown_deadline)
            if active is None:
                failures.append(
                    "initial active-process query failed: " + "; ".join(job.diagnostics)
                )
            elif active != 0:
                cleanup = terminate_and_prove_empty(
                    process,
                    deadline=teardown_deadline,
                    stage="integration-teardown",
                )
                if not cleanup.process_tree_proven or cleanup.active_processes != 0:
                    failures.append(
                        "terminate proof failed: " + "; ".join(cleanup.diagnostics)
                    )
            try:
                process.communicate(timeout=5.0)
            except subprocess.TimeoutExpired:
                failures.append("communicate timeout")
                try:
                    process.kill()
                    process.communicate(timeout=5.0)
                except (OSError, subprocess.TimeoutExpired) as error:
                    failures.append(f"reap failed: {type(error).__name__}")
            except OSError as error:
                failures.append(f"communicate failed: {type(error).__name__}")
            close_diagnostics = close_for(
                process,
                stage="integration-teardown",
                deadline=time.monotonic() + 5.0,
            )
            if close_diagnostics:
                failures.append("close failed: " + "; ".join(close_diagnostics))
        elif process.poll() is None:
            try:
                process.kill()
                process.communicate(timeout=5.0)
            except (OSError, subprocess.TimeoutExpired) as error:
                failures.append(f"uncontained reap failed: {type(error).__name__}")
        if failures:
            self.fail("; ".join(failures))

    def test_suspended_process_is_assigned_before_resume_and_proves_empty(self) -> None:
        process = self._launch(
            "import sys; sys.stdout.write('resumed\\n'); sys.stdout.flush()",
            stage="integration-suspended",
        )
        try:
            job = job_for(process)
            self.assertIsNotNone(job)
            assert job is not None
            self.assertTrue(job.assigned)
            stdout, stderr = process.communicate(timeout=10.0)
            self.assertEqual(stdout, b"resumed" + os.linesep.encode("ascii"))
            self.assertEqual(stderr, b"")
            self.assertTrue(
                wait_for_empty(process, deadline=time.monotonic() + 10.0).process_tree_proven
            )
        finally:
            self._close_process(process)

    def test_root_exit_with_child_and_grandchild_is_contained_and_cleaned(self) -> None:
        with tempfile.TemporaryDirectory(prefix="windows-job-descendants-") as temporary:
            root = Path(temporary)
            child_ready = root / "child.ready"
            grandchild_ready = root / "grandchild.ready"
            grandchild_code = (
                "import pathlib, time; "
                f"pathlib.Path({str(grandchild_ready)!r}).write_text('ready'); "
                "time.sleep(30)"
            )
            child_code = (
                "import pathlib, subprocess, sys, time; "
                f"subprocess.Popen([sys.executable, '-c', {grandchild_code!r}], "
                "stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL); "
                f"pathlib.Path({str(child_ready)!r}).write_text('ready'); "
                "time.sleep(30)"
            )
            code = (
                "import pathlib, subprocess, sys, time\n"
                f"subprocess.Popen([sys.executable, '-c', {child_code!r}], "
                "stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\n"
                f"deadline = time.monotonic() + 10\n"
                f"ready = pathlib.Path({str(child_ready)!r}), pathlib.Path({str(grandchild_ready)!r})\n"
                "while time.monotonic() < deadline and not all(path.is_file() for path in ready):\n"
                "    time.sleep(0.01)\n"
                "print('root-exited', flush=True)\n"
            )
            process = self._launch(code, stage="integration-descendants")
            try:
                stdout, stderr = process.communicate(timeout=15.0)
                self.assertEqual(stdout, b"root-exited" + os.linesep.encode("ascii"))
                self.assertEqual(stderr, b"")
                self.assertIsNotNone(process.returncode)
                self.assertTrue(child_ready.is_file())
                self.assertTrue(grandchild_ready.is_file())
                job = job_for(process)
                self.assertIsNotNone(job)
                assert job is not None
                active = job.query_active_processes(deadline=time.monotonic() + 10.0)
                self.assertIsNotNone(active)
                self.assertGreaterEqual(active or 0, 2)
                cleanup = terminate_and_prove_empty(
                    process,
                    deadline=time.monotonic() + 10.0,
                    stage="integration-descendants",
                )
                self.assertTrue(cleanup.process_tree_proven, cleanup.diagnostics)
                self.assertEqual(cleanup.active_processes, 0)
            finally:
                self._close_process(process)

    def test_job_termination_does_not_touch_outside_sentinel(self) -> None:
        sentinel = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        process = self._launch("import time; time.sleep(30)", stage="integration-sentinel")
        try:
            cleanup = terminate_and_prove_empty(
                process,
                deadline=time.monotonic() + 10.0,
                stage="integration-sentinel",
            )
            self.assertTrue(cleanup.process_tree_proven, cleanup.diagnostics)
            self.assertIsNone(sentinel.poll())
        finally:
            self._close_process(process)
            sentinel.kill()
            sentinel.wait(timeout=10.0)

    def test_complete_stdout_and_stderr_are_retained(self) -> None:
        expected_stdout = b"complete-out\x00\xff"
        expected_stderr = b"complete-err\x00\xfe"
        with tempfile.TemporaryDirectory() as temporary:
            raw_directory = Path(temporary) / "runner-raw"
            runner = SubprocessRunner(raw_directory)
            result = runner.run(
                (
                    sys.executable,
                    "-c",
                    "import sys; sys.stdout.buffer.write(b'complete-out\\x00\\xff'); "
                    "sys.stderr.buffer.write(b'complete-err\\x00\\xfe'); "
                    "sys.stdout.flush(); sys.stderr.flush()",
                ),
                timeout_seconds=10.0,
            )

            self.assertEqual(result.stdout, expected_stdout)
            self.assertEqual(result.stderr, expected_stderr)
            retained_files = list(raw_directory.glob("command-001*.raw.log"))
            self.assertEqual(len(retained_files), 1)
            retained = retained_files[0]
            content = retained.read_bytes()
            self.assertIn(b"stdout-begin\n" + expected_stdout + b"\nstdout-end\n", content)
            self.assertIn(b"stderr-begin\n" + expected_stderr + b"\nstderr-end\n", content)

    def test_parent_can_close_redirected_handles_immediately(self) -> None:
        expected_stdout = b"delayed-out\x00\xff"
        expected_stderr = b"delayed-err\x00\xfe"
        with tempfile.TemporaryDirectory(prefix="windows-job-closed-handles-") as temporary:
            root = Path(temporary)
            stdout_path = root / "child.stdout"
            stderr_path = root / "child.stderr"
            stdout_file = stdout_path.open("wb", buffering=0)
            stderr_file = stderr_path.open("wb", buffering=0)
            try:
                process = popen_with_windows_job(
                    subprocess.Popen,
                    [
                        sys.executable,
                        "-c",
                        "import sys, time; time.sleep(0.2); "
                        "sys.stdout.buffer.write(b'delayed-out\\x00\\xff'); "
                        "sys.stderr.buffer.write(b'delayed-err\\x00\\xfe'); "
                        "sys.stdout.flush(); sys.stderr.flush()",
                    ],
                    stdin=subprocess.DEVNULL,
                    stdout=stdout_file,
                    stderr=stderr_file,
                    stage="integration-closed-parent-handles",
                    deadline=time.monotonic() + 10.0,
                )
            finally:
                # The child owns inherited duplicates of these native handles;
                # the hosted controller may close its parent-side objects as
                # soon as Popen returns.
                stdout_file.close()
                stderr_file.close()
            try:
                process.wait(timeout=10.0)
                self.assertTrue(
                    wait_for_empty(process, deadline=time.monotonic() + 10.0).process_tree_proven
                )
            finally:
                self._close_process(process)
            self.assertEqual(stdout_path.read_bytes(), expected_stdout)
            self.assertEqual(stderr_path.read_bytes(), expected_stderr)
            self.assertIsNone(job_for(process))

    def test_windows_service_controller_owns_native_replacement_lifecycle(self) -> None:
        with tempfile.TemporaryDirectory(prefix="windows-service-controller-") as temporary:
            root = Path(temporary)
            raw = root / "raw"
            raw.mkdir()
            identity_file = root / "service.identity"
            pid_file = root / "service.pid"
            port = self._unused_tcp_port()
            initial = subprocess.Popen(
                (sys.executable, "-c", "import time; time.sleep(30)"),
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            controller = None
            try:
                identity = self._process_identity(initial.pid)
                identity_file.write_text(identity + "\n", encoding="ascii")
                replacement_code = (
                    "import socket,time; "
                    "listener=socket.socket(socket.AF_INET,socket.SOCK_STREAM); "
                    "listener.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); "
                    f"listener.bind(('127.0.0.1',{port})); listener.listen(); "
                    "print('service-ready',flush=True); time.sleep(30)"
                )
                controller = WindowsServiceProcessController(
                    pid=initial.pid,
                    binary=Path(sys.executable),
                    pid_file=pid_file,
                    identity_file=identity_file,
                    runner=SubprocessRunner(raw),
                    raw_directory=raw,
                    control_address=f"127.0.0.1:{port}",
                    expected_initial_identity=identity,
                    initialization_deadline=time.monotonic() + 10.0,
                    replacement_command=(sys.executable, "-c", replacement_code),
                )
                controller.restart_after_loss(25.0)
                initial.wait(timeout=10.0)
                initial_stdout, initial_stderr = initial.communicate(timeout=1.0)
                self.assertEqual(initial_stdout, b"")
                self.assertEqual(initial_stderr, b"")
                replacement = controller._replacement_process
                self.assertIsNotNone(replacement)
                assert replacement is not None
                self.assertIsNotNone(job_for(replacement))
                controller.finalize_restarted_service(15.0)
                controller.finalize_evidence(5.0)
                self.assertIsNotNone(replacement.poll())
                self.assertIsNone(job_for(replacement))
            finally:
                if controller is not None and controller._replacement_process is not None:
                    replacement = controller._replacement_process
                    terminate_and_prove_empty(
                        replacement,
                        deadline=time.monotonic() + 5.0,
                        stage="integration-service-controller-finally",
                    )
                    try:
                        replacement.wait(timeout=5.0)
                    except subprocess.TimeoutExpired:
                        replacement.kill()
                        replacement.wait(timeout=5.0)
                    close_for(
                        replacement,
                        stage="integration-service-controller-finally",
                        deadline=time.monotonic() + 5.0,
                    )
                if initial.poll() is None:
                    initial.kill()
                    initial.wait(timeout=5.0)
                initial.communicate(timeout=1.0)

class WindowsJobIdentityDiagnosticsTests(unittest.TestCase):
    """Keep the direct WMI test helper diagnosable on every host."""

    def _probe_helper(self) -> WindowsJobIntegrationTests:
        return WindowsJobIntegrationTests("runTest")

    def test_identity_timeout_reports_complete_streams(self) -> None:
        error = subprocess.TimeoutExpired(
            ("powershell.exe",),
            _WINDOWS_PROCESS_IDENTITY_PROBE_TIMEOUT_SECONDS,
            output=b"identity-partial-stdout\x00\xff",
            stderr=b"identity-partial-stderr\x00\xfe",
        )
        with mock.patch.object(subprocess, "run", side_effect=error), mock.patch.object(
            sys, "stderr", new_callable=io.StringIO
        ) as diagnostics:
            with self.assertRaises(AssertionError) as caught:
                self._probe_helper()._process_identity(123)
        message = str(caught.exception)
        self.assertIn("exception_type=TimeoutExpired", message)
        self.assertIn("timeout=30.0", message)
        self.assertIn("identity-partial-stdout", message)
        self.assertIn("identity-partial-stderr", message)
        self.assertIn(message + "\n", diagnostics.getvalue())

    def test_identity_oserror_reports_complete_streams(self) -> None:
        error = OSError(5, "private host path must not be emitted")
        error.output = b"identity-oserror-output\x00\xff"  # type: ignore[attr-defined]
        error.stdout = b"identity-oserror-stdout\x00\xff"  # type: ignore[attr-defined]
        error.stderr = b"identity-oserror-stderr\x00\xfe"  # type: ignore[attr-defined]
        with mock.patch.object(subprocess, "run", side_effect=error), mock.patch.object(
            sys, "stderr", new_callable=io.StringIO
        ) as diagnostics:
            with self.assertRaises(AssertionError) as caught:
                self._probe_helper()._process_identity(123)
        message = str(caught.exception)
        self.assertIn("exception_type=OSError", message)
        self.assertIn("errno=5", message)
        self.assertIn("identity-oserror-output", message)
        self.assertIn("identity-oserror-stdout", message)
        self.assertIn("identity-oserror-stderr", message)
        self.assertNotIn("private host path", message)
        self.assertIn(message + "\n", diagnostics.getvalue())


if __name__ == "__main__":
    unittest.main()
