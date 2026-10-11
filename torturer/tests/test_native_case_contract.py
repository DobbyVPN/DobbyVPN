from __future__ import annotations

import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from torturer_runner.ui.native_case import run_desktop_case


_MISSING = object()


class NativeCaseResultContractTests(unittest.TestCase):
    def run_case(self, result=_MISSING, *, returncode=0):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "case"

            def run_logged(_command, *, logs, input_data, **_kwargs):
                request = json.loads(input_data)
                self.assertEqual(request["operation"], "native-case")
                self.assertEqual(request["case"], "logs-clear")
                if result is not _MISSING:
                    payload = result if isinstance(result, str) else json.dumps(result)
                    Path(request["result_path"]).write_text(payload, encoding="utf-8")
                Path(logs, "native-case.stdout.log").write_bytes(b"helper stdout\x00\xff\n")
                Path(logs, "native-case.stderr.log").write_bytes(b"helper stderr\x00\xfe\n")
                return SimpleNamespace(returncode=returncode)

            with patch("torturer_runner.local_vm._run_logged", side_effect=run_logged):
                return run_desktop_case(
                    Path("native-helper"),
                    root / "DobbyVPN.app",
                    "logs-clear",
                    directory,
                    30.0,
                )

    @staticmethod
    def passed_result(**overrides):
        result = {
            "case_id": "logs-clear",
            "outcome": "passed",
            "assertions": [{"id": "logs.clear.rendered", "outcome": "passed"}],
            "cleanup_errors": [],
        }
        result.update(overrides)
        return result

    def test_zero_exit_without_readable_result_fails(self):
        for result in (_MISSING, "not-json"):
            with self.subTest(result=result), self.assertRaisesRegex(
                RuntimeError, "without a readable result"
            ):
                self.run_case(result)

    def test_invalid_result_shape_fails_even_after_zero_exit(self):
        with self.assertRaisesRegex(RuntimeError, "native helper result is invalid"):
            self.run_case({
                "case_id": "another-case",
                "outcome": "passed",
                "assertions": [{"id": "logs.clear.rendered", "outcome": "passed"}],
                "cleanup_errors": [],
            })

    def test_pass_without_successful_assertions_is_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "passed result has no successful assertions"):
            self.run_case(self.passed_result(assertions=[]))
        with self.assertRaisesRegex(RuntimeError, "passed result has no successful assertions"):
            self.run_case(self.passed_result(assertions=[{"id": "logs.clear.rendered"}]))

    def test_nonzero_exit_and_cleanup_errors_each_prevent_pass(self):
        nonzero = self.run_case(self.passed_result(), returncode=7)
        self.assertEqual(nonzero["outcome"], "failed")
        self.assertEqual(nonzero["process_exit_code"], 7)
        self.assertEqual(nonzero["error"], "native helper exited 7")

        cleanup_failed = self.run_case(self.passed_result(cleanup_errors=["app remained running"]))
        self.assertEqual(cleanup_failed["outcome"], "failed")
        self.assertEqual(cleanup_failed["cleanup_errors"], ["app remained running"])

    def test_not_run_result_is_failed_and_cannot_claim_cleanup(self):
        result = self.run_case({
            "case_id": "logs-clear",
            "outcome": "not-run",
            "assertions": [],
            "cleanup_errors": [],
        })
        self.assertEqual(result["outcome"], "failed")
        self.assertEqual(
            result["error"], "native helper did not produce a terminal case result"
        )
        self.assertEqual(
            result["cleanup_errors"], ["native helper did not confirm app cleanup"]
        )


if __name__ == "__main__":
    unittest.main()
