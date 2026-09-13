import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("verify_android_apk_source.py")
SPEC = importlib.util.spec_from_file_location("verify_android_apk_source", SCRIPT)
assert SPEC and SPEC.loader
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class VerifyAndroidApkSourceTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.repo = "DobbyVPN/DobbyVPN"

    def test_bounded_capture_keeps_inherited_cwd_and_environment(self):
        process = mock.Mock(pid=123, returncode=0)
        process.communicate.return_value = (b"analyzer output", b"")
        with mock.patch.object(VERIFY.subprocess, "Popen", return_value=process) as popen:
            result = VERIFY.run_bounded_capture(["apkanalyzer"], timeout_seconds=7)

        self.assertEqual(result.stdout, "analyzer output")
        self.assertEqual(popen.call_args.args, (["apkanalyzer"],))
        self.assertNotIn("cwd", popen.call_args.kwargs)
        self.assertNotIn("env", popen.call_args.kwargs)
        process.communicate.assert_called_once_with(timeout=7)

    def code(self, sha=None, link=None):
        sha = self.sha if sha is None else sha
        link = f"https://github.com/{self.repo}/tree/{self.sha}" if link is None else link
        return (
            '.class public final Lcom/dobby/vpn/BuildConfig;\n'
            f'.field public static final PROJECT_REPOSITORY_COMMIT:Ljava/lang/String; = "{sha}"\n'
            f'.field public static final PROJECT_REPOSITORY_COMMIT_LINK:Ljava/lang/String; = "{link}"\n'
        )

    def testCommitAndLinkPass(self):
        VERIFY.verify_code(self.code(), self.sha, self.repo)

    def testRejectsMissingOrWrongCommit(self):
        for wrong in ("N/A", "b" * 40):
            with self.subTest(wrong=wrong), self.assertRaises(VERIFY.VerificationError):
                VERIFY.verify_code(self.code(sha=wrong), self.sha, self.repo)

    def testRejectsWrongLinkOrDuplicateField(self):
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_code(self.code(link="https://example.invalid/source"), self.sha, self.repo)
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_code(self.code() + self.code(), self.sha, self.repo)

    def test_apkanalyzer_failure_preserves_combined_child_output(self):
        completed = subprocess.CompletedProcess(
            ["apkanalyzer"], 9, stdout="apkanalyzer stdout\n", stderr="apkanalyzer stderr\n",
        )
        diagnostics = io.StringIO()
        with tempfile.NamedTemporaryFile() as apk:
            apk.write(b"apk")
            apk.flush()
            with (
                mock.patch.object(VERIFY, "run_apkanalyzer", return_value=completed),
                mock.patch.object(VERIFY.sys, "stderr", diagnostics),
            ):
                with self.assertRaisesRegex(VERIFY.VerificationError, "exit code 9"):
                    VERIFY.verify_apk("apkanalyzer", Path(apk.name), self.sha, self.repo)
        self.assertIn("apkanalyzer stdout\n", diagnostics.getvalue())
        self.assertIn("apkanalyzer stderr\n", diagnostics.getvalue())

    def test_apkanalyzer_timeout_preserves_partial_child_output(self):
        timeout = subprocess.TimeoutExpired(
            "apkanalyzer", 120, output="partial analyzer output\n", stderr="partial analyzer stderr\n",
        )
        diagnostics = io.StringIO()
        with tempfile.NamedTemporaryFile() as apk:
            apk.write(b"apk")
            apk.flush()
            with (
                mock.patch.object(VERIFY, "run_apkanalyzer", side_effect=timeout),
                mock.patch.object(VERIFY.sys, "stderr", diagnostics),
            ):
                with self.assertRaisesRegex(VERIFY.VerificationError, "timed out"):
                    VERIFY.verify_apk("apkanalyzer", Path(apk.name), self.sha, self.repo)
        self.assertIn("partial analyzer output\n", diagnostics.getvalue())
        self.assertIn("partial analyzer stderr\n", diagnostics.getvalue())

    def test_apkanalyzer_failure_prints_complete_output_in_actions(self):
        completed = subprocess.CompletedProcess(
            ["apkanalyzer"], 9, stdout="private apk path\n", stderr="private analyzer endpoint\n",
        )
        with tempfile.TemporaryDirectory() as temporary:
            diagnostics = io.StringIO()
            apk = Path(temporary) / "test.apk"
            apk.write_bytes(b"apk")
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                    clear=False,
                ),
                mock.patch.object(VERIFY, "run_apkanalyzer", return_value=completed),
                mock.patch.object(VERIFY.sys, "stderr", diagnostics),
            ):
                with self.assertRaisesRegex(VERIFY.VerificationError, "exit code 9"):
                    VERIFY.verify_apk("apkanalyzer", apk, self.sha, self.repo)
            self.assertIn("private apk path", diagnostics.getvalue())
            self.assertIn("private analyzer endpoint", diagnostics.getvalue())

    def test_apkanalyzer_success_parses_stdout_without_stderr_warning(self):
        completed = subprocess.CompletedProcess(
            ["apkanalyzer"], 0, stdout=self.code(), stderr="apkanalyzer warning\n",
        )
        with tempfile.NamedTemporaryFile() as apk:
            apk.write(b"apk")
            apk.flush()
            with mock.patch.object(VERIFY, "run_apkanalyzer", return_value=completed):
                VERIFY.verify_apk("apkanalyzer", Path(apk.name), self.sha, self.repo)

    def test_companion_manifest_parses_multiline_exact_source_metadata(self):
        manifest = f'''<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
  <application>
    <meta-data
      android:name="com.dobby.test.source_sha"
      android:value="{self.sha}" />
  </application>
</manifest>'''
        results = [
            subprocess.CompletedProcess(["apkanalyzer"], 0, stdout="com.dobby.vpn.test\n", stderr=""),
            subprocess.CompletedProcess(["apkanalyzer"], 0, stdout=manifest, stderr=""),
        ]
        with tempfile.NamedTemporaryFile() as apk:
            apk.write(b"companion")
            apk.flush()
            with mock.patch.object(VERIFY, "run_apkanalyzer", side_effect=results):
                VERIFY.verify_test_companion("apkanalyzer", Path(apk.name), self.sha)

    def test_companion_manifest_rejects_wrong_or_duplicate_source_metadata(self):
        for values in (("b" * 40,), (self.sha, self.sha)):
            metadata = "".join(
                '<meta-data android:name="com.dobby.test.source_sha" '
                f'android:value="{value}" />'
                for value in values
            )
            manifest = (
                '<manifest xmlns:android="http://schemas.android.com/apk/res/android">'
                f"<application>{metadata}</application></manifest>"
            )
            results = [
                subprocess.CompletedProcess(["apkanalyzer"], 0, stdout="com.dobby.vpn.test\n", stderr=""),
                subprocess.CompletedProcess(["apkanalyzer"], 0, stdout=manifest, stderr=""),
            ]
            with self.subTest(values=values), tempfile.NamedTemporaryFile() as apk:
                apk.write(b"companion")
                apk.flush()
                with mock.patch.object(VERIFY, "run_apkanalyzer", side_effect=results):
                    with self.assertRaisesRegex(VERIFY.VerificationError, "source identity"):
                        VERIFY.verify_test_companion("apkanalyzer", Path(apk.name), self.sha)
