from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
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

    def test_resolve_downloads_and_validates_the_selected_main_build(self) -> None:
        repository = "DobbyVPN/DobbyVPN"
        run = {
            "id": 10, "run_number": 20, "repository": {"full_name": repository},
            "path": build_manifest.BUILD_WORKFLOW_PATH + "@refs/heads/main",
            "event": "workflow_dispatch", "head_branch": "main", "head_sha": "a" * 40,
            "run_attempt": 1, "status": "completed", "conclusion": "success",
        }
        inventory = {
            name: {
                "name": name, "digest": "sha256:" + "c" * 64,
                "size_in_bytes": 10, "expired": False,
            }
            for name in build_manifest.required_artifacts("1.5.4")
        }
        manifest = {
            "schema": 1, "repository": repository,
            "workflow_path": build_manifest.BUILD_WORKFLOW_PATH,
            "run_id": 10, "run_number": 20, "source_sha": "a" * 40,
            "source_tree": "b" * 40, "version": "1.5.4",
            "android_version_code": 1_005_004, "apple_build_number": 1_005_024,
            "artifacts": [
                {"name": name, "sha256": "c" * 64, "size": 10}
                for name in sorted(inventory)
            ],
        }
        raw_manifest = json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode() + b"\n"

        def download(command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[bytes]:
            (Path(command[-1]) / "build.json").write_bytes(raw_manifest)
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / "manifest"
            with (
                patch.object(build_manifest, "_json_command", return_value=run),
                patch.object(build_manifest, "_artifact_inventory", return_value=inventory),
                patch.object(build_manifest.subprocess, "run", side_effect=download),
            ):
                resolved = build_manifest.resolve_build(
                    repository, 10, destination, require_main=True,
                )

        self.assertEqual(resolved["source_sha"], run["head_sha"])
        self.assertEqual(resolved["run_id"], run["id"])
        self.assertEqual(resolved["manifest_sha256"], hashlib.sha256(raw_manifest).hexdigest())
        self.assertEqual({item["name"] for item in resolved["artifacts"]}, set(inventory))

    def test_resolve_rejects_wrong_build_identity_and_changed_artifacts(self) -> None:
        repository = "DobbyVPN/DobbyVPN"
        run = {
            "id": 10, "run_number": 20, "repository": {"full_name": repository},
            "path": build_manifest.BUILD_WORKFLOW_PATH + "@refs/heads/main",
            "event": "workflow_dispatch", "head_branch": "main", "head_sha": "a" * 40,
            "run_attempt": 1, "status": "completed", "conclusion": "success",
        }
        for field, value in (
            ("id", 11), ("repository", {"full_name": "someone/else"}),
            ("path", ".github/workflows/other.yml"), ("event", "push"),
            ("head_branch", "feature"), ("run_attempt", 2),
            ("conclusion", "failure"), ("head_sha", "invalid"),
        ):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                with patch.object(build_manifest, "_json_command", return_value={**run, field: value}):
                    with self.assertRaises(build_manifest.BuildManifestError):
                        build_manifest.resolve_build(
                            repository, 10, Path(temporary) / "manifest", require_main=True,
                        )

        inventory = {
            name: {
                "name": name, "digest": "sha256:" + "c" * 64,
                "size_in_bytes": 10, "expired": False,
            }
            for name in build_manifest.required_artifacts("1.5.4")
        }
        name = next(iter(inventory))
        inventory[name] = {**inventory[name], "digest": "sha256:" + "d" * 64}
        manifest = {
            "schema": 1, "repository": repository,
            "workflow_path": build_manifest.BUILD_WORKFLOW_PATH,
            "run_id": 10, "run_number": 20, "source_sha": "a" * 40,
            "source_tree": "b" * 40, "version": "1.5.4",
            "android_version_code": 1_005_004, "apple_build_number": 1_005_024,
            "artifacts": [
                {"name": artifact, "sha256": "c" * 64, "size": 10}
                for artifact in sorted(inventory)
            ],
        }
        raw_manifest = json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode() + b"\n"

        def download(command: list[str], **_kwargs: object) -> subprocess.CompletedProcess[bytes]:
            (Path(command[-1]) / "build.json").write_bytes(raw_manifest)
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as temporary:
            with (
                patch.object(build_manifest, "_json_command", return_value=run),
                patch.object(build_manifest, "_artifact_inventory", return_value=inventory),
                patch.object(build_manifest.subprocess, "run", side_effect=download),
            ):
                with self.assertRaisesRegex(build_manifest.BuildManifestError, "changed since its manifest"):
                    build_manifest.resolve_build(
                        repository, 10, Path(temporary) / "manifest", require_main=True,
                    )


if __name__ == "__main__":
    unittest.main()
