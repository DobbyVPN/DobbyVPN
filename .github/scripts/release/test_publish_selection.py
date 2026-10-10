from __future__ import annotations

import unittest

import publish_selection


class PublishSelectionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.run = {
            "id": 303, "run_number": 60, "repository": {"full_name": "DobbyVPN/DobbyVPN"},
            "path": ".github/workflows/publish.yml@refs/heads/main", "head_branch": "main",
            "event": "workflow_dispatch", "status": "completed", "conclusion": "success", "run_attempt": 1,
        }
        self.build = {
            "run_id": 101, "run_number": 50, "source_sha": "a" * 40,
            "version": "1.5.4", "android_version_code": 1_005_004,
            "apple_build_number": 1_005_054, "manifest_sha256": "b" * 64,
        }
        self.test = {
            "run_id": 202, "run_number": 40, "build_run_id": 101, "source_sha": "a" * 40,
            "manifest_sha256": "c" * 64,
        }
        names = publish_selection.expected_assets("1.5.4")
        self.payload = {
            "schema": 2, "tag": "v1.5.4", "version": "1.5.4", "source_sha": "a" * 40,
            "build_run_id": 101, "build_run_number": 50, "test_run_id": 202, "test_run_number": 40,
            "publish_run_id": 303, "publish_run_number": 60,
            "android_version_code": 1_005_004, "apple_build_number": 1_005_054,
            "build_manifest_sha256": "b" * 64, "test_result_sha256": "c" * 64,
            "release_notes_sha256": "d" * 64,
            "assets": [{"name": name, "sha256": "e" * 64, "size": 1} for name in names],
        }

    def test_accepts_successful_publish_manifest_for_exact_build_and_test(self) -> None:
        self.assertEqual(
            publish_selection.validate_publish_run(self.run, repository="DobbyVPN/DobbyVPN", run_id=303),
            self.run,
        )
        publish_selection.validate_publish_manifest(
            self.payload, repository="DobbyVPN/DobbyVPN", publish_run=self.run,
            build=self.build, test=self.test,
        )

    def test_rejects_non_successful_or_wrong_publish_identity(self) -> None:
        for field, value in (("conclusion", "failure"), ("run_attempt", 0), ("head_branch", "feature")):
            with self.subTest(field=field):
                with self.assertRaises(publish_selection.PublishSelectionError):
                    publish_selection.validate_publish_run(
                        {**self.run, field: value}, repository="DobbyVPN/DobbyVPN", run_id=303,
                    )

    def test_rejects_build_test_or_asset_identity_mismatch(self) -> None:
        variants = (
            ({**self.payload, "build_run_id": 999}, self.build, self.test),
            ({**self.payload, "test_run_id": 999}, self.build, self.test),
            ({**self.payload, "build_manifest_sha256": "f" * 64}, self.build, self.test),
            ({**self.payload, "assets": self.payload["assets"][:-1]}, self.build, self.test),
        )
        for payload, build, test in variants:
            with self.subTest(payload=payload):
                with self.assertRaises(publish_selection.PublishSelectionError):
                    publish_selection.validate_publish_manifest(
                        payload, repository="DobbyVPN/DobbyVPN", publish_run=self.run,
                        build=build, test=test,
                    )


if __name__ == "__main__":
    unittest.main()
