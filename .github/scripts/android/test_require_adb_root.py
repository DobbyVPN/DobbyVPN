#!/usr/bin/env python3
"""Focused tests for the bounded hosted Android root handshake."""

from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


SCRIPT = Path(__file__).with_name("require_adb_root.sh")
KNOWN_DISCONNECT = b"adb: unable to connect for root: closed\n"
FAKE_ADB = textwrap.dedent(
    """\
    #!/usr/bin/env python3
    import base64
    import json
    import os
    from pathlib import Path
    import sys

    calls_path = Path(os.environ["FAKE_ADB_CALLS"])
    calls = calls_path.read_text(encoding="utf-8").splitlines() if calls_path.exists() else []
    index = len(calls)
    args = sys.argv[1:]
    with calls_path.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(args) + "\\n")
    plan = json.loads(Path(os.environ["FAKE_ADB_PLAN"]).read_text(encoding="utf-8"))
    if index >= len(plan) or plan[index]["args"] != args:
        os.write(2, b"unexpected fake adb invocation\\n")
        raise SystemExit(97)
    response = plan[index]
    sys.stdout.buffer.write(base64.b64decode(response["stdout"]))
    sys.stderr.buffer.write(base64.b64decode(response["stderr"]))
    raise SystemExit(response["status"])
    """
)


class RequireAdbRootTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.run_dir = self.root / "run"
        self.run_dir.mkdir()
        self.fake_bin = self.root / "bin"
        self.fake_bin.mkdir()
        fake_adb = self.fake_bin / "adb"
        fake_adb.write_text(FAKE_ADB, encoding="utf-8")
        fake_adb.chmod(0o755)
        self.plan = self.root / "plan.json"
        self.calls = self.root / "calls.jsonl"

    def tearDown(self) -> None:
        self.temporary.cleanup()

    @staticmethod
    def reply(args: list[str], status: int = 0, stdout: bytes = b"", stderr: bytes = b"") -> dict[str, object]:
        return {
            "args": args,
            "status": status,
            "stdout": base64.b64encode(stdout).decode("ascii"),
            "stderr": base64.b64encode(stderr).decode("ascii"),
        }

    def run_helper(self, plan: list[dict[str, object]]) -> subprocess.CompletedProcess[bytes]:
        self.plan.write_text(json.dumps(plan), encoding="utf-8")
        environment = os.environ.copy()
        environment.update({
            "PATH": f"{self.fake_bin}{os.pathsep}{environment['PATH']}",
            "FAKE_ADB_PLAN": str(self.plan),
            "FAKE_ADB_CALLS": str(self.calls),
        })
        return subprocess.run(
            ["bash", str(SCRIPT), str(self.run_dir)],
            cwd=SCRIPT.parents[3],
            env=environment,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
            check=False,
        )

    def calls_made(self) -> list[list[str]]:
        if not self.calls.exists():
            return []
        return [json.loads(line) for line in self.calls.read_text(encoding="utf-8").splitlines()]

    def assert_log(self, label: str, stdout: bytes, stderr: bytes) -> None:
        self.assertEqual((self.run_dir / f"{label}.stdout.log").read_bytes(), stdout)
        self.assertEqual((self.run_dir / f"{label}.stderr.log").read_bytes(), stderr)

    def test_normal_root_requires_uid_zero_and_forwards_binary_bytes(self) -> None:
        root_out, root_err = b"root\x00\xff\n", b"warning\x00\xfe\n"
        wait_out, wait_err = b"wait\x00\x80", b"wait err\x00\xff"
        uid_out, uid_err = b"0\n", b"probe err\x00\xfd"
        result = self.run_helper([
            self.reply(["root"], stdout=root_out, stderr=root_err),
            self.reply(["wait-for-device"], stdout=wait_out, stderr=wait_err),
            self.reply(["shell", "id", "-u"], stdout=uid_out, stderr=uid_err),
        ])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [["root"], ["wait-for-device"], ["shell", "id", "-u"]])
        self.assert_log("android-adb-root-1", root_out, root_err)
        self.assert_log("android-adb-root-wait-1", wait_out, wait_err)
        self.assert_log("android-adb-root-uid-1", uid_out, uid_err)
        for payload in (root_out, wait_out, uid_out):
            self.assertIn(payload, result.stdout)
        for payload in (root_err, wait_err, uid_err):
            self.assertIn(payload, result.stderr)

    def test_known_disconnect_and_uid_zero_does_not_repeat_root(self) -> None:
        result = self.run_helper([
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"0\r\n"),
        ])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [["root"], ["wait-for-device"], ["shell", "id", "-u"]])
        self.assert_log("android-adb-root-1", b"", KNOWN_DISCONNECT)

    def test_known_disconnect_nonroot_gets_one_retry_then_uid_zero(self) -> None:
        result = self.run_helper([
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
            self.reply(["root"], stdout=b"restarting adbd as root\n"),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"0\n"),
        ])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [
            ["root"], ["wait-for-device"], ["shell", "id", "-u"],
            ["root"], ["wait-for-device"], ["shell", "id", "-u"],
        ])

    def test_unknown_or_non_one_root_error_is_fatal_without_retry(self) -> None:
        for status, stderr in (
            (1, b"adb: device offline\n"),
            (1, KNOWN_DISCONNECT + b"extra\n"),
            (1, KNOWN_DISCONNECT[:-1] + b"\x00\n"),
            (2, KNOWN_DISCONNECT),
        ):
            with self.subTest(status=status, stderr=stderr):
                self.calls.unlink(missing_ok=True)
                result = self.run_helper([self.reply(["root"], status=status, stderr=stderr)])
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.calls_made(), [["root"]])
                self.assert_log("android-adb-root-1", b"", stderr)
                self.assertIn(stderr, result.stderr)

    def test_normal_root_with_nonroot_uid_is_fatal_without_retry(self) -> None:
        result = self.run_helper([
            self.reply(["root"]),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
        ])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len([args for args in self.calls_made() if args == ["root"]]), 1)
        self.assertIn(b"Android ADB is not root", result.stderr)

    def test_wait_probe_and_malformed_uid_fail_closed(self) -> None:
        wait_failure = self.run_helper([
            self.reply(["root"]),
            self.reply(["wait-for-device"], status=1, stderr=b"wait failed\x00\xff"),
        ])
        self.assertNotEqual(wait_failure.returncode, 0)
        self.assertEqual(self.calls_made(), [["root"], ["wait-for-device"]])
        self.assert_log("android-adb-root-wait-1", b"", b"wait failed\x00\xff")

        self.calls.unlink(missing_ok=True)
        probe_failure = self.run_helper([
            self.reply(["root"]),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], status=1, stdout=b"partial\x00\xfe", stderr=b"probe failed\xff"),
        ])
        self.assertNotEqual(probe_failure.returncode, 0)
        self.assert_log("android-adb-root-uid-1", b"partial\x00\xfe", b"probe failed\xff")
        self.assertIn(b"partial\x00\xfe", probe_failure.stdout)
        self.assertIn(b"probe failed\xff", probe_failure.stderr)

        for payload in (b"0\x00\n", b"\xff\n", b"0\n\n", b" 0\n", b"0\r"):
            with self.subTest(payload=payload):
                self.calls.unlink(missing_ok=True)
                malformed = self.run_helper([
                    self.reply(["root"]),
                    self.reply(["wait-for-device"]),
                    self.reply(["shell", "id", "-u"], stdout=payload),
                ])
                self.assertNotEqual(malformed.returncode, 0)
                self.assertEqual(len([args for args in self.calls_made() if args == ["root"]]), 1)
                self.assert_log("android-adb-root-uid-1", payload, b"")
                self.assertIn(payload, malformed.stdout)
                self.assertIn(b"UID probe was malformed", malformed.stderr)

    def test_second_known_disconnect_succeeds_only_with_uid_zero(self) -> None:
        succeeded = self.run_helper([
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"0\n"),
        ])
        self.assertEqual(succeeded.returncode, 0, succeeded.stderr)
        self.assertEqual(len([args for args in self.calls_made() if args == ["root"]]), 2)
        self.assert_log("android-adb-root-2", b"", KNOWN_DISCONNECT)

        self.calls.unlink(missing_ok=True)
        failed = self.run_helper([
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
        ])
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(len([args for args in self.calls_made() if args == ["root"]]), 2)
        self.assertIn(b"not root after attempt 2", failed.stderr)

        self.calls.unlink(missing_ok=True)
        unknown_retry = self.run_helper([
            self.reply(["root"], status=1, stderr=KNOWN_DISCONNECT),
            self.reply(["wait-for-device"]),
            self.reply(["shell", "id", "-u"], stdout=b"2000\n"),
            self.reply(["root"], status=1, stderr=b"adb: device offline\n"),
        ])
        self.assertNotEqual(unknown_retry.returncode, 0)
        self.assertEqual(len([args for args in self.calls_made() if args == ["root"]]), 2)
        self.assert_log("android-adb-root-2", b"", b"adb: device offline\n")


if __name__ == "__main__":
    unittest.main()
