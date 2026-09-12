"""Windows Job Object containment for short-lived Torturer subprocesses.

``CREATE_NEW_PROCESS_GROUP`` is useful for console control events, but it is
not a descendant boundary.  A Windows Job Object is the native boundary we
need for artifact/preflight commands: the process is created suspended,
assigned to a job configured with ``KILL_ON_JOB_CLOSE``, and only then is its
initial thread resumed.  The job's ``ActiveProcesses`` count is the
authoritative cleanup observation.  PID/parent census data remains useful
diagnostic context, but is never used to certify an empty job.

This module deliberately keeps all public diagnostics stage-scoped and
numeric.  It never includes a command, working directory, environment, or
handle-bearing object representation in an error string.
"""

from __future__ import annotations

from dataclasses import dataclass
import ctypes
import os
import subprocess
import time
from typing import Any, Callable


CREATE_SUSPENDED = 0x00000004
CREATE_NEW_PROCESS_GROUP = 0x00000200
JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000
JOB_OBJECT_BASIC_ACCOUNTING_INFORMATION = 1
JOB_OBJECT_EXTENDED_LIMIT_INFORMATION = 9
THREAD_SUSPEND_RESUME = 0x0002
TH32CS_SNAPTHREAD = 0x00000004
ERROR_NO_MORE_FILES = 18
ERROR_TIMEOUT = 1460
INVALID_HANDLE_VALUE = (1 << (ctypes.sizeof(ctypes.c_void_p) * 8)) - 1
WAIT_POLL_SECONDS = 0.01


class WindowsJobError(RuntimeError):
    """A Windows Job Object setup or cleanup operation failed."""

    def __init__(
        self,
        stage: str,
        diagnostics: tuple[str, ...] | list[str],
        *,
        stdout: bytes = b"",
        stderr: bytes = b"",
    ) -> None:
        self.stage = stage
        self.diagnostics = tuple(diagnostics)
        self.stdout = bytes(stdout)
        self.stderr = bytes(stderr)
        detail = "; ".join(self.diagnostics) or "reason=unspecified"
        # Stage names are supplied by the fixed public caller, not by command
        # arguments.  Keep this one-line summary suitable for hosted output.
        super().__init__(f"stage={stage} windows-job failure; {detail}")


@dataclass(frozen=True)
class WindowsJobCleanup:
    """Result of a bounded Job Object termination/proof attempt."""

    process_tree_proven: bool
    active_processes: int | None
    diagnostics: tuple[str, ...] = ()


class WindowsJobCloseDiagnostics(tuple):
    """Close diagnostics with a fatal outcome flag."""

    failed: bool

    def __new__(
        cls,
        values: tuple[str, ...] | list[str] = (),
        *,
        failed: bool = False,
    ) -> "WindowsJobCloseDiagnostics":
        result = super().__new__(cls, values)
        result.failed = bool(failed)
        return result

    def __bool__(self) -> bool:
        return self.failed


class _JobObjectBasicAccountingInformation(ctypes.Structure):
    _fields_ = [
        ("TotalUserTime", ctypes.c_longlong),
        ("TotalKernelTime", ctypes.c_longlong),
        ("ThisPeriodTotalUserTime", ctypes.c_longlong),
        ("ThisPeriodTotalKernelTime", ctypes.c_longlong),
        ("TotalPageFaultCount", ctypes.c_uint32),
        ("TotalProcesses", ctypes.c_uint32),
        ("ActiveProcesses", ctypes.c_uint32),
        ("TotalTerminatedProcesses", ctypes.c_uint32),
    ]


class _JobObjectBasicLimitInformation(ctypes.Structure):
    _fields_ = [
        ("PerProcessUserTimeLimit", ctypes.c_longlong),
        ("PerJobUserTimeLimit", ctypes.c_longlong),
        ("LimitFlags", ctypes.c_uint32),
        ("MinimumWorkingSetSize", ctypes.c_size_t),
        ("MaximumWorkingSetSize", ctypes.c_size_t),
        ("ActiveProcessLimit", ctypes.c_uint32),
        ("Affinity", ctypes.c_size_t),
        ("PriorityClass", ctypes.c_uint32),
        ("SchedulingClass", ctypes.c_uint32),
    ]


