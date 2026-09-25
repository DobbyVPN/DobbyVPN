"""Forward complete diagnostics from disposable test processes."""

from __future__ import annotations

import sys
from typing import TextIO


def output_text(value: bytes | str | None) -> str:
    """Render complete bytes reversibly when an exception needs text notes."""
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="backslashreplace")
    return value


def _write_payload(destination: TextIO, payload: bytes | str) -> None:
    binary = getattr(destination, "buffer", None)
    if binary is not None:
        binary.write(payload if isinstance(payload, bytes) else payload.encode("utf-8"))
        return
    if isinstance(payload, bytes):
        destination.write(payload.decode("utf-8", errors="backslashreplace"))
    else:
        destination.write(payload)


def emit_streams(
    label: str,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
    *,
    stream: TextIO | None = None,
) -> None:
    """Forward both complete child streams to the invoking process."""
    destination = stream or sys.stderr
    for name, payload in (("stdout", stdout), ("stderr", stderr)):
        if not payload:
            continue
        _write_payload(destination, f"[{label} {name}]\n")
        _write_payload(destination, payload)
        if not output_text(payload).endswith("\n"):
            _write_payload(destination, "\n")
    destination.flush()


def add_stream_notes(
    error: BaseException,
    label: str,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
) -> None:
    """Attach complete streams to an exception without replacing its code."""
    for name, payload in (("stdout", stdout), ("stderr", stderr)):
        text = output_text(payload)
        error.add_note(f"{label}_{name}:\n{text}" if text else f"{label}_{name}: <empty>")


def add_exception_notes(
    error: BaseException,
    label: str,
    secondary: BaseException,
) -> None:
    """Retain a secondary exception and any complete streams it carries."""
    error.add_note(f"{label}_error={type(secondary).__name__}: {secondary}")
    for note in getattr(secondary, "__notes__", ()):
        error.add_note(f"{label}_{note}")
    secondary_stdout = getattr(secondary, "stdout", None)
    secondary_stderr = getattr(secondary, "stderr", None)
    if secondary_stdout is not None or secondary_stderr is not None:
        add_stream_notes(error, label, secondary_stdout, secondary_stderr)


__all__ = [
    "add_exception_notes",
    "add_stream_notes",
    "emit_streams",
    "output_text",
]
