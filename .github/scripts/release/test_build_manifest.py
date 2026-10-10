from __future__ import annotations

import unittest
from unittest.mock import patch

import build_manifest


class BuildManifestTests(unittest.TestCase):
    def test_create_binds_required_artifact_digests_to_source_and_run(self) -> None:
        source_sha = "a" * 40
        source_tree = "b" * 40
        inventory = {
            name: {"name": name, "digest": "sha256:" + "c" * 64, "size_in_bytes": 10, "expired": False}
            for name in build_manifest.required_artifacts("1.5.4")
        }
        with patch.object(build_manifest, "_current_build_run"), patch.object(build_manifest, "_artifact_inventory", return_value=inventory):
            manifest = build_manifest.create_manifest(
                repository="DobbyVPN/DobbyVPN", run_id=10, run_number=20,
                source_sha=source_sha, source_tree=source_tree, version="1.5.4",
                android_version_code=1_005_004, apple_build_number=1_005_024,
            )
        self.assertEqual(manifest["run_id"], 10)
        self.assertEqual(manifest["run_number"], 20)
        self.assertEqual(manifest["source_sha"], source_sha)
        self.assertEqual(manifest["source_tree"], source_tree)
        self.assertEqual(manifest["apple_build_number"], 1_005_024)
        self.assertEqual({item["name"] for item in manifest["artifacts"]}, set(inventory))

    def test_create_rejects_expired_or_missing_build_outputs(self) -> None:
        name = build_manifest.required_artifacts("1.5.4")[0]
        with patch.object(build_manifest, "_current_build_run"), patch.object(build_manifest, "_artifact_inventory", return_value={}):
            with self.assertRaisesRegex(build_manifest.BuildManifestError, name):
                build_manifest.create_manifest(
                    repository="DobbyVPN/DobbyVPN", run_id=10, run_number=20,
                    source_sha="a" * 40, source_tree="b" * 40, version="1.5.4",
                    android_version_code=1_005_004, apple_build_number=1_005_024,
                )

    def test_create_rejects_invalid_digest_and_identity(self) -> None:
        inventory = {
            name: {"name": name, "digest": "sha256:broken", "size_in_bytes": 10, "expired": False}
            for name in build_manifest.required_artifacts("1.5.4")
        }
        with patch.object(build_manifest, "_current_build_run"), patch.object(build_manifest, "_artifact_inventory", return_value=inventory):
            with self.assertRaises(build_manifest.BuildManifestError):
                build_manifest.create_manifest(
                    repository="DobbyVPN/DobbyVPN", run_id=10, run_number=20,
                    source_sha="a" * 40, source_tree="b" * 40, version="1.5.4",
                    android_version_code=1_005_004, apple_build_number=1_005_024,
                )
        with patch.object(build_manifest, "_current_build_run"), self.assertRaises(build_manifest.BuildManifestError):
            build_manifest.create_manifest(
                repository="DobbyVPN/DobbyVPN", run_id=10, run_number=20,
                source_sha="bad", source_tree="b" * 40, version="1.5.4",
                android_version_code=1_005_004, apple_build_number=1_005_024,
            )

    def test_required_artifact_names_are_unique(self) -> None:
        artifacts = build_manifest.required_artifacts("1.5.4")
        self.assertEqual(len(artifacts), len(set(artifacts)))


if __name__ == "__main__":
    unittest.main()
