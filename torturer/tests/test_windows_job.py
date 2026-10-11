from __future__ import annotations

import ctypes
import os
import re
import subprocess
import sys
import time
import unittest

from torturer_runner.process_capture import run_finite_capture
from torturer_runner.windows_job import (
    WindowsJobCloseResult,
    WindowsJobError,
    _close_job,
    close_for,
    popen_with_windows_job,
    terminate_and_prove_empty,
)


def _windows_process_exited(pid: int, timeout_ms: int) -> bool:
    from ctypes import wintypes

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.OpenProcess.argtypes = (wintypes.DWORD, wintypes.BOOL, wintypes.DWORD)
    kernel32.OpenProcess.restype = wintypes.HANDLE
    kernel32.WaitForSingleObject.argtypes = (wintypes.HANDLE, wintypes.DWORD)
    kernel32.WaitForSingleObject.restype = wintypes.DWORD
    kernel32.CloseHandle.argtypes = (wintypes.HANDLE,)
    kernel32.CloseHandle.restype = wintypes.BOOL
    handle = kernel32.OpenProcess(0x00100000, False, pid)  # SYNCHRONIZE
    if not handle:
        error = ctypes.get_last_error()
        if error == 87:  # ERROR_INVALID_PARAMETER means the PID no longer exists.
            return True
        raise ctypes.WinError(error)
    try:
        result = kernel32.WaitForSingleObject(handle, timeout_ms)
        if result == 0:  # WAIT_OBJECT_0
            return True
        if result != 258:  # WAIT_TIMEOUT
            raise ctypes.WinError(ctypes.get_last_error())
        return False
    finally:
        kernel32.CloseHandle(handle)


class WindowsJobTests(unittest.TestCase):
    def test_close_result_keeps_explicit_outcome_and_diagnostics(self) -> None:
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

    @unittest.skipUnless(os.name == "nt", "requires a Windows Job Object")
    def test_job_ends_descendant_holding_capture_pipes(self) -> None:
        operation_deadline = time.monotonic() + 3.0
        processes = []
        child_script = (
            "import sys,time; "
            "print('child-stdout-marker', flush=True); "
            "print('child-stderr-marker', file=sys.stderr, flush=True); time.sleep(60)"
        )
        parent_script = (
            "import subprocess,sys,time\n"
            "child=subprocess.Popen([sys.executable, '-c', sys.argv[1]])\n"
            "print(f'parent-child-pid={child.pid}', flush=True)\n"
            "time.sleep(1)\n"
            "print('parent-stderr-marker', file=sys.stderr, flush=True)\n"
        )
        command = [sys.executable, "-c", parent_script, child_script]

        def capture_spawn(arguments, **popen_kwargs):
            process = popen_with_windows_job(
                subprocess.Popen,
                arguments,
                stage="test-capture",
                deadline=operation_deadline,
                **popen_kwargs,
            )
            processes.append(process)
            return process

        def terminate(process, deadline):
            result = terminate_and_prove_empty(
                process, deadline=deadline, stage="test-capture-timeout",
            )
            if not result.process_tree_proven:
                raise WindowsJobError("test-capture-timeout", result.diagnostics)

        def close(process, deadline):
            result = close_for(process, stage="test-capture-cleanup", deadline=deadline)
            if result.failed:
                raise WindowsJobError("test-capture-cleanup", result.diagnostics)

        cleanup_seconds = 2.0
        try:
            with self.assertRaises(subprocess.TimeoutExpired) as caught:
                run_finite_capture(
                    command,
                    timeout_seconds=3.0,
                    popen_factory=capture_spawn,
                    terminate=terminate,
                    close_boundary=close,
                    termination_grace_seconds=1.0,
                    cleanup_timeout_seconds=cleanup_seconds,
                )
            process = processes[0]
            stdout, stderr = caught.exception.stdout or b"", caught.exception.stderr or b""
            match = re.search(rb"parent-child-pid=(\d+)", stdout)
            if match is None:
                self.fail(f"child PID missing from captured stdout: {stdout!r}")
            child_pid = int(match.group(1))
            self.assertTrue(all(marker in stdout for marker in (
                b"child-stdout-marker", b"parent-child-pid=",
            )))
            self.assertTrue(all(marker in stderr for marker in (
                b"child-stderr-marker", b"parent-stderr-marker",
            )))
            self.assertFalse(getattr(caught.exception, "__notes__", ()))
            self.assertTrue(_windows_process_exited(child_pid, int(cleanup_seconds * 1000)))
        finally:
            for process in processes:
                close(process, time.monotonic() + cleanup_seconds)


if __name__ == "__main__":
    unittest.main()
