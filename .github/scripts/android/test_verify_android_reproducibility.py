from __future__ import annotations

import json
from pathlib import Path
import shutil
import tempfile
import unittest
import zipfile

from verify_android_reproducibility import VerificationError, verify_signed_payload


def write_apk(path: Path, entries: list[tuple[str, bytes]]) -> None:
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, payload in entries:
            archive.writestr(name, payload)


class AndroidPayloadComparisonTests(unittest.TestCase):
    def test_identical_payload_passes_and_mismatches_report_complete_records(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            expected = directory / "expected.apk"
            identical = directory / "identical.apk"
            actual = directory / "actual.apk"
            expected_entries = [
                ("same.bin", b"same"),
                ("changed.txt", b"before"),
                ("removed.bin", b"removed"),
                ("duplicate.bin", b"first"),
                ("duplicate.bin", b"second"),
            ]
            with self.assertWarnsRegex(UserWarning, "Duplicate name"):
                write_apk(expected, expected_entries)
            shutil.copyfile(expected, identical)

            verify_signed_payload(expected, identical)

            write_apk(
                actual,
                [
                    ("same.bin", b"same"),
                    ("changed.txt", b"after"),
                    ("added.bin", b"added"),
                    ("duplicate.bin", b"first"),
                ],
            )
            with self.assertRaises(VerificationError) as raised:
                verify_signed_payload(expected, actual)

        message = str(raised.exception)
        prefix, diagnostic = message.split("\nDiffering payload records:\n", 1)
        self.assertEqual(prefix, "signed APK payload differs from the verified unsigned APK")
        differences = {entry["path"]: entry for entry in json.loads(diagnostic)}
        self.assertNotIn("same.bin", differences)
        self.assertEqual(differences["added.bin"]["expected"], [])
        self.assertEqual(len(differences["added.bin"]["actual"]), 1)
        self.assertEqual(len(differences["removed.bin"]["expected"]), 1)
        self.assertEqual(differences["removed.bin"]["actual"], [])
        self.assertEqual(differences["duplicate.bin"]["expected"][0]["path"], "duplicate.bin")
        self.assertEqual(len(differences["duplicate.bin"]["expected"]), 2)
        self.assertEqual(len(differences["duplicate.bin"]["actual"]), 1)
        self.assertEqual(differences["changed.txt"]["expected"][0]["bytes"], 6)
        self.assertEqual(differences["changed.txt"]["actual"][0]["bytes"], 5)
        self.assertNotEqual(
            differences["changed.txt"]["expected"][0]["sha256"],
            differences["changed.txt"]["actual"][0]["sha256"],
        )


if __name__ == "__main__":
    unittest.main()
