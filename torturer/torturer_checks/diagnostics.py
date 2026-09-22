"""Small, shared helpers for complete disposable process diagnostics.

The functional runners are deliberately short-lived.  Child output is sent to
the invoking test process as it is produced; it is never written to a second
evidence/log archive.  Exception notes use the same helper for failures that
are raised before a caller can otherwise forward the streams.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
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


_PRIVATE_FIELD = (
    rb"password|passphrase|secret|token|private[_-]?key|credential|"
    rb"access[_-]?key|username|server|address|url"
)

# Only infer individual values from fields that conventionally contain
# credentials. Registering a whole profile/document remains supported, but
# treating every quoted word as private would redact useful protocol/UI
# diagnostics (for example a public host or a status string). This parser is
# deliberately permissive about quoting and value length: a real credential
# can be one or two bytes in a fixture or a development profile.
_PRIVATE_ASSIGNMENT = re.compile(
    rb"(?im)(?:^|[,{ \t])['\"]?[\w.-]*(?:"
    + _PRIVATE_FIELD
    + rb")[\w.-]*['\"]?[ \t]*[:=][ \t]*['\"]?"
    rb"([^'\"\r\n,}\]]+)"
)

_PRIVATE_ASSIGNMENT_PREFIX = re.compile(
    rb"(?im)(?:^|[,{ \t])['\"]?[\w.-]*(?:"
    + _PRIVATE_FIELD
    + rb")[\w.-]*['\"]?[ \t]*[:=][ \t]*['\"]?$"
)


@dataclass(frozen=True)
class _SensitivePatterns:
    """Byte patterns and contextual short values registered for a stream."""

    # Values at least four bytes long are safe to match globally. A complete
    # profile/document is also normally in this set, so its exact bytes are
    # redacted even when it contains newlines or malformed UTF-8.
    global_values: tuple[bytes, ...]
    # Short values are intentionally not global substitutions: ``ok`` may be
    # an ordinary status word. They are replaced only in a sensitive-field
    # assignment or when the complete rendered value is that secret.
    contextual_values: tuple[bytes, ...]


def _pattern_bytes(value: bytes | str) -> bytes:
    if isinstance(value, bytes):
        return value
    return value.encode("utf-8", errors="backslashreplace")


def _sensitive_patterns(values: Iterable[bytes | str] | None) -> _SensitivePatterns:
    if values is None:
        return _SensitivePatterns((), ())
    global_values: set[bytes] = set()
    contextual_values: set[bytes] = set()
    for value in values:
        if value is None:
            continue
        raw = _pattern_bytes(value)
        if not raw:
            continue
        # Long registered values are exact profile/private values and can be
        # replaced anywhere. Short values need surrounding sensitive context.
        (global_values if len(raw) >= 4 else contextual_values).add(raw)
        for match in _PRIVATE_ASSIGNMENT.finditer(raw):
            field_value = match.group(1).strip()
            if field_value:
                (global_values if len(field_value) >= 4 else contextual_values).add(
                    field_value
                )
    return _SensitivePatterns(
        tuple(sorted(global_values, key=len, reverse=True)),
        tuple(sorted(contextual_values, key=len, reverse=True)),
    )


def _sensitive_texts(values: Iterable[bytes | str] | None) -> tuple[str, ...]:
    """Return registered patterns for compatibility with older callers.

    Redaction itself uses byte patterns so malformed and split UTF-8 remains
    reversible. This helper is kept private and is only useful to diagnostics
    tests or local debugging.
    """

    patterns = _sensitive_patterns(values)
    return tuple(
        value.decode("utf-8", errors="backslashreplace")
        for value in (*patterns.global_values, *patterns.contextual_values)
    )


def redact_text(value: bytes | str | None, sensitive_values: Iterable[bytes | str] | None = None) -> str:
    """Redact exact registered private values while retaining all context."""

    redactor = StreamingRedactor(sensitive_values)
    return redactor.feed(value) + redactor.finish()


class StreamingRedactor:
    """Redact registered values without leaking a value split across chunks."""

    def __init__(self, sensitive_values: Iterable[bytes | str] | None = None) -> None:
        self._patterns = _sensitive_patterns(sensitive_values)
        self._max_secret_length = max(
            (len(secret) for secret in self._patterns.global_values),
            default=0,
        )
        self._pending = b""

    @staticmethod
    def _decode(value: bytes) -> str:
        # Decode only complete, already-safe byte spans. backslashreplace
        # preserves malformed provider/child bytes instead of introducing a
        # lossy U+FFFD character.
        return value.decode("utf-8", errors="backslashreplace")

    def _global_redact(self, value: bytes) -> bytes:
        for secret in self._patterns.global_values:
            value = value.replace(secret, b"[REDACTED]")
        return value

    @staticmethod
    def _after_assignment(value: bytes, start: int, end: int) -> bool:
        """Return whether a value occurrence is a complete assignment value."""

        line_start = value.rfind(b"\n", 0, start) + 1
        prefix = value[line_start:start]
        if _PRIVATE_ASSIGNMENT_PREFIX.fullmatch(prefix) is None:
            return False
        suffix = value[end:]
        if suffix.startswith((b"'", b'\"')):
            suffix = suffix[1:]
        return not suffix or suffix[:1] in b" \t\r\n,;}]#"

    def _contextual_redact(self, value: bytes) -> bytes:
        for secret in self._patterns.contextual_values:
            if not secret:
                continue
            offset = 0
            while True:
                start = value.find(secret, offset)
                if start < 0:
                    break
                end = start + len(secret)
                line_start = value.rfind(b"\n", 0, start) + 1
                line_end = value.find(b"\n", end)
                if line_end < 0:
                    line_end = len(value)
                line = value[line_start:line_end]
                stripped = line.strip(b" \t\r")
                value_only = stripped == secret
                if value_only or self._after_assignment(value, start, end):
                    value = value[:start] + b"[REDACTED]" + value[end:]
                    offset = start + len(b"[REDACTED]")
                else:
                    offset = end
        return value

    def _redact_complete(self, value: bytes) -> bytes:
        return self._contextual_redact(self._global_redact(value))

    def feed(self, value: bytes | str | None) -> str:
        """Return safe complete lines while retaining possible secret tails.

        The pending buffer is bytes, not independently decoded chunks. A
        multi-byte UTF-8 sequence split over reads therefore remains intact,
        and a malformed byte is rendered reversibly only when it is safe to
        forward.
        """

        if value is None:
            return ""
        rendered = value if isinstance(value, bytes) else value.encode(
            "utf-8", errors="backslashreplace"
        )
        if not rendered:
            return ""
        candidate = self._pending + rendered
        safe_end = len(candidate)
        for secret in self._patterns.global_values:
            # A suffix which is a strict prefix of a secret may become the
            # beginning of a private value in the next read. Retain it until
            # the next chunk, while leaving all other content streamable.
            maximum = min(len(secret) - 1, len(candidate))
            for length in range(maximum, 0, -1):
                if candidate.endswith(secret[:length]):
                    safe_end = min(safe_end, len(candidate) - length)
                    break
            start = candidate.find(secret)
            while start >= 0:
                end = start + len(secret)
                if start < safe_end < end:
                    # The complete profile/private value crosses the line
                    # boundary we would otherwise forward. Keep its complete
                    # raw span so replacement can happen atomically.
                    safe_end = start
                start = candidate.find(secret, start + 1)
        safe = candidate[:safe_end]
        possible_secret_tail = candidate[safe_end:]
        last_newline = safe.rfind(b"\n")
        if last_newline < 0:
            self._pending = candidate
            return ""
        complete = safe[: last_newline + 1]
        self._pending = safe[last_newline + 1 :] + possible_secret_tail
        return self._decode(self._redact_complete(complete))

    def finish(self) -> str:
        """Flush the final tail after the producer has closed its stream."""

        value = self._decode(self._redact_complete(self._pending))
        self._pending = b""
        return value


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
    "StreamingRedactor",
    "register_sensitive_values",
]
