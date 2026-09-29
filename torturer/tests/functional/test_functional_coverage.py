from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import functional
from torturer_contract.functional.coverage import (
    coverage_contract,
    qualification_exit_code,
)
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import select_scenarios


def _matrix_results(connections, scenarios):
    return [
        {
            "connection": connection.to_dict(),
            "scenario": {"id": scenario.id},
            "outcome": "passed",
        }
        for connection in connections
        for scenario in scenarios
    ]


class FunctionalCoverageTests(unittest.TestCase):
    def setUp(self) -> None:
        self.connections = (
            ConnectionIdentity(index=0, protocol="OUTLINE"),
            ConnectionIdentity(index=1, protocol="XRAY"),
        )
        self.scenarios = select_scenarios(suite="mini")
        self.results = _matrix_results(self.connections, self.scenarios)

    def coverage(self, results=None, *, scenarios=None, explicit=False, connections=None):
        return coverage_contract(
            self.connections if connections is None else connections,
            self.scenarios if scenarios is None else scenarios,
            self.results if results is None else results,
            suite="mini",
            explicit_scenario_selection=explicit,
        )

    def test_all_enabled_scenarios_pass_for_every_discovered_profile(self) -> None:
        coverage = self.coverage()

        self.assertTrue(coverage["complete"])
        self.assertEqual(coverage["status"], "complete")
        self.assertEqual(coverage["expected_result_count"], 2 * len(self.scenarios))
        self.assertEqual(qualification_exit_code(coverage), 0)

    def test_omitted_profile_scenario_pair_fails_coverage(self) -> None:
        omitted = self.results[:-1]
        coverage = self.coverage(omitted)

        self.assertFalse(coverage["complete"])
        self.assertEqual(coverage["status"], "coverage-contract-failed")
        self.assertEqual(len(coverage["missing_results"]), 1)
        self.assertEqual(qualification_exit_code(coverage), 2)

    def test_failed_and_unavailable_scenarios_fail_coverage(self) -> None:
        for outcome in ("failed", "unavailable"):
            with self.subTest(outcome=outcome):
                results = [dict(result) for result in self.results]
                results[0]["outcome"] = outcome
                if outcome == "unavailable":
                    results[0]["failure"] = {"code": "CAPABILITY_UNAVAILABLE"}

                coverage = self.coverage(results)

                self.assertFalse(coverage["complete"])
                self.assertEqual(qualification_exit_code(coverage), 2)
                if outcome == "unavailable":
                    self.assertEqual(
                        coverage["actual_unavailable"],
                        [{"scenario_id": self.scenarios[0].id,
                          "reason_code": "CAPABILITY_UNAVAILABLE"}],
                    )

    def test_duplicate_or_unexpected_matrix_rows_fail_coverage(self) -> None:
        duplicated = self.results + [dict(self.results[0])]
        duplicate_coverage = self.coverage(duplicated)
        self.assertFalse(duplicate_coverage["complete"])
        self.assertEqual(len(duplicate_coverage["duplicate_results"]), 1)

        unexpected = [dict(result) for result in self.results]
        unexpected[0]["connection"] = {"index": 8, "protocol": "OUTLINE"}
        unexpected_coverage = self.coverage(unexpected)
        self.assertFalse(unexpected_coverage["complete"])
        self.assertEqual(len(unexpected_coverage["missing_results"]), 1)
        self.assertEqual(len(unexpected_coverage["unexpected_results"]), 1)

    def test_focused_diagnostics_never_qualify_even_if_all_scenarios_are_named(self) -> None:
        coverage = self.coverage(explicit=True)

        self.assertTrue(coverage["matrix_complete"])
        self.assertFalse(coverage["selection_complete"])
        self.assertFalse(coverage["complete"])
        self.assertEqual(qualification_exit_code(coverage), 2)

    def test_no_discovered_profiles_or_no_results_cannot_qualify(self) -> None:
        self.assertFalse(self.coverage(connections=())["complete"])
        self.assertFalse(self.coverage(results=[])["complete"])

    def test_duplicate_discovered_profile_identity_fails_coverage(self) -> None:
        duplicated_inventory = self.connections + (self.connections[0],)
        coverage = self.coverage(connections=duplicated_inventory)

        self.assertFalse(coverage["connection_inventory_valid"])
        self.assertEqual(coverage["connection_count"], 3)
        self.assertEqual(coverage["unique_connection_count"], 2)
        self.assertFalse(coverage["complete"])


class LocalFunctionalCoverageTests(unittest.TestCase):
    def test_local_runner_keeps_passing_focused_diagnostic_nonqualifying(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            connection = ConnectionIdentity(index=0, protocol="OUTLINE")
            adapter = SimpleNamespace()
            selected_results = [{
                "connection": connection.to_dict(),
                "scenario": {"id": "functional.configure"},
                "outcome": "passed",
            }]
            output = root / "functional.json"
            args = [
                "--platform", "linux",
                "--profile", str(root / "profile"),
                "--output", str(output),
                "--raw-log-dir", str(root / "scratch"),
                "--routing-firewall-helper", str(root / "routing-helper"),
                "--scenario", "functional.configure",
            ]
            with (
                mock.patch.object(functional, "adapter_for_platform", return_value=adapter),
                mock.patch.object(
                    functional,
                    "_execute_lane",
                    return_value=((connection,), selected_results),
                ),
            ):
                exit_code = functional.main(args)

            document = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(exit_code, 0)
            self.assertFalse(document["coverage"]["complete"])
            self.assertTrue(document["coverage"]["passed"])
            self.assertFalse(document["complete_test_set"])

    def test_local_runner_qualifies_a_complete_profile_scenario_matrix(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            connections = (
                ConnectionIdentity(index=0, protocol="OUTLINE"),
                ConnectionIdentity(index=1, protocol="XRAY"),
            )
            adapter = SimpleNamespace()
            output = root / "functional.json"
            args = [
                "--platform", "linux",
                "--profile", str(root / "profile"),
                "--output", str(output),
                "--raw-log-dir", str(root / "scratch"),
                "--routing-firewall-helper", str(root / "routing-helper"),
            ]

            def run_lane(_engine, scenarios, _adapter, _provenance, **_kwargs):
                return connections, _matrix_results(connections, scenarios)

            with (
                mock.patch.object(functional, "adapter_for_platform", return_value=adapter),
                mock.patch.object(functional, "_execute_lane", side_effect=run_lane),
            ):
                exit_code = functional.main(args)

            document = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(exit_code, 0)
            self.assertTrue(document["coverage"]["complete"])
            self.assertEqual(document["coverage"]["connection_count"], 2)


if __name__ == "__main__":
    unittest.main()
