from __future__ import annotations

import unittest
from unittest.mock import patch

import mobile_android_ui_smoke as smoke


class MobileAndroidInputTests(unittest.TestCase):
    def test_input_text_encoding_preserves_toml_punctuation_and_spaces(self) -> None:
        self.assertEqual(
            smoke._encode_input_text('[[Outline]] Password = "a%b c"'),
            '[[Outline]]%sPassword%s=%s"a%25b%sc"',
        )

    def test_profile_input_uses_a_native_key_combination_for_select_all(self) -> None:
        calls: list[list[str]] = []

        def fake_run(command, **kwargs):
            calls.append(command)
            return None

        with patch.object(smoke, "run", side_effect=fake_run):
            import tempfile
            from pathlib import Path

            with tempfile.TemporaryDirectory() as temporary:
                profile = Path(temporary) / "profile.toml"
                profile.write_text('[[Outline]]\nPassword = "a%b c"\n', encoding="utf-8")
                smoke.enter_profile("adb", profile)

        self.assertIn(
            ["adb", "shell", "input", "keycombination", "KEYCODE_CTRL_LEFT", "KEYCODE_A"],
            calls,
        )
        text_calls = [call for call in calls if call[3:5] == ["text", '[[Outline]]']]
        self.assertTrue(text_calls)
        self.assertTrue(all(call[3] == "text" for call in calls if len(call) > 3 and call[3] == "text"))


if __name__ == "__main__":
    unittest.main()
