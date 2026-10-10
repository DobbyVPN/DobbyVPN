from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

import release_provenance


class PublishedProvenanceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.metadata = {
            "tag": "v1.5.4", "version": "1.5.4", "source_sha": "a" * 40,
            "build_run_id": 101, "build_run_number": 50, "test_run_id": 202, "test_run_number": 40,
            "publish_run_id": 303, "publish_run_number": 60,
            "android_version_code": 1_005_004, "apple_build_number": 1_005_054,
            "build_manifest_sha256": "b" * 64, "test_result_sha256": "c" * 64,
            "release_notes_sha256": "d" * 64,
        }
        self.assets = ["dobbyVPN-linux.deb"]

    def test_create_and_verify_bind_assets_and_all_run_identities(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / self.assets[0]).write_bytes(b"package bytes")
            manifest = release_provenance.create_published_manifest(
                directory, **self.metadata, assets=self.assets,
            )
            payload = json.loads(manifest.read_text(encoding="utf-8"))
            self.assertEqual(payload["schema"], 2)
            self.assertEqual(payload["build_run_id"], 101)
            self.assertEqual(payload["test_run_id"], 202)
            self.assertEqual(payload["publish_run_id"], 303)
            release_provenance.verify_published_manifest(
                directory, **self.metadata, assets=self.assets,
            )

    def test_verify_rejects_changed_lineage_notes_or_asset_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            asset = directory / self.assets[0]
            asset.write_bytes(b"package bytes")
            release_provenance.create_published_manifest(directory, **self.metadata, assets=self.assets)
            for field, value in (("test_run_id", 999), ("release_notes_sha256", "e" * 64)):
                with self.subTest(field=field):
                    changed = {**self.metadata, field: value}
                    with self.assertRaises(release_provenance.ProvenanceError):
                        release_provenance.verify_published_manifest(
                            directory, **changed, assets=self.assets,
                        )
            asset.write_bytes(b"modified package")
            with self.assertRaisesRegex(release_provenance.ProvenanceError, "digest"):
                release_provenance.verify_published_manifest(
                    directory, **self.metadata, assets=self.assets,
                )

    def test_verify_rejects_schema_or_asset_allowlist_changes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / self.assets[0]).write_bytes(b"package bytes")
            manifest = release_provenance.create_published_manifest(
                directory, **self.metadata, assets=self.assets,
            )
            payload = json.loads(manifest.read_text(encoding="utf-8"))
            payload["schema"] = 1
            manifest.write_text(json.dumps(payload), encoding="utf-8")
            with self.assertRaisesRegex(release_provenance.ProvenanceError, "schema"):
                release_provenance.verify_published_manifest(
                    directory, **self.metadata, assets=self.assets,
                )


if __name__ == "__main__":
    unittest.main()
