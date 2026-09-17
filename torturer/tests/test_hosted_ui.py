from __future__ import annotations

import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

from torturer_checks.hosted.ui import HeadlessUIAdapter
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.scenarios import ScenarioDefinition, ScenarioStep


class FakeBase:
    capabilities = frozenset(Capability)
    capability_unavailable_reasons: dict[Capability, str] = {}

    def __init__(self) -> None:
        self.selected = None
        self.baselines = 0
        self.reset_calls = 0
        self.finalize_calls = 0

    def set_progress_sink(self, _sink) -> None:
        pass

    def discover_connections(self, timeout_seconds: float = 30.0):
        from torturer_contract.functional.results import ConnectionIdentity

        return (
            ConnectionIdentity(index=0, protocol="OUTLINE"),
            ConnectionIdentity(index=1, protocol="XRAY"),
        )

    def select_connection(self, connection) -> None:
        self.selected = connection

    def _emit_progress(self, _event: str, **_fields: object) -> None:
        pass

    def _capture_baseline(self, _timeout: float) -> None:
        self.baselines += 1

    def _connected(self, _timeout: float) -> bool:
        return True

    def _cleanup_verified(self, _timeout: float) -> bool:
        return True

    def execute(self, step: ScenarioStep) -> dict[str, object]:
        return {
            "tunnel_interface": True,
            "routing_verified": True,
            "cleanup_verified": True,
        } if step.operation in {"observe_tunnel", "observe_routing_identity", "inspect_cleanup"} else {}

    def reset(self, _timeout_seconds: float = 30.0) -> None:
        self.reset_calls += 1

    def finalize(self, _timeout_seconds: float = 30.0, *, deadline=None) -> None:
        self.finalize_calls += 1


class HeadlessUIAdapterTests(unittest.TestCase):
    def test_ui_actions_use_one_auto_selected_connection_and_delegate_observations(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile.toml"
            profile.write_text("[[Outline]]\nendpoint = 'example.invalid'\n", encoding="utf-8")
            companion = root / "companion.py"
            companion.write_text(
                "#!/usr/bin/env python3\n"
                "import json, sys\n"
                "for line in sys.stdin:\n"
                " request=json.loads(line)\n"
                " op=request.get('op')\n"
                " if op == 'configure': response={'ok': True, 'status': 'Ready', 'button': 'Connect'}\n"
                " elif op == 'connect': response={'ok': True, 'status': 'Connected', 'button': 'Disconnect'}\n"
                " elif op == 'disconnect': response={'ok': True, 'status': 'Disconnected', 'button': 'Connect'}\n"
                " elif op == 'close': response={'ok': True, 'status': 'Disconnected', 'button': 'Connect'}\n"
                " elif op == 'wait': response={'ok': True, 'status': request.get('state', 'Disconnected')}\n"
                " else: response={'ok': True, 'status': 'Disconnected'}\n"
                " print(json.dumps(response), flush=True)\n"
                " if op == 'close': break\n",
                encoding="utf-8",
            )
            companion.chmod(0o700)
            runner = mock.Mock(raw_directory=root / "raw", environment=dict())
            base = FakeBase()
            adapter = HeadlessUIAdapter(base=base, ui_test=companion, profile=profile, runner=runner)
            connection = adapter.discover_connections()[0]
            self.assertEqual(connection.protocol, "AUTO")
            self.assertEqual(connection.index, 0)
            adapter.select_connection(connection)
            scenario = ScenarioDefinition(
                id="functional.ui-smoke",
                steps=(
                    ScenarioStep(id="configure", operation="configure", timeout_seconds=8),
                    ScenarioStep(id="connect", operation="connect", timeout_seconds=8),
                    ScenarioStep(id="tunnel", operation="observe_tunnel", timeout_seconds=8),
                    ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=8),
                ),
                required_capabilities=frozenset(),
                assertion_ids=("configure.accepted",),
                max_duration_seconds=40,
            )
            observations = adapter.execute_scenario(scenario)
            self.assertTrue(observations["configured"])
            self.assertTrue(observations["tunnel_interface"])
            self.assertEqual(base.baselines, 1)
            adapter.reset()
            adapter.finalize()
            self.assertEqual(base.reset_calls, 1)
            self.assertEqual(base.finalize_calls, 1)
            self.assertIsNone(adapter._process)


if __name__ == "__main__":
    unittest.main()
