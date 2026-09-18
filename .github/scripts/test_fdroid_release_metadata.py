#!/usr/bin/env python3
"""Tests for temporary F-Droid release metadata preparation."""

from __future__ import annotations

from contextlib import redirect_stdout
from copy import deepcopy
import io
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest import mock

import yaml

from fdroid_release_metadata import MetadataError, autoupdate, finalize, prepare


SOURCE_SHA = "0123456789abcdef0123456789abcdef01234567"
SIGNING_KEY = "c3f0414a74012060d7c6aa3a3d9dac0aa13c1bd23b7512eefd860fb865e67933"
UPDATE_DATA = (
    "https://github.com/DobbyVPN/DobbyVPN/releases/latest/download/version.txt|"
    r"versionCode=(\d+)|.|versionName=([\d.]+)"
)


def metadata_document() -> dict[str, object]:
    return {
        "Categories": ["Internet"],
        "RepoType": "git",
        "Repo": "https://github.com/DobbyVPN/DobbyVPN",
        "Binaries": "https://example.invalid/DobbyVPN-v%v-sign.apk",
        "Builds": [
            {
                "versionName": "1.4.8",
                "versionCode": 1004008,
                "commit": "a" * 40,
                "subdir": "android_module",
                "submodules": True,
                "gradle": True,
                "build": ["echo fixture"],
            }
        ],
        "AllowedAPKSigningKeys": SIGNING_KEY,
        "AutoUpdateMode": "Version v%v",
        "UpdateCheckMode": "HTTP",
        "UpdateCheckData": UPDATE_DATA,
        "CurrentVersion": "1.4.8",
        "CurrentVersionCode": 1004008,
    }


class FdroidReleaseMetadataTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        self.metadata = self.directory / "com.dobby.vpn.yml"
        self.baseline = self.directory / "baseline.yml"
        self.metadata.write_text(
            yaml.safe_dump(metadata_document(), sort_keys=False), encoding="utf-8"
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def prepare(self, version_name: str = "1.5.0", version_code: int = 1005000) -> str:
        output = io.StringIO()
        with redirect_stdout(output):
            prepare(
                self.metadata,
                self.baseline,
                version_name,
                version_code,
            )
        return output.getvalue().strip()

    def test_candidate_preserves_every_inherited_build_field(self) -> None:
        self.assertEqual(self.prepare(), "candidate")
        document = yaml.safe_load(self.metadata.read_text(encoding="utf-8"))
        inherited = deepcopy(document["Builds"][-1])
        inherited.update(
            versionName="1.5.0",
            versionCode=1005000,
            commit="v1.5.0",
            gradle=["yes"],
        )
        document["Builds"].append(inherited)
        document["CurrentVersion"] = "1.5.0"
        document["CurrentVersionCode"] = 1005000
        self.metadata.write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )

        finalize(
            self.metadata,
            self.baseline,
            "candidate",
            "1.5.0",
            1005000,
            SOURCE_SHA,
            "https://127.0.0.1:8765/DobbyVPN-v%v-sign.apk",
        )

        result = yaml.safe_load(self.metadata.read_text(encoding="utf-8"))
        target = result["Builds"][-1]
        self.assertTrue(target["submodules"])
        self.assertEqual(target["gradle"], ["yes"])
        self.assertEqual(target["subdir"], "android_module")
        self.assertEqual(target["build"], ["echo fixture"])
        self.assertEqual(target["commit"], SOURCE_SHA)
        self.assertEqual(result["Binaries"], "https://127.0.0.1:8765/DobbyVPN-v%v-sign.apk")
        self.assertEqual(result["UpdateCheckData"], UPDATE_DATA)

    def test_existing_recipe_can_be_bound_to_exact_release_source(self) -> None:
        document = metadata_document()
        document["Builds"][0]["versionName"] = "1.5.0"
        document["Builds"][0]["versionCode"] = 1005000
        document["CurrentVersion"] = "1.5.0"
        document["CurrentVersionCode"] = 1005000
        self.metadata.write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )
        self.assertEqual(self.prepare(), "existing")

        finalize(
            self.metadata,
            self.baseline,
            "existing",
            "1.5.0",
            1005000,
            SOURCE_SHA,
            "https://127.0.0.1:8765/DobbyVPN-v%v-sign.apk",
        )
        result = yaml.safe_load(self.metadata.read_text(encoding="utf-8"))
        self.assertEqual(result["Builds"][0]["commit"], SOURCE_SHA)
        self.assertEqual(result["CurrentVersionCode"], 1005000)

    def test_rejects_duplicate_version_code_with_different_name(self) -> None:
        document = metadata_document()
        document["Builds"][0]["versionName"] = "1.4.7"
        self.metadata.write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )
        with self.assertRaisesRegex(MetadataError, "different recipe"):
            self.prepare(version_name="1.5.0", version_code=1004008)

    def test_rejects_candidate_that_is_not_newer(self) -> None:
        with self.assertRaisesRegex(MetadataError, "newer"):
            self.prepare(version_name="1.5.0", version_code=1004007)

    def test_rejects_unexpected_existing_build_change(self) -> None:
        self.assertEqual(self.prepare(), "candidate")
        document = yaml.safe_load(self.metadata.read_text(encoding="utf-8"))
        document["Builds"][0]["submodules"] = False
        candidate = deepcopy(document["Builds"][0])
        candidate.update(versionName="1.5.0", versionCode=1005000, commit="v1.5.0")
        document["Builds"].append(candidate)
        self.metadata.write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )
        with self.assertRaisesRegex(MetadataError, "existing build recipe"):
            finalize(
                self.metadata,
                self.baseline,
                "candidate",
                "1.5.0",
                1005000,
                SOURCE_SHA,
                "https://127.0.0.1:8765/DobbyVPN-v%v-sign.apk",
            )

    def test_rejects_invalid_request_and_missing_signing_key(self) -> None:
        with self.assertRaisesRegex(MetadataError, "version name"):
            self.prepare(version_name="v1.5.0")
        document = metadata_document()
        del document["AllowedAPKSigningKeys"]
        self.metadata.write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )
        with self.assertRaisesRegex(MetadataError, "AllowedAPKSigningKeys"):
            self.prepare()

    def test_autoupdate_uses_fdroidserver_logic_with_fixed_release_values(self) -> None:
        calls: list[tuple[object, bool, bool]] = []
        checkupdates = types.SimpleNamespace()
        metadata = types.SimpleNamespace(
            parse_metadata=lambda _path: object(),
        )

        def checkupdates_app(app: object, *, auto: bool, make_commit: bool) -> None:
            calls.append((app, auto, make_commit))
            self.assertEqual(checkupdates.check_http(app), ("1.5.0", 1005000))
            self.assertIsNone(checkupdates.fetch_autoname(app, None))
            vcs = checkupdates.common.getvcs(None, None, None)
            self.assertIsNone(vcs.getref("v1.5.0"))

        checkupdates.checkupdates_app = checkupdates_app
        checkupdates.common = types.SimpleNamespace()
        package = types.ModuleType("fdroidserver")
        package.checkupdates = checkupdates
        package.metadata = metadata
        with mock.patch.dict(sys.modules, {"fdroidserver": package}):
            autoupdate(self.metadata, "1.5.0", 1005000)
        self.assertEqual(len(calls), 1)
        self.assertTrue(calls[0][1])
        self.assertFalse(calls[0][2])


if __name__ == "__main__":
    unittest.main()
