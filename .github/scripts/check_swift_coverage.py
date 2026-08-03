#!/usr/bin/env python3
"""Enforce the tracked line-coverage floor for the host-testable Swift core."""
from __future__ import annotations

import argparse
import json
from pathlib import Path


def parse_lcov(text: str, source_root: Path) -> tuple[int, int]:
    root = source_root.resolve()
    current_file: Path | None = None
    lines: dict[Path, dict[int, int]] = {}
    for record in text.splitlines():
        if record.startswith("SF:"):
            candidate = Path(record.removeprefix("SF:")).resolve()
            current_file = candidate if candidate.is_relative_to(root) else None
        elif record.startswith("DA:") and current_file is not None:
            line, count, *_ = record.removeprefix("DA:").split(",")
            lines.setdefault(current_file, {})[int(line)] = int(count)

    total = sum(len(file_lines) for file_lines in lines.values())
    covered = sum(
        1 for file_lines in lines.values() for count in file_lines.values() if count > 0
    )
    if total == 0:
        raise ValueError(f"no executable coverage lines found below {root}")
    return covered, total


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--lcov", type=Path, required=True)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--summary", type=Path, required=True)
    args = parser.parse_args()

    baseline = json.loads(args.baseline.read_text(encoding="utf-8"))
    minimum = baseline.get("line_coverage_percent")
    if not isinstance(minimum, (int, float)) or not 0 <= minimum <= 100:
        raise ValueError("baseline line_coverage_percent must be a number from 0 through 100")

    covered, total = parse_lcov(args.lcov.read_text(encoding="utf-8"), args.source_root)
    actual = covered * 100 / total
    summary = (
        "## Swift lifecycle-core coverage\n\n"
        f"- Line coverage: **{actual:.2f}%** ({covered}/{total})\n"
        f"- Tracked baseline: **{minimum:.2f}%**\n"
    )
    args.summary.write_text(summary, encoding="utf-8")
    print(summary, end="")
    if actual < minimum:
        raise SystemExit(
            f"Swift lifecycle-core coverage {actual:.2f}% is below the {minimum:.2f}% baseline"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
