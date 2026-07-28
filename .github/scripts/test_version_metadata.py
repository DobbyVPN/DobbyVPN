#!/usr/bin/env python3

import unittest

from version_metadata import parse_version


class VersionMetadataTests(unittest.TestCase):
    def test_fdroid_version_code_scheme(self):
        metadata = parse_version("1.4.3\n")
        self.assertEqual(metadata.version_name, "1.4.3")
        self.assertEqual(metadata.android_version_code, 1_004_003)

    def test_previous_published_code_remains_compatible(self):
        self.assertEqual(parse_version("1.3.9").android_version_code, 1_003_009)

    def test_rejects_noncanonical_or_ambiguous_versions(self):
        for value in ("1.4", "v1.4.3", "01.4.3", "1.1000.0", "1.4.1000"):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    parse_version(value)


if __name__ == "__main__":
    unittest.main()
