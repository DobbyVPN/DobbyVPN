import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SOURCE = Path(__file__).with_name("public_output.py")
SPEC = importlib.util.spec_from_file_location("public_output", SOURCE)
assert SPEC and SPEC.loader
PUBLIC = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PUBLIC)


class PublicOutputTests(unittest.TestCase):
    def test_public_workflow_device_commands_use_private_capture_wrapper(self) -> None:
        workflow = SOURCE.parents[1] / "workflows" / "test.yml"
        source = workflow.read_text(encoding="utf-8")
        self.assertIn(".github/scripts/public_output.py", source)
        for command in (
            'xcrun simctl boot "$SIMULATOR_UDID"',
            'xcrun simctl bootstatus "$SIMULATOR_UDID" -b',
        ):
            command_line = next(line for line in source.splitlines() if command in line)
            line_number = source.splitlines().index(command_line)
            self.assertIn(
                "public_output.py",
                "\n".join(source.splitlines()[max(0, line_number - 2) : line_number + 1]),
            )

    def test_trusted_android_verifier_sparse_checkout_includes_output_helper(self) -> None:
        workflow = SOURCE.parents[1] / "workflows" / "android_build.yml"
        source = workflow.read_text(encoding="utf-8")
        self.assertIn(
            ".github/scripts/public_output.py\n"
            "            .github/scripts/verify_android_apk_source.py",
            source,
        )

    def test_torturer_workflow_has_no_public_diagnostic_artifact_or_private_input(self) -> None:
        workflow = SOURCE.parents[1] / "workflows" / "torturer.yml"
        source = workflow.read_text(encoding="utf-8")
        self.assertNotIn("upload-artifact", source)
        self.assertNotIn("gh api", source)
        self.assertNotIn("profile", source.lower())
        self.assertNotIn("credential", source.lower())

    def test_public_output_retains_complete_bytes_and_publishes_only_digest(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            diagnostics = io.StringIO()
            payload = b"private endpoint\xff\nprivate profile bytes\n"
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                    clear=False,
                ),
                mock.patch.object(PUBLIC.sys, "stderr", diagnostics),
            ):
                PUBLIC.emit_diagnostic("test-command", (payload,), root_dir=Path(temporary))
            self.assertNotIn("private endpoint", diagnostics.getvalue())
            self.assertNotIn("private profile", diagnostics.getvalue())
            self.assertIn("dobbyvpn_diagnostic kind=test-command", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            self.assertEqual(retained[0].read_bytes(), payload)
            self.assertEqual(retained[0].stat().st_mode & 0o777, 0o600)

    @unittest.skipIf(os.name == "nt", "POSIX symlink assertion")
    def test_public_output_rejects_symlinked_runner_temp(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "target"
            target.mkdir()
            link = root / "link"
            link.symlink_to(target, target_is_directory=True)
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": str(link)},
                    clear=False,
                ),
                self.assertRaisesRegex(RuntimeError, "symlink"),
            ):
                PUBLIC.emit_diagnostic("test-command", (b"diagnostic",), root_dir=root)

    def test_command_wrapper_retains_output_and_returns_status(self) -> None:
        completed = subprocess.CompletedProcess(
            ["fake"], 7, stdout=b"private stdout\n", stderr=b"private stderr\n"
        )
        with tempfile.TemporaryDirectory() as temporary:
            diagnostics = io.StringIO()
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                    clear=False,
                ),
                mock.patch.object(PUBLIC.subprocess, "run", return_value=completed),
                mock.patch.object(PUBLIC.sys, "argv", ["public_output.py", "--kind", "wrapper", "--", "fake"]),
                mock.patch.object(PUBLIC.sys, "stderr", diagnostics),
            ):
                self.assertEqual(PUBLIC.main(), 7)
            self.assertNotIn("private stdout", diagnostics.getvalue())
            self.assertNotIn("private stderr", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            self.assertIn(b"private stdout", retained[0].read_bytes())
            self.assertIn(b"private stderr", retained[0].read_bytes())

    def test_command_wrapper_fails_closed_when_retention_fails(self) -> None:
        completed = subprocess.CompletedProcess(["fake"], 1, stdout=b"raw", stderr=b"raw-error")
        diagnostics = io.StringIO()
        with (
            mock.patch.object(PUBLIC.subprocess, "run", return_value=completed),
            mock.patch.object(PUBLIC, "emit_diagnostic", side_effect=RuntimeError("unsafe path")),
            mock.patch.object(PUBLIC.sys, "argv", ["public_output.py", "--kind", "wrapper", "--", "fake"]),
            mock.patch.object(PUBLIC.sys, "stderr", diagnostics),
        ):
            self.assertEqual(PUBLIC.main(), PUBLIC.RETENTION_FAILURE_EXIT)
        self.assertIn("status=retention-failed", diagnostics.getvalue())
        self.assertNotIn("unsafe path", diagnostics.getvalue())


if __name__ == "__main__":
    unittest.main()
