from __future__ import annotations

import unittest

import test_manifest


class TestManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.build = {
            "run_id": 101,
            "source_sha": "a" * 40,
            "manifest_sha256": "b" * 64,
        }
        self.result = {
            "schema": 1,
            "repository": "DobbyVPN/DobbyVPN",
            "workflow_path": test_manifest.TEST_WORKFLOW_PATH,
            "run_id": 202,
            "build_run_id": 101,
            "source_sha": "a" * 40,
            "build_manifest_sha256": "b" * 64,
            "checks": {name: "success" for name in test_manifest.REQUIRED_CHECKS},
        }

    def test_accepts_complete_result_for_exact_build_identity(self) -> None:
        test_manifest.validate_result(
            self.result, repository="DobbyVPN/DobbyVPN", run_id=202,
            build=self.build, source_sha="a" * 40,
        )

    def test_rejects_result_from_another_build_or_source(self) -> None:
        for field, value in (("build_run_id", 100), ("source_sha", "c" * 40),
                             ("build_manifest_sha256", "d" * 64)):
            with self.subTest(field=field):
                result = {**self.result, field: value}
                with self.assertRaises(test_manifest.TestManifestError):
                    test_manifest.validate_result(
                        result, repository="DobbyVPN/DobbyVPN", run_id=202,
                        build=self.build, source_sha="a" * 40,
                    )

    def test_rejects_missing_failed_or_skipped_checks(self) -> None:
        for checks in (
            {**self.result["checks"], "qualify_android": "failure"},
            {key: value for key, value in self.result["checks"].items() if key != "render_cleanup"},
            {**self.result["checks"], "qualify_android": "skipped"},
        ):
            with self.subTest(checks=checks):
                with self.assertRaises(test_manifest.TestManifestError):
                    test_manifest.validate_result(
                        {**self.result, "checks": checks}, repository="DobbyVPN/DobbyVPN",
                        run_id=202, build=self.build, source_sha="a" * 40,
                    )


if __name__ == "__main__":
    unittest.main()
