from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from torturer_checks.hosted import run as hosted_run
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.engine import FunctionalEngine
from torturer_contract.functional.results import ConnectionIdentity, RunProvenance
from torturer_contract.functional.scenarios import get_scenario


class Adapter:
    capabilities = frozenset({Capability.CONFIGURE})

    def __init__(self):
        self.reset_calls = 0
        self.selected = []

    def select_connection(self, connection):
        self.selected.append(connection)

    def execute(self, step):
        return {"configured": True}

    def execute_scenario(self, scenario):
        observations = {}
        for step in scenario.steps:
            observations.update(self.execute(step))
        return observations

    def reset(self, *, timeout_seconds=5):
        self.reset_calls += 1


def provenance():
    return RunProvenance(platform="linux", platform_version="24.04", architecture="amd64")


class HostedRunPreflightTests(unittest.TestCase):
    def test_matrix_runs_every_scenario_for_every_discovered_connection(self):
        adapter = Adapter()
        connections = (
            ConnectionIdentity(0, "OUTLINE"),
            ConnectionIdentity(1, "XRAY"),
            ConnectionIdentity(2, "TRUST_TUNNEL"),
            ConnectionIdentity(3, "OUTLINE"),
        )
        results = hosted_run._run_connection_matrix(
            FunctionalEngine(),
            (get_scenario("functional.configure"),),
            adapter,
            provenance(),
            connections,
        )
        self.assertEqual(adapter.selected, list(connections))
        self.assertEqual(
            [item["connection"] for item in results],
            [item.to_dict() for item in connections],
        )

    def test_run_uses_shared_engine_and_reset_without_result_evidence(self):
        adapter = Adapter()
        results = hosted_run._run_scenarios(
            FunctionalEngine(), (get_scenario("functional.configure"),), adapter, provenance(),
            ConnectionIdentity(0, "OUTLINE"),
        )
        self.assertEqual(adapter.reset_calls, 1)
        self.assertEqual(results[0]["scenario"]["id"], "functional.configure")
        self.assertNotIn("evidence_refs", results[0])
        self.assertNotIn("scenario_set_digest", results[0])

    def test_reset_failure_propagates_exactly(self):
        class FailingReset(Adapter):
            def reset(self, *, timeout_seconds=5):
                raise RuntimeError("synthetic reset failure")

        with self.assertRaisesRegex(
            RuntimeError, "synthetic reset failure"
        ):
            hosted_run._run_scenarios(
                FunctionalEngine(), (get_scenario("functional.configure"),), FailingReset(), provenance(),
                ConnectionIdentity(0, "OUTLINE"),
            )

    def test_result_writer_replaces_previous_attempt(self):
        with tempfile.TemporaryDirectory(prefix="hosted-run-result-") as temporary:
            output = Path(temporary) / "result.json"
            hosted_run._write_json(output, {"result": "old"})
            hosted_run._write_json(output, {"result": "written"})
            self.assertEqual(json.loads(output.read_text(encoding="utf-8")), {"result": "written"})

    def test_parser_does_not_expose_measurement_service_overrides(self):
        parser = hosted_run.build_parser()
        options = {
            option
            for action in parser._actions
            for option in action.option_strings
        }
        self.assertTrue(
            {"--identity-url", "--latency-url", "--download-url", "--upload-url"}.isdisjoint(options)
        )
        self.assertIn("--scenario", options)
        self.assertIn("--suite", options)
        arguments = parser.parse_args([
            "--platform", "linux", "--profile", "profile.toml",
            "--platform-version", "24.04", "--lane-timeout-seconds", "60",
            "--output", "result.json",
        ])
        self.assertEqual(arguments.suite, "mini")
        self.assertNotIn("--scenario-id", options)

    def test_hosted_full_suite_is_rejected_before_adapter_setup(self):
        with self.assertRaisesRegex(ValueError, "FULL_SUITE_UNSUPPORTED_BY_HOSTED_ENTRYPOINT"):
            hosted_run.main([
                "--platform", "linux", "--suite", "full",
                "--profile", "profile.toml", "--platform-version", "24.04",
                "--lane-timeout-seconds", "60", "--output", "result.json",
            ])


if __name__ == "__main__":
    unittest.main()