class _IoCounters(ctypes.Structure):
    _fields_ = [
        ("ReadOperationCount", ctypes.c_ulonglong),
        ("WriteOperationCount", ctypes.c_ulonglong),
        ("OtherOperationCount", ctypes.c_ulonglong),
        ("ReadTransferCount", ctypes.c_ulonglong),
        ("WriteTransferCount", ctypes.c_ulonglong),
        ("OtherTransferCount", ctypes.c_ulonglong),
    ]


class _JobObjectExtendedLimitInformation(ctypes.Structure):
    _fields_ = [
        ("BasicLimitInformation", _JobObjectBasicLimitInformation),
        ("IoInfo", _IoCounters),
        ("ProcessMemoryLimit", ctypes.c_size_t),
        ("JobMemoryLimit", ctypes.c_size_t),
        ("PeakProcessMemoryUsed", ctypes.c_size_t),
        ("PeakJobMemoryUsed", ctypes.c_size_t),
    ]


class _ThreadEntry32(ctypes.Structure):
    _fields_ = [
        ("dwSize", ctypes.c_uint32),
        ("cntUsage", ctypes.c_uint32),
        ("th32ThreadID", ctypes.c_uint32),
        ("th32OwnerProcessID", ctypes.c_uint32),
        ("tpBasePri", ctypes.c_long),
        ("tpDeltaPri", ctypes.c_long),
        ("dwFlags", ctypes.c_uint32),
    ]


def _is_windows() -> bool:
    # Read os.name at call time so the native layer can be fully mocked in
    # platform-independent unit tests.
    return os.name == "nt"


def _handle_value(value: Any) -> int:
    """Convert a native/Python handle without truncating 64-bit values."""

    if value is None:
        return 0
    nested = getattr(value, "value", value)
    if nested is None:
        return 0
    return int(nested)


def _handle_arg(value: int) -> ctypes.c_void_p:
    # Explicitly use c_void_p rather than c_uint32: Windows handles are
    # pointer-sized and may be above the 32-bit range on a 64-bit runner.
    return ctypes.c_void_p(int(value))


def _last_error() -> int:
    try:
        return int(ctypes.get_last_error())
    except (AttributeError, OSError, TypeError, ValueError):
        return 0


def _exception_error_code(error: BaseException) -> int:
    """Return a native-looking numeric code without exposing exception text."""

    for name in ("winerror", "errno"):
        value = getattr(error, name, None)
        if value is not None:
            try:
                return int(value)
            except (TypeError, ValueError):
                pass
    return _last_error()


def _native_failure(api: str, code: int | None = None, *, detail: str | None = None) -> str:
    value = _last_error() if code is None else int(code)
    suffix = f" detail={detail}" if detail else ""
    return f"api={api} winerror={value}{suffix}"


def _kernel32() -> Any:
    try:
        return ctypes.WinDLL("kernel32", use_last_error=True)
    except (AttributeError, OSError) as error:
        raise WindowsJobError(
            "load-kernel32",
            (
                f"api=LoadLibrary winerror={_exception_error_code(error)} "
                f"detail={type(error).__name__}",
            ),
        ) from error


def _signature(function: Any, *, argtypes: list[Any], restype: Any) -> Any:
    """Set ctypes signatures while remaining friendly to mocked functions."""

    try:
        function.argtypes = argtypes
        function.restype = restype
    except (AttributeError, TypeError):
        pass
    return function


def _api(kernel: Any, name: str, *, argtypes: list[Any], restype: Any) -> Any:
    try:
        function = getattr(kernel, name)
    except AttributeError as error:
        raise WindowsJobError("load-kernel32", (f"api={name} winerror=127",)) from error
    return _signature(function, argtypes=argtypes, restype=restype)


def _native_handle_from_process(process: Any) -> int:
    handle = getattr(process, "_handle", None)
    value = _handle_value(handle)
    if value == 0:
        raise WindowsJobError("process-handle", ("api=Popen._handle winerror=6",))
    return value


def _deadline_expired(deadline: float | None) -> bool:
    return deadline is not None and time.monotonic() >= deadline


def _deadline_diagnostic(api: str, *, detail: str = "deadline-expired") -> str:
    return f"api={api} winerror={ERROR_TIMEOUT} detail={detail}"


def _require_deadline(stage: str, deadline: float | None, api: str) -> None:
    if _deadline_expired(deadline):
        raise WindowsJobError(stage, (_deadline_diagnostic(api),))


