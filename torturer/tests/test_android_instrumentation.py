import unittest

from torturer_checks.android_instrumentation import (
    parse_instrumentation_result,
    validate_complete_throwable_report,
)


class AndroidInstrumentationParserTests(unittest.TestCase):
    def test_requires_anchored_junit_summary_and_final_completion_marker(self):
        valid = parse_instrumentation_result(
            returncode=0,
            stdout=b"INSTRUMENTATION_STATUS: class=Smoke\nOK (1 test)\n"
            b"INSTRUMENTATION_CODE: -1\n",
        )
        self.assertTrue(valid.succeeded)

        for stdout in (
            b"OK (1 test)\nINSTRUMENTATION_CODE: -1\ntrailing\n",
            b"NOT OK (1 test)\nINSTRUMENTATION_CODE: -1\n",
            b"OK (1 test)\nINSTRUMENTATION_CODE: -1x\n",
            b"OK (1 test)\nINSTRUMENTATION_CODE: -1\nOK (1 test)\n",
        ):
            with self.subTest(stdout=stdout):
                self.assertFalse(
                    parse_instrumentation_result(
                        returncode=0, stdout=stdout
                    ).succeeded
                )

    def test_stderr_is_diagnostic_and_never_supplies_success_evidence(self):
        self.assertTrue(
            parse_instrumentation_result(
                returncode=0,
                stdout=b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n",
                stderr=b"adb warning\n",
            ).succeeded
        )
        self.assertFalse(
            parse_instrumentation_result(
                returncode=0,
                stdout=b"",
                stderr=b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n",
            ).succeeded
        )
        self.assertTrue(
            parse_instrumentation_result(
                returncode=0,
                stdout=b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n",
                stderr=b"FAILURES!!!\n",
            ).failures_marker_present
        )

    def test_requires_success_return_code_and_no_timeout(self):
        output = b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n"
        for returncode, timed_out in ((1, False), (0, True)):
            with self.subTest(returncode=returncode, timed_out=timed_out):
                parsed = parse_instrumentation_result(
                    returncode=returncode,
                    stdout=output,
                    timed_out=timed_out,
                )
                self.assertFalse(parsed.succeeded)

    def test_complete_throwable_report_requires_one_ordered_complete_sequence(self):
        valid = (
            b"DOBBY_COMPLETE_THROWABLE_BEGIN id=4 chunks=2 bytes=3 chars=3\n"
            b"DOBBY_COMPLETE_THROWABLE_CHUNK id=4 sequence=1/2\nxy\n"
            b"DOBBY_COMPLETE_THROWABLE_CHUNK id=4 sequence=2/2\nz\n"
            b"DOBBY_COMPLETE_THROWABLE_END id=4 chunks=2 bytes=3 chars=3\n"
        )
        validate_complete_throwable_report(valid)
        for invalid in (
            valid.replace(b"sequence=2/2", b"sequence=1/2"),
            valid.replace(b"chunks=2 bytes=3", b"chunks=3 bytes=3"),
            valid.replace(b"DOBBY_COMPLETE_THROWABLE_END", b"OTHER"),
        ):
            with self.subTest(invalid=invalid):
                with self.assertRaises(ValueError):
                    validate_complete_throwable_report(invalid)


if __name__ == "__main__":
    unittest.main()
