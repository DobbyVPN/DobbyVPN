from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest
import time
from types import SimpleNamespace
from unittest import mock

from disposable_vpn_server.outline import OutlineWSSProfile
from torturer_contract.engine import ScenarioExecutionError
from torturer_contract.results import ConnectionIdentity
from torturer_contract.scenarios import select_scenarios
from torturer_runner import local_vm_android
from torturer_runner.adapters.android import (
    AndroidAdapter,
    _duplicate_single_android_profile,
    _MAIN_ACTIVITY,
    _PACKAGE_NAME,
)


FIXTURE_CA_PEM = "synthetic run-owned fixture CA\n"


def fixture_stub(
    root: Path, subscription_gets: int, profile_bytes: bytes = b"owner profile bytes"
) -> SimpleNamespace:
    certificate = root / "fixture-ca.pem"
    certificate.write_text(FIXTURE_CA_PEM, encoding="ascii")
    fixture = SimpleNamespace(
        url="https://127.0.0.1:54432/subscription",
        control_url="https://127.0.0.1:54432/control",
        control_key="fixture-key",
        certificate=certificate,
        profile_bytes=profile_bytes,
        replacement_calls=[],
        control_stats=lambda: {"subscription_gets": subscription_gets},
    )

    def replace_response(content: bytes) -> None:
        fixture.profile_bytes = content
        fixture.replacement_calls.append(content)

    fixture.replace_response = replace_response
    return fixture


def phase_adapter(root: Path) -> AndroidAdapter:
    adapter = object.__new__(AndroidAdapter)
    adapter.ui_mode = "gui-auto"
    adapter.runner = SimpleNamespace(raw_directory=root)
    adapter._active_controls = ()
    adapter._scratch_files = set()
    adapter._connections = (ConnectionIdentity(index=0, protocol="AUTO"),)
    adapter._selected_connection = adapter._connections[0]
    adapter.source_sha = None
    adapter._progress_scenario_id = "functional.configure"
    adapter.identity_url = None
    adapter.latency_url = None
    adapter.download_url = None
    adapter.upload_url = None
    adapter._process_cold_import_queued = False
    adapter._saved_source_restore_preverified = False
    adapter.profile = root / "owner-profile.toml"
    adapter.profile.write_bytes(b"immutable owner profile bytes")
    adapter._subscription_fixture = fixture_stub(
        root, 0, adapter.profile.read_bytes()
    )
    return adapter


def output_result(*, connected: bool = False) -> SimpleNamespace:
    connection = {"index": 0, "protocol": "AUTO"}
    observation = {
        "configured": True,
        "connected": connected,
        "connections": [connection],
        "connection": connection,
        "tunnel_interface": False,
        "routing_verified": False,
        "stability_verified": False,
        "stability_sample_count": 5,
        "stability_sample_interval_seconds": 1.0,
        "process_loss_verified": False,
        "latency_ms": 0.0,
        "download_mbps": 0.0,
        "upload_mbps": 0.0,
        "disconnect_clean": False,
        "restart_verified": False,
        "reconnect_completed": False,
        "second_tunnel_interface": False,
        "second_routing_verified": False,
        "final_disconnect_clean": False,
        "cleanup_verified": False,
        "coverage_lane": "gui-auto",
        "gui_auto_verified": True,
        "vpn_consent_handled": False,
    }
    return SimpleNamespace(
        returncode=0,
        stdout=json.dumps(observation).encode("utf-8"),
        stderr=b"",
        timed_out=False,
    )