def _set_kill_on_close(
    job_handle: int,
    kernel: Any,
    diagnostics: list[str],
    *,
    stage: str,
    deadline: float | None,
) -> bool:
    try:
        _require_deadline(stage, deadline, "SetInformationJobObject")
    except WindowsJobError as error:
        diagnostics.extend(error.diagnostics)
        return False
    function = _api(
        kernel,
        "SetInformationJobObject",
        argtypes=[ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32],
        restype=ctypes.c_int,
    )
    limits = _JobObjectExtendedLimitInformation()
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
    if not function(
        _handle_arg(job_handle),
        JOB_OBJECT_EXTENDED_LIMIT_INFORMATION,
        ctypes.byref(limits),
        ctypes.sizeof(limits),
    ):
        diagnostics.append(_native_failure("SetInformationJobObject"))
        return False
    if _deadline_expired(deadline):
        diagnostics.append(
            _deadline_diagnostic(
                "SetInformationJobObject",
                detail="completed-after-deadline",
            )
        )
        return False
    return True


class WindowsJob:
    """A retained native Job Object and its bounded operations."""

    def __init__(self, handle: int, kernel: Any) -> None:
        self.handle = int(handle)
        self._kernel = kernel
        self.closed = False
        self.assigned = False
        self.diagnostics: list[str] = []

    @classmethod
    def create(cls, *, stage: str, deadline: float | None = None) -> "WindowsJob":
        _require_deadline(stage, deadline, "CreateJobObjectW")
        kernel = _kernel32()
        function = _api(
            kernel,
            "CreateJobObjectW",
            argtypes=[ctypes.c_void_p, ctypes.c_wchar_p],
            restype=ctypes.c_void_p,
        )
        value = _handle_value(function(None, None))
        if value == 0:
            raise WindowsJobError(stage, (_native_failure("CreateJobObjectW"),))
        job = cls(value, kernel)
        # Keep the caller's one absolute deadline on the native object so a
        # direct create/assign sequence cannot accidentally manufacture a new
        # setup window between operations.
        job._deadline = deadline
        if _deadline_expired(deadline):
            diagnostics = [
                _deadline_diagnostic("CreateJobObjectW", detail="completed-after-deadline")
            ]
            close_diagnostics = _close_job(
                job,
                stage=stage,
                deadline=deadline,
            )
            diagnostics.extend(close_diagnostics)
            raise WindowsJobError(stage, tuple(diagnostics))
        try:
            configured = _set_kill_on_close(
                value,
                kernel,
                job.diagnostics,
                stage=stage,
                deadline=deadline,
            )
        except WindowsJobError as error:
            job.diagnostics.extend(error.diagnostics)
            configured = False
        if not configured:
            diagnostics = list(job.diagnostics)
            close_diagnostics = _close_job(
                job,
                stage=stage,
                deadline=deadline,
            )
            diagnostics.extend(close_diagnostics)
            raise WindowsJobError(stage, tuple(diagnostics))
        return job

    def assign(self, process_handle: int, *, stage: str) -> None:
        _require_deadline(stage, getattr(self, "_deadline", None), "AssignProcessToJobObject")
        function = _api(
            self._kernel,
            "AssignProcessToJobObject",
            argtypes=[ctypes.c_void_p, ctypes.c_void_p],
            restype=ctypes.c_int,
        )
        if not function(_handle_arg(self.handle), _handle_arg(process_handle)):
            diagnostic = _native_failure("AssignProcessToJobObject")
            self.diagnostics.append(diagnostic)
            raise WindowsJobError(stage, tuple(self.diagnostics))
        self.assigned = True
        if _deadline_expired(getattr(self, "_deadline", None)):
            diagnostic = _deadline_diagnostic(
                "AssignProcessToJobObject",
                detail="completed-after-deadline",
            )
            self.diagnostics.append(diagnostic)
            raise WindowsJobError(stage, tuple(self.diagnostics))

    def query_active_processes(self, *, deadline: float | None = None) -> int | None:
        if self.closed:
            self.diagnostics.append("api=QueryInformationJobObject winerror=6 detail=job-closed")
            return None
        if deadline is not None and time.monotonic() >= deadline:
            self.diagnostics.append(
                "api=QueryInformationJobObject winerror=1460 detail=deadline-expired"
            )
            return None
        try:
            function = _api(
                self._kernel,
                "QueryInformationJobObject",
                argtypes=[ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32, ctypes.c_void_p],
                restype=ctypes.c_int,
            )
        except WindowsJobError as error:
            self.diagnostics.extend(error.diagnostics)
            return None
        accounting = _JobObjectBasicAccountingInformation()
        returned = ctypes.c_uint32(0)
        if not function(
            _handle_arg(self.handle),
            JOB_OBJECT_BASIC_ACCOUNTING_INFORMATION,
            ctypes.byref(accounting),
            ctypes.sizeof(accounting),
            ctypes.byref(returned),
        ):
            self.diagnostics.append(_native_failure("QueryInformationJobObject"))
            return None
        if _deadline_expired(deadline):
            self.diagnostics.append(
                _deadline_diagnostic(
                    "QueryInformationJobObject",
                    detail="completed-after-deadline",
                )
            )
            return None
        return int(accounting.ActiveProcesses)

    def terminate(self, *, deadline: float, stage: str) -> WindowsJobCleanup:
        if self.closed:
            diagnostic = "api=TerminateJobObject winerror=6 detail=job-closed"
            self.diagnostics.append(diagnostic)
            return WindowsJobCleanup(False, None, (diagnostic,))
        try:
            function = _api(
                self._kernel,
                "TerminateJobObject",
                argtypes=[ctypes.c_void_p, ctypes.c_uint32],
                restype=ctypes.c_int,
            )
        except WindowsJobError as error:
            self.diagnostics.extend(error.diagnostics)
            return WindowsJobCleanup(False, None, tuple(self.diagnostics))
        if _deadline_expired(deadline):
            diagnostic = _deadline_diagnostic("TerminateJobObject")
            self.diagnostics.append(diagnostic)
            return WindowsJobCleanup(False, None, tuple(self.diagnostics))
        if not function(_handle_arg(self.handle), 1):
            self.diagnostics.append(_native_failure("TerminateJobObject"))
            return WindowsJobCleanup(False, None, tuple(self.diagnostics))
        if _deadline_expired(deadline):
            diagnostic = _deadline_diagnostic(
                "TerminateJobObject",
                detail="completed-after-deadline",
            )
            self.diagnostics.append(diagnostic)
            return WindowsJobCleanup(False, None, tuple(self.diagnostics))
        active: int | None = None
        while True:
            active = self.query_active_processes(deadline=deadline)
            if active == 0:
                return WindowsJobCleanup(True, active, tuple(self.diagnostics))
            remaining = (deadline - time.monotonic()) if deadline is not None else 0.0
            if remaining <= 0:
                self.diagnostics.append(
                    f"stage={stage} api=QueryInformationJobObject winerror=1460 "
                    f"detail=active-processes-unproven value={active}"
                )
                return WindowsJobCleanup(False, active, tuple(self.diagnostics))
            time.sleep(min(WAIT_POLL_SECONDS, remaining))

    def close(self, *, deadline: float | None = None) -> tuple[str, ...]:
        if self.closed:
            return ()
        try:
            function = _api(
                self._kernel,
                "CloseHandle",
                argtypes=[ctypes.c_void_p],
                restype=ctypes.c_int,
            )
        except WindowsJobError as error:
            self.diagnostics.extend(error.diagnostics)
            return tuple(self.diagnostics)
        if _deadline_expired(deadline):
            self.diagnostics.append(_deadline_diagnostic("CloseHandle"))
        if not function(_handle_arg(self.handle)):
            self.diagnostics.append(_native_failure("CloseHandle"))
            # Keep the attachment and native handle ownership when Windows
            # rejects CloseHandle so the exact failure remains observable.
            return tuple(self.diagnostics)
        if _deadline_expired(deadline):
            self.diagnostics.append(
                _deadline_diagnostic("CloseHandle", detail="completed-after-deadline")
            )
        self.closed = True
        return tuple(self.diagnostics)


