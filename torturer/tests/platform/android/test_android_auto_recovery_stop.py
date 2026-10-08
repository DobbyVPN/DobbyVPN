from __future__ import annotations

import json
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.android_observation import AndroidProfileObservation
from torturer_contract.results import ConnectionIdentity
from torturer_contract.scenarios import select_scenarios
from torturer_runner.adapters.android import AndroidAdapter


class AndroidAutoRecoveryStopAdapterTests(unittest.TestCase):
    def test_tagged_case_command_suppresses_only_process_cold_import(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = command_adapter(root)
            scenario = select_scenarios(
                scenario_ids=["functional.core-connection"]
            )[0]

            command_file, _, _ = adapter._write_command(
                scenario,
                test_case="android:auto-recovery-stop",
            )

            command = json.loads(command_file.read_text(encoding="utf-8"))
            self.assertEqual(command["test_case"], "android:auto-recovery-stop")
            self.assertEqual(
                command["subscription_control_ca_pem"],
                (root / "fixture-ca.pem").read_text(encoding="ascii"),
            )
            self.assertEqual(
                [step["operation"] for step in command["operations"]],
                [step.operation for step in scenario.steps],
            )
            self.assertNotIn("process_cold_import", command)

    def test_native_case_facts_are_separate_and_screenshot_paths_are_retained(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = object.__new__(AndroidAdapter)
            adapter.runner = SimpleNamespace(raw_directory=root)
            token = "android-hosted-case"
            screenshot_root = root / "screenshots" / "android" / token
            screenshot_root.mkdir(parents=True)
            stop_label = "0012-disconnect-recovery-stop-state.png"
            idle_label = "0020-disconnect-disconnected-state.png"
            (screenshot_root / stop_label).write_bytes(b"stop frame")
            (screenshot_root / idle_label).write_bytes(b"idle frame")

            facts = adapter._validated_auto_recovery_stop_facts(
                recovery_facts(stop_label, idle_label),
                f"{token}.command.json",
            )

            self.assertEqual(
                facts["screenshot_paths"],
                {
                    "recovery_stop": str(screenshot_root / stop_label),
                    "disconnected": str(screenshot_root / idle_label),
                },
            )
            self.assertEqual(facts["recovery_generation"], 8)

    def test_run_uses_core_connection_steps_and_returns_independent_case_facts(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = object.__new__(AndroidAdapter)
            adapter.ui_mode = "gui-auto"
            adapter.app_apk = root / "tagged.apk"
            adapter.test_companion_apk = root / "tagged-test.apk"
            adapter.source_sha = "a" * 40
            adapter.runner = SimpleNamespace(raw_directory=root)
            adapter._active_controls = ()
            adapter._connections = ()
            adapter._selected_connection = None
            adapter._progress_scenario_id = None
            adapter._subscription_fixture = None
            adapter._scratch_files = set()
            adapter._process_cold_import_queued = False
            native_facts = recovery_facts(
                "0012-disconnect-recovery-stop-state.png",
                "0020-disconnect-disconnected-state.png",
            )
            standard_observation = core_connection_observation()
            captured: dict[str, object] = {}

            def execute_phase(_scenario, steps, _deadline, _files, **kwargs):
                captured["steps"] = steps
                captured["test_case"] = kwargs["test_case"]
                kwargs["native_case_facts"].update(native_facts)
                return standard_observation

            adapter._install_fresh_apk_pair = mock.Mock()
            adapter._execute_phase = mock.Mock(side_effect=execute_phase)
            adapter._validate_observation_identity = mock.Mock()
            adapter._validate_gui_observation = mock.Mock()
            adapter._cleanup_device = mock.Mock(return_value=None)
            adapter._cleanup_local_scratch = mock.Mock(return_value=None)

            result = adapter.run_auto_recovery_stop(
                deadline=time.monotonic() + 80
            )

            canonical = select_scenarios(
                scenario_ids=["functional.core-connection"]
            )[0]
            self.assertEqual(result["case_id"], "android:auto-recovery-stop")
            self.assertTrue(result["passed"])
            self.assertEqual(result["scenario_id"], canonical.id)
            self.assertEqual(
                [item["id"] for item in result["assertions"]],
                list(canonical.assertion_ids),
            )
            self.assertEqual(captured["test_case"], "android:auto-recovery-stop")
            self.assertEqual(
                [(step.id, step.operation) for step in captured["steps"]],
                [(step.id, step.operation) for step in canonical.steps],
            )
            disconnect = next(
                step for step in captured["steps"] if step.operation == "disconnect"
            )
            self.assertEqual(disconnect.timeout_seconds, 45)
            self.assertIn("native_case_facts", result)
            self.assertNotIn("native_case_facts", result["observations"])
            adapter._install_fresh_apk_pair.assert_called_once()


def command_adapter(root: Path) -> AndroidAdapter:
    adapter = object.__new__(AndroidAdapter)
    adapter.ui_mode = "gui-auto"
    adapter.runner = SimpleNamespace(raw_directory=root)
    adapter._active_controls = ()
    adapter._scratch_files = set()
    adapter._selected_connection = None
    adapter.source_sha = None
    adapter.identity_url = "https://identity.example.test"
    adapter.latency_url = "https://latency.example.test/?bytes=1"
    adapter.download_url = "https://download.example.test/?bytes=1024"
    adapter.upload_url = "https://upload.example.test"
    adapter._process_cold_import_queued = False
    certificate = root / "fixture-ca.pem"
    certificate.write_text("synthetic run-owned fixture CA\n", encoding="ascii")
    adapter._subscription_fixture = SimpleNamespace(
        url="https://127.0.0.1:54432/subscription",
        control_url="https://127.0.0.1:54432/control",
        control_key="fixture-key",
        certificate=certificate,
        control_stats=lambda: {"subscription_gets": 3},
    )
    return adapter


def recovery_facts(stop_label: str, idle_label: str) -> dict[str, object]:
    return {
        "case_id": "android:auto-recovery-stop",
        "passed": True,
        "seam_enabled": True,
        "seam_armed": True,
        "arm_after_tunnel_route_and_traffic": True,
        "initial_generation": 7,
        "recovery_generation": 8,
        "recovery_observed": True,
        "recovery_hold_sample_count": 10,
        "recovery_poll_interval_ms": 100,
        "recovery_ui_reconnecting": True,
        "main_stop_visible": True,
        "main_stop_enabled": True,
        "competing_profile_action_count": 12,
        "competing_profile_actions_disabled": True,
        "profile_actions_verified_while_visible": True,
        "stop_clicked": True,
        "final_generation": 8,
        "final_idle": True,
        "cleanup_complete": True,
        "recovering_cleared": True,
        "pending_cleared": True,
        "active_profile_cleared": True,
        "vpn_network_absent": True,
        "no_later_generation": True,
        "final_stable_sample_count": 10,
        "stop_screenshot_label": stop_label,
        "idle_screenshot_label": idle_label,
        "screenshot_labels": [stop_label, idle_label],
    }


def core_connection_observation() -> AndroidProfileObservation:
    connection = ConnectionIdentity(index=0, protocol="AUTO")
    return AndroidProfileObservation(
        source_sha="a" * 40,
        connections=(connection,),
        connection=connection,
        configured=True,
        connected=True,
        tunnel_interface=True,
        routing_verified=True,
        stability_verified=True,
        stability_sample_count=5,
        stability_sample_interval_seconds=1.0,
        process_loss_verified=False,
        latency_ms=1.0,
        download_mbps=1.0,
        upload_mbps=1.0,
        disconnect_clean=True,
        restart_verified=False,
        reconnect_completed=False,
        second_tunnel_interface=False,
        second_routing_verified=False,
        final_disconnect_clean=True,
        cleanup_verified=True,
        coverage_lane="gui-auto",
        gui_auto_verified=True,
        ui_reopen_verified=True,
        vpn_consent_handled=True,
    )


if __name__ == "__main__":
    unittest.main()
