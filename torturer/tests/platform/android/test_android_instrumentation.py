from __future__ import annotations

import unittest

from torturer_runner.android_instrumentation import parse_instrumentation_result


class AndroidInstrumentationTests(unittest.TestCase):
    def test_completed_single_and_multi_test_suites(self) -> None:
        for summary in (b"OK (1 test)", b"OK (3 tests)", b"OK (12 tests)"):
            with self.subTest(summary=summary):
                result = parse_instrumentation_result(
                    returncode=0, stdout=summary + b"\n\nINSTRUMENTATION_CODE: -1\n",
                )
                self.assertTrue(result.succeeded)

    def test_empty_failed_or_incomplete_suite_never_passes(self) -> None:
        for output in (
            b"OK (0 tests)\nINSTRUMENTATION_CODE: -1\n",
            b"OK (3 tests)\nFAILURES!!!\nINSTRUMENTATION_CODE: -1\n",
            b"OK (3 tests)\n",
            b"OK (3 tests)\nINSTRUMENTATION_CODE: -1\nrunner crashed\n",
            b"message: OK (3 tests)\nINSTRUMENTATION_CODE: -1\n",
        ):
            with self.subTest(output=output):
                self.assertFalse(parse_instrumentation_result(returncode=0, stdout=output).succeeded)
        output = b"OK (3 tests)\nINSTRUMENTATION_CODE: -1\n"
        for overrides in ({"returncode": 1}, {"timed_out": True}, {"stderr": b"FAILURES!!!\n"}):
            with self.subTest(overrides=overrides):
                arguments = {"returncode": 0, "stdout": output, **overrides}
                self.assertFalse(parse_instrumentation_result(**arguments).succeeded)
        self.assertFalse(parse_instrumentation_result(returncode=0, stdout=b"", stderr=output).succeeded)
