from __future__ import annotations

import math
import unittest

from torturer_contract.functional.android_observation import (
    AndroidObservationError,
    AndroidProfileObservation,
)


def _valid() -> dict[str, object]:
    return {
        "source_sha": "a" * 40,
        "connections": [{"index": 0, "protocol": "OUTLINE"}],
        "connection": {"index": 0, "protocol": "OUTLINE"},
        "configured": True,
        "connected": True,
        "tunnel_interface": True,
        "routing_verified": True,
        "stability_verified": True,
        "stability_sample_count": 5,
        "stability_sample_interval_seconds": 1.0,
        "network_transition_verified": True,
        "process_loss_verified": True,
        "latency_ms": 12.5,
        "download_mbps": 20.0,
        "upload_mbps": 10.0,
        "disconnect_clean": True,
        "restart_verified": True,
        "reconnect_completed": True,
        "second_tunnel_interface": True,
        "second_routing_verified": True,
        "final_disconnect_clean": True,
        "cleanup_verified": True,
    }


class AndroidObservationContractTests(unittest.TestCase):
    def test_accepts_observation_and_maps_engine_facts(self) -> None:
        observation = AndroidProfileObservation.from_mapping(_valid())
        self.assertEqual(observation.to_observations()["routing_verified"], True)
        self.assertNotIn("source_sha", observation.to_observations())
        self.assertNotIn("observed_ipv4", observation.to_observations())

    def test_requires_matching_source_sha_and_rejects_reported_errors(self) -> None:
        value = _valid()
        with self.assertRaisesRegex(AndroidObservationError, "source_sha"):
            AndroidProfileObservation.from_mapping(value, expected_source_sha="b" * 40)
        value["error_code"] = "DRIVER_ERROR"
        observation = AndroidProfileObservation.from_mapping(
            value, expected_source_sha="a" * 40
        )
        with self.assertRaisesRegex(AndroidObservationError, "reports an error"):
            observation.to_observations()
        value = _valid()
        value["error_code"] = "UNREVIEWED_CODE"
        observation = AndroidProfileObservation.from_mapping(
            value, expected_source_sha="a" * 40
        )
        with self.assertRaisesRegex(AndroidObservationError, "reports an error"):
            observation.to_observations()

    def test_any_driver_error_fails_functional_observation(self) -> None:
        value = _valid()
        value["error_code"] = "ROUTING_PROOF_FAILED"
        observation = AndroidProfileObservation.from_mapping(value)
        with self.assertRaisesRegex(AndroidObservationError, "reports an error"):
            observation.to_observations()
        value["error_code"] = "NEW_DRIVER_ERROR"
        observation = AndroidProfileObservation.from_mapping(value)
        with self.assertRaisesRegex(AndroidObservationError, "reports an error"):
            observation.to_observations()

    def test_ignores_extra_observation_fields(self) -> None:
        value = _valid()
        value["diagnostic"] = "available"
        AndroidProfileObservation.from_mapping(value)

    def test_rejects_invalid_source_identity_and_nonfinite_metrics(self) -> None:
        value = _valid()
        value["source_sha"] = "not-a-sha"
        with self.assertRaisesRegex(AndroidObservationError, "source_sha"):
            AndroidProfileObservation.from_mapping(value)
        value = _valid()
        value["latency_ms"] = math.nan
        with self.assertRaisesRegex(AndroidObservationError, "finite"):
            AndroidProfileObservation.from_mapping(value)


if __name__ == "__main__":
    unittest.main()
