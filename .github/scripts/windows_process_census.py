#!/usr/bin/env python3
"""Read a Windows process census without signalling any process."""

from __future__ import annotations

import json
import os
import subprocess


class WindowsProcessCensusError(RuntimeError):
    """Raised when the Windows process census cannot be proven complete."""


def _toolhelp_process_census(root_pid: int) -> tuple[set[int], set[int]]:
    """Read the process table through Win32 Toolhelp, without WMI/PowerShell.

    The hosted Windows runner's WMI provider can take longer than the bounded
    cleanup window even for a small process table.  Toolhelp is the native,
    read-only API intended for this snapshot and avoids starting a second
    process whose startup/output pipes would themselves need supervision.
    """

    try:
        import ctypes
        from ctypes import wintypes

        class ProcessEntry(ctypes.Structure):
            _fields_ = [
                ("dwSize", wintypes.DWORD),
                ("cntUsage", wintypes.DWORD),
                ("th32ProcessID", wintypes.DWORD),
                ("th32DefaultHeapID", ctypes.c_void_p),
                ("th32ModuleID", wintypes.DWORD),
                ("cntThreads", wintypes.DWORD),
                ("th32ParentProcessID", wintypes.DWORD),
                ("pcPriClassBase", ctypes.c_long),
                ("dwFlags", wintypes.DWORD),
                ("szExeFile", wintypes.WCHAR * 260),
            ]

        kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
        kernel32.CreateToolhelp32Snapshot.restype = wintypes.HANDLE
        snapshot_handle = kernel32.CreateToolhelp32Snapshot(0x00000002, 0)
        invalid_handle = ctypes.c_void_p(-1).value
        if snapshot_handle in (None, invalid_handle):
            error = ctypes.get_last_error()
            raise WindowsProcessCensusError(
                f"native Toolhelp process snapshot failed winerror={error} evidence_incomplete=1"
            )

        entry = ProcessEntry()
        entry.dwSize = ctypes.sizeof(ProcessEntry)
        active_pids: set[int] = set()
        children_by_parent: dict[int, set[int]] = {}
        try:
            first = kernel32.Process32FirstW(snapshot_handle, ctypes.byref(entry))
            if not first:
                error = ctypes.get_last_error()
                raise WindowsProcessCensusError(
                    f"native Toolhelp process enumeration failed winerror={error} evidence_incomplete=1"
                )
            while first:
                pid = int(entry.th32ProcessID)
                parent_pid = int(entry.th32ParentProcessID)
                active_pids.add(pid)
                children_by_parent.setdefault(parent_pid, set()).add(pid)
                first = kernel32.Process32NextW(snapshot_handle, ctypes.byref(entry))
                if not first and ctypes.get_last_error() != 18:  # ERROR_NO_MORE_FILES
                    error = ctypes.get_last_error()
                    raise WindowsProcessCensusError(
                        f"native Toolhelp process enumeration failed winerror={error} evidence_incomplete=1"
                    )
        finally:
            kernel32.CloseHandle(snapshot_handle)

        descendants: set[int] = set()
        pending = list(children_by_parent.get(root_pid, set()))
        while pending:
            pid = pending.pop()
            if pid in descendants:
                continue
            descendants.add(pid)
            pending.extend(children_by_parent.get(pid, set()))
        if not active_pids:
            raise WindowsProcessCensusError(
                "native Toolhelp process snapshot returned an empty process census evidence_incomplete=1"
            )
        return descendants, active_pids
    except WindowsProcessCensusError:
        raise
    except (AttributeError, OSError, TypeError, ValueError) as error:
        raise WindowsProcessCensusError(
            f"native Toolhelp process snapshot unavailable: {type(error).__name__} evidence_incomplete=1"
        ) from error


def _output_text(output: str | bytes | None) -> str:
    if output is None:
        return ""
    if isinstance(output, bytes):
        return output.decode("utf-8", errors="replace")
    return output


def _output_bytes(output: str | bytes | None) -> bytes:
    if output is None:
        return b""
    if isinstance(output, bytes):
        return output
    return output.encode("utf-8", errors="surrogatepass")


def _merge_output_fragments(*outputs: str | bytes | None) -> bytes:
    """Merge cumulative or incremental subprocess output without duplicating it."""
    merged = b""
    for output in outputs:
        data = _output_bytes(output)
        if not data:
            continue
        if not merged:
            merged = data
            continue
        if data == merged:
            continue
        if data.startswith(merged):
            merged = data
            continue
        # Independent partial reads are appended in order.  The only
        # duplicate forms produced by subprocess APIs are the stdout/output
        # aliases and cumulative snapshots handled above; do not scan large
        # diagnostics byte-by-byte looking for an arbitrary overlap.
        merged += data
    return merged


