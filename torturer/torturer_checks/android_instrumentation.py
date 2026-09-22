"""Strict parsing for the Android instrumentation UI smoke test.

``adb shell am instrument`` writes its JUnit summary and completion marker to
stdout.  The marker is not, by itself, evidence that the test passed: a
crashed runner can still return a successful shell status, and an earlier
``OK`` line can be followed by a failure.  Keep this parser shared by local
VMs, hosted adapters, and the hosted workflow so all three paths apply the
same contract.

Only stdout is authoritative for a pass.  Stderr is deliberately retained as
diagnostic output and never supplies a success marker; an explicit
``FAILURES!!!`` line on either stream still rejects the result.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
import re
import sys


ROUTING_RULE_CHAIN = "DOBBYVPN_TORTURER"

_JUNIT_SUCCESS = re.compile(rb"^[ \t]*OK \(1 test\)[ \t]*$")
_JUNIT_FAILURES = re.compile(rb"^[ \t]*FAILURES!!![ \t]*$")
_INSTRUMENTATION_SUCCESS = re.compile(
    rb"^[ \t]*INSTRUMENTATION_CODE: -1[ \t]*$"
)
_COMPLETE_THROWABLE_BEGIN = re.compile(
    rb"DOBBY_COMPLETE_THROWABLE_BEGIN id=([0-9]+) chunks=([1-9][0-9]*) "
    rb"bytes=([0-9]+) chars=([0-9]+)"
)
_COMPLETE_THROWABLE_CHUNK = re.compile(
    rb"DOBBY_COMPLETE_THROWABLE_CHUNK id=([0-9]+) "
    rb"sequence=([1-9][0-9]*)/([1-9][0-9]*)"
)
_COMPLETE_THROWABLE_END = re.compile(
    rb"DOBBY_COMPLETE_THROWABLE_END id=([0-9]+) chunks=([1-9][0-9]*) "
    rb"bytes=([0-9]+) chars=([0-9]+)"
)


@dataclass(frozen=True)
class InstrumentationParse:
    """The evidence fields needed by callers and failure diagnostics."""

    returncode: int
    timed_out: bool
    success_marker_present: bool
    junit_summary_present: bool
    failures_marker_present: bool
    stderr_present: bool

    @property
    def succeeded(self) -> bool:
        return (
            self.returncode == 0
            and not self.timed_out
            and self.success_marker_present
            and self.junit_summary_present
            and not self.failures_marker_present
        )


def _lines_without_trailing_newlines(value: bytes) -> list[bytes]:
    """Return output lines while permitting the normal final newline only."""

    return value.rstrip(b"\r\n").splitlines()


def parse_instrumentation_result(
    *,
    returncode: int,
    stdout: bytes,
    stderr: bytes = b"",
    timed_out: bool = False,
) -> InstrumentationParse:
    """Parse one completed ``am instrument`` invocation.

    The completion marker must be the final non-newline stdout line and the
    JUnit summary must be an anchored ``OK (1 test)`` line.  Markers found only
    on stderr never turn a result into a pass.  ``FAILURES!!!`` is rejected on
    either stream so a runner failure cannot be hidden in diagnostics.
    """

    stdout_lines = _lines_without_trailing_newlines(stdout)
    success_marker_present = bool(stdout_lines) and bool(
        _INSTRUMENTATION_SUCCESS.fullmatch(stdout_lines[-1])
    )
    junit_summary_present = any(
        _JUNIT_SUCCESS.fullmatch(line) for line in stdout_lines
    )
    failures_marker_present = any(
        _JUNIT_FAILURES.fullmatch(line) for line in stdout_lines
    ) or any(
        _JUNIT_FAILURES.fullmatch(line)
        for line in _lines_without_trailing_newlines(stderr)
    )
    return InstrumentationParse(
        returncode=returncode,
        timed_out=timed_out,
        success_marker_present=success_marker_present,
        junit_summary_present=junit_summary_present,
        failures_marker_present=failures_marker_present,
        stderr_present=bool(stderr),
    )


def validate_complete_throwable_report(output: bytes) -> None:
    """Validate ordered, exhaustive reporter transport framing."""

    begins = list(_COMPLETE_THROWABLE_BEGIN.finditer(output))
    ends = list(_COMPLETE_THROWABLE_END.finditer(output))
    chunks = list(_COMPLETE_THROWABLE_CHUNK.finditer(output))
    if len(begins) != 1 or len(ends) != 1:
        raise ValueError("complete throwable output must contain one begin and end")
    begin = begins[0]
    end = ends[0]
    report_id = begin.group(1)
    expected_chunks = int(begin.group(2))
    if (
        end.group(1) != report_id
        or int(end.group(2)) != expected_chunks
        or int(end.group(3)) != int(begin.group(3))
        or int(end.group(4)) != int(begin.group(4))
        or len(chunks) != expected_chunks
    ):
        raise ValueError("complete throwable output framing is inconsistent")
    sequences = [
        (match.group(1), int(match.group(2)), int(match.group(3)))
        for match in chunks
    ]
    if any(item[0] != report_id or item[2] != expected_chunks for item in sequences):
        raise ValueError("complete throwable chunk identity is invalid")
    if [item[1] for item in sequences] != list(range(1, expected_chunks + 1)):
        raise ValueError("complete throwable chunks are not ordered")


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stdout", type=Path, required=True)
    parser.add_argument("--stderr", type=Path, required=True)
    parser.add_argument("--returncode", type=int, required=True)
    parser.add_argument("--timed-out", action="store_true")
    parser.add_argument("--complete-throwable", action="store_true")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)
    try:
        stdout = args.stdout.read_bytes()
        stderr = args.stderr.read_bytes()
    except OSError as error:
        print(f"instrumentation evidence unavailable: {error}", file=sys.stderr)
        return 2
    parsed = parse_instrumentation_result(
        returncode=args.returncode,
        stdout=stdout,
        stderr=stderr,
        timed_out=args.timed_out,
    )
    if args.complete_throwable:
        try:
            validate_complete_throwable_report(stdout)
        except ValueError as error:
            print(f"Android complete throwable report failed: {error}", file=sys.stderr)
            return 1
    if not parsed.succeeded:
        print(
            "Android instrumentation failed: "
            f"returncode={parsed.returncode}, timed_out={parsed.timed_out}, "
            f"success_marker_present={parsed.success_marker_present}, "
            f"junit_summary_present={parsed.junit_summary_present}, "
            f"failures_marker_present={parsed.failures_marker_present}",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