def _close_job(
    job: WindowsJob,
    *,
    stage: str,
    deadline: float | None = None,
) -> WindowsJobCloseDiagnostics:
    """Close one Job Object once and retain the native result."""

    before = len(job.diagnostics)
    job.close(deadline=deadline)
    diagnostics = list(job.diagnostics[before:])
    deadline_unproven = any(
        "api=CloseHandle winerror=1460" in item
        or "detail=completed-after-deadline" in item
        for item in diagnostics
    )
    if not job.closed:
        diagnostics.append(
            f"stage={stage} api=CloseHandle winerror=6 detail=job-still-attached"
        )
    return WindowsJobCloseDiagnostics(
        diagnostics,
        failed=not job.closed or deadline_unproven,
    )


def _terminate_unassigned_process(
    process: Any,
    *,
    stage: str,
    deadline: float | None,
    diagnostics: list[str],
) -> None:
    """Terminate a still-suspended process after setup failed."""

    try:
        kernel = _kernel32()
        function = _api(
            kernel,
            "TerminateProcess",
            argtypes=[ctypes.c_void_p, ctypes.c_uint32],
            restype=ctypes.c_int,
        )
        process_handle = _native_handle_from_process(process)
        if _deadline_expired(deadline):
            diagnostics.append(_deadline_diagnostic("TerminateProcess"))
        elif not function(_handle_arg(process_handle), 1):
            diagnostics.append(_native_failure("TerminateProcess"))
        elif _deadline_expired(deadline):
            diagnostics.append(
                _deadline_diagnostic(
                    "TerminateProcess",
                    detail="completed-after-deadline",
                )
            )
    except WindowsJobError as error:
        diagnostics.extend(error.diagnostics)
    try:
        remaining = max(0.0, deadline - time.monotonic()) if deadline is not None else 1.0
        process.wait(timeout=remaining)
    except subprocess.TimeoutExpired:
        diagnostics.append(f"api=ProcessWait winerror={ERROR_TIMEOUT} detail=stage={stage}")
        try:
            process.kill()
        except ProcessLookupError:
            pass
        except (OSError, ValueError) as error:
            diagnostics.append(
                f"api=ProcessKill winerror={_exception_error_code(error)} "
                f"detail={type(error).__name__}"
            )
        try:
            remaining = max(0.0, deadline - time.monotonic()) if deadline is not None else 0.0
            process.wait(timeout=remaining)
        except subprocess.TimeoutExpired:
            diagnostics.append(f"api=ProcessWait winerror={ERROR_TIMEOUT} detail=stage={stage}-reap")
        except (OSError, ValueError) as error:
            diagnostics.append(
                f"api=ProcessWait winerror={_exception_error_code(error)} "
                f"detail={type(error).__name__}"
            )
    except (OSError, ValueError) as error:
        diagnostics.append(
            f"api=ProcessWait winerror={_exception_error_code(error)} "
            f"detail={type(error).__name__}"
        )