def _exception_output(error: BaseException) -> tuple[bytes, bytes]:
    """Return all partial streams exposed by a subprocess exception once."""
    return (
        _merge_output_fragments(
            getattr(error, "stdout", None),
            getattr(error, "output", None),
        ),
        _merge_output_fragments(getattr(error, "stderr", None)),
    )


def windows_process_census(
    root_pid: int,
    *,
    timeout_seconds: float,
) -> tuple[set[int], set[int]]:
    """Return descendants of ``root_pid`` and all active process IDs."""

    if isinstance(root_pid, bool) or not isinstance(root_pid, int) or root_pid <= 0:
        raise WindowsProcessCensusError(f"invalid root PID: {root_pid}")
    if os.name == "nt":
        return _toolhelp_process_census(root_pid)
    try:
        result = subprocess.run(
            [
                "powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "Get-CimInstance Win32_Process | "
                "Select-Object ProcessId,ParentProcessId | ConvertTo-Json -Compress",
            ],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False,
            timeout=timeout_seconds,
        )
    except (subprocess.TimeoutExpired, OSError) as error:
        stdout, stderr = _exception_output(error)
        if isinstance(error, subprocess.TimeoutExpired):
            detail = f"timed out after {timeout_seconds}s"
        else:
            detail = f"could not start: {error}"
        raise WindowsProcessCensusError(
            f"PowerShell process-tree query {detail} "
            f"stdout={_output_text(stdout).strip()} "
            f"stderr={_output_text(stderr).strip()} evidence_incomplete=1"
        ) from error
    if result.returncode != 0:
        raise WindowsProcessCensusError(
            f"PowerShell process-tree query failed exit={result.returncode} "
            f"stdout={_output_text(result.stdout).strip()} "
            f"stderr={_output_text(result.stderr).strip()}"
        )
    if result.stderr:
        raise WindowsProcessCensusError(
            "PowerShell process-tree query emitted diagnostics "
            f"stdout={_output_text(result.stdout).strip()} "
            f"stderr={_output_text(result.stderr).strip()}"
        )
    stdout = _output_text(result.stdout)
    if not stdout.strip():
        raise WindowsProcessCensusError(
            "PowerShell process-tree query returned empty output"
        )
    try:
        records = json.loads(stdout)
    except json.JSONDecodeError as error:
        raise WindowsProcessCensusError(
            f"PowerShell process-tree query returned invalid JSON stdout={stdout.strip()}"
        ) from error
    if isinstance(records, dict):
        records = [records]
    if not isinstance(records, list):
        raise WindowsProcessCensusError(
            "PowerShell process-tree query returned an invalid record collection"
        )
    if not records:
        raise WindowsProcessCensusError(
            "PowerShell process-tree query returned an empty process census"
        )

    active_pids: set[int] = set()
    children_by_parent: dict[int, set[int]] = {}
    for record in records:
        if not isinstance(record, dict):
            raise WindowsProcessCensusError(
                f"PowerShell process-tree query returned a non-object record: {record!r}"
            )
        try:
            pid = record["ProcessId"]
            parent_pid = record["ParentProcessId"]
        except KeyError as error:
            raise WindowsProcessCensusError(
                f"PowerShell process-tree query returned an invalid process record: {record!r}"
            ) from error
        if (
            isinstance(pid, bool)
            or not isinstance(pid, int)
            or isinstance(parent_pid, bool)
            or not isinstance(parent_pid, int)
        ):
            raise WindowsProcessCensusError(
                f"PowerShell process-tree query returned an invalid process record: {record!r}"
            )
        # PID 0 is the legitimate Windows System Idle Process.  It cannot be a
        # tracked child root, but it can appear in the complete process census.
        if pid < 0 or parent_pid < 0:
            raise WindowsProcessCensusError(
                f"PowerShell process-tree query returned an invalid process identifier: {record!r}"
            )
        active_pids.add(pid)
        children_by_parent.setdefault(parent_pid, set()).add(pid)

    descendants: set[int] = set()
    pending = list(children_by_parent.get(root_pid, set()))
    while pending:
        pid = pending.pop()
        if pid in descendants:
            continue
        descendants.add(pid)
        pending.extend(children_by_parent.get(pid, set()))
    return descendants, active_pids
