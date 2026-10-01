from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from android_dependency_provenance import (
    JAVA_MAJOR,
    REPOSITORY_ROOT,
    _read_android_gradle_values,
)


class AndroidGradlePinTests(unittest.TestCase):
    def test_rejects_mismatched_java_and_kotlin_targets(self) -> None:
        build_file = REPOSITORY_ROOT / "ui/android/app/build.gradle.kts"
        source = build_file.read_text(encoding="utf-8")
        old_target = f'JvmTarget.fromTarget("{JAVA_MAJOR}")'
        self.assertEqual(source.count(old_target), 1)
        modified = source.replace(
            old_target, f'JvmTarget.fromTarget("{JAVA_MAJOR + 1}")', 1
        )

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "build.gradle.kts"
            path.write_text(modified, encoding="utf-8")

            with self.assertRaisesRegex(ValueError, "must agree"):
                _read_android_gradle_values(path)


if __name__ == "__main__":
    unittest.main()
