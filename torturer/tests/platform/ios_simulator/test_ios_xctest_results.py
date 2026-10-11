from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from torturer_runner import ios_simulator, ios_simulator_app
from torturer_runner.native_cases import (
    IOS_LEAF_CASES,
    IOS_LOGS_FREEZE_RESUME_CASE,
    IOS_RENDERER_SEVERITY_CASE,
    IOS_SUBSCRIPTION_FIXTURE_CASE,
)


_ABOUT = (
    "iosAppUITests/NativeUIInteractionTests/"
    "testAboutShowsSelectedProductRevision"
)
_FIXTURE_PASTE = (
    "iosAppUITests/NativeSubscriptionFixtureInteractionTests/"
    "testNativePasteStartsFixtureRequest"
)


def _case(name: str, result: str, *, node_type: str = "Test Case", children=None) -> dict:
    return {
        "nodeType": node_type,
        "name": name,
        "result": result,
        "children": [] if children is None else children,
    }


def _repetition(index: int, cases: list[dict]) -> dict:
    return {
        "nodeType": "Repetition",
        "name": f"Repetition {index}",
        "result": "Passed",
        "children": [
            {
                "nodeType": "UI test bundle",
                "name": "iosAppUITests",
                "children": [
                    {
                        "nodeType": "Test Suite",
                        "name": "NativeUIInteractionTests",
                        "children": cases,
                    }
                ],
            }
        ],
    }


