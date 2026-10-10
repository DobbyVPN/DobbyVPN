from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

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

    def test_resolve_downloads_result_for_exact_successful_main_build(self) -> None:
        repository = "DobbyVPN/DobbyVPN"
        run = {
            "id": 202, "run_number": 40, "repository": {"full_name": repository},
            "path": test_manifest.TEST_WORKFLOW_PATH + "@refs/heads/main",
            "event": "workflow_dispatch", "head_branch": "main", "head_sha": "a" * 40,
            "run_attempt": 1, "status": "completed", "conclusion": "success",
        }
        raw_result = json.dumps(self.result, sort_keys=True, separators=(",", ":")).encode() + b"\n"
        inventory = {
            test_manifest.RESULT_ARTIFACT: {
                "name": test_manifest.RESULT_ARTIFACT,
                "digest": "sha256:" + hashlib.sha256(raw_result).hexdigest(),
                "size_in_bytes": len(raw_result), "expired": False,
            },
        }

        def download(command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[bytes]:
            (Path(command[-1]) / "test-result.json").write_bytes(raw_result)
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as temporary:
            with (
                patch.object(test_manifest, "_json_command", return_value=run),
                patch.object(test_manifest, "_artifact_inventory", return_value=inventory),
                patch.object(test_manifest.subprocess, "run", side_effect=download),
            ):
                resolved = test_manifest.resolve_test(
                    repository, 202, self.build, Path(temporary) / "result",
                )

        self.assertEqual(resolved["run_id"], 202)
        self.assertEqual(resolved["run_number"], 40)
        self.assertEqual(resolved["build_run_id"], 101)
        self.assertEqual(resolved["source_sha"], self.build["source_sha"])
        self.assertEqual(resolved["manifest_sha256"], hashlib.sha256(raw_result).hexdigest())

    def test_resolve_rejects_wrong_run_source_and_failed_checks(self) -> None:
        repository = "DobbyVPN/DobbyVPN"
        run = {
            "id": 202, "run_number": 40, "repository": {"full_name": repository},
            "path": test_manifest.TEST_WORKFLOW_PATH + "@refs/heads/main",
            "event": "workflow_dispatch", "head_branch": "main", "head_sha": "a" * 40,
            "run_attempt": 1, "status": "completed", "conclusion": "success",
        }
        for field, value in (
            ("id", 203), ("repository", {"full_name": "someone/else"}),
            ("path", ".github/workflows/other.yml"), ("event", "push"),
            ("head_branch", "feature"), ("run_attempt", 2),
            ("conclusion", "failure"), ("head_sha", "c" * 40),
        ):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                with patch.object(test_manifest, "_json_command", return_value={**run, field: value}):
                    with self.assertRaises(test_manifest.TestManifestError):
                        test_manifest.resolve_test(
                            repository, 202, self.build, Path(temporary) / "result",
                        )

        failed_result = {
            **self.result,
            "checks": {**self.result["checks"], "qualify_linux": "failure"},
        }
        raw_result = json.dumps(failed_result, sort_keys=True, separators=(",", ":")).encode() + b"\n"
        inventory = {
            test_manifest.RESULT_ARTIFACT: {
                "name": test_manifest.RESULT_ARTIFACT,
                "digest": "sha256:" + hashlib.sha256(raw_result).hexdigest(),
                "size_in_bytes": len(raw_result), "expired": False,
            },
        }

        def download(command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[bytes]:
            (Path(command[-1]) / "test-result.json").write_bytes(raw_result)
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as temporary:
            with (
                patch.object(test_manifest, "_json_command", return_value=run),
                patch.object(test_manifest, "_artifact_inventory", return_value=inventory),
                patch.object(test_manifest.subprocess, "run", side_effect=download),
            ):
                with self.assertRaisesRegex(test_manifest.TestManifestError, "failed or skipped"):
                    test_manifest.resolve_test(
                        repository, 202, self.build, Path(temporary) / "result",
                    )


if __name__ == "__main__":
    unittest.main()
