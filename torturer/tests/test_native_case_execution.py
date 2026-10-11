from __future__ import annotations

import argparse
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from torturer_runner.ui.journey import _run_case, run_native_cases, run_journey


class NativeCaseExecutionTests(unittest.TestCase):
    def args(self, root):
        return argparse.Namespace(platform="windows", native_cases=["logs-clear", "logs-details"],
                                  repeat=2, raw_log_dir=root, timeout=60,
                                  ui_helper=Path("helper"), ui=Path("app"))

    def test_fixed_repeats_preserve_first_failure_and_run_unrelated_cases(self):
        outcomes = iter(["failed", "passed", "passed", "passed"])
        calls = []
        def execute(helper, app, case, directory, timeout):
            calls.append((case, directory.name))
            return {"case_id": case, "outcome": next(outcomes), "cleanup_errors": []}
        with tempfile.TemporaryDirectory() as directory, patch(
                "torturer_runner.ui.native_case.run_desktop_case", side_effect=execute):
            result = run_native_cases(self.args(Path(directory)))
        self.assertFalse(result["complete"])
        self.assertEqual([case["outcome"] for case in result["cases"]],
                         ["failed", "passed", "passed", "passed"])
        self.assertEqual(calls, [("logs-clear", "logs-clear-1"),
                                 ("logs-details", "logs-details-1"),
                                 ("logs-clear", "logs-clear-2"),
                                 ("logs-details", "logs-details-2")])

    def test_unproved_cleanup_leaves_remaining_cases_not_run(self):
        with tempfile.TemporaryDirectory() as directory, patch(
                "torturer_runner.ui.native_case.run_desktop_case",
                return_value={"outcome": "failed", "cleanup_errors": ["app did not exit"]}) as execute:
            result = run_native_cases(self.args(Path(directory)))
        self.assertEqual(execute.call_count, 1)
        self.assertEqual([case["outcome"] for case in result["cases"]],
                         ["failed", "not-run", "not-run", "not-run"])

    def test_exhausted_deadline_never_manufactures_passes(self):
        with tempfile.TemporaryDirectory() as directory, patch(
                "torturer_runner.ui.native_case.run_desktop_case") as execute:
            args = self.args(Path(directory))
            args.timeout = 1
            result = run_native_cases(args)
        execute.assert_not_called()
        self.assertFalse(result["complete"])
        self.assertEqual([case["outcome"] for case in result["cases"]], ["not-run"] * 4)

    def test_full_suite_uses_same_cases_once_and_keeps_clean_failures(self):
        calls = []
        def execute(_args, case, attempt, _deadline, record):
            calls.append((case, attempt))
            record.update(outcome="failed" if case == "logs-details" else "passed", cleanup_errors=[])
        with tempfile.TemporaryDirectory() as directory, patch(
                "torturer_runner.ui.journey._run_case", side_effect=execute):
            args = self.args(Path(directory))
            args.native_cases = None
            result = run_journey(args)
        self.assertFalse(result["complete"])
        self.assertEqual(result["repeat"], 1)
        self.assertEqual(result["coverage"]["native_case_selection"], "default")
        self.assertEqual(calls[-1], ("connected-session", 1))
        self.assertIn(("logs-severity", 1), calls)
        self.assertTrue(all(attempt == 1 for _, attempt in calls))
        self.assertEqual(result["cases"][-1]["outcome"], "passed")

    def test_full_suite_never_starts_connected_case_after_unsafe_cleanup(self):
        calls = []
        def execute(_args, case, attempt, _deadline, record):
            calls.append(case)
            record.update(outcome="failed", cleanup_errors=["app still alive"])
        with tempfile.TemporaryDirectory() as directory, patch(
                "torturer_runner.ui.journey._run_case", side_effect=execute):
            args = self.args(Path(directory))
            args.native_cases = None
            result = run_journey(args)
        self.assertEqual(calls, ["logs-details"])
        self.assertEqual(result["cases"][-1]["case_id"], "connected-session")
        self.assertEqual(result["cases"][-1]["outcome"], "not-run")

    def test_active_observer_withholds_backend_teardown_and_stops_later_cases(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                platform="windows", native_cases=["connected-session", "logs-clear"],
                repeat=1, raw_log_dir=Path(directory), timeout=1200,
                ui_helper=Path("helper"), ui=Path("app"), profile=Path("profile"),
            )
            base = Mock()
            fixture = Mock()
            fixture.start.return_value = "https://127.0.0.1/subscription"
            fixture.failure_url = "https://127.0.0.1/failure"
            fixture.control_url = "https://127.0.0.1/control"
            fixture.control_key = "fixture-key"
            observer_error = RuntimeError("native helper stopped during observation")
            observer_error.observer_quiescent = False

            with (
                patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=fixture),
                patch("torturer_runner.ui.journey._base_adapter", return_value=base),
                patch("torturer_runner.ui.native_case.run_desktop_case", side_effect=observer_error) as run_case,
                patch("torturer_runner.ui.journey.traceback.print_exception"),
            ):
                result = run_native_cases(args)

            self.assertEqual(run_case.call_count, 1)
            base.reset.assert_not_called()
            base.finalize.assert_not_called()
            fixture.close.assert_called_once()
            self.assertFalse(result["complete"])
            first, second = result["cases"]
            self.assertEqual(first["outcome"], "failed")
            self.assertIs(first["backend_cleanup_confirmed"], False)
            self.assertTrue(any("backend cleanup unconfirmed" in value for value in first["cleanup_errors"]))
            self.assertEqual(second["case_id"], "logs-clear")
            self.assertEqual(second["outcome"], "not-run")

    def test_quiescent_observer_error_still_attempts_backend_teardown(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                platform="windows", native_cases=["connected-session"],
                repeat=1, raw_log_dir=Path(directory), timeout=1200,
                ui_helper=Path("helper"), ui=Path("app"), profile=Path("profile"),
            )
            base = Mock()
            fixture = Mock()
            fixture.start.return_value = "https://127.0.0.1/subscription"
            fixture.failure_url = "https://127.0.0.1/failure"
            fixture.control_url = "https://127.0.0.1/control"
            fixture.control_key = "fixture-key"
            observer_error = RuntimeError("native helper timed out after observer returned")
            observer_error.observer_quiescent = True

            with (
                patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=fixture),
                patch("torturer_runner.ui.journey._base_adapter", return_value=base),
                patch("torturer_runner.ui.native_case.run_desktop_case", side_effect=observer_error),
                patch("torturer_runner.ui.journey.traceback.print_exception"),
            ):
                record = {}
                _run_case(args, "connected-session", 1, 1200, record)

            base.reset.assert_called_once()
            base.finalize.assert_called_once()
            self.assertNotIn("backend_cleanup_confirmed", record)

    def test_auto_recovery_repeat_resets_marker_between_clean_attempts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = argparse.Namespace(
                platform="windows", native_cases=["auto-recovery-stop"], repeat=2,
                raw_log_dir=root, timeout=1200, ui_helper=Path("helper"),
                ui=Path("app"), profile=Path("profile"),
            )
            base = Mock()
            fixture = Mock()
            fixture.start.return_value = "https://127.0.0.1/subscription"
            fixture.failure_url = "https://127.0.0.1/failure"
            fixture.control_url = "https://127.0.0.1/control"
            fixture.control_key = "fixture-key"
            marker_states = []

            def run_case(_helper, _app, _case, _directory, _timeout, *, context, observer):
                marker = Path(context["auto_recovery_marker"])
                marker_states.append(marker.exists())
                marker.parent.mkdir(parents=True, exist_ok=True)
                marker.write_text("armed after real route and traffic\n", encoding="utf-8")
                return {"outcome": "passed", "cleanup_errors": []}

            with (
                patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=fixture),
                patch("torturer_runner.ui.journey._base_adapter", return_value=base),
                patch("torturer_runner.ui.native_case.run_desktop_case", side_effect=run_case),
            ):
                result = run_native_cases(args)

            self.assertTrue(result["complete"])
            self.assertEqual([case["outcome"] for case in result["cases"]], ["passed", "passed"])
            self.assertEqual(marker_states, [False, False])
            self.assertEqual((root / "recovery-stop.arm").read_text(encoding="utf-8"),
                             "armed after real route and traffic\n")
            self.assertEqual(base.reset.call_count, 2)
            self.assertEqual(base.finalize.call_count, 2)

    def test_auto_recovery_repeat_stops_when_marker_reset_is_unproven(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = argparse.Namespace(
                platform="windows", native_cases=["auto-recovery-stop"], repeat=2,
                raw_log_dir=root, timeout=1200, ui_helper=Path("helper"),
                ui=Path("app"), profile=Path("profile"),
            )
            fixture = Mock()
            fixture.start.return_value = "https://127.0.0.1/subscription"
            fixture.failure_url = "https://127.0.0.1/failure"
            fixture.control_url = "https://127.0.0.1/control"
            fixture.control_key = "fixture-key"
            with (
                patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=fixture),
                patch("torturer_runner.ui.journey._base_adapter", return_value=Mock()),
                patch("torturer_runner.ui.native_case.run_desktop_case",
                      return_value={"outcome": "passed", "cleanup_errors": []}) as run_case,
            ):
                result = run_native_cases(args)

            self.assertEqual(run_case.call_count, 1)
            self.assertEqual([case["outcome"] for case in result["cases"]], ["failed", "not-run"])
            self.assertTrue(any("repeat marker reset" in error
                                for error in result["cases"][0]["cleanup_errors"]))

    def test_auto_recovery_setup_failure_keeps_original_error_and_stops_repeat(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                platform="windows", native_cases=["auto-recovery-stop"], repeat=2,
                raw_log_dir=Path(directory), timeout=1200, ui_helper=Path("helper"),
                ui=Path("app"), profile=Path("profile"),
            )
            fixture = Mock()
            fixture.start.side_effect = RuntimeError("fixture setup failed")
            with (
                patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=fixture),
                patch("torturer_runner.ui.journey._base_adapter") as base_adapter,
                patch("torturer_runner.ui.native_case.run_desktop_case") as run_case,
            ):
                result = run_native_cases(args)

            self.assertEqual([case["outcome"] for case in result["cases"]], ["failed", "not-run"])
            self.assertIn("fixture setup failed", result["cases"][0]["error"])
            self.assertEqual(run_case.call_count, 0)
            base_adapter.assert_not_called()
            fixture.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
