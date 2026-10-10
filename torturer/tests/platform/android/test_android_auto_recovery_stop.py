from __future__ import annotations

import json
from dataclasses import asdict
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.android_observation import AndroidProfileObservation
from torturer_contract.engine import ScenarioExecutionError
from torturer_contract.results import ConnectionIdentity
from torturer_contract.scenarios import select_scenarios
from torturer_runner.adapters.android import AndroidAdapter


class AndroidAutoRecoveryStopAdapterTests(unittest.TestCase):
    def test_failure_observation_precedes_absent_success_facts(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = command_adapter(root)
            scenario = select_scenarios(
                scenario_ids=["functional.core-connection"]
            )[0]
            value = asdict(core_connection_observation())
            value.update(
                {
                    "test_case": "android:auto-recovery-stop",
                    "error_code": "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_ENABLED",
                    "ui_operation": "disconnect",
                    "ui_phase": "recovery-arm",
                    "ui_phase_state": "failed",
                }
            )
            output = SimpleNamespace(
                stdout=json.dumps(value).encode("utf-8"), stderr=b""
            )
            adapter._stage_private_file = mock.Mock()
            adapter._run_instrumentation = mock.Mock(
                return_value=SimpleNamespace(
                    returncode=0, stdout=b"", stderr=b"", timed_out=False
                )
            )
            adapter._adb = mock.Mock(return_value=output)
            native_case_facts: dict[str, object] = {}

            with (
                mock.patch(
                    "torturer_runner.adapters.android._instrumentation_succeeded",
                    return_value=True,
                ),
                self.assertRaises(ScenarioExecutionError) as caught,
            ):
                adapter._execute_phase(
                    scenario,
                    scenario.steps,
                    time.monotonic() + 60,
                    [],
                    test_case="android:auto-recovery-stop",
                    native_case_facts=native_case_facts,
                )

            self.assertEqual(
                caught.exception.reason_code,
                "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_ENABLED",
            )
            self.assertEqual(native_case_facts, {})

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
            stopped_label = "0020-disconnect-stopped-state.png"
            (screenshot_root / stop_label).write_bytes(b"stop frame")
            (screenshot_root / stopped_label).write_bytes(b"stopped frame")

            facts = adapter._validated_auto_recovery_stop_facts(
                recovery_facts(stop_label, stopped_label),
                f"{token}.command.json",
            )

            self.assertEqual(
                facts["screenshot_paths"],
                {
                    "recovery_stop": str(screenshot_root / stop_label),
                    "stopped": str(screenshot_root / stopped_label),
                },
            )
            self.assertEqual(facts["recovery_generation"], 8)
            self.assertEqual(facts["rendered_status"], "Failed")

            invalid_failure = recovery_facts(stop_label, stopped_label)
            invalid_failure["rendered_failure_message"] = "unrelated runtime failure"
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._validated_auto_recovery_stop_facts(
                    invalid_failure,
                    f"{token}.command.json",
                )
            self.assertEqual(
                caught.exception.reason_code,
                "ANDROID_AUTO_RECOVERY_STOP_RENDERED_FAILURE_INVALID",
            )

            disconnected = recovery_facts(stop_label, stopped_label)
            disconnected.update(
                {
                    "rendered_status": "Disconnected",
                    "rendered_failure_code": "",
                    "rendered_failure_message": "",
                }
            )
            self.assertEqual(
                adapter._validated_auto_recovery_stop_facts(
                    disconnected,
                    f"{token}.command.json",
                )["rendered_status"],
                "Disconnected",
            )

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
                "0020-disconnect-stopped-state.png",
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
    adapter.profile = root / "owner-profile.toml"
    adapter.profile.write_bytes(b"immutable owner profile bytes")
    certificate = root / "fixture-ca.pem"
    certificate.write_text("synthetic run-owned fixture CA\n", encoding="ascii")
    fixture = SimpleNamespace(
        url="https://127.0.0.1:54432/subscription",
        control_url="https://127.0.0.1:54432/control",
        control_key="fixture-key",
        certificate=certificate,
        profile_bytes=b"synthetic fixture response",
        replacement_calls=[],
        control_stats=lambda: {"subscription_gets": 3},
    )

    def replace_response(content: bytes) -> None:
        fixture.profile_bytes = content
        fixture.replacement_calls.append(content)

    fixture.replace_response = replace_response
    adapter._subscription_fixture = fixture
    return adapter


def recovery_facts(stop_label: str, stopped_label: str) -> dict[str, object]:
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
        "competing_profile_actions_enabled_after_stop": True,
        "primary_auto_connect_enabled": True,
        "rendered_status": "Failed",
        "rendered_failure_code": "RUNTIME_FAILED",
        "rendered_failure_message": "connected health check failed: test recovery Stop health fault",
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
        "stopped_screenshot_label": stopped_label,
        "screenshot_labels": [stop_label, stopped_label],
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