class IOSXCTestResultsTests(unittest.TestCase):
    def test_default_group_expands_only_test_methods_and_fixture_gets_total_seven(self) -> None:
        required = ios_simulator.required_ui_test_cases()
        self.assertEqual(len(required), 21)
        self.assertEqual(len(set(required)), 21)
        self.assertTrue(all("/test" in test for test in required))
        self.assertFalse(any("setUp" in test or "tearDown" in test for test in required))
        self.assertEqual(ios_simulator.expected_subscription_fixture_gets(), 7)
        self.assertEqual(
            ios_simulator.expected_subscription_fixture_gets(
                [IOS_SUBSCRIPTION_FIXTURE_CASE]
            ),
            7,
        )
        self.assertEqual(
            ios_simulator.expected_subscription_fixture_gets(
                [IOS_SUBSCRIPTION_FIXTURE_CASE]
            ),
            sum(ios_simulator.SUBSCRIPTION_FIXTURE_TEST_GETS.values()),
        )

    def test_native_leaf_registry_maps_to_independent_xctest_methods(self) -> None:
        self.assertEqual(set(IOS_LEAF_CASES), set(ios_simulator._NATIVE_CASE_TEST_SELECTIONS) - {
            IOS_LOGS_FREEZE_RESUME_CASE,
            IOS_RENDERER_SEVERITY_CASE,
            IOS_SUBSCRIPTION_FIXTURE_CASE,
        })
        fixture_gets = {
            "ios-subscription-paste": 1,
            "ios-subscription-failure-retry": 2,
            "ios-subscription-profiles": 1,
            "ios-subscription-logs": 1,
            "ios-subscription-cold-import": 1,
            "ios-subscription-warm-import": 1,
        }
        for case in IOS_LEAF_CASES:
            with self.subTest(case=case):
                required = ios_simulator.required_ui_test_cases([case])
                self.assertEqual(len(required), 1)
                self.assertIn("/test", required[0])
                self.assertEqual(
                    ios_simulator.expected_subscription_fixture_gets([case]),
                    fixture_gets.get(case, 0),
                )

    def test_each_leaf_builds_one_filter_and_preserves_requested_iterations(self) -> None:
        for case in IOS_LEAF_CASES:
            with self.subTest(case=case):
                command = ios_simulator.xcodebuild_ui_test_without_building_command(
                    "01234567-89ab-cdef-0123-456789abcdef",
                    "candidate/iosApp.xcodeproj",
                    "work/derived-data",
                    "work/results.xcresult",
                    architecture="arm64",
                    native_cases=[case],
                    repeat=3,
                )
                filters = [
                    value for value in command
                    if value.startswith("-only-testing:")
                ]
                self.assertEqual(len(filters), 1)
                self.assertIn("-test-iterations", command)
                self.assertEqual(command[command.index("-test-iterations") + 1], "3")
                self.assertEqual(
                    filters[0],
                    "-only-testing:" + ios_simulator.required_ui_test_cases([case])[0],
                )

    def test_default_single_iteration_omits_xcodebuild_iteration_flag(self) -> None:
        for repeat in (None, 1):
            with self.subTest(repeat=repeat):
                options = {} if repeat is None else {"repeat": repeat}
                command = ios_simulator.xcodebuild_ui_test_without_building_command(
                    "01234567-89ab-cdef-0123-456789abcdef",
                    "candidate/iosApp.xcodeproj",
                    "work/derived-data",
                    "work/results.xcresult",
                    architecture="arm64",
                    native_cases=["ios-about"],
                    **options,
                )
                self.assertNotIn("-test-iterations", command)

    def test_required_test_case_and_test_case_run_results_count_each_iteration(self) -> None:
        report = {
            "testNodes": [
                _repetition(index, [
                    _case(
                        "testAboutShowsSelectedProductRevision()",
                        "Passed",
                        children=[
                            _case(
                                "Run 1",
                                "Passed",
                                node_type="Test Case Run",
                            ),
                        ],
                    ),
                    _case("testUnselectedSibling()", "Skipped"),
                ])
                for index in (1, 2, 3)
            ]
        }
        summary = ios_simulator_app._validate_xctest_test_results(
            json.dumps(report), required_tests=[_ABOUT], repeat=3
        )
        self.assertEqual(summary["repeat"], 3)
        self.assertEqual(summary["required_tests"][0]["test"], _ABOUT)
        self.assertEqual(len(summary["required_tests"][0]["iterations"]), 3)
        self.assertEqual(summary["result"], "Passed")
        self.assertEqual(
            [run["iteration"] for run in summary["required_tests"][0]["iterations"]],
            [1, 2, 3],
        )

    def test_duplicate_repetition_index_cannot_stand_in_for_a_missing_iteration(self) -> None:
        report = {
            "testNodes": [
                _repetition(1, [_case("testAboutShowsSelectedProductRevision()", "Passed")]),
                _repetition(1, [_case("testAboutShowsSelectedProductRevision()", "Passed")]),
            ]
        }
        with self.assertRaisesRegex(
            ios_simulator_app.IOSSimulatorAppContractError,
            "indexes are incomplete or duplicated",
        ):
            ios_simulator_app._validate_xctest_test_results(
                json.dumps(report), required_tests=[_ABOUT], repeat=2
            )

    def test_unindexed_successes_cannot_claim_multiple_requested_iterations(self) -> None:
        report = {
            "testNodes": [
                {
                    "nodeType": "Test Suite",
                    "name": "NativeUIInteractionTests",
                    "children": [
                        _case("testAboutShowsSelectedProductRevision()", "Passed"),
                        _case("testAboutShowsSelectedProductRevision()", "Passed"),
                    ],
                }
            ]
        }
        with self.assertRaisesRegex(
            ios_simulator_app.IOSSimulatorAppContractError,
            "did not identify each of 2 repetitions",
        ):
            ios_simulator_app._validate_xctest_test_results(
                json.dumps(report), required_tests=[_ABOUT], repeat=2
            )

    def test_zero_exit_report_with_missing_or_skipped_method_fails(self) -> None:
        report = {
            "testNodes": [
                _repetition(1, [_case("testAboutShowsSelectedProductRevision()", "Passed")]),
                _repetition(2, [_case("testAboutShowsSelectedProductRevision()", "Skipped")]),
            ]
        }
        with self.assertRaisesRegex(
            ios_simulator_app.IOSSimulatorAppContractError,
            "result='Skipped'",
        ):
            ios_simulator_app._validate_xctest_test_results(
                json.dumps(report), required_tests=[_ABOUT], repeat=2
            )

        with self.assertRaisesRegex(
            ios_simulator_app.IOSSimulatorAppContractError,
            "expected 2 test results, found 0",
        ):
            ios_simulator_app._validate_xctest_test_results(
                json.dumps({"testNodes": [_repetition(1, [])]}),
                required_tests=[_ABOUT], repeat=2,
            )

    def test_failed_and_unknown_test_case_run_results_fail_even_with_passed_parent(self) -> None:
        for result in ("Failed", "unknown", None):
            report = {
                "testNodes": [_repetition(1, [
                    _case(
                        "testNativePasteStartsFixtureRequest()",
                        "Passed",
                        children=[_case("Run 1", result, node_type="Test Case Run")],
                    )
                ])]
            }
            with self.subTest(result=result), self.assertRaises(
                ios_simulator_app.IOSSimulatorAppContractError
            ):
                ios_simulator_app._validate_xctest_test_results(
                    json.dumps(report), required_tests=[_FIXTURE_PASTE], repeat=1
                )

    def test_raw_result_json_is_retained_with_diagnostics_without_reformatting(self) -> None:
        raw = '{\n  "testNodes": [],\n  "exact spacing": "λ"\n}\n'
        with tempfile.TemporaryDirectory() as temporary:
            work_dir = Path(temporary) / "work"
            report = ios_simulator_app._write_xctest_test_results(raw, work_dir)
            retained_dir = Path(temporary) / "logs"
            retained = ios_simulator_app.retain_ios_diagnostics(work_dir, retained_dir)
            report_bytes = report.read_bytes()
            retained_bytes = (retained_dir / "xctest-test-results.json").read_bytes()

        self.assertEqual(report_bytes, raw.encode("utf-8"))
        self.assertEqual(len(retained), 1)
        self.assertEqual(retained_bytes, raw.encode("utf-8"))

    def test_missing_result_json_fails_without_creating_an_empty_artifact(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            work_dir = Path(temporary) / "work"
            with self.assertRaisesRegex(
                ios_simulator_app.IOSSimulatorAppContractError,
                "returned no iOS XCTest test-results JSON",
            ):
                ios_simulator_app._write_xctest_test_results("", work_dir)
            self.assertFalse((work_dir / "diagnostics/ios-simulator/xctest-test-results.json").exists())


if __name__ == "__main__":
    unittest.main()
