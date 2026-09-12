from __future__ import annotations

import ctypes
import subprocess
import time
import unittest
from unittest.mock import ANY, MagicMock, patch

import torturer_checks.windows_job as windows_job


class _FakeProcess:
    pid = 701
    _handle = ctypes.c_void_p(0x1_0000_1234)

    def __init__(self) -> None:
        self.wait_calls: list[float] = []
        self.stdout = None
        self.stderr = None

    def wait(self, timeout: float | None = None) -> int:
        self.wait_calls.append(0.0 if timeout is None else timeout)
        return 1


class _NativeFunction:
    def __init__(self, result: object = 1, side_effect: object = None) -> None:
        self.result = result
        self.side_effect = side_effect
        self.calls: list[tuple[object, ...]] = []
        self.argtypes = None
        self.restype = None

    def __call__(self, *args: object) -> object:
        self.calls.append(args)
        if self.side_effect is not None:
            if callable(self.side_effect):
                return self.side_effect(*args)
            if isinstance(self.side_effect, list):
                return self.side_effect.pop(0)
        return self.result


class _FakeKernel:
    def __init__(self, *, active: list[int] | None = None) -> None:
        self.CreateJobObjectW = _NativeFunction(ctypes.c_void_p(0x2_0000_5678))
        self.SetInformationJobObject = _NativeFunction(1)
        self.AssignProcessToJobObject = _NativeFunction(1)
        self.TerminateJobObject = _NativeFunction(1)
        self.TerminateProcess = _NativeFunction(1)
        self.CloseHandle = _NativeFunction(1)
        self.ResumeThread = _NativeFunction(1)
        self.QueryInformationJobObject = _NativeFunction(
            side_effect=self._query(active or [0])
        )

    @staticmethod
    def _query(values: list[int]):
        remaining = list(values)

        def fill(*args: object) -> int:
            pointer = ctypes.cast(args[2], ctypes.POINTER(windows_job._JobObjectBasicAccountingInformation))
            pointer.contents.ActiveProcesses = remaining.pop(0) if remaining else 0
            return 1

        return fill


