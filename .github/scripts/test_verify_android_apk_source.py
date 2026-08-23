import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
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

    @unittest.skipIf(os.name == "nt", "POSIX process identity assertion")
    def test_process_state_change_is_not_pid_reuse(self):
        with mock.patch.object(VERIFY, "_proc_identity", return_value=("S", "123")):
            self.assertTrue(VERIFY._pid_is_alive(123, ("R", "123")))
        with mock.patch.object(VERIFY, "_proc_identity", return_value=("S", "456")):
            self.assertFalse(VERIFY._pid_is_alive(123, ("R", "123")))

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

    def test_public_apkanalyzer_failure_retains_raw_output_without_public_echo(self):
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
            self.assertNotIn("private apk path", diagnostics.getvalue())
            self.assertNotIn("private analyzer endpoint", diagnostics.getvalue())
            self.assertIn("dobbyvpn_diagnostic kind=android-apk-source", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            retained_bytes = retained[0].read_bytes()
            self.assertIn(b"private apk path\n", retained_bytes)
            self.assertIn(b"private analyzer endpoint\n", retained_bytes)

    def test_apkanalyzer_success_parses_stdout_without_stderr_warning(self):
        completed = subprocess.CompletedProcess(
            ["apkanalyzer"], 0, stdout=self.code(), stderr="apkanalyzer warning\n",
        )
        with tempfile.NamedTemporaryFile() as apk:
            apk.write(b"apk")
            apk.flush()
            with mock.patch.object(VERIFY, "run_apkanalyzer", return_value=completed):
                VERIFY.verify_apk("apkanalyzer", Path(apk.name), self.sha, self.repo)

    def test_analyzer_diagnostics_are_unique_and_non_overwriting(self):
        with tempfile.TemporaryDirectory() as temporary:
            with mock.patch.object(VERIFY, "ROOT_DIR", Path(temporary)):
                first = VERIFY.retain_process_diagnostics(
                    "first stdout\n", "first stderr\n", "exit-1",
                )
                second = VERIFY.retain_process_diagnostics(
                    "second stdout\n", "second stderr\n", "exit-2",
                )
            self.assertNotEqual(first, second)
            self.assertIn("first stderr\n", first.read_text(encoding="utf-8"))
            self.assertIn("second stderr\n", second.read_text(encoding="utf-8"))

    def test_analyzer_diagnostics_preserve_raw_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            with mock.patch.object(VERIFY, "ROOT_DIR", Path(temporary)):
                retained = VERIFY.retain_process_diagnostics(
                    b"stdout-\xff\n", b"stderr-\xfe\n", "exit-1",
                )
            retained_bytes = retained.read_bytes()
        self.assertIn(b"stdout-\xff\n", retained_bytes)
        self.assertIn(b"stderr-\xfe\n", retained_bytes)

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_process_group_census_read_error_fails_closed(self):
        class BrokenStatPath:
            def read_text(self, **kwargs):
                raise OSError("permission denied")

            def __str__(self):
                return "/proc/12345/stat"

        with mock.patch.object(
            VERIFY.Path, "glob", return_value=[BrokenStatPath()]
        ):
            with self.assertRaisesRegex(VERIFY.ProcessTreeProofError, "permission denied"):
                VERIFY._active_proc_group_members(12345)

    @unittest.skipIf(os.name == "nt", "POSIX process identity assertion")
    def test_process_identity_read_error_fails_closed(self):
        with mock.patch.object(
            VERIFY.Path,
            "read_text",
            side_effect=OSError("permission denied"),
        ):
            with self.assertRaisesRegex(VERIFY.ProcessTreeProofError, "permission denied"):
                VERIFY._proc_identity(12345)

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_sigterm_resistant_analyzer_descendant_is_killed(self):
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        with tempfile.TemporaryDirectory(prefix="android-source-resistant-child-") as temporary:
            root = Path(temporary)
            child_stdout = root / "child.stdout.raw.log"
            child_stderr = root / "child.stderr.raw.log"
            parent_code = (
                "import os,signal,subprocess,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                f"child_stdout=os.fdopen(os.open({str(child_stdout)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child_stderr=os.fdopen(os.open({str(child_stderr)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}], stdout=child_stdout, stderr=child_stderr); "
                "print(child.pid, flush=True); time.sleep(60)"
            )
            process = subprocess.Popen(
                [sys.executable, "-c", parent_code],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                start_new_session=True,
            )
            process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
            try:
                child_pid = int(process.stdout.readline().strip())
                VERIFY.terminate_process_group(process, grace_seconds=0.1)
                self.assertIsNotNone(process.poll())
                for _ in range(30):
                    if not VERIFY._pid_is_alive(child_pid):
                        break
                    time.sleep(0.05)
                else:
                    self.fail("SIGTERM-resistant analyzer descendant survived cleanup")
                self.assertEqual(child_stdout.stat().st_mode & 0o777, 0o600)
                self.assertEqual(child_stderr.stat().st_mode & 0o777, 0o600)
            finally:
                if process.poll() is None:
                    VERIFY.terminate_process_group(process, grace_seconds=0.1)
                process.stdout.close()
                process.stderr.close()

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_analyzer_timeout_retains_streams_and_kills_descendants(self):
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        parent_code = (
            "import signal,subprocess,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}]); "
            "print('childpid='+str(child.pid), flush=True); "
            "print('analyzer stderr', file=sys.stderr, flush=True); time.sleep(60)"
        )
        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(VERIFY, "ROOT_DIR", Path(temporary)),
                mock.patch.object(VERIFY, "APK_ANALYZER_TIMEOUT_SECONDS", 0.1),
                mock.patch.object(VERIFY, "PROCESS_CLEANUP_GRACE_SECONDS", 0.1),
            ):
                with self.assertRaises(subprocess.TimeoutExpired) as raised:
                    VERIFY.run_apkanalyzer([sys.executable, "-c", parent_code])
            output = raised.exception.stdout or raised.exception.output or ""
            child_pid = int(VERIFY.output_text(output).split("childpid=", 1)[1].splitlines()[0])
            logs = list((Path(temporary) / "runtime" / "android-apk-source-diagnostics").glob("apkanalyzer-*.log"))
            self.assertEqual(len(logs), 1)
            log_text = logs[0].read_text(encoding="utf-8")
            self.assertIn("childpid=", log_text)
            self.assertIn("analyzer stderr", log_text)
            for _ in range(30):
                if not VERIFY._pid_is_alive(child_pid):
                    break
                time.sleep(0.05)
            else:
                self.fail("timed-out analyzer descendant survived cleanup")

    @unittest.skipIf(os.name == "nt", "POSIX detached-process assertion")
    def test_zero_exit_leader_detached_resistant_descendant_is_proven_gone(self):
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child_stdout = root / "child.stdout.raw.log"
            child_stderr = root / "child.stderr.raw.log"
            parent_code = (
                "import os,subprocess,sys,time; "
                f"child_stdout=os.fdopen(os.open({str(child_stdout)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child_stderr=os.fdopen(os.open({str(child_stderr)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}], "
                "start_new_session=True, stdout=child_stdout, stderr=child_stderr); "
                "childstat=open('/proc/%s/stat' % child.pid).read(); "
                "print('childpid='+str(child.pid), flush=True); "
                "print('childstart='+childstat[childstat.rfind(')')+2:].split()[19], flush=True); time.sleep(1.5)"
            )
            with mock.patch.object(VERIFY, "ROOT_DIR", root):
                with mock.patch.object(VERIFY, "APK_ANALYZER_TIMEOUT_SECONDS", 2):
                    result = VERIFY.run_apkanalyzer([sys.executable, "-c", parent_code])
            child_output = result.stdout
            child_pid = int(child_output.split("childpid=", 1)[1].splitlines()[0])
            child_start = child_output.split("childstart=", 1)[1].splitlines()[0]
            logs = list((Path(temporary) / "runtime" / "android-apk-source-diagnostics").glob("apkanalyzer-*.log"))
            self.assertEqual(len(logs), 1)
            self.assertIn(b"tree=gone", logs[0].read_bytes())
            for _ in range(30):
                identity = VERIFY._proc_identity(child_pid)
                if identity is None or identity[1] != child_start or identity[0] == "Z":
                    break
                time.sleep(0.05)
            else:
                self.fail("zero-exit detached analyzer descendant survived process-tree cleanup")
            self.assertEqual(child_stdout.stat().st_mode & 0o777, 0o600)
            self.assertEqual(child_stderr.stat().st_mode & 0o777, 0o600)

if __name__ == "__main__":
    unittest.main()
