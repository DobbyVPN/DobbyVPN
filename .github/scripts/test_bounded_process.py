import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import bounded_process


class BoundedProcessTests(unittest.TestCase):
    def test_output_text_preserves_invalid_bytes_reversibly(self) -> None:
        self.assertEqual(bounded_process.output_text(b"ok\xff"), r"ok\xff")

    def test_taskkill_forwards_complete_success_streams(self) -> None:
        result = subprocess.CompletedProcess(
            ["taskkill"], 0, stdout=b"taskkill stdout", stderr=b"taskkill stderr",
        )
        output = io.StringIO()
        with mock.patch.object(bounded_process.subprocess, "run", return_value=result), \
                mock.patch.object(bounded_process.sys, "stderr", output):
            bounded_process._run_windows_taskkill(123, 1)
        self.assertIn("taskkill stdout", output.getvalue())
        self.assertIn("taskkill stderr", output.getvalue())
        self.assertIn("[taskkill 123 stdout end]", output.getvalue())

    def test_taskkill_forwards_streams_before_failure(self) -> None:
        result = subprocess.CompletedProcess(
            ["taskkill"], 1, stdout=b"failed stdout", stderr=b"failed stderr",
        )
        output = io.StringIO()
        with mock.patch.object(bounded_process.subprocess, "run", return_value=result), \
                mock.patch.object(bounded_process.sys, "stderr", output):
            with self.assertRaises(bounded_process.ProcessCleanupError):
                bounded_process._run_windows_taskkill(123, 1)
        self.assertIn("failed stdout", output.getvalue())
        self.assertIn("failed stderr", output.getvalue())

    def test_output_fragments_merge_aliases_and_cumulative_snapshots(self) -> None:
        error = subprocess.TimeoutExpired("probe", 1, output=b"first")
        error.stdout = b"first"
        error.stderr = b"diagnostic"

        self.assertEqual(
            bounded_process.exception_output(error),
            (b"first", b"diagnostic"),
        )
        self.assertEqual(
            bounded_process.merge_output_fragments(b"first", b"first and second"),
            b"first and second",
        )

    def test_diagnostic_adds_a_real_newline(self) -> None:
        output = io.StringIO()
        with mock.patch.object(bounded_process.sys, "stderr", output):
            bounded_process.emit_process_diagnostic("analyzer failed")
        self.assertEqual(output.getvalue(), "analyzer failed\n")

    def test_cleanup_errors_are_attached_to_the_original_timeout(self) -> None:
        process = mock.Mock(pid=123, returncode=None)
        process.communicate.side_effect = subprocess.TimeoutExpired(
            "probe", 1, output=b"partial stdout", stderr=b"partial stderr",
        )
        with (
            mock.patch.object(bounded_process.subprocess, "Popen", return_value=process),
            mock.patch.object(
                bounded_process,
                "terminate_process_group",
                side_effect=bounded_process.ProcessCleanupError("tree cleanup failed"),
            ),
            mock.patch.object(
                bounded_process,
                "_drain_after_cleanup",
                side_effect=bounded_process.ProcessCleanupError("output drain failed"),
            ),
        ):
            with self.assertRaises(subprocess.TimeoutExpired) as raised:
                bounded_process.run_bounded_capture(
                    ["probe"], timeout_seconds=1, cleanup_grace_seconds=0.1,
                )

        self.assertEqual(raised.exception.stdout, "partial stdout")
        self.assertEqual(raised.exception.stderr, "partial stderr")
        notes = " ".join(getattr(raised.exception, "__notes__", []))
        self.assertIn("tree cleanup failed", notes)
        self.assertIn("output drain failed", notes)

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_timeout_preserves_streams_and_kills_sigterm_resistant_descendant(self) -> None:
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        parent_code = (
            "import signal,subprocess,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}]); "
            "print('childpid='+str(child.pid), flush=True); "
            "print('probe stderr', file=sys.stderr, flush=True); time.sleep(60)"
        )

        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(subprocess.TimeoutExpired) as raised:
                bounded_process.run_bounded_capture(
                    [sys.executable, "-c", parent_code],
                    cwd=Path(temporary),
                    timeout_seconds=1,
                    cleanup_grace_seconds=0.1,
                )

        stdout = bounded_process.output_text(raised.exception.stdout)
        self.assertIn("childpid=", stdout)
        self.assertIn("probe stderr", raised.exception.stderr)
        child_pid = int(stdout.split("childpid=", 1)[1].splitlines()[0])
        for _ in range(30):
            try:
                state = Path(f"/proc/{child_pid}/stat").read_text(encoding="ascii")
            except (FileNotFoundError, ProcessLookupError):
                break
            if state[state.rfind(")") + 2 :].split()[0] == "Z":
                break
            time.sleep(0.05)
        else:
            self.fail("timed-out probe descendant survived process-group cleanup")

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_timeout_escalates_when_leader_exits_but_descendant_ignores_sigterm(self) -> None:
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        parent_code = (
            "import subprocess,sys,time; "
            f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}]); "
            "print('childpid='+str(child.pid), flush=True); time.sleep(60)"
        )

        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(subprocess.TimeoutExpired) as raised:
                bounded_process.run_bounded_capture(
                    [sys.executable, "-c", parent_code],
                    cwd=Path(temporary),
                    timeout_seconds=1,
                    cleanup_grace_seconds=0.1,
                )

        stdout = bounded_process.output_text(raised.exception.stdout)
        self.assertIn("childpid=", stdout)
        child_pid = int(stdout.split("childpid=", 1)[1].splitlines()[0])
        for _ in range(30):
            try:
                state = Path(f"/proc/{child_pid}/stat").read_text(encoding="ascii")
            except (FileNotFoundError, ProcessLookupError):
                break
            if state[state.rfind(")") + 2 :].split()[0] == "Z":
                break
            time.sleep(0.05)
        else:
            self.fail("SIGTERM-resistant descendant survived after its leader exited")


if __name__ == "__main__":
    unittest.main()
