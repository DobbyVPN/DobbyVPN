#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("check_swift_coverage.py")
SPEC = importlib.util.spec_from_file_location("check_swift_coverage", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
coverage = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(coverage)


class CheckSwiftCoverageTests(unittest.TestCase):
    def test_counts_only_requested_source_tree_and_deduplicates_lines(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "CommonDI"
            root.mkdir()
            source = root / "Core.swift"
            source.touch()
            unrelated = Path(directory) / "Tests.swift"
            unrelated.touch()
            lcov = "\n".join(
                (
                    f"SF:{source}",
                    "DA:1,2",
                    "DA:2,0",
                    f"SF:{source}",
                    "DA:1,3",
                    f"SF:{unrelated}",
                    "DA:1,9",
                )
            )
            self.assertEqual(coverage.parse_lcov(lcov, root), (1, 2))

    def test_rejects_a_report_without_requested_sources(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                coverage.parse_lcov("SF:/tmp/other.swift\nDA:1,1", Path(directory))


if __name__ == "__main__":
    unittest.main()
