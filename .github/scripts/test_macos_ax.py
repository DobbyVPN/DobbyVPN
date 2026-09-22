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
    def test_window_obstruction_probe_only_reports_frontmost_intersections(self):
        records = [
            {"owner_pid": 77, "layer": 0, "bounds": [0, 0, 100, 100], "owner_name": "Other"},
            {"owner_pid": 42, "layer": 0, "bounds": [10, 10, 400, 900], "owner_name": "Dobby Vpn"},
            {"owner_pid": 88, "layer": 0, "bounds": [200, 200, 300, 300], "owner_name": "Unrelated"},
        ]
        with patch.object(macos_ax, "_cg_window_records", return_value=records):
            result = macos_ax._window_obstruction_probe(_Frameworks(), 42)
        self.assertEqual(result["stage"], "obstruction")
        self.assertEqual([entry["owner_pid"] for entry in result["obstructions"]], [77])

    def test_window_obstruction_probe_keeps_system_backing_surfaces_as_ignored(self):
        records = [
            {
                "owner_pid": 562,
                "layer": 23,
                "bounds": [0, 0, 1920, 1080],
                "owner_name": "Notification Center",
            },
            {
                "owner_pid": 408,
                "layer": 20,
                "bounds": [0, 0, 1920, 1080],
                "owner_name": "Dock",
            },
            {
                "owner_pid": 166,
                "layer": 2147483630,
                "bounds": [326, 853, 343, 876],
                "owner_name": "Window Server",
            },
            {"owner_pid": 77, "layer": 0, "bounds": [0, 0, 100, 100], "owner_name": "Other"},
            {"owner_pid": 42, "layer": 0, "bounds": [10, 10, 400, 900], "owner_name": "Dobby Vpn"},
        ]
        with patch.object(macos_ax, "_cg_window_records", return_value=records):
            result = macos_ax._window_obstruction_probe(_Frameworks(), 42)
        self.assertEqual([entry["owner_pid"] for entry in result["obstructions"]], [77])
        self.assertEqual(
            [entry["owner_pid"] for entry in result["ignored_obstructions"]],
            [562, 408, 166],
        )

    def _find(self, frames: dict[int, tuple[int, int, int, int]]):
        children = {1: (2, 3), 2: (), 3: (4, 5), 4: (), 5: ()}
        framework = _Frameworks()

        def element_texts(_frameworks, element):
            return ("Connection configuration",) if element in {4, 5} else ()

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
            2: (0, 0, 100, 100),
            3: (0, 0, 100, 100),
            4: (10, 10, 90, 40),
            5: (10, 10, 90, 40),
        })
        self.assertEqual(result["bounds"], [10, 10, 90, 40])

    def test_distinct_same_name_controls_remain_ambiguous(self):
        with self.assertRaisesRegex(
            macos_ax.AXLookupError, "matched multiple controls"
        ):
            self._find({
                1: (0, 0, 100, 100),
                2: (0, 0, 100, 100),
                3: (0, 0, 100, 100),
                4: (10, 10, 90, 40),
                5: (10, 50, 90, 80),
            })

    def test_raise_window_uses_only_public_ax_raise_action(self):
        class AX:
            @staticmethod
            def AXUIElementPerformAction(element, action):
                self.assertEqual(element, 1)
                self.assertEqual(action, "AXRaise")
                return 0

        class Framework:
            core = _Core()
            ax = AX()

            @staticmethod
            def string(value):
                return value

            @staticmethod
            def release(_value):
                return None

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", return_value=(0, 0, 100, 100)),
        ):
            result = macos_ax._raise_window(Framework(), 42)
        self.assertTrue(result["ok"])
        self.assertEqual(result["stage"], "raise")

    def test_raise_window_fails_closed_when_ax_raise_is_rejected(self):
        class AX:
            @staticmethod
            def AXUIElementPerformAction(_element, _action):
                return -25205

        class Framework:
            core = _Core()
            ax = AX()

            @staticmethod
            def string(value):
                return value

            @staticmethod
            def release(_value):
                return None

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", return_value=(0, 0, 100, 100)),
        ):
            with self.assertRaisesRegex(macos_ax.AXLookupError, "AXRaise failed"):
                macos_ax._raise_window(Framework(), 42)

    def test_window_title_probe_reads_public_ax_title(self):
        class Framework:
            core = _Core()

            @staticmethod
            def text(value):
                return value if isinstance(value, str) else None

            @staticmethod
            def release(_value):
                return None

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", return_value=(0, 0, 100, 100)),
            patch.object(
                macos_ax,
                "_copy_attribute",
                return_value=(macos_ax._AX_SUCCESS, "Dobby VPN — Connected"),
            ),
        ):
            result = macos_ax._window_title_probe(Framework(), 42)
        self.assertEqual(result["stage"], "window-title")
        self.assertEqual(result["title"], "Dobby VPN — Connected")

    def test_window_title_probe_rejects_missing_or_non_text_title(self):
        class Framework:
            core = _Core()

            @staticmethod
            def text(_value):
                return None

            @staticmethod
            def release(_value):
                return None

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", return_value=(0, 0, 100, 100)),
            patch.object(
                macos_ax,
                "_copy_attribute",
                return_value=(macos_ax._AX_SUCCESS, object()),
            ),
        ):
            with self.assertRaisesRegex(macos_ax.AXLookupError, "no readable title"):
                macos_ax._window_title_probe(Framework(), 42)

    def test_window_title_probe_marks_ax_title_windowserver_errors_transient(self):
        class Framework:
            core = _Core()

            @staticmethod
            def release(_value):
                return None

        with (
            patch.object(macos_ax, "_windows", return_value=(1,)),
            patch.object(macos_ax, "_frame", return_value=(0, 0, 100, 100)),
            patch.object(
                macos_ax,
                "_copy_attribute",
                return_value=(macos_ax._AX_ERROR_CANNOT_COMPLETE, None),
            ),
        ):
            with self.assertRaisesRegex(macos_ax.AXLookupError, "status=-25204") as raised:
                macos_ax._window_title_probe(Framework(), 42)
        self.assertTrue(raised.exception.transient)


if __name__ == "__main__":
    unittest.main()
