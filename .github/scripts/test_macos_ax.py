from __future__ import annotations

import unittest
from unittest.mock import patch

import macos_ax


class _Core:
    @staticmethod
    def CFHash(value):
        return value

    @staticmethod
    def CFEqual(left, right):
        return left == right


class _Frameworks:
    core = _Core()

    @staticmethod
    def release(_value):
        return None


class MacOSAXMatchTests(unittest.TestCase):
    def _find(self, frames: dict[int, tuple[int, int, int, int]]):
        children = {1: (2, 3), 2: (), 3: ()}
        framework = _Frameworks()

        def element_texts(_frameworks, element):
            return ("Connection configuration",) if element in {2, 3} else ()

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", side_effect=lambda _f, e: frames[e]),
            patch.object(macos_ax, "_children", side_effect=lambda _f, e: children[e]),
            patch.object(macos_ax, "_element_texts", side_effect=element_texts),
            patch.object(
                macos_ax,
                "_cg_window_diagnostics",
                return_value={"cg_window_count": 1, "cg_window_total_count": 1},
            ),
        ):
            return macos_ax._find_control(
                framework, 42, "Connection configuration", False
            )

    def test_same_frame_stale_roots_are_one_logical_control(self):
        result = self._find({
            1: (0, 0, 100, 100),
            2: (10, 10, 90, 40),
            3: (10, 10, 90, 40),
        })
        self.assertEqual(result["bounds"], [10, 10, 90, 40])

    def test_distinct_same_name_controls_remain_ambiguous(self):
        with self.assertRaisesRegex(
            macos_ax.AXLookupError, "matched multiple controls"
        ):
            self._find({
                1: (0, 0, 100, 100),
                2: (10, 10, 90, 40),
                3: (10, 50, 90, 80),
            })


if __name__ == "__main__":
    unittest.main()