def _close_thread_handle(
    kernel: Any,
    handle: int,
    diagnostics: list[str],
    *,
    deadline: float | None,
) -> None:
    function = _api(
        kernel,
        "CloseHandle",
        argtypes=[ctypes.c_void_p],
        restype=ctypes.c_int,
    )
    if _deadline_expired(deadline):
        diagnostics.append(_deadline_diagnostic("CloseHandle", detail="primary-thread-deadline"))
    if not function(_handle_arg(handle)):
        diagnostics.append(_native_failure("CloseHandle", detail="primary-thread"))
    elif _deadline_expired(deadline):
        diagnostics.append(
            _deadline_diagnostic("CloseHandle", detail="primary-thread-completed-after-deadline")
        )


def _open_primary_thread(
    kernel: Any,
    process_id: int,
    diagnostics: list[str],
    *,
    stage: str,
    deadline: float | None,
) -> int:
    """Find the sole initial thread of a newly-created suspended process."""

    _require_deadline(stage, deadline, "CreateToolhelp32Snapshot")
    snapshot_fn = _api(
        kernel,
        "CreateToolhelp32Snapshot",
        argtypes=[ctypes.c_uint32, ctypes.c_uint32],
        restype=ctypes.c_void_p,
    )
    snapshot = _handle_value(snapshot_fn(TH32CS_SNAPTHREAD, 0))
    if snapshot in (0, INVALID_HANDLE_VALUE):
        diagnostics.append(_native_failure("CreateToolhelp32Snapshot"))
        raise WindowsJobError("resume-primary-thread", tuple(diagnostics))
    close_snapshot = _api(
        kernel,
        "CloseHandle",
        argtypes=[ctypes.c_void_p],
        restype=ctypes.c_int,
    )
    first_fn = _api(
        kernel,
        "Thread32First",
        argtypes=[ctypes.c_void_p, ctypes.c_void_p],
        restype=ctypes.c_int,
    )
    next_fn = _api(
        kernel,
        "Thread32Next",
        argtypes=[ctypes.c_void_p, ctypes.c_void_p],
        restype=ctypes.c_int,
    )
    entry = _ThreadEntry32()
    entry.dwSize = ctypes.sizeof(entry)
    matches: list[int] = []
    failure: str | None = None
    close_failed = False
    try:
        if not first_fn(_handle_arg(snapshot), ctypes.byref(entry)):
            diagnostics.append(_native_failure("Thread32First"))
            failure = "Thread32First"
        while failure is None:
            _require_deadline(stage, deadline, "Thread32Next")
            if int(entry.th32OwnerProcessID) == int(process_id):
                matches.append(int(entry.th32ThreadID))
            if not next_fn(_handle_arg(snapshot), ctypes.byref(entry)):
                if _last_error() != ERROR_NO_MORE_FILES:
                    diagnostics.append(_native_failure("Thread32Next"))
                    failure = "Thread32Next"
                break
    finally:
        if not close_snapshot(_handle_arg(snapshot)):
            diagnostics.append(_native_failure("CloseHandle", detail="thread-snapshot"))
            close_failed = True
    if failure is not None or close_failed:
        raise WindowsJobError(stage, tuple(diagnostics))
    if len(matches) != 1:
        diagnostics.append(
            f"api=Thread32First winerror=0 detail=primary-thread-count={len(matches)}"
        )
        raise WindowsJobError(stage, tuple(diagnostics))
    _require_deadline(stage, deadline, "OpenThread")
    open_thread = _api(
        kernel,
        "OpenThread",
        argtypes=[ctypes.c_uint32, ctypes.c_int, ctypes.c_uint32],
        restype=ctypes.c_void_p,
    )
    thread_handle = _handle_value(open_thread(THREAD_SUSPEND_RESUME, 0, matches[0]))
    if thread_handle == 0:
        diagnostics.append(_native_failure("OpenThread", detail=f"thread-id={matches[0]}"))
        raise WindowsJobError(stage, tuple(diagnostics))
    return thread_handle


