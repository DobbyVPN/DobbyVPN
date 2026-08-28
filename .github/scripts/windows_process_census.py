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
