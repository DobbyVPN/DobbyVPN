from __future__ import annotations

import unittest

from torturer_contract.functional import (
    Capability,
    ConnectionIdentity,
    FunctionalEngine,
    RunProvenance,
    ScenarioResult,
    evaluate_assertions,
    test_set as canonical_test_set,
)
from torturer_contract.functional.assertions import (
    STABILITY_SAMPLE_COUNT,
    STABILITY_SAMPLE_INTERVAL_SECONDS,
)
from torturer_contract.functional.scenarios import (
    get_scenario,
    select_scenarios,
    suite_set,
    validate_suite,
)


class FakeAdapter:
    capabilities = frozenset(Capability)

    def execute(self, step):
        values = {
            "configure": {"configured": True},
            "connect": {},
            "observe_tunnel": {
                "second_tunnel_interface" if step.id == "second-tunnel" else "tunnel_interface": True
            },
            "observe_routing_identity": {
                "second_routing_verified"
                if step.id == "second-routing"
                else "routing_verified": True
            },
            "measure_stability": {
                "stability_verified": True,
                "stability_sample_count": STABILITY_SAMPLE_COUNT,
                "stability_sample_interval_seconds": STABILITY_SAMPLE_INTERVAL_SECONDS,
            },
            "measure_throughput": {
                "latency_ms": 10.0,
                "download_mbps": 20.0,
                "upload_mbps": 15.0,
            },
            "disconnect": {
                "final_disconnect_clean" if step.id == "final-disconnect" else "disconnect_clean": True
            },
            "reconnect": {"restart_verified": True, "reconnect_completed": True},
            "inspect_cleanup": {"cleanup_verified": True},
            "network_transition": {"network_transition_verified": True},
            "process_loss": {"process_loss_verified": True},
        }
        return values[step.operation]

    def execute_scenario(self, scenario):
        observations = {}
        for step in scenario.steps:
            observations.update(self.execute(step))
        return observations


def provenance() -> RunProvenance:
    return RunProvenance(platform="linux", platform_version="24.04", architecture="amd64")


def connection() -> ConnectionIdentity:
    return ConnectionIdentity(index=0, protocol="OUTLINE")


class FunctionalContractTests(unittest.TestCase):
    def test_mini_and_full_are_explicit_cumulative_suites(self):
        mini_ids = {scenario.id for scenario in suite_set("mini")}
        full_ids = {scenario.id for scenario in suite_set("full")}
        self.assertNotIn("functional.network-transition", mini_ids)
        self.assertTrue(mini_ids <= full_ids)
        self.assertEqual(mini_ids, full_ids)

    def test_deferred_network_transition_remains_a_focused_diagnostic(self):
        selected = select_scenarios(
            suite="mini", scenario_ids=["functional.network-transition"]
        )
        self.assertEqual([scenario.id for scenario in selected], ["functional.network-transition"])

    def test_full_policy_rejects_android_before_setup(self):
        with self.assertRaisesRegex(ValueError, "FULL_SUITE_PHYSICAL_DEVICE_UNIMPLEMENTED"):
            validate_suite("full", platform="android", entrypoint="local")

    def test_hosted_policy_is_mini_only(self):
        with self.assertRaisesRegex(ValueError, "FULL_SUITE_UNSUPPORTED_BY_HOSTED_ENTRYPOINT"):
            validate_suite("full", platform="windows", entrypoint="hosted")

    def test_linux_is_cli_service_mini_only(self):
        with self.assertRaisesRegex(ValueError, "FULL_SUITE_UNSUPPORTED_PLATFORM"):
            validate_suite("full", platform="linux", entrypoint="local")

    def test_local_desktops_accept_full(self):
        for platform in ("windows", "macos"):
            with self.subTest(platform=platform):
                validate_suite("full", platform=platform, entrypoint="local")

    def test_set_is_unique_and_contains_required_semantics(self):
        scenarios = canonical_test_set()
        self.assertEqual(
            {scenario.id for scenario in scenarios},
            {
                "functional.configure",
                "functional.core-connection",
                "functional.start-stop-start",
                "functional.network-transition",
                "functional.product-process-loss",
            },
        )
        self.assertEqual(len(scenarios), 5)
        core = get_scenario("functional.core-connection")
        self.assertIn("traffic.stable", core.assertion_ids)
        self.assertIn("traffic.metrics_positive", core.assertion_ids)
        self.assertIn("routing.verified", core.assertion_ids)
        self.assertIn("cleanup.restored", core.assertion_ids)

    def test_every_qualification_configure_step_has_the_mobile_ui_budget(self):
        for scenario in suite_set("mini"):
            configure = next(
                step for step in scenario.steps if step.operation == "configure"
            )
            with self.subTest(scenario=scenario.id):
                self.assertEqual(configure.timeout_seconds, 60)

    def test_engine_returns_readable_scenario_result(self):
        result = FunctionalEngine().run(
            get_scenario("functional.core-connection"), FakeAdapter(), provenance(), connection()
        )
        payload = result.to_dict()
        self.assertEqual(
            set(payload),
            {"environment", "connection", "scenario", "outcome", "assertions", "measurements", "timing", "cleanup"},
        )
        self.assertEqual(payload["scenario"], {"id": "functional.core-connection"})
        self.assertEqual(payload["outcome"], "passed")
        self.assertEqual(payload["measurements"]["stability_sample_count"], 5)
        self.assertEqual(payload["measurements"]["stability_sample_interval_seconds"], 1.0)

    def test_failed_zero_measurement_does_not_mask_the_functional_result(self):
        class ZeroStabilityAdapter(FakeAdapter):
            def execute(self, step):
                if step.operation == "measure_stability":
                    return {
                        "stability_verified": False,
                        "stability_sample_count": 0,
                        "stability_sample_interval_seconds": STABILITY_SAMPLE_INTERVAL_SECONDS,
                    }
                return super().execute(step)

        payload = FunctionalEngine().run(
            get_scenario("functional.core-connection"),
            ZeroStabilityAdapter(),
            provenance(),
            connection(),
        ).to_dict()
        self.assertEqual(payload["outcome"], "failed")
        self.assertEqual(payload["failure"], {"code": "ASSERTION_FAILED"})
        self.assertNotIn("stability_sample_count", payload["measurements"])

    def test_failed_result_has_a_stable_failure_code(self):
        scenario = get_scenario("functional.configure")
        result = ScenarioResult(
            scenario_id=scenario.id,
            provenance=provenance(),
            connection=connection(),
            outcome="failed",
            assertions=tuple(evaluate_assertions(scenario.assertion_ids, {})),
            cleanup={"required": False, "verified": True},
            measurements={},
            elapsed_ms=4,
            failure_code="CONFIGURE_REJECTED",
        )
        self.assertEqual(result.to_dict()["failure"], {"code": "CONFIGURE_REJECTED"})

    def test_cleanup_provider_runs_without_becoming_result_evidence(self):
        calls = []
        FunctionalEngine().run(
            get_scenario("functional.configure"),
            FakeAdapter(),
            provenance(),
            connection(),
            cleanup_provider=lambda: calls.append(True),
        )
        self.assertEqual(calls, [True])

    def test_adapter_exception_propagates_unchanged(self):
        error = RuntimeError("exact adapter failure")

        class ExactBrokenAdapter(FakeAdapter):
            def execute(self, step):
                raise error

        with self.assertRaises(RuntimeError) as raised:
            FunctionalEngine().run(
                get_scenario("functional.configure"),
                ExactBrokenAdapter(),
                provenance(),
                connection(),
            )
        self.assertIs(raised.exception, error)


if __name__ == "__main__":
    unittest.main()