def _resume_primary_thread(
    process: Any,
    kernel: Any,
    diagnostics: list[str],
    *,
    stage: str,
    deadline: float | None,
) -> None:
    # A Popen created by current CPython closes the CreateProcess thread
    # handle before returning.  Prefer it when a compatible implementation
    # exposes it; otherwise reopen the one and only initial thread from the
    # Toolhelp snapshot while the process remains suspended.
    thread_value = getattr(process, "_thread", None)
    owned_thread = False
    if thread_value is None:
        thread_value = getattr(process, "_thread_handle", None)
    if thread_value is None:
        thread_handle = _open_primary_thread(
            kernel,
            int(process.pid),
            diagnostics,
            stage=stage,
            deadline=deadline,
        )
        owned_thread = True
    else:
        thread_handle = _handle_value(thread_value)
        if thread_handle == 0:
            diagnostics.append("api=primary-thread-handle winerror=6")
            raise WindowsJobError("resume-primary-thread", tuple(diagnostics))
    _require_deadline(stage, deadline, "ResumeThread")
    resume = _api(
        kernel,
        "ResumeThread",
        argtypes=[ctypes.c_void_p],
        restype=ctypes.c_uint32,
    )
    try:
        previous_count = int(resume(_handle_arg(thread_handle)))
        if previous_count == 0xFFFFFFFF:
            diagnostics.append(_native_failure("ResumeThread"))
            raise WindowsJobError(stage, tuple(diagnostics))
        if previous_count != 1:
            diagnostics.append(
                f"api=ResumeThread winerror=0 detail=previous-suspend-count={previous_count}"
            )
            raise WindowsJobError(stage, tuple(diagnostics))
        if _deadline_expired(deadline):
            diagnostics.append(
                _deadline_diagnostic("ResumeThread", detail="completed-after-deadline")
            )
            raise WindowsJobError(stage, tuple(diagnostics))
    finally:
        if owned_thread:
            _close_thread_handle(kernel, thread_handle, diagnostics, deadline=deadline)


