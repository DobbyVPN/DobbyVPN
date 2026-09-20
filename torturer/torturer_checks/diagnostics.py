"""Small, shared helpers for complete disposable process diagnostics.

The functional runners are deliberately short-lived.  Child output is sent to
the invoking test process as it is produced; it is never written to a second
evidence/log archive.  Exception notes use the same helper for failures that
are raised before a caller can otherwise forward the streams.
"""

from __future__ import annotations

from collections.abc import Iterable
import re
import sys
from typing import TextIO


def output_text(value: bytes | str | None) -> str:
    """Decode a captured stream without dropping non-UTF-8 bytes."""

    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="backslashreplace")
    return value


# Only infer individual values from fields that conventionally contain
# credentials.  Registering a whole profile/document remains supported, but
# treating every quoted word as private would redact useful protocol/UI
# diagnostics (for example a public host or a status string).
_PRIVATE_ASSIGNMENT = re.compile(
    r"(?im)^[ \t]*[\"']?[\w.-]*(?:password|passphrase|secret|token|"
    r"private[_-]?key|credential|access[_-]?key|username|server|address|url)"
    r"[\w.-]*[\"']?[ \t]*[:=][ \t]*"
    r"[\"']([^'\"\r\n]{4,})[\"']"
)


def _sensitive_texts(values: Iterable[bytes | str] | None) -> tuple[str, ...]:
    if values is None:
        return ()
    candidates: set[str] = set()
    for value in values:
        if value is None:
            continue
        rendered = output_text(value)
        if rendered:
            candidates.add(rendered)
            candidates.update(
                match.group(1) for match in _PRIVATE_ASSIGNMENT.finditer(rendered)
            )
    # Never replace short syntax words (or an empty value).  A complete
    # profile/document is still safe to register and catches the common case
    # where a command prints the source document verbatim.
    return tuple(sorted((value for value in candidates if len(value) >= 4), key=len, reverse=True))


def redact_text(value: bytes | str | None, sensitive_values: Iterable[bytes | str] | None = None) -> str:
    """Redact exact registered private values while retaining all context."""

    rendered = output_text(value)
    for secret in _sensitive_texts(sensitive_values):
        rendered = rendered.replace(secret, "[REDACTED]")
    return rendered


def emit_streams(
    label: str,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
    *,
    sensitive_values: Iterable[bytes | str] | None = None,
    stream: TextIO | None = None,
) -> None:
    """Forward complete child streams to the invoking process.

    The stream labels are deliberately written to stderr so stdout protocols
    (JSON lines, command output consumed by a parser, and so on) remain
    machine-readable.  No size or tail limit is applied.
    """

    destination = stream or sys.stderr
    for name, payload in (("stdout", stdout), ("stderr", stderr)):
        text = redact_text(payload, sensitive_values)
        if not text:
            continue
        destination.write(f"[{label} {name}]\n")
        destination.write(text)
        if not text.endswith("\n"):
            destination.write("\n")
    destination.flush()


def add_stream_notes(
    error: BaseException,
    label: str,
    stdout: bytes | str | None,
    stderr: bytes | str | None,
    *,
    sensitive_values: Iterable[bytes | str] | None = None,
) -> None:
    """Attach complete streams to an exception without replacing its code."""

    for name, payload in (("stdout", stdout), ("stderr", stderr)):
        text = redact_text(payload, sensitive_values)
        error.add_note(f"{label}_{name}:\n{text}" if text else f"{label}_{name}: <empty>")


def add_exception_notes(
    error: BaseException,
    label: str,
    secondary: BaseException,
    *,
    sensitive_values: Iterable[bytes | str] | None = None,
) -> None:
    """Retain a secondary/cleanup exception and any streams attached to it."""

    error.add_note(f"{label}_error={type(secondary).__name__}: {secondary}")
    for note in getattr(secondary, "__notes__", ()):
        error.add_note(f"{label}_{note}")
    secondary_stdout = getattr(secondary, "stdout", None)
    secondary_stderr = getattr(secondary, "stderr", None)
    if secondary_stdout is not None or secondary_stderr is not None:
        add_stream_notes(
            error,
            label,
            secondary_stdout,
            secondary_stderr,
            sensitive_values=sensitive_values,
        )


def register_sensitive_values(runner: object, *values: bytes | str | None) -> None:
    """Register profile/private values when the runner supports redaction."""

    register = getattr(runner, "register_sensitive_values", None)
    if callable(register):
        register(*values)


__all__ = [
    "add_exception_notes",
    "add_stream_notes",
    "emit_streams",
    "output_text",
    "redact_text",
    "register_sensitive_values",
]
