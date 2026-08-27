#!/usr/bin/env python3
"""Read a Windows process census without signalling any process."""

from __future__ import annotations

import json
import subprocess


class WindowsProcessCensusError(RuntimeError):
    """Raised when the Windows process census cannot be proven complete."""


def _output_text(output: str | bytes | None) -> str:
    if output is None:
        return ""
    if isinstance(output, bytes):
        return output.decode("utf-8", errors="replace")
    return output


def windows_process_census(
    root_pid: int,
    *,
    timeout_seconds: float,
) -> tuple[set[int], set[int]]:
    """Return descendants of ``root_pid`` and all active process IDs."""

    if isinstance(root_pid, bool) or not isinstance(root_pid, int) or root_pid <= 0:
        raise WindowsProcessCensusError(f"invalid root PID: {root_pid}")
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
    except subprocess.TimeoutExpired as error:
        raise WindowsProcessCensusError(
            f"PowerShell process-tree query timed out after {timeout_seconds}s "
            f"stdout={_output_text(error.output).strip()} "
            f"stderr={_output_text(error.stderr).strip()}"
        ) from error
    except OSError as error:
        raise WindowsProcessCensusError(
            f"PowerShell process-tree query could not start: {error}"
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