def attach_and_resume(
    process: Any,
    *,
    stage: str,
    deadline: float | None = None,
) -> WindowsJob:
    """Assign a suspended Popen process to a kill-on-close job and resume it."""

    if deadline is None:
        deadline = time.monotonic() + 1.0
    if not _is_windows():
        raise WindowsJobError(stage, ("api=JobObject winerror=50 detail=non-windows",))
    try:
        process_handle = _native_handle_from_process(process)
    except WindowsJobError as error:
        diagnostics = list(error.diagnostics)
        _terminate_unassigned_process(
            process,
            stage=stage,
            deadline=deadline,
            diagnostics=diagnostics,
        )
        raise WindowsJobError(stage, tuple(diagnostics)) from error
    try:
        job = WindowsJob.create(stage=stage, deadline=deadline)
        job._deadline = deadline
    except WindowsJobError as error:
        diagnostics = list(error.diagnostics)
        _terminate_unassigned_process(
            process,
            stage=stage,
            deadline=deadline,
            diagnostics=diagnostics,
        )
        raise WindowsJobError(stage, tuple(diagnostics)) from error
    try:
        job.assign(process_handle, stage=stage)
        # Retain the job on the Popen object before resuming.  This makes the
        # ownership relationship explicit and prevents a cleanup path from
        # losing the handle during the first scheduling window.
        setattr(process, "_torturer_windows_job", job)
        _resume_primary_thread(
            process,
            job._kernel,
            job.diagnostics,
            stage=stage,
            deadline=deadline,
        )
        return job
    except WindowsJobError as error:
        diagnostics = list(error.diagnostics)
        # If assignment succeeded, TerminateJobObject handles every member;
        # if it did not, the suspended process is terminated directly.  In
        # both cases close the handle only after the bounded attempt.
        if job.assigned:
            cleanup_deadline = deadline if deadline is not None else time.monotonic() + 1.0
            cleanup = job.terminate(deadline=cleanup_deadline, stage=stage)
            diagnostics.extend(cleanup.diagnostics)
        else:
            _terminate_unassigned_process(
                process,
                stage=stage,
                deadline=deadline,
                diagnostics=diagnostics,
            )
        close_diagnostics = _close_job(
            job,
            stage=stage,
            deadline=deadline,
        )
        diagnostics.extend(close_diagnostics)
        if job.closed:
            try:
                delattr(process, "_torturer_windows_job")
            except AttributeError:
                pass
        raise WindowsJobError(stage, tuple(diagnostics)) from error


def popen_with_windows_job(
    popen: Callable[..., Any],
    *args: Any,
    stage: str,
    deadline: float | None = None,
    on_setup_failure_output: Callable[[bytes, bytes], None] | None = None,
    **kwargs: Any,
) -> Any:
    """Launch a command with a Windows Job Object containment boundary."""

    if not _is_windows():
        return popen(*args, **kwargs)
    setup_deadline = deadline if deadline is not None else time.monotonic() + 1.0
    flags = int(kwargs.get("creationflags", 0) or 0)
    kwargs["creationflags"] = flags | CREATE_NEW_PROCESS_GROUP | CREATE_SUSPENDED
    process = popen(*args, **kwargs)
    try:
        attach_and_resume(process, stage=stage, deadline=setup_deadline)
    except WindowsJobError as error:
        # Setup failure already attempted bounded native termination and
        # retained every numeric diagnostic; make sure the Popen object does
        # not outlive its pipes in a caller that catches the exception.
        diagnostics = list(error.diagnostics)
        if deadline is None:
            wait_timeout = 1.0
        else:
            wait_timeout = max(0.0, deadline - time.monotonic())
        captured_stdout = b""
        captured_stderr = b""
        try:
            captured = process.communicate(timeout=wait_timeout)
            if isinstance(captured, tuple) and len(captured) == 2:
                captured_stdout = _merge_output(captured_stdout, _output_bytes(captured[0]))
                captured_stderr = _merge_output(captured_stderr, _output_bytes(captured[1]))
            else:
                diagnostics.append("api=ProcessCommunicate winerror=0 detail=invalid-result")
        except subprocess.TimeoutExpired as wait_error:
            captured_stdout = _merge_output(captured_stdout, _output_bytes(getattr(wait_error, "output", None)))
            captured_stderr = _merge_output(captured_stderr, _output_bytes(getattr(wait_error, "stderr", None)))
            diagnostics.append(f"api=ProcessWait winerror={ERROR_TIMEOUT} detail=setup-failure")
            diagnostics.append(f"api=ProcessCommunicate winerror={ERROR_TIMEOUT} detail=setup-failure")
            diagnostics.append("EVIDENCE_INCOMPLETE=1 reason=setup-communicate-timeout")
        except (AttributeError, OSError, ValueError, TypeError) as wait_error:
            # communicate() may expose bytes read before a non-timeout pipe
            # failure on either ``output`` or ``stdout``.  Keep the partial
            # buffers on the native error so the hosted adapter can retain
            # them before classifying the setup failure.
            captured_stdout = _merge_output(
                captured_stdout,
                _merge_output(
                    _output_bytes(getattr(wait_error, "output", None)),
                    _output_bytes(getattr(wait_error, "stdout", None)),
                ),
            )
            captured_stderr = _merge_output(
                captured_stderr,
                _output_bytes(getattr(wait_error, "stderr", None)),
            )
            diagnostics.append(
                f"api=ProcessCommunicate winerror={_exception_error_code(wait_error)} "
                f"detail={type(wait_error).__name__}"
            )
            diagnostics.append("EVIDENCE_INCOMPLETE=1 reason=setup-communicate-error")
        try:
            process.wait(timeout=0)
        except subprocess.TimeoutExpired:
            diagnostics.append(
                f"api=ProcessWait winerror={ERROR_TIMEOUT} detail=setup-failure-reap"
            )
        except (OSError, ValueError) as wait_error:
            diagnostics.append(
                f"api=ProcessWait winerror={_exception_error_code(wait_error)} "
                f"detail={type(wait_error).__name__}"
            )
        for stream_name in ("stdin", "stdout", "stderr"):
            stream = getattr(process, stream_name, None)
            if stream is not None:
                try:
                    stream.close()
                except (OSError, ValueError) as close_error:
                    diagnostics.append(
                        f"api=ClosePipe winerror={_exception_error_code(close_error)} "
                        f"detail={stream_name}:{type(close_error).__name__}"
                    )
        if on_setup_failure_output is not None:
            try:
                on_setup_failure_output(captured_stdout, captured_stderr)
            except (OSError, ValueError, TypeError) as output_error:
                diagnostics.append(
                    f"api=SetupOutputRetention winerror={_exception_error_code(output_error)} "
                    f"detail={type(output_error).__name__}"
                )
        raise WindowsJobError(
            error.stage,
            tuple(diagnostics),
            stdout=captured_stdout,
            stderr=captured_stderr,
        ) from error
    return process