class WindowsJobTests(unittest.TestCase):
    def test_accounting_layout_keeps_active_processes_at_native_offset(self) -> None:
        info = windows_job._JobObjectBasicAccountingInformation
        self.assertEqual(
            {
                field_name: getattr(info, field_name).offset
                for field_name, _field_type in info._fields_
            },
            {
                "TotalUserTime": 0,
                "TotalKernelTime": 8,
                "ThisPeriodTotalUserTime": 16,
                "ThisPeriodTotalKernelTime": 24,
                "TotalPageFaultCount": 32,
                "TotalProcesses": 36,
                "ActiveProcesses": 40,
                "TotalTerminatedProcesses": 44,
            },
        )
        self.assertEqual(ctypes.sizeof(info), 48)

    def test_popen_is_created_suspended_before_job_attach(self) -> None:
        process = _FakeProcess()
        popen = MagicMock(return_value=process)
        attach = MagicMock()
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job, "attach_and_resume", attach
        ):
            result = windows_job.popen_with_windows_job(
                popen,
                ["safe-command"],
                stage="preflight",
                creationflags=0x400,
            )
        self.assertIs(result, process)
        flags = popen.call_args.kwargs["creationflags"]
        self.assertEqual(flags & windows_job.CREATE_SUSPENDED, windows_job.CREATE_SUSPENDED)
        self.assertEqual(
            flags & windows_job.CREATE_NEW_PROCESS_GROUP,
            windows_job.CREATE_NEW_PROCESS_GROUP,
        )
        attach.assert_called_once_with(process, stage="preflight", deadline=ANY)

    def test_job_assignment_precedes_primary_thread_resume(self) -> None:
        process = _FakeProcess()
        kernel = _FakeKernel()
        job = windows_job.WindowsJob(0x2_0000_5678, kernel)
        events: list[str] = []

        def assign(_handle: int, *, stage: str) -> None:
            events.append("assign")
            job.assigned = True

        job.assign = assign  # type: ignore[method-assign]
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job.WindowsJob, "create", return_value=job
        ), patch.object(
            windows_job,
            "_resume_primary_thread",
            side_effect=lambda *_args, **_kwargs: events.append("resume"),
        ):
            result = windows_job.attach_and_resume(process, stage="command")
        self.assertIs(result, job)
        self.assertEqual(events, ["assign", "resume"])

    def test_assignment_failure_terminates_suspended_process_and_reports_numeric_error(self) -> None:
        kernel = _FakeKernel()
        kernel.AssignProcessToJobObject.result = 0
        kernel.CloseHandle.result = 0
        process = _FakeProcess()
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job.ctypes, "WinDLL", return_value=kernel, create=True
        ), patch.object(windows_job.ctypes, "get_last_error", return_value=87, create=True):
            with self.assertRaises(windows_job.WindowsJobError) as caught:
                windows_job.attach_and_resume(process, stage="desktop-command")
        message = str(caught.exception)
        self.assertIn("stage=desktop-command", message)
        self.assertIn("api=AssignProcessToJobObject winerror=87", message)
        self.assertEqual(len(kernel.TerminateProcess.calls), 1)
        self.assertEqual(kernel.CloseHandle.calls[0][0].value, 0x2_0000_5678)
        self.assertEqual(len(kernel.CloseHandle.calls), 1)
        self.assertTrue(process.wait_calls)

    def test_job_configuration_failure_reports_close_failure(self) -> None:
        kernel = _FakeKernel()
        kernel.SetInformationJobObject.result = 0
        kernel.CloseHandle.result = 0
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job.ctypes, "WinDLL", return_value=kernel, create=True
        ), patch.object(windows_job.ctypes, "get_last_error", return_value=87, create=True):
            with self.assertRaises(windows_job.WindowsJobError) as caught:
                windows_job.WindowsJob.create(
                    stage="source-preflight",
                    deadline=time.monotonic() + 1.0,
                )
        self.assertIn("api=SetInformationJobObject winerror=87", str(caught.exception))
        self.assertIn("api=CloseHandle", str(caught.exception))
        self.assertEqual(len(kernel.CloseHandle.calls), 1)

    def test_job_termination_does_not_touch_outside_sentinel(self) -> None:
        kernel = _FakeKernel(active=[0])
        job = windows_job.WindowsJob(0x2_0000_5678, kernel)
        job.assigned = True
        cleanup = job.terminate(deadline=time.monotonic() + 1, stage="command")
        self.assertTrue(cleanup.process_tree_proven)
        self.assertEqual(
            kernel.TerminateJobObject.calls[0][0].value,
            0x2_0000_5678,
        )
        self.assertNotIn(
            0x5_0000_9ABC,
            [getattr(argument, "value", argument) for call in kernel.TerminateJobObject.calls for argument in call],
        )

    def test_short_lived_root_with_child_and_grandchild_is_proven_empty(self) -> None:
        kernel = _FakeKernel(active=[3, 0])
        job = windows_job.WindowsJob(0x2_0000_5678, kernel)
        job.assigned = True
        cleanup = job.terminate(deadline=time.monotonic() + 1, stage="command")
        self.assertTrue(cleanup.process_tree_proven)
        self.assertEqual(cleanup.active_processes, 0)
        self.assertEqual(len(kernel.TerminateJobObject.calls), 1)
        self.assertEqual(len(kernel.QueryInformationJobObject.calls), 2)

    def test_high_64_bit_job_handle_is_passed_without_truncation(self) -> None:
        high_handle = 0xF123_4567_89AB_CDEF
        kernel = _FakeKernel()
        job = windows_job.WindowsJob(high_handle, kernel)
        job.close()
        self.assertEqual(kernel.CloseHandle.calls[0][0].value, high_handle)

    def test_close_failure_retains_job_attachment_and_fails_closed(self) -> None:
        kernel = _FakeKernel()
        kernel.CloseHandle.result = 0
        job = windows_job.WindowsJob(0x2_0000_5678, kernel)
        process = _FakeProcess()
        process._torturer_windows_job = job
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job.ctypes, "get_last_error", return_value=6, create=True
        ):
            diagnostics = windows_job.close_for(
                process,
                stage="hosted-cli",
                deadline=time.monotonic() + 1.0,
            )
        self.assertFalse(job.closed)
        self.assertIs(process._torturer_windows_job, job)
        self.assertTrue(any("api=CloseHandle winerror=6" in item for item in diagnostics))
        self.assertEqual(len(kernel.CloseHandle.calls), 1)
        self.assertTrue(diagnostics.failed)
        self.assertTrue(bool(diagnostics))

    def test_close_after_deadline_is_not_certified_even_when_handle_closes(self) -> None:
        kernel = _FakeKernel()
        job = windows_job.WindowsJob(0x2_0000_5678, kernel)
        process = _FakeProcess()
        process._torturer_windows_job = job
        with patch.object(windows_job.os, "name", "nt"):
            diagnostics = windows_job.close_for(
                process,
                stage="desktop-command",
                deadline=time.monotonic() - 1.0,
            )
        self.assertTrue(job.closed)
        self.assertTrue(diagnostics.failed)
        self.assertTrue(bool(diagnostics))
        self.assertTrue(any("winerror=1460" in item for item in diagnostics))

    def test_setup_failure_keeps_wait_and_pipe_close_diagnostics(self) -> None:
        process = _FakeProcess()
        process.wait = MagicMock(side_effect=subprocess.TimeoutExpired(["safe"], 0))

        class BrokenStream:
            def close(self) -> None:
                raise OSError("close failed")

        process.stdout = BrokenStream()
        process.stderr = BrokenStream()
        original = windows_job.attach_and_resume
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job,
            "attach_and_resume",
            side_effect=windows_job.WindowsJobError("desktop-command", ("api=AssignProcessToJobObject winerror=5",)),
        ):
            with self.assertRaises(windows_job.WindowsJobError) as caught:
                windows_job.popen_with_windows_job(
                    MagicMock(return_value=process),
                    ["safe-command"],
                    stage="desktop-command",
                )
        self.assertIn("api=ProcessWait winerror=1460 detail=setup-failure", str(caught.exception))
        self.assertIn("api=ClosePipe winerror=0 detail=stdout:OSError", str(caught.exception))
        self.assertIsNotNone(original)

    def test_setup_communicate_oserror_retains_partial_output(self) -> None:
        class PipeFailureProcess(_FakeProcess):
            def communicate(self, *, timeout: float) -> tuple[bytes, bytes]:
                error = OSError(232, "pipe closed")
                error.output = b"partial-stdout\x00"  # type: ignore[attr-defined]
                error.stderr = b"partial-stderr\xff"  # type: ignore[attr-defined]
                raise error

        process = PipeFailureProcess()
        retained: list[tuple[bytes, bytes]] = []
        with patch.object(windows_job.os, "name", "nt"), patch.object(
            windows_job,
            "attach_and_resume",
            side_effect=windows_job.WindowsJobError(
                "desktop-command", ("api=AssignProcessToJobObject winerror=5",)
            ),
        ):
            with self.assertRaises(windows_job.WindowsJobError) as caught:
                windows_job.popen_with_windows_job(
                    MagicMock(return_value=process),
                    ["safe-command"],
                    stage="desktop-command",
                    on_setup_failure_output=lambda stdout, stderr: retained.append(
                        (stdout, stderr)
                    ),
                )
        self.assertEqual(caught.exception.stdout, b"partial-stdout\x00")
        self.assertEqual(caught.exception.stderr, b"partial-stderr\xff")
        self.assertEqual(retained, [(b"partial-stdout\x00", b"partial-stderr\xff")])
        self.assertIn("api=ProcessCommunicate winerror=232 detail=OSError", str(caught.exception))
        self.assertIn("EVIDENCE_INCOMPLETE=1 reason=setup-communicate-error", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
