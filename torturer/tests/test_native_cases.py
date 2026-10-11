from __future__ import annotations

import unittest

from torturer_runner.native_cases import (
    ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE,
    validate_native_cases,
)


class NativeCaseSelectionTests(unittest.TestCase):
    def test_each_lane_accepts_its_exact_case_and_setup_suite(self):
        self.assertEqual(
            validate_native_cases("android", "mini", ["small-screen-log-viewport"]),
            ("small-screen-log-viewport",),
        )
        self.assertEqual(
            validate_native_cases("android", "mini", [ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE]),
            (ANDROID_LOGS_CLEAR_PROCESS_RESTART_CASE,),
        )
        self.assertEqual(
            validate_native_cases("windows", "full", ["configure-acceptance"]),
            ("configure-acceptance",),
        )
        self.assertEqual(
            validate_native_cases("windows", "full", ["logs-clear"]),
            ("logs-clear",),
        )

    def test_multiple_cases_keep_the_requested_order(self):
        self.assertEqual(validate_native_cases(
            "windows", "full", ["logs-clear", "logs-details"]
        ), ("logs-clear", "logs-details"))

    def test_duplicate_cases_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "unique"):
            validate_native_cases("windows", "full", ["logs-clear", "logs-clear"])

    def test_unsupported_case_and_setup_suite_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "unsupported"):
            validate_native_cases("windows", "full", ["Configure-tree"])
        with self.assertRaisesRegex(ValueError, "requires suite full"):
            validate_native_cases("windows", "mini", ["configure-acceptance"])


if __name__ == "__main__":
    unittest.main()