def job_for(process: Any) -> WindowsJob | None:
    if not _is_windows():
        return None
    value = getattr(process, "_torturer_windows_job", None)
    return value if isinstance(value, WindowsJob) else None


def wait_for_empty(process: Any, *, deadline: float) -> WindowsJobCleanup:
    """Authoritatively prove that a contained process tree is empty."""

    job = job_for(process)
    if job is None:
        return WindowsJobCleanup(False, None, ("api=JobObject winerror=6 detail=not-attached",))
    active: int | None = None
    while True:
        active = job.query_active_processes(deadline=deadline)
        if active == 0:
            return WindowsJobCleanup(True, active, tuple(job.diagnostics))
        if active is None or time.monotonic() >= deadline:
            return WindowsJobCleanup(False, active, tuple(job.diagnostics))
        time.sleep(min(WAIT_POLL_SECONDS, max(0.0, deadline - time.monotonic())))


def terminate_and_prove_empty(process: Any, *, deadline: float, stage: str) -> WindowsJobCleanup:
    job = job_for(process)
    if job is None:
        return WindowsJobCleanup(False, None, ("api=JobObject winerror=6 detail=not-attached",))
    return job.terminate(deadline=deadline, stage=stage)


def close_for(
    process: Any,
    *,
    stage: str,
    deadline: float | None = None,
) -> WindowsJobCloseDiagnostics:
    job = job_for(process)
    if job is None:
        return WindowsJobCloseDiagnostics()
    diagnostics = _close_job(
        job,
        stage=stage,
        deadline=deadline,
    )
    if job.closed:
        try:
            delattr(process, "_torturer_windows_job")
        except AttributeError:
            pass
    return WindowsJobCloseDiagnostics(
        tuple(f"stage={stage} {item}" for item in diagnostics),
        failed=diagnostics.failed,
    )


def _output_bytes(value: Any) -> bytes:
    if isinstance(value, bytes):
        return value
    if isinstance(value, str):
        return value.encode("utf-8", errors="replace")
    return b""


def _merge_output(partial: bytes, recovered: bytes) -> bytes:
    if not partial:
        return recovered
    if not recovered or recovered.startswith(partial) or partial.startswith(recovered):
        return recovered if len(recovered) >= len(partial) else partial
    return partial + recovered
