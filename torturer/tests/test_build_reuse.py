from __future__ import annotations

import copy
from pathlib import Path
import tempfile
import unittest

from torturer_runner.build_reuse import file_sha256, production_inputs, validate_reuse_manifest


class BuildReuseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.run = Path(self.temporary.name)
        self.source = self.run / "source"
        (self.source / "core").mkdir(parents=True)
        (self.source / "core/session.go").write_text("package core\n")
        self.package = self.run / "reuse/app.msi"
        self.package.parent.mkdir()
        self.package.write_bytes(b"identified package\x00")
        self.manifest = {
            "schema": 1, "mode": "local-reuse", "platform": "windows",
            "app_sha": "a" * 40, "test_sha": "b" * 40,
            "production_inputs": production_inputs(self.source),
            "artifacts": [{"role": "package", "architecture": "amd64",
                           "relative_path": "reuse/app.msi", "file_name": "app.msi",
                           "sha256": file_sha256(self.package)}],
        }

    def validate(self, manifest=None):
        return validate_reuse_manifest(self.source, self.run, manifest or self.manifest,
                                       "windows", "amd64")

    def test_test_changes_reuse_the_identified_application(self):
        (self.source / "torturer").mkdir()
        (self.source / "torturer/helper.py").write_text("new test implementation")
        (self.source / "core/session_test.go").write_text("new behavior tests")
        self.assertEqual(self.validate(), self.package)

    def test_worktree_git_file_and_checkout_git_directory_have_the_same_identity(self):
        (self.source / ".git").write_text("gitdir: /owner/private/worktrees/candidate\n")
        self.assertEqual(self.validate(), self.package)

        (self.source / ".git").unlink()
        (self.source / ".git").mkdir()
        (self.source / ".git" / "HEAD").write_text("another checkout identity\n")
        self.assertEqual(self.validate(), self.package)

    def test_optional_mutation_and_source_check_scripts_are_test_inputs(self):
        for name in ("source_checks.py", "check_production_mutations.py", "test_checks.py"):
            path = self.source / ".github" / "scripts" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("updated test implementation")
        self.assertEqual(self.validate(), self.package)

    def test_local_tools_and_generated_staging_do_not_change_package_inputs(self):
        for name in (
            ".local-tools/go/VERSION", ".venv/pyvenv.cfg", "output/app.msi",
            ".pytest_cache/v/cache/nodeids",
            "runtime/services/libdobby_bridge.so", "core/runtime/result.json",
            "go_module/libc++.so.1", "android_module/generated.kt",
            "ui/android/.kotlin/state.bin", "core/libdobby_bridge.so",
            "core/libc++.so.1", "core/libc++abi.so.1",
        ):
            path = self.source / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"disposable local build state")
        self.assertEqual(self.validate(), self.package)

    def test_changed_production_code_is_rejected(self):
        (self.source / "core/session.go").write_text("changed production behavior")
        with self.assertRaisesRegex(ValueError, "production inputs changed"):
            self.validate()

    def test_changed_package_bytes_are_rejected(self):
        self.package.write_bytes(b"different package")
        with self.assertRaisesRegex(ValueError, "hash mismatch"):
            self.validate()

    def test_wrong_guest_platform_and_architecture_are_rejected(self):
        for field, value in (("platform", "macos"), ("architecture", "arm64")):
            with self.subTest(field=field):
                manifest = copy.deepcopy(self.manifest)
                target = manifest if field == "platform" else manifest["artifacts"][0]
                target[field] = value
                with self.assertRaisesRegex(ValueError, field):
                    self.validate(manifest)

    def test_escaping_path_and_symlink_are_rejected(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["artifacts"][0]["relative_path"] = "../app.msi"
        with self.assertRaisesRegex(ValueError, "path"):
            self.validate(manifest)
        self.package.unlink()
        target = self.run / "other.msi"
        target.write_bytes(b"identified package\x00")
        self.package.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.validate()


if __name__ == "__main__":
    unittest.main()
