"""Expose the repository's shared finite subprocess capture to Torturer."""

from __future__ import annotations

from pathlib import Path
import sys

_SCRIPT_DIRECTORY = str(Path(__file__).resolve().parents[2] / ".github" / "scripts")
if _SCRIPT_DIRECTORY not in sys.path:
    sys.path.insert(0, _SCRIPT_DIRECTORY)

from bounded_process import (  # noqa: E402
    exception_output,
    merge_output_fragments,
    output_bytes,
    output_text,
    run_finite_capture,
)

__all__ = [
    "exception_output",
    "merge_output_fragments",
    "output_bytes",
    "output_text",
    "run_finite_capture",
]