class AndroidNativeUiColdLaunchTests(unittest.TestCase):
    def test_n11_single_profile_fixture_duplicates_only_the_profile_entry(self) -> None:
        source = OutlineWSSProfile(
            web_path="/dobby-test", secret="synthetic-secret"
        ).client_toml("https://vpn.invalid").encode("utf-8")

        duplicated = _duplicate_single_android_profile(source)

        self.assertIsNotNone(duplicated)
        original_config = tomllib.loads(source.decode("utf-8"))
        duplicated_config = tomllib.loads(duplicated.decode("utf-8"))
        self.assertEqual(
            duplicated_config["Outline"],
            [original_config["Outline"][0], original_config["Outline"][0]],
        )
        self.assertEqual(duplicated_config["ExcludeIPs"], original_config["ExcludeIPs"])

    def test_n11_single_nested_profile_fixture_duplicates_its_nested_tables(self) -> None:
        source = (
            b'[[TrustTunnel]]\n'
            b'Description = "single nested profile"\n'
            b'vpn_mode = "general"\n'
            b'\n[TrustTunnel.endpoint]\n'
            b'hostname = "vpn.invalid"\n'
            b'addresses = ["198.51.100.10:443"]\n'
            b'username = "synthetic-user"\n'
            b'password = "synthetic-secret"\n'
            b'\n[TrustTunnel.listener.socks]\n'
            b'address = "127.0.0.1:10808"\n'
            b'\n[ExcludeIPs]\nIPs = []\n'
        )

        duplicated = _duplicate_single_android_profile(source)

        self.assertIsNotNone(duplicated)
        original_config = tomllib.loads(source.decode("utf-8"))
        duplicated_config = tomllib.loads(duplicated.decode("utf-8"))
        self.assertEqual(
            duplicated_config["TrustTunnel"],
            [original_config["TrustTunnel"][0], original_config["TrustTunnel"][0]],
        )
        self.assertEqual(duplicated_config["ExcludeIPs"], original_config["ExcludeIPs"])

    def test_n11_multi_profile_fixture_preserves_protocol_matrix_bytes(self) -> None:
        source = (
            b'[[Outline]]\nDescription = "outline"\nServer = "outline.invalid"\n'
            b'\n[[Xray]]\noutbounds = [{ tag = "proxy", protocol = "vless" }]\n'
            b'\n[ExcludeIPs]\nIPs = []\n'
        )

        self.assertIsNone(_duplicate_single_android_profile(source))

    def test_reused_fixture_restores_only_for_configure(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = object.__new__(AndroidAdapter)
            adapter.ui_mode = "gui-auto"
            adapter.runner = SimpleNamespace(raw_directory=root)
            adapter._active_controls = ()
            adapter._scratch_files = set()
            adapter._selected_connection = None
            adapter.source_sha = None
            adapter.identity_url = None
            adapter.latency_url = None
            adapter.download_url = None
            adapter.upload_url = None
            adapter._process_cold_import_queued = False
            adapter.profile = root / "owner-profile.toml"
            owner_profile = b"immutable owner profile bytes"
            synthetic_profile = b"synthetic long-list profile bytes"
            adapter.profile.write_bytes(owner_profile)
            fixture = fixture_stub(root, 3, synthetic_profile)
            adapter._subscription_fixture = fixture
            configure = SimpleNamespace(
                id="functional-configure", operation="configure", timeout_seconds=30
            )

            first, _, _ = adapter._write_command(
                SimpleNamespace(), steps=(configure,)
            )
            fixture.profile_bytes = synthetic_profile
            second, _, _ = adapter._write_command(
                SimpleNamespace(), steps=(configure,)
            )

            first_command = json.loads(first.read_text(encoding="utf-8"))
            second_command = json.loads(second.read_text(encoding="utf-8"))
            self.assertTrue(first_command["process_cold_import"])
            self.assertEqual(first_command["process_cold_import_request_count"], 3)
            self.assertEqual(first_command["subscription_control_ca_pem"], FIXTURE_CA_PEM)
            self.assertNotIn("process_cold_import", second_command)
            self.assertEqual(fixture.profile_bytes, owner_profile)
            self.assertEqual(fixture.replacement_calls, [owner_profile, owner_profile])

            fixture.profile_bytes = synthetic_profile
            disconnect = SimpleNamespace(
                id="disconnect", operation="disconnect", timeout_seconds=10
            )
            adapter._write_command(SimpleNamespace(), steps=(disconnect,))
            self.assertEqual(fixture.profile_bytes, synthetic_profile)
            self.assertEqual(fixture.replacement_calls, [owner_profile, owner_profile])

    def test_consent_n11_command_dispatches_rendered_selection_after_configure(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = object.__new__(AndroidAdapter)
            adapter.ui_mode = "gui-auto"
            adapter.runner = SimpleNamespace(raw_directory=root)
            adapter._active_controls = ()
            adapter._scratch_files = set()
            adapter._selected_connection = None
            adapter.source_sha = None
            adapter.identity_url = None
            adapter.latency_url = None
            adapter.download_url = None
            adapter.upload_url = None
            adapter._process_cold_import_queued = False
            adapter.profile = root / "owner-profile.toml"
            adapter.profile.write_bytes(b"immutable owner profile bytes")
            adapter._subscription_fixture = fixture_stub(root, 0)
            steps = (
                SimpleNamespace(id="configure", operation="configure", timeout_seconds=90),
                SimpleNamespace(
                    id="consent-grant-selection",
                    operation="consent_grant_selection",
                    timeout_seconds=120,
                ),
            )

            command_file, _, _ = adapter._write_command(SimpleNamespace(), steps=steps)

            command = json.loads(command_file.read_text(encoding="utf-8"))
            self.assertEqual(
                [item["operation"] for item in command["operations"]],
                ["configure", "consent_grant_selection"],
            )
            self.assertTrue(command["process_cold_import"])
            self.assertEqual(command["process_cold_import_request_count"], 0)
            self.assertEqual(command["subscription_control_ca_pem"], FIXTURE_CA_PEM)

    def test_saved_source_phase_precedes_n11_and_full_lane_imports(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = phase_adapter(root)
            adapter.app_apk = root / "app.apk"
            adapter.test_companion_apk = root / "test.apk"
            fixture_stats = {"subscription_gets": 0}
            adapter._subscription_fixture.control_stats = lambda: dict(fixture_stats)
            commands: list[dict[str, object]] = []
            configure = SimpleNamespace(
                id="configure", operation="configure", timeout_seconds=90
            )
            consent_selection = SimpleNamespace(
                id="consent-grant-selection",
                operation="consent_grant_selection",
                timeout_seconds=120,
            )

            def instrument(command_name: str, _deadline: float, **_kwargs: object):
                command = json.loads((root / command_name).read_text(encoding="utf-8"))
                commands.append(command)
                if command.get("saved_source_restore_only"):
                    fixture_stats["subscription_gets"] = 2
                elif command.get("process_cold_import"):
                    self.assertEqual(
                        command["process_cold_import_request_count"],
                        fixture_stats["subscription_gets"],
                    )
                    fixture_stats["subscription_gets"] += 1
                return SimpleNamespace(returncode=0, stdout=b"", stderr=b"", timed_out=False)

            adapter._run_instrumentation = mock.Mock(side_effect=instrument)
            adapter._stage_private_file = mock.Mock()
            adapter._adb = mock.Mock(side_effect=lambda *_args, **_kwargs: output_result())
            scenario = select_scenarios(scenario_ids=["functional.configure"])[0]
            with mock.patch(
                "torturer_runner.adapters.android._instrumentation_succeeded",
                return_value=True,
            ):
                observation = adapter._execute_phase(
                    scenario,
                    (configure, consent_selection),
                    time.monotonic() + 120,
                    [],
                )

            self.assertTrue(observation.configured)
            self.assertFalse(observation.connected)
            self.assertEqual(len(commands), 2)
            self.assertTrue(commands[0]["saved_source_restore_only"])
            self.assertNotIn("process_cold_import", commands[0])
            self.assertTrue(commands[1]["saved_source_restore_preverified"])
            self.assertTrue(commands[1]["process_cold_import"])
            self.assertEqual(commands[1]["process_cold_import_request_count"], 2)

            # N11's exact APK reinstall invalidates its first-process import
            # queue. Keep only the successful restore observation so the full
            # lane runs its own cold import without repeating that restore.
            def install_result(arguments, *_args, **_kwargs):
                return SimpleNamespace(
                    stdout_text=(
                        "package:/data/app/com.dobby.vpn/base.apk"
                        if arguments[:3] == ("shell", "pm", "path")
                        else ""
                    )
                )

            adapter._process_cold_import_queued = True
            adapter._adb = mock.Mock(side_effect=install_result)
            adapter._install_fresh_apk_pair(time.monotonic() + 120)
            self.assertFalse(adapter._process_cold_import_queued)
            adapter._adb = mock.Mock(side_effect=lambda *_args, **_kwargs: output_result())
            with mock.patch(
                "torturer_runner.adapters.android._instrumentation_succeeded",
                return_value=True,
            ):
                observation = adapter._execute_phase(
                    scenario,
                    scenario.steps,
                    time.monotonic() + 120,
                    [],
                )

            self.assertTrue(observation.configured)
            self.assertEqual(len(commands), 3)
            self.assertTrue(commands[2]["saved_source_restore_preverified"])
            self.assertTrue(commands[2]["process_cold_import"])
            self.assertEqual(commands[2]["process_cold_import_request_count"], 3)
            self.assertEqual(fixture_stats["subscription_gets"], 4)

    def test_failed_saved_source_phase_blocks_process_cold_command(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = phase_adapter(root)
            commands: list[dict[str, object]] = []

            def instrument(command_name: str, _deadline: float, **_kwargs: object):
                commands.append(json.loads((root / command_name).read_text(encoding="utf-8")))
                return SimpleNamespace(returncode=0, stdout=b"", stderr=b"", timed_out=False)

            adapter._run_instrumentation = mock.Mock(side_effect=instrument)
            adapter._stage_private_file = mock.Mock()
            adapter._adb = mock.Mock(
                side_effect=lambda *_args, **_kwargs: output_result(connected=True)
            )
            scenario = select_scenarios(scenario_ids=["functional.configure"])[0]
            with (
                mock.patch(
                    "torturer_runner.adapters.android._instrumentation_succeeded",
                    return_value=True,
                ),
                self.assertRaisesRegex(
                    ScenarioExecutionError,
                    "ANDROID_SAVED_SOURCE_RESTORE_PREPHASE_INVALID",
                ),
            ):
                adapter._execute_phase(
                    scenario,
                    scenario.steps,
                    time.monotonic() + 120,
                    [],
                )

            self.assertEqual(len(commands), 1)
            self.assertTrue(commands[0]["saved_source_restore_only"])
            self.assertNotIn("process_cold_import", commands[0])

    def test_consent_n11_reinstalls_exact_apk_pair_around_rendered_case(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            app_apk = root / "app.apk"
            companion_apk = root / "test.apk"
            app_apk.write_bytes(b"app")
            companion_apk.write_bytes(b"test")
            adapter = object.__new__(AndroidAdapter)
            adapter.ui_mode = "gui-auto"
            adapter.app_apk = app_apk
            adapter.test_companion_apk = companion_apk
            adapter._progress_scenario_id = None
            adapter._active_controls = ()
            adapter._scratch_files = set()
            adapter._diagnostic_collection_sequence = 0
            adapter._connections = ()
            profile = root / "profile.toml"
            source_profile = (
                b'[[Outline]]\n'
                b'Description = "single"\n'
                b'Server = "vpn.invalid"\n'
                b'Port = 443\n'
                b'Password = "synthetic-secret"\n'
                b'\n[ExcludeIPs]\nIPs = []\n'
            )
            profile.write_bytes(source_profile)
            adapter.profile = profile
            adapter._subscription_fixture = None
            adapter._process_cold_import_queued = False
            events: list[str] = []
            expected = {
                "source_verified": True,
                "digest_verified": True,
                "profile_identity_verified": True,
                "mode": "PROFILE_INDEX",
                "index": 1,
                "protocol": "OUTLINE",
                "generation": 2,
                "generation_advanced": True,
                "disconnect_clean": True,
            }
            lifecycle_valid = False

            def execute_phase(_scenario, steps, _deadline, _device_files):
                events.append("rendered-case")
                self.assertNotEqual(adapter.profile, profile)
                temporary_config = tomllib.loads(
                    adapter.profile.read_text(encoding="utf-8")
                )
                self.assertEqual(len(temporary_config["Outline"]), 2)
                self.assertEqual(
                    temporary_config["Outline"][0],
                    temporary_config["Outline"][1],
                )
                self.assertEqual(
                    [step.operation for step in steps],
                    ["configure", "consent_grant_selection"],
                )
                adapter._subscription_fixture = SimpleNamespace(
                    close=lambda: events.append("fixture-close")
                )
                return SimpleNamespace(
                    configured=lifecycle_valid,
                    connected=True,
                    vpn_consent_handled=True,
                    disconnect_clean=True,
                    final_disconnect_clean=True,
                    cleanup_verified=True,
                    consent_grant_selection=expected,
                    gui_auto_verified=True,
                )

            adapter._install_fresh_apk_pair = lambda _deadline: events.append("install")
            adapter._execute_phase = execute_phase
            adapter._validate_gui_observation = lambda *_args, **_kwargs: None
            adapter._cleanup_device = lambda *_args: events.append("cleanup")
            adapter._cleanup_local_scratch = lambda: None
            adapter._collect_functional_failure_diagnostics = lambda *_args: None

            with self.assertRaisesRegex(
                ScenarioExecutionError,
                "ANDROID_CONSENT_N11_LIFECYCLE_OBSERVATION_INVALID",
            ):
                adapter.run_unchanged_consent_selection(
                    deadline=time.monotonic() + 240,
                )
            self.assertEqual(
                events,
                ["install", "rendered-case", "cleanup", "fixture-close"],
            )
            self.assertEqual(adapter.profile, profile)
            self.assertEqual(profile.read_bytes(), source_profile)
            self.assertFalse(any(root.glob(".android-n11-*.toml")))
            self.assertIsNone(adapter._subscription_fixture)
            events.clear()
            lifecycle_valid = True

            result = adapter.run_unchanged_consent_selection(
                deadline=time.monotonic() + 240,
            )

            self.assertEqual(
                events,
                ["install", "rendered-case", "cleanup", "fixture-close", "install"],
            )
            self.assertEqual(result, {"passed": True, **expected})
            self.assertEqual(adapter.profile, profile)
            self.assertEqual(profile.read_bytes(), source_profile)
            self.assertFalse(any(root.glob(".android-n11-*")))
            self.assertIsNone(adapter._subscription_fixture)

    def test_hosted_valid_import_launches_after_force_stop_before_instrumentation(self) -> None:
        adapter = object.__new__(AndroidAdapter)
        adapter.ui_mode = "gui-auto"
        adapter._active_controls = ()
        adapter._progress_sink = None
        calls: list[tuple[tuple[str, ...], dict[str, object]]] = []

        def adb(arguments: tuple[str, ...], _timeout: float, _failure: str, **options: object):
            calls.append((arguments, options))
            output = b"Status: ok\nComplete\n" if "start" in arguments else b""
            return SimpleNamespace(returncode=0, stdout=output, stderr=b"", timed_out=False)

        adapter._adb = adb  # type: ignore[method-assign]
        url = "https://127.0.0.1:54432/subscription"

        result = adapter._run_instrumentation(
            "android-hosted.command.json",
            time.monotonic() + 20,
            process_cold_import_url=url,
        )

        self.assertEqual(result.returncode, 0)
        self.assertEqual(calls[0][0], ("shell", "am", "force-stop", _PACKAGE_NAME))
        self.assertEqual(
            calls[1][0],
            (
                "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW",
                "-d", "dobbyvpn://import?url=https%3A%2F%2F127.0.0.1%3A54432%2Fsubscription",
                "-n", _MAIN_ACTIVITY,
            ),
        )
        self.assertEqual(calls[2][0][:6], ("shell", "am", "instrument", "-w", "-r", "--no-restart"))

    def _run_ui(
        self, start_output: bytes, native_cases: list[str] | None = None,
        instrumentation_succeeded: bool = True,
    ) -> tuple[
        subprocess.CompletedProcess[bytes] | None,
        Exception | None,
        list[tuple[str, list[str]]],
        list[tuple[int, bytes, bytes]],
    ]:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            logs = root / "logs"
            logs.mkdir()
            calls: list[tuple[str, list[str]]] = []
            parsed_inputs: list[tuple[int, bytes, bytes]] = []

            def parse_result(*, returncode: int, stdout: bytes, stderr: bytes):
                parsed_inputs.append((returncode, stdout, stderr))
                return SimpleNamespace(succeeded=instrumentation_succeeded)

            def adb_call(_adb, _serial, arguments, *, label, **_kwargs):
                calls.append((label, arguments))
                if label in {
                    "android-native-ui-cold-bare-link-start",
                    "android-clear-boundary-process-restart-launch",
                }:
                    return subprocess.CompletedProcess(("adb",), 0, start_output, b"")
                if label == "android-native-ui":
                    return subprocess.CompletedProcess(("adb",), 0, b"instrumentation output\n", b"")
                if label == "android-clear-boundary-process-restart-test":
                    return subprocess.CompletedProcess(
                        ("adb",), 0,
                        b"restart instrumentation stdout\n",
                        b"restart instrumentation stderr\n",
                    )
                return subprocess.CompletedProcess(("adb",), 0, b"", b"")

            with (
                mock.patch.dict(os.environ, {"ADB_SERVER_SOCKET": "tcp:localhost:5037"}),
                mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                mock.patch.object(
                    local_vm_android,
                    "parse_instrumentation_result",
                    side_effect=parse_result,
                ),
                mock.patch.object(local_vm_android, "_collect_rendered_screenshots", return_value=[]),
                mock.patch.object(local_vm_android, "_collect_launcher_artwork", return_value=[]),
                mock.patch.object(local_vm_android, "_collect_android_diagnostics", return_value=[]),
            ):
                try:
                    result = local_vm_android.run_ui(
                        root,
                        {"adb": "adb", "serial": "emulator-5554"},
                        logs,
                        timeout=30,
                        native_cases=native_cases,
                    )
                except Exception as error:
                    return None, error, calls, parsed_inputs
                return result, None, calls, parsed_inputs

    def test_force_stop_then_implicit_bare_link_start_precedes_instrumentation(self) -> None:
        result, error, calls, _parsed_inputs = self._run_ui(
            b"Starting: Intent\nStatus: ok\nLaunchState: COLD\nComplete\n"
        )

        self.assertIsNone(error)
        self.assertIsNotNone(result)
        self.assertEqual(result.returncode, 0)
        self.assertIn(b"--- ANDROID CLEAR PROCESS-RESTART CHECK ---", result.stdout)
        self.assertEqual(
            calls[:3],
            [
                (
                    "android-native-ui-cold-start",
                    ["shell", "am", "force-stop", local_vm_android.APP_PACKAGE],
                ),
                (
                    "android-native-ui-cold-bare-link-start",
                    [
                        "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW",
                        "-d", "dobbyvpn://",
                    ],
                ),
                (
                    "android-native-ui",
                    [
                        "shell", "am", "instrument", "-w", "-r", "-e", "class",
                        "com.dobby.NativeUiInstrumentedTest,com.dobby.NativeDiagnosticRetentionTest",
                        "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner",
                    ],
                ),
            ],
        )
        self.assertEqual(
            calls[3:],
            [
                (
                    "android-clear-boundary-process-death",
                    ["shell", "am", "force-stop", local_vm_android.APP_PACKAGE],
                ),
                (
                    "android-clear-boundary-process-restart-launch",
                    ["shell", "am", "start", "-W", "-n", "com.dobby.vpn/com.dobby.ui.MainActivity"],
                ),
                (
                    "android-clear-boundary-process-restart-test",
                    [
                        "shell", "am", "instrument", "-w", "-r", "--no-restart",
                        "-e", "class",
                        "com.dobby.NativeUiClearProcessRestartTest#"
                        "clearBoundarySurvivesAppProcessDeath",
                        "com.dobby.vpn.test/androidx.test.runner.AndroidJUnitRunner",
                    ],
                ),
            ],
        )

    def test_small_screen_case_runs_only_its_exact_instrumentation_method(self) -> None:
        result, error, calls, _parsed_inputs = self._run_ui(
            b"Starting: Intent\nStatus: ok\nLaunchState: COLD\nComplete\n",
            ["small-screen-log-viewport"],
        )

        self.assertIsNone(error)
        self.assertEqual(result.returncode, 0)
        instrument = next(arguments for label, arguments in calls if label == "android-native-ui")
        self.assertEqual(
            instrument[instrument.index("-e") + 2],
            "com.dobby.NativeUiSmallScreenLogViewportTest#smallScreenLogViewportIsUsable",
        )
        self.assertFalse(any("clear-boundary-process" in label for label, _ in calls))

    def test_clear_process_restart_case_runs_exact_method_then_rechecks_after_restart(self) -> None:
        result, error, calls, parsed_inputs = self._run_ui(
            b"Starting: Intent\nStatus: ok\nLaunchState: COLD\nComplete\n",
            ["logs-clear-process-restart"],
        )

        self.assertIsNone(error)
        self.assertIsNotNone(result)
        self.assertEqual(result.returncode, 0)
        self.assertIn(b"--- ANDROID CLEAR PROCESS-RESTART CHECK ---", result.stdout)
        initial_instrument = next(
            arguments for label, arguments in calls if label == "android-native-ui"
        )
        self.assertEqual(
            initial_instrument[initial_instrument.index("-e") + 2],
            "com.dobby.NativeUiInstrumentedTest#clearBoundaryBeforeProcessRestart",
        )
        self.assertEqual(
            [label for label, _ in calls if "clear-boundary-process" in label],
            [
                "android-clear-boundary-process-death",
                "android-clear-boundary-process-restart-launch",
                "android-clear-boundary-process-restart-test",
            ],
        )
        self.assertEqual(len(parsed_inputs), 2)
        self.assertEqual(parsed_inputs[-1][1], result.stdout)
        self.assertIn(b"instrumentation output\n", parsed_inputs[-1][1])
        self.assertIn(b"--- ANDROID CLEAR PROCESS-RESTART CHECK ---", parsed_inputs[-1][1])
        self.assertIn(b"restart instrumentation stdout\n", parsed_inputs[-1][1])
        self.assertEqual(parsed_inputs[-1][2], result.stderr)
        self.assertIn(b"restart instrumentation stderr\n", parsed_inputs[-1][2])

    def test_clear_process_restart_case_skips_controller_restart_after_failure(self) -> None:
        _result, error, calls, _parsed_inputs = self._run_ui(
            b"Starting: Intent\nStatus: ok\nLaunchState: COLD\nComplete\n",
            ["logs-clear-process-restart"],
            instrumentation_succeeded=False,
        )

        self.assertIsNone(error)
        self.assertFalse(any("clear-boundary-process" in label for label, _ in calls))

    def test_missing_foreground_marker_stops_before_instrumentation(self) -> None:
        for output in (b"Status: ok\n", b"Complete\n"):
            with self.subTest(output=output):
                result, error, calls, _parsed_inputs = self._run_ui(output)
                self.assertIsNone(result)
                self.assertIsNotNone(error)
                self.assertIn("cold bare-link foreground launch", str(error))
                self.assertEqual(
                    [label for label, _ in calls],
                    [
                        "android-native-ui-cold-start",
                        "android-native-ui-cold-bare-link-start",
                    ],
                )


if __name__ == "__main__":
    unittest.main()
