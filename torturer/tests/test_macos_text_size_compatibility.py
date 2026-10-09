"""Keep an unavailable OS setting distinct from verified size-change coverage."""
import unittest
from unittest.mock import Mock, patch

from torturer_runner.ui import journey


class MacOSTextSizeCompatibilityTests(unittest.TestCase):
    def inspect(self, eligible, **updates):
        details = {
            "ready": True, "available": True, "rows_complete": True,
            "eligible": eligible, "candidate_bundle_identifier": "test.dobbyvpn",
            "candidate_name": "Dobby VPN", "candidate_version": "1.5.4",
            "os_version": "macOS 15", "settings_pid": 100,
            "settings_window_owner_pid": 100,
            "app_rows": [{"app": "Books", "size": "Default", "enabled": True}],
        }
        details.update(updates)
        checks = {}
        with patch.object(journey, "_native_ui_action", return_value=details):
            journey._inspect_macos_text_size_compatibility(Mock(), 10, checks)
        return checks

    def test_complete_ineligible_list_records_compatibility_without_claiming_scaling(self):
        checks = self.inspect(False)
        self.assertTrue(checks["macos_text_size_compatibility_inspected"])
        self.assertIs(checks["macos_text_size_settings"]["eligible"], False)
        self.assertNotIn("macos_text_size_layout", checks)

    def test_unknown_or_wrong_window_cannot_pass(self):
        for eligible, updates in ((None, {}), (False, {"rows_complete": False}),
                                  (False, {"settings_window_owner_pid": 101})):
            with self.subTest(updates=updates):
                with self.assertRaisesRegex(journey.NativeUIJourneyError, "was not established"):
                    self.inspect(eligible, **updates)

    def test_eligible_candidate_requires_actual_apply_restore_coverage(self):
        with self.assertRaisesRegex(journey.NativeUIJourneyError, "apply/restore layout coverage is required"):
            self.inspect(True)


if __name__ == "__main__":
    unittest.main()
