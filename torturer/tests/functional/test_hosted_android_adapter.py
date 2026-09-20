from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shlex
import tempfile
import time
import unittest
from unittest.mock import patch

from torturer_checks.android_instrumentation import ROUTING_RULE_CHAIN
from torturer_checks.hosted.android import (
    AndroidCompositeHostedAdapter,
    AndroidHostedAdapter,
    _observation_error_code,
    _GUI_PROFILE_MAX_BYTES,
    _scenario_deadlines,
    _select_gui_profile,
)
from torturer_checks.hosted.cli import CommandResult, HostedAdapterError
from torturer_checks.hosted.factory import (
    PUBLIC_DOWNLOAD_URL,
    PUBLIC_IDENTITY_URL,
    PUBLIC_LATENCY_URL,
    PUBLIC_UPLOAD_URL,
    adapter_for_platform,
)
from torturer_contract.functional.engine import FunctionalEngine, ScenarioExecutionError
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.android_observation import AndroidObservationError
from torturer_contract.functional.results import ConnectionIdentity, RunProvenance
from torturer_contract.functional.scenarios import get_scenario


_SOURCE_SHA = "a" * 40
_PROFILE_BUNDLE = (
    b"[[TrustTunnel]]\n"
    b"name = 'emulator-excluded'\n\n"
    b"[[Outline]] # rendered representative\n"
    b"Server = 'vpn.invalid'\n"
    b"Port = 443\n"
    b"Password = 'synthetic-outline'\n\n"
    b"[[Xray]]\n"
    b"outbounds = []\n"
)
_GUI_PROFILE = (
    b"[[Outline]] # rendered representative\n"
    b"Server = 'vpn.invalid'\n"
    b"Port = 443\n"
    b"Password = 'synthetic-outline'\n\n"
)


def _observation(error_code: str | None = None, **overrides: object) -> bytes:
    value: dict[str, object] = {
        "source_sha": _SOURCE_SHA,
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
    if error_code is not None:
        value["error_code"] = error_code
    value.update(overrides)
    return (json.dumps(value) + "\n").encode("utf-8")


class FakeAndroidRunner:
    def __init__(self, raw_directory: Path) -> None:
        self.raw_directory = raw_directory
        self.raw_directory.mkdir(mode=0o700, parents=True)
        self.calls: list[tuple[str, ...]] = []
        self.timeouts: list[float] = []
        self.command_payload: dict[str, object] | None = None
        self.command_payloads: list[dict[str, object]] = []
        self.observation = _observation()
        self.observations: list[bytes] = []
        self.instrumentation = b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n"
        self.fail_cleanup = False
        self.routing_phases: dict[str, str] = {}
        self.routing_counter_reads = 0
        self.routing_counter_values = [(100, 200), (101, 201)]
        self.routing_rule_packets = 1
        self.routing_rule_protocol = "tcp"
        self.routing_rule_interface = "eth0"
        self.routing_rule_destinations = ("203.0.113.10",)
        self.routing_ready_override: dict[str, object] | None = None
        self.routing_blocked_override: dict[str, object] | None = None
        self.routing_unblocked_override: dict[str, object] | None = None
        self.routing_insert_failure: CommandResult | None = None
        self.routing_remove_failure: CommandResult | None = None
        self.routing_cleanup_residual = False
        self.routing_cleanup_inventory_failure = False
        self.activity_start_result: CommandResult | None = None
        self.staged_control_payloads: list[tuple[str, dict[str, object]]] = []
        self.staged_profile_payloads: list[tuple[str, bytes]] = []

    def _record_staged_payload(self, argv: tuple[str, ...], payload: bytes) -> None:
        try:
            value = json.loads(payload.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            return
        if not isinstance(value, dict):
            return
        destinations = re.findall(r"/files/([A-Za-z0-9._-]+)", argv[-1])
        if not destinations:
            return
        # Atomic staging mentions the sibling temp path before the final
        # destination; model the app-visible final name.
        name = destinations[-1]
        operation = value.get("operation")
        phase = value.get("phase")
        self.staged_control_payloads.append((name, value))
        if phase in {"blocked", "unblocked", "ready"}:
            self.routing_phases[f"{name}.ready"] = phase
        elif phase == "finish":
            self.routing_phases.pop(f"{name}.ready", None)
        elif operation == "network_transition":
            self.routing_phases[f"{name}.routing.ready"] = "ready"

    def run(self, command, *, timeout_seconds, input_bytes=None):
        argv = tuple(command)
        self.calls.append(argv)
        self.timeouts.append(float(timeout_seconds))
        if input_bytes is not None and ".command.json" in argv[-1]:
            self.command_payload = json.loads(input_bytes.decode("utf-8"))
            self.command_payloads.append(self.command_payload)
            self.routing_counter_reads = 0
            for item in self.command_payload.get("operations", []):
                control_file = item.get("control_file")
                if isinstance(control_file, str):
                    self.routing_phases[f"{control_file}.ready"] = "ready"
                    if item.get("operation") == "network_transition":
                        self.routing_phases[
                            f"{control_file}.routing.ready"
                        ] = "ready"
        if input_bytes is not None and argv[1:4] == ("shell", "-T", "sh"):
            destinations = re.findall(r"/files/([A-Za-z0-9._-]+)", argv[-1])
            if destinations and destinations[-1].endswith(".profile"):
                self.staged_profile_payloads.append(
                    (destinations[-1], bytes(input_bytes))
                )
            self._record_staged_payload(argv, input_bytes)
        tail = argv[1:]
        shell_script = None
        if tail[:4] == ("shell", "-T", "sh", "-c"):
            shell_script = shlex.split(tail[4])[0]
        cat_paths = tail[3:] if tail[:3] == ("shell", "-T", "cat") else None
        if tail[:3] == ("shell", "sh", "-c") and "test -f" in tail[3]:
            match = re.search(r"/files/([A-Za-z0-9._-]+)", tail[3])
            present = match is not None and match.group(1) in self.routing_phases
            return CommandResult(argv, 0, b"READY" if present else b"ABSENT", b"")
        if tail[:4] == ("shell", "am", "start", "-W"):
            if self.activity_start_result is not None:
                result = self.activity_start_result
                return CommandResult(
                    argv,
                    result.returncode,
                    result.stdout,
                    result.stderr,
                    result.timed_out,
                )
            return CommandResult(
                argv,
                0,
                b"Starting: Intent { cmp=com.dobby.vpn/org.golang.app.GoNativeActivity }\n"
                b"Status: ok\nComplete\n",
                b"",
            )
        if tail[:3] == ("shell", "iptables", "-L"):
            packets = self.routing_rule_packets
            rules = "\n".join(
                f"1 {packets} 64 REJECT {self.routing_rule_protocol} -- * "
                f"{self.routing_rule_interface} 0.0.0.0/0 {destination} tcp dpt:443"
                for destination in self.routing_rule_destinations
            )
            return CommandResult(
                argv,
                0,
                (
                    "num pkts bytes target prot opt in out source destination\n"
                    + rules
                    + "\n"
                ).encode(),
                b"",
            )
        if (
            tail[:4] == ("shell", "iptables", "-D", "OUTPUT")
            and self.routing_remove_failure is not None
        ):
            failure = self.routing_remove_failure
            return CommandResult(
                argv,
                failure.returncode,
                failure.stdout,
                failure.stderr,
                failure.timed_out,
            )
        if (
            tail[:4] == ("shell", "iptables", "-I", "OUTPUT")
            and self.routing_insert_failure is not None
        ):
            failure = self.routing_insert_failure
            return CommandResult(
                argv,
                failure.returncode,
                failure.stdout,
                failure.stderr,
                failure.timed_out,
            )
        if tail == ("shell", "iptables", "-S"):
            if self.routing_cleanup_inventory_failure:
                return CommandResult(argv, 1, b"", b"inventory failed\n")
            stdout = (
                b"-A OUTPUT -j DOBBYVPN_TORTURER\n"
                if self.routing_cleanup_residual else b"-P OUTPUT ACCEPT\n"
            )
            return CommandResult(argv, 0, stdout, b"")
        if tail[:2] == ("shell", "iptables"):
            return CommandResult(argv, 0, b"", b"")
        if cat_paths is not None:
            if any("/sys/class/net/" in path for path in cat_paths):
                self.routing_counter_reads += 1
                index = min(
                    self.routing_counter_reads - 1,
                    len(self.routing_counter_values) - 1,
                )
                counters = (
                    f"{self.routing_counter_values[index][0]}\n"
                    f"{self.routing_counter_values[index][1]}\n"
                ).encode()
                return CommandResult(argv, 0, counters, b"")
            ready_path = next((path for path in cat_paths if ".ready" in path), None)
            if ready_path is not None:
                name = Path(ready_path).name
                phase = self.routing_phases.get(name, "ready")
                if phase == "ready" and self.routing_ready_override is not None:
                    value = self.routing_ready_override
                elif phase == "blocked" and self.routing_blocked_override is not None:
                    value = self.routing_blocked_override
                elif phase == "unblocked" and self.routing_unblocked_override is not None:
                    value = self.routing_unblocked_override
                elif phase == "blocked":
                    value = {
                        "phase": "blocked",
                        "direct": {
                            "error_code": "ANDROID_NETWORK_REQUEST_FAILED",
                        },
                        "vpn": {"status": 200, "body": "198.51.100.7"},
                    }
                elif phase == "unblocked":
                    value = {
                        "phase": "unblocked",
                        "direct": {"status": 200, "body": "203.0.113.10"},
                    }
                else:
                    value = {
                        "phase": "ready",
                        "physical_interface": "eth0",
                        "physical_transport": "ethernet",
                        "vpn_interface": "tun0",
                        "ipv4s": ["203.0.113.10", "203.0.113.11"],
                        "port": 443,
                    }
                return CommandResult(
                    argv, 0, (json.dumps(value) + "\n").encode(), b""
                )
            observation = (
                self.observations.pop(0) if self.observations else self.observation
            )
            return CommandResult(argv, 0, observation, b"")
        if "instrument" in argv:
            return CommandResult(argv, 0, self.instrumentation, b"")
        if argv[1:3] == ("shell", "pidof"):
            return CommandResult(argv, 1, b"", b"adb pidof: no matching process\n")
        if self.fail_cleanup and (
            argv[1:4] == ("shell", "sh", "-c")
            and ("rm -f" in argv[-1] or "am force-stop" in argv[-1])
        ):
            return CommandResult(argv, 1, b"adb cleanup stdout diagnostic\n", b"adb cleanup stderr diagnostic\n")
        return CommandResult(argv, 0, b"adb stdout diagnostic\n", b"adb stderr diagnostic\n")

def _select_connection(adapter: AndroidHostedAdapter) -> ConnectionIdentity:
    connections = adapter.discover_connections()
    adapter.select_connection(connections[0])
    return connections[0]


class ExternalControlRunner(FakeAndroidRunner):
    def __init__(self, raw_directory: Path) -> None:
        super().__init__(raw_directory)
        self.uplink_up = True
        self.app_alive = True
        self.transition_failure: CommandResult | None = None

    def run(self, command, *, timeout_seconds, input_bytes=None):
        argv = tuple(command)
        self.calls.append(argv)
        self.timeouts.append(float(timeout_seconds))
        tail = argv[1:]
        if tail[:3] == ("shell", "sh", "-c") and "DobbyVPN uplink" in tail[3]:
            if self.transition_failure is not None:
                failure = self.transition_failure
                return CommandResult(
                    argv,
                    failure.returncode,
                    failure.stdout,
                    failure.stderr,
                    failure.timed_out,
                )
            interface = argv[-1]
            self.uplink_up = False
            self.uplink_up = True
            return CommandResult(
                argv,
                0,
                (
                    f"DobbyVPN uplink interface={interface} state=present\n"
                    f"DobbyVPN uplink interface={interface} state=absent\n"
                    f"DobbyVPN uplink interface={interface} state=restored\n"
                ).encode(),
                b"",
            )
        if tail == ("shell", "pidof", "com.dobby.vpn"):
            if self.app_alive:
                return CommandResult(argv, 0, b"1234\n", b"")
            return CommandResult(argv, 1, b"", b"no process\n")
        if tail == ("shell", "am", "force-stop", "com.dobby.vpn"):
            self.app_alive = False
            return CommandResult(argv, 0, b"", b"")
        if "instrument" in tail:
            self.app_alive = True
        self.calls.pop()
        self.timeouts.pop()
        return super().run(
            command, timeout_seconds=timeout_seconds, input_bytes=input_bytes
        )


def _provenance(adapter: AndroidHostedAdapter) -> RunProvenance:
    return RunProvenance(
        platform="android",
        platform_version="android-14",
        architecture="x86_64",
    )


class HostedAndroidAdapterTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory(prefix="hosted-android-adapter-")
        root = Path(self.directory.name)
        self.adb = root / "adb"
        self.adb.write_bytes(b"synthetic adb executable\n")
        self.adb.chmod(0o700)
        self.profile = root / "profile.conf"
        self.profile.write_bytes(_PROFILE_BUNDLE)
        os.chmod(self.profile, 0o600)
        self.runner = FakeAndroidRunner(root / "raw")
        self.adapter = AndroidHostedAdapter(
            runner=self.runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        self.connection = _select_connection(self.adapter)
        self.runner.calls.clear()
        self.runner.timeouts.clear()
        self.runner.command_payload = None
        self.runner.command_payloads.clear()
        self.runner.staged_profile_payloads.clear()
        self.addCleanup(self.directory.cleanup)

    def test_gui_profile_selector_preserves_one_complete_non_trusttunnel_block(self) -> None:
        self.assertEqual(_select_gui_profile(_PROFILE_BUNDLE), _GUI_PROFILE)
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_GUI_PROFILE_UNAVAILABLE"
        ):
            _select_gui_profile(b"[[TrustTunnel]]\nname = 'only'\n")

    def test_gui_profile_selector_skips_oversized_block_for_later_bounded_block(self) -> None:
        oversized = (
            b"[[Outline]]\nDescription = \""
            + (b"x" * _GUI_PROFILE_MAX_BYTES)
            + b"\"\n"
        )
        later = b"[[Xray]]\noutbounds = []\n"
        self.assertEqual(_select_gui_profile(oversized + later), later)
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_GUI_PROFILE_UNAVAILABLE"
        ):
            _select_gui_profile(oversized)

    def test_bulk_adapter_uses_one_product_session_and_canonical_engine(self) -> None:
        scenario = get_scenario("functional.core-connection")
        result = FunctionalEngine().run(
            scenario, self.adapter, _provenance(self.adapter), self.connection
        )
        self.assertEqual(result.outcome, "passed")
        self.assertEqual(
            [item["operation"] for item in self.runner.command_payload["operations"]],
            [step.operation for step in scenario.steps],
        )
        self.assertEqual(
            [payload for _name, payload in self.runner.staged_profile_payloads],
            [_PROFILE_BUNDLE],
        )
        self.assertEqual(self.runner.command_payload["coverage_lane"], "protocol-matrix")
        self.assertEqual(self.runner.command_payload["ui_mode"], "protocol-matrix")
        instrumentation = [
            call for call in self.runner.calls if "instrument" in call
        ]
        self.assertEqual(len(instrumentation), 1)
        self.assertIn("dobby.real_profile", instrumentation[0])
        self.assertIn("dobby.hosted_command_file", instrumentation[0])
        self.assertFalse(any(call[1:3] == ("shell", "dumpsys") for call in self.runner.calls))
        cleanup_calls = [
            call for call in self.runner.calls
            if call[1:4] == ("shell", "sh", "-c")
            and "force-stop" in call[-1]
        ]
        self.assertEqual(len(cleanup_calls), 1)
        self.assertTrue(cleanup_calls[0][-1].startswith("'"))
        self.assertTrue(cleanup_calls[0][-1].endswith("'"))
        self.assertIn(".tmp", cleanup_calls[0][-1])
        self.assertTrue(
            all(timeout <= scenario.max_duration_seconds for timeout in self.runner.timeouts)
        )
        self.assertTrue(all(timeout <= 15.0 for timeout in self.runner.timeouts[-2:]))
        self.assertEqual(self.runner.timeouts[-1], 15.0)

    def test_gui_auto_lane_is_explicit_and_does_not_claim_protocol_matrix(self) -> None:
        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "gui-auto-raw")
        runner.observation = _observation(
            connections=[{"index": 0, "protocol": "AUTO"}],
            connection={"index": 0, "protocol": "AUTO"},
            coverage_lane="gui-auto",
            gui_auto_verified=True,
            ui_reopen_verified=True,
            vpn_consent_handled=True,
        )
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            ui_mode="gui-auto",
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        connections = adapter.discover_connections()
        self.assertEqual(connections, (ConnectionIdentity(0, "AUTO"),))
        adapter.select_connection(connections[0])

        result = FunctionalEngine().run(
            get_scenario("functional.core-connection"),
            adapter,
            _provenance(adapter),
            connections[0],
        )

        self.assertEqual(result.outcome, "passed")
        self.assertEqual(
            [payload for _name, payload in runner.staged_profile_payloads],
            [_GUI_PROFILE],
        )
        command = runner.command_payload
        assert command is not None
        self.assertEqual(command["coverage_lane"], "gui-auto")
        self.assertEqual(command["ui_mode"], "gui-auto")
        self.assertNotIn("profile_index", command)

    def test_gui_auto_observation_requires_rendered_reopen_for_cleanup_scenarios(self) -> None:
        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "gui-auto-reopen-raw")
        runner.observation = _observation(
            connections=[{"index": 0, "protocol": "AUTO"}],
            connection={"index": 0, "protocol": "AUTO"},
            coverage_lane="gui-auto",
            gui_auto_verified=True,
            ui_reopen_verified=False,
        )
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            ui_mode="gui-auto",
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        connection = adapter.discover_connections()[0]
        adapter.select_connection(connection)
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_UI_REOPEN_NOT_VERIFIED"
        ):
            adapter.execute_scenario(get_scenario("functional.core-connection"))

    def test_instrumentation_progress_does_not_require_product_logs(self) -> None:
        progress: list[tuple[str, dict[str, object]]] = []
        self.adapter.set_progress_sink(
            lambda event, fields: progress.append((event, fields))
        )

        result = self.adapter._run_instrumentation(
            "command.json", time.monotonic() + 10.0
        )

        self.assertEqual(result.returncode, 0)
        states = [
            fields["state"]
            for event, fields in progress
            if event == "native-state" and fields.get("kind") == "instrumentation"
        ]
        self.assertEqual(states, ["started", "completed"])
        self.assertFalse(
            any(
                "app_logs.txt" in " ".join(call)
                or "go_android_logs.jsonl" in " ".join(call)
                for call in self.runner.calls
            )
        )

    def test_regular_instrumentation_cold_stops_target_before_runner(self) -> None:
        result = self.adapter._run_instrumentation(
            "command.json", time.monotonic() + 10.0
        )

        self.assertEqual(result.returncode, 0)
        cold_start_indices = [
            index
            for index, call in enumerate(self.runner.calls)
            if call[1:] == ("shell", "am", "force-stop", "com.dobby.vpn")
        ]
        instrumentation_indices = [
            index for index, call in enumerate(self.runner.calls)
            if "instrument" in call
        ]
        self.assertEqual(len(cold_start_indices), 1)
        self.assertEqual(len(instrumentation_indices), 1)
        self.assertLess(cold_start_indices[0], instrumentation_indices[0])

    def test_preserve_active_instrumentation_does_not_cold_stop_target(self) -> None:
        result = self.adapter._run_instrumentation(
            "command.json", time.monotonic() + 10.0, preserve_active=True
        )

        self.assertEqual(result.returncode, 0)
        instrumentation_index = next(
            index for index, call in enumerate(self.runner.calls)
            if "instrument" in call
        )
        self.assertFalse(
            any(
                call[1:] == ("shell", "am", "force-stop", "com.dobby.vpn")
                for call in self.runner.calls[:instrumentation_index]
            )
        )
        self.assertTrue(
            any(
                call[1:5] == ("shell", "am", "start", "-W")
                for call in self.runner.calls[:instrumentation_index]
            )
        )

    def test_gui_preserve_active_instrumentation_resets_prior_renderer_state(self) -> None:
        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "gui-preserve-raw")
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            ui_mode="gui-auto",
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )

        result = adapter._run_instrumentation(
            "command.json", time.monotonic() + 10.0, preserve_active=True
        )

        self.assertEqual(result.returncode, 0)
        stop_index = next(
            index
            for index, call in enumerate(runner.calls)
            if call[1:] == ("shell", "am", "force-stop", "com.dobby.vpn")
        )
        start_index = next(
            index
            for index, call in enumerate(runner.calls)
            if call[1:5] == ("shell", "am", "start", "-W")
        )
        instrumentation_index = next(
            index for index, call in enumerate(runner.calls) if "instrument" in call
        )
        self.assertLess(stop_index, start_index)
        self.assertLess(start_index, instrumentation_index)

    def test_worker_observation_error_precedes_external_control_timeout(self) -> None:
        self.runner.observation = _observation("ANDROID_PROFILE_OPERATION_FAILED")
        self.adapter._active_controls = (
            ("never-ready.json", "network_transition", 5.0),
        )

        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_PROFILE_OPERATION_FAILED"
        ) as raised:
            self.adapter._run_instrumentation(
                "command.json",
                time.monotonic() + 5.0,
                output_name="observation.json",
            )

        self.assertNotIn("ANDROID_CONTROL_TIMEOUT", str(raised.exception))
        self.assertTrue(
            any(
                call[1:4] == ("shell", "-T", "cat")
                and call[-1].endswith("/observation.json")
                for call in self.runner.calls
            )
        )

    def test_control_failure_preserves_host_progress_and_complete_result(self) -> None:
        self.runner.routing_phases["routing.json.ready"] = "ready"
        self.runner.routing_insert_failure = CommandResult(
            ("synthetic", "iptables", "-I"),
            3,
            b"",
            b"iptables: filter table unavailable; missing kernel module\n",
        )
        self.adapter._active_controls = (
            ("routing.json", "observe_routing_identity", 5.0),
        )
        progress: list[tuple[str, dict[str, object]]] = []
        self.adapter.set_progress_sink(
            lambda event, fields: progress.append((event, fields))
        )

        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_RULE_INSTALL_FAILED"
        ) as raised:
            self.adapter._run_instrumentation(
                "command.json", time.monotonic() + 10.0
            )

        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_stdout: <empty>", notes)
        self.assertIn("command_stderr:\niptables: filter table unavailable; missing kernel module", notes)
        finish = [
            value
            for _name, value in self.runner.staged_control_payloads
            if value.get("phase") == "finish"
        ]
        self.assertEqual(len(finish), 1)
        self.assertEqual(finish[0]["operation"], "observe_routing_identity")
        self.assertFalse(finish[0]["passed"])
        self.assertIn("ANDROID_ROUTING_RULE_INSTALL_FAILED", finish[0]["error"])
        self.assertTrue(
            any(
                event == "native-state"
                and fields.get("kind") == "instrumentation"
                and fields.get("state") == "started"
                for event, fields in progress
            )
        )
        self.assertTrue(
            any(
                event == "native-state"
                and fields.get("kind") == "routing-proof"
                and fields.get("phase") == "ready"
                for event, fields in progress
            )
        )
        self.assertTrue(
            any(
                event == "native-state"
                and fields.get("kind") == "instrumentation"
                and fields.get("state") == "failed"
                for event, fields in progress
            )
        )

    def test_routing_rule_counter_accepts_native_numeric_tcp_protocol(self) -> None:
        self.runner.routing_rule_protocol = "6"
        self.assertEqual(
            self.adapter._routing_rule_counter(
                "eth0", ("203.0.113.10",), 443, time.monotonic() + 5.0
            ),
            1,
        )

    def test_routing_rule_counter_aggregates_all_announced_destinations(self) -> None:
        self.runner.routing_rule_packets = 2
        self.runner.routing_rule_destinations = (
            "203.0.113.10", "203.0.113.11"
        )
        self.assertEqual(
            self.adapter._routing_rule_counter(
                "eth0",
                ("203.0.113.10", "203.0.113.11"),
                443,
                time.monotonic() + 5.0,
            ),
            4,
        )

    def test_product_error_propagates_and_cleanup_commands_are_still_attempted(self) -> None:
        self.runner.observation = _observation("DRIVER_ERROR")
        with self.assertRaisesRegex(ScenarioExecutionError, "DRIVER_ERROR"):
            FunctionalEngine().run(
                get_scenario("functional.core-connection"),
                self.adapter,
                _provenance(self.adapter),
                self.connection,
            )
        self.assertTrue(
            any(
                "am force-stop" in call[-1]
                for call in self.runner.calls
            )
        )

    def test_instrumentation_crash_is_reported_before_observation_read(self) -> None:
        self.runner.instrumentation = (
            b"INSTRUMENTATION_STATUS: class=com.dobby.GoUiHostedProfileTest\n"
            b"INSTRUMENTATION_STATUS: stack=org.koin.core.error.NoBeanDefFoundException: No definition found for PermissionEventsChannel\n"
            b"INSTRUMENTATION_STATUS_CODE: -2\n"
            b"INSTRUMENTATION_RESULT: shortMsg=Process crashed.\n"
            b"INSTRUMENTATION_CODE: 0\n"
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError,
            "Android instrumentation failed: returncode=0, timed_out=False, "
            "success_marker_present=False",
        ) as raised:
            FunctionalEngine().run(
                get_scenario("functional.core-connection"),
                self.adapter,
                _provenance(self.adapter),
                self.connection,
            )
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_returncode=0", notes)
        self.assertIn(
            "command_stdout:\nINSTRUMENTATION_STATUS: class=com.dobby.GoUiHostedProfileTest",
            notes,
        )
        self.assertIn("NoBeanDefFoundException", notes)
        self.assertIn("PermissionEventsChannel", notes)
        self.assertFalse(any(call[1:2] == ("exec-out",) for call in self.runner.calls))

    def test_junit_failure_is_rejected_even_with_instrumentation_code_minus_one(self) -> None:
        self.runner.instrumentation = (
            b"There was 1 failure:\n"
            b"FAILURES!!!\n"
            b"Tests run: 4,  Failures: 1\n"
            b"INSTRUMENTATION_CODE: -1\n"
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError,
            "junit_summary_present=False, failures_marker_present=True",
        ) as raised:
            FunctionalEngine().run(
                get_scenario("functional.core-connection"),
                self.adapter,
                _provenance(self.adapter),
                self.connection,
            )
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_stdout:\nThere was 1 failure:", notes)
        self.assertIn("FAILURES!!!", notes)
        self.assertIn("INSTRUMENTATION_CODE: -1", notes)

    def test_junit_zero_or_empty_summary_is_not_success(self) -> None:
        for output in (
            b"OK (0 tests)\nINSTRUMENTATION_CODE: -1\n",
            b"INSTRUMENTATION_CODE: -1\n",
        ):
            with self.subTest(output=output):
                self.runner.instrumentation = output
                with self.assertRaisesRegex(
                    ScenarioExecutionError, "junit_summary_present=False"
                ):
                    FunctionalEngine().run(
                        get_scenario("functional.core-connection"),
                        self.adapter,
                        _provenance(self.adapter),
                        self.connection,
                    )

    def test_routing_identity_accepts_plain_and_json_string_bodies(self) -> None:
        for suffix, body in (
            ("plain-ip", "198.51.100.7"),
            ("json-ip", '"198.51.100.7"'),
        ):
            with self.subTest(suffix=suffix):
                runner = FakeAndroidRunner(
                    self.runner.raw_directory.parent / suffix
                )
                runner.routing_phases["routing.json.ready"] = "ready"
                runner.routing_blocked_override = {
                    "phase": "blocked",
                    "direct": {
                        "error_code": "ANDROID_NETWORK_REQUEST_FAILED",
                    },
                    "vpn": {"status": 200, "body": body},
                }
                adapter = AndroidHostedAdapter(
                    runner=runner,
                    profile=self.profile,
                    adb=self.adb,
                    source_sha=_SOURCE_SHA,
                    identity_url="https://identity.example.test/ip",
                    latency_url="https://latency.example.test/blob",
                    download_url="https://download.example.test/blob",
                    upload_url="https://upload.example.test/blob",
                )
                adapter._routing_proof("routing.json", time.monotonic() + 5.0)
                self.assertEqual(adapter._observed_baseline_ip, "203.0.113.10")
                self.assertEqual(adapter._observed_tunneled_ips, {"198.51.100.7"})

    def test_product_error_code_fails_and_still_cleans_up(self) -> None:
        self.runner.observation = _observation("UNREVIEWED_CODE")
        with self.assertRaisesRegex(
            ScenarioExecutionError, "UNREVIEWED_CODE"
        ) as raised:
            FunctionalEngine().run(
                get_scenario("functional.core-connection"),
                self.adapter,
                _provenance(self.adapter),
                self.connection,
            )
        self.assertTrue(
            any(
                "am force-stop" in call[-1]
                for call in self.runner.calls
            )
        )

    def test_finalization_deadlines_are_reserved_inside_the_lane(self) -> None:
        work, cleanup = _scenario_deadlines(100.0, 141.0)
        self.assertLess(work, cleanup)
        self.assertLessEqual(cleanup, 100.0 + 1_800.0)
        self.assertAlmostEqual(cleanup - work, 17.625)

    def test_observation_validation_failure_categories_are_precise(self) -> None:
        self.assertEqual(
            _observation_error_code(AndroidObservationError("observation has an unexpected shape")),
            "ANDROID_OBSERVATION_SHAPE_INVALID",
        )
        self.assertEqual(
            _observation_error_code(AndroidObservationError("connections are invalid")),
            "ANDROID_OBSERVATION_CONNECTIONS_INVALID",
        )
        self.assertEqual(
            _observation_error_code(AndroidObservationError("source_sha does not match candidate")),
            "ANDROID_OBSERVATION_SOURCE_INVALID",
        )

    def test_command_staging_is_unique_and_removed_after_use(self) -> None:
        scenario = get_scenario("functional.core-connection")
        first, _profile_one, _output_one = self.adapter._write_command(scenario)
        first_bytes = first.read_bytes()
        second, _profile_two, _output_two = self.adapter._write_command(scenario)
        self.assertNotEqual(first, second)
        self.assertEqual(first.read_bytes(), first_bytes)
        self.adapter._cleanup_local_scratch()
        self.assertFalse(first.exists())
        self.assertFalse(second.exists())

    def test_advanced_android_operations_have_unique_token_bound_controls(self) -> None:
        scenario = get_scenario("functional.network-transition")
        command_file, _profile, _output = self.adapter._write_command(scenario)
        payload = json.loads(command_file.read_text(encoding="utf-8"))
        operation = next(
            item for item in payload["operations"]
            if item["operation"] == "network_transition"
        )
        self.assertRegex(operation["control_file"], r"^[A-Za-z0-9][A-Za-z0-9._-]+$")
        self.assertNotIn("control_token", operation)
        self.assertEqual(len(self.adapter._active_controls), 2)
        self.assertEqual(
            [operation for _file, operation, _timeout in self.adapter._active_controls],
            ["observe_routing_identity", "network_transition"],
        )

    def _prime_routing_ready(self, name: str = "routing.json") -> str:
        self.runner.routing_phases[f"{name}.ready"] = "ready"
        return name

    def test_routing_proof_uses_exact_rule_counters_and_restores_before_finish(self) -> None:
        progress: list[tuple[str, dict[str, object]]] = []
        self.adapter.set_progress_sink(
            lambda event, fields: progress.append((event, fields))
        )
        name = self._prime_routing_ready()
        self.adapter._routing_proof(name, time.monotonic() + 5.0)

        phases = [
            value["phase"]
            for _name, value in self.runner.staged_control_payloads
            if value.get("operation") == "observe_routing_identity"
        ]
        self.assertEqual(phases, ["blocked", "unblocked", "finish"])
        iptables = [
            call[1:] for call in self.runner.calls
            if call[1:3] == ("shell", "iptables")
        ]
        create = ("shell", "iptables", "-N", ROUTING_RULE_CHAIN)
        first_rule = (
            "shell", "iptables", "-A", ROUTING_RULE_CHAIN, "-o", "eth0",
            "-d", "203.0.113.10", "-p", "tcp", "--dport", "443", "-j", "REJECT",
        )
        second_rule = (
            "shell", "iptables", "-A", ROUTING_RULE_CHAIN, "-o", "eth0",
            "-d", "203.0.113.11", "-p", "tcp", "--dport", "443", "-j", "REJECT",
        )
        jump = (
            "shell", "iptables", "-I", "OUTPUT", "1", "-j", ROUTING_RULE_CHAIN,
        )
        remove_jump = (
            "shell", "iptables", "-D", "OUTPUT", "-j", ROUTING_RULE_CHAIN,
        )
        self.assertIn(create, iptables)
        self.assertIn(first_rule, iptables)
        self.assertIn(second_rule, iptables)
        self.assertIn(jump, iptables)
        self.assertLess(iptables.index(create), iptables.index(first_rule))
        self.assertLess(iptables.index(first_rule), iptables.index(jump))
        self.assertGreaterEqual(iptables.count(remove_jump), 2)
        self.assertEqual(
            iptables[-4:],
            [
                remove_jump,
                ("shell", "iptables", "-F", ROUTING_RULE_CHAIN),
                ("shell", "iptables", "-X", ROUTING_RULE_CHAIN),
                ("shell", "iptables", "-S"),
            ],
        )
        cat_reads = [
            call
            for call in self.runner.calls
            if call[1:4] == ("shell", "-T", "cat")
        ]
        self.assertTrue(any(".ready" in path for call in cat_reads for path in call[4:]))
        self.assertTrue(
            any(
                "/sys/class/net/tun0/statistics/rx_bytes" in path
                for call in cat_reads
                for path in call[4:]
            )
        )
        proof_events = [fields for event, fields in progress if event == "native-state"]
        self.assertTrue(any(fields.get("phase") == "blocked" for fields in proof_events))
        self.assertEqual(self.adapter._validated_physical_interface, "eth0")
        self.assertEqual(self.adapter._validated_physical_transport, "ethernet")
        self.assertNotIn(
            "203.0.113.10",
            json.dumps(proof_events, sort_keys=True),
        )

    def test_routing_proof_rejects_invalid_direct_vpn_counter_and_recovery_facts(self) -> None:
        cases = (
            (
                "direct-success",
                {"status": 200, "body": "203.0.113.10"},
                None,
                None,
                "ANDROID_ROUTING_DIRECT_NOT_BLOCKED",
            ),
            (
                "vpn-invalid",
                None,
                {"status": 200, "body": '"not-an-ip"'},
                None,
                "ANDROID_ROUTING_VPN_INVALID",
            ),
            (
                "rule-not-hit",
                None,
                None,
                0,
                "ANDROID_ROUTING_RULE_NOT_HIT",
            ),
        )
        for suffix, direct, vpn, packets, expected in cases:
            with self.subTest(suffix=suffix):
                runner = FakeAndroidRunner(
                    self.runner.raw_directory.parent / suffix
                )
                adapter = AndroidHostedAdapter(
                    runner=runner,
                    profile=self.profile,
                    adb=self.adb,
                    source_sha=_SOURCE_SHA,
                    identity_url="https://identity.example.test/ip",
                    latency_url="https://latency.example.test/blob",
                    download_url="https://download.example.test/blob",
                    upload_url="https://upload.example.test/blob",
                )
                name = "routing.json"
                runner.routing_phases[f"{name}.ready"] = "ready"
                if direct is not None or vpn is not None:
                    runner.routing_blocked_override = {
                        "phase": "blocked",
                        "direct": direct or {
                            "error_code": "ANDROID_NETWORK_REQUEST_FAILED",
                        },
                        "vpn": vpn or {
                            "status": 200,
                            "body": "198.51.100.7",
                        },
                    }
                if packets is not None:
                    runner.routing_rule_packets = packets
                with self.assertRaisesRegex(
                    ScenarioExecutionError, expected
                ) as raised:
                    adapter._routing_proof(name, time.monotonic() + 5.0)
                self.assertIsNone(adapter._validated_physical_interface)
                self.assertIsNone(adapter._validated_physical_transport)
                notes = "\n".join(getattr(raised.exception, "__notes__", ()))
                if suffix in {"direct-success", "vpn-invalid"}:
                    self.assertIn("android_routing_phase=blocked", notes)
                    self.assertNotIn("status", notes)
                    self.assertNotIn("not-an-ip", notes)
                if suffix == "rule-not-hit":
                    self.assertIn("packets=0", str(raised.exception))
                self.assertTrue(
                    any(call[1:4] == ("shell", "iptables", "-D") for call in runner.calls)
                )

        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "request-before-rule")
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_rule_packets = 0
        runner.routing_blocked_override = {
            "phase": "blocked",
            "direct": {"status": 200, "body": "203.0.113.10"},
            "vpn": {"status": 200, "body": "198.51.100.7"},
        }
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_DIRECT_NOT_BLOCKED"
        ):
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)

        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "recovery-invalid")
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_unblocked_override = {
            "phase": "unblocked",
            "direct": {"status": 200, "body": "not-an-ip"},
        }
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_RECOVERY_INVALID"
        ):
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)

    def test_routing_proof_preserves_primary_and_rule_cleanup_failure(self) -> None:
        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "rule-cleanup")
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_blocked_override = {
            "phase": "blocked",
            "direct": {"status": 200, "body": "203.0.113.10"},
            "vpn": {"status": 200, "body": "198.51.100.7"},
        }
        runner.routing_remove_failure = CommandResult(
            ("synthetic", "iptables", "-D"),
            7,
            b"remove stdout\n",
            b"remove stderr\n",
        )
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_DIRECT_NOT_BLOCKED"
        ) as raised:
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("android_routing_secondary_error=ANDROID_ROUTING_RULE_REMOVE_FAILED", notes)
        self.assertIn("remove stdout", notes)
        self.assertIn("remove stderr", notes)
        finish = [
            value for _name, value in runner.staged_control_payloads
            if value.get("phase") == "finish"
        ]
        self.assertEqual(len(finish), 1)
        self.assertFalse(finish[0]["passed"])
        self.assertIn("ANDROID_ROUTING_DIRECT_NOT_BLOCKED", finish[0]["error"])

    def test_routing_proof_rejects_same_interface_and_missing_tun_traffic(self) -> None:
        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "same-interface")
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_ready_override = {
            "phase": "ready",
            "physical_interface": "eth0",
            "physical_transport": "ethernet",
            "vpn_interface": "eth0",
            "ipv4s": ["203.0.113.10", "203.0.113.11"],
            "port": 443,
        }
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_READY_INVALID"
        ):
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)

        runner = FakeAndroidRunner(self.runner.raw_directory.parent / "no-traffic")
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_counter_values = [(100, 200), (100, 200)]
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_TRAFFIC_NOT_OBSERVED"
        ) as raised:
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        self.assertIn("before_rx=100", str(raised.exception))
        self.assertIn("after_tx=200", str(raised.exception))

    def test_routing_proof_rejects_unsupported_physical_transport(self) -> None:
        runner = FakeAndroidRunner(
            self.runner.raw_directory.parent / "unsupported-transport"
        )
        runner.routing_phases["routing.json.ready"] = "ready"
        runner.routing_ready_override = {
            "phase": "ready",
            "physical_interface": "eth0",
            "physical_transport": "cellular",
            "vpn_interface": "tun0",
            "ipv4s": ["203.0.113.10", "203.0.113.11"],
            "port": 443,
        }
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_ROUTING_READY_INVALID"
        ):
            adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        self.assertIsNone(adapter._validated_physical_interface)
        self.assertIsNone(adapter._validated_physical_transport)
        self.assertFalse(
            any(call[1:3] == ("shell", "iptables") for call in runner.calls)
        )

    def test_routing_proof_without_transport_cannot_trigger_transition(self) -> None:
        self.runner.routing_phases["routing.json.ready"] = "ready"
        self.runner.routing_ready_override = {
            "phase": "ready",
            "physical_interface": "eth0",
            "vpn_interface": "tun0",
            "ipv4s": ["203.0.113.10", "203.0.113.11"],
            "port": 443,
        }

        self.adapter._routing_proof("routing.json", time.monotonic() + 5.0)

        self.assertEqual(self.adapter._validated_physical_interface, "eth0")
        self.assertIsNone(self.adapter._validated_physical_transport)
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_UPLINK_IDENTITY_UNAVAILABLE"
        ):
            self.adapter._perform_external_control(
                "network_transition", time.monotonic() + 5.0
            )
        self.assertFalse(
            any("DobbyVPN uplink" in call[-1] for call in self.runner.calls)
        )

    def test_post_transition_routing_wait_observes_worker_failure(self) -> None:
        worker_failure = ScenarioExecutionError("ANDROID_INSTRUMENTATION_FAILED")

        def abort() -> None:
            raise worker_failure

        with (
            patch.object(self.adapter, "_perform_external_control"),
            patch.object(self.adapter, "_stage_control_payload"),
            self.assertRaisesRegex(
                ScenarioExecutionError, "ANDROID_INSTRUMENTATION_FAILED"
            ) as raised,
        ):
            self.adapter._complete_external_control(
                "transition.json",
                "network_transition",
                time.monotonic() + 1.0,
                ready={},
                abort=abort,
            )

        self.assertIs(raised.exception, worker_failure)

    def test_process_loss_uses_two_sessions_with_proven_absence_between_them(self) -> None:
        runner = ExternalControlRunner(self.runner.raw_directory.parent / "loss-raw")
        phase_observations = [
            _observation(
                cleanup_verified=False,
                disconnect_clean=False,
                process_loss_verified=False,
                restart_verified=False,
                reconnect_completed=False,
                second_tunnel_interface=False,
                second_routing_verified=False,
                final_disconnect_clean=False,
            ),
            _observation(
                process_loss_verified=False,
                restart_verified=False,
                reconnect_completed=False,
                second_tunnel_interface=False,
                second_routing_verified=False,
                final_disconnect_clean=False,
            ),
        ]
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )

        connection = _select_connection(adapter)
        runner.observations = phase_observations
        runner.command_payloads.clear()
        runner.calls.clear()
        runner.timeouts.clear()
        result = FunctionalEngine().run(
            get_scenario("functional.product-process-loss"),
            adapter,
            _provenance(adapter),
            connection,
        )

        self.assertEqual(result.outcome, "passed")
        self.assertEqual(len(runner.command_payloads), 2)
        self.assertTrue(runner.command_payloads[0]["preserve_active"])
        self.assertEqual(
            [item["operation"] for item in runner.command_payloads[0]["operations"]],
            ["configure", "connect", "observe_tunnel", "observe_routing_identity"],
        )
        self.assertNotIn("preserve_active", runner.command_payloads[1])
        self.assertEqual(
            [item["operation"] for item in runner.command_payloads[1]["operations"]],
            [
                "configure", "connect", "observe_tunnel", "observe_routing_identity",
                "disconnect", "inspect_cleanup",
            ],
        )
        instrument_indices = [
            index for index, call in enumerate(runner.calls) if "instrument" in call
        ]
        stop_indices = [
            index
            for index, call in enumerate(runner.calls)
            if (
                call[1:] == ("shell", "am", "force-stop", "com.dobby.vpn")
                or "am force-stop com.dobby.vpn" in call[-1]
            )
        ]
        self.assertEqual(len(instrument_indices), 2)
        launch_indices = [
            index
            for index, call in enumerate(runner.calls)
            if call[1:5] == ("shell", "am", "start", "-W")
        ]
        self.assertEqual(len(launch_indices), 1)
        self.assertLess(launch_indices[0], instrument_indices[0])
        self.assertIn("--no-restart", runner.calls[instrument_indices[0]])
        self.assertNotIn("--no-restart", runner.calls[instrument_indices[1]])
        self.assertGreaterEqual(len(stop_indices), 2)
        self.assertLess(instrument_indices[0], stop_indices[0])
        self.assertLess(stop_indices[0], instrument_indices[1])
        absent_probes = [
            call
            for call in runner.calls[stop_indices[0] + 1:instrument_indices[1]]
            if call[1:] == ("shell", "pidof", "com.dobby.vpn")
        ]
        self.assertEqual(len(absent_probes), 2)

    def test_process_loss_probe_reports_complete_status(self) -> None:
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_PROCESS_LOSS_PROBE_FAILED"
        ) as raised:
            self.adapter._device_text(
                ("shell", "pidof", "com.dobby.vpn"),
                time.monotonic() + 5.0,
                "ANDROID_PROCESS_LOSS_PROBE_FAILED",
            )
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_returncode=1", notes)
        self.assertIn("command_stdout: <empty>", notes)
        self.assertIn("command_stderr:\nadb pidof: no matching process", notes)

    def test_preserve_active_start_failure_reports_complete_status(self) -> None:
        runner = ExternalControlRunner(self.runner.raw_directory.parent / "start-failure-raw")
        runner.activity_start_result = CommandResult(
            ("synthetic", "am", "start"),
            8,
            b"start stdout diagnostic\n",
            b"start stderr diagnostic\n",
        )
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_APP_START_FAILED"
        ) as raised:
            adapter._run_instrumentation(
                "command.json", time.monotonic() + 5.0, preserve_active=True
            )
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_returncode=8", notes)
        self.assertIn("command_stdout:\nstart stdout diagnostic", notes)
        self.assertIn("command_stderr:\nstart stderr diagnostic", notes)
        self.assertFalse(any("instrument" in call for call in runner.calls))

    def test_android_advertises_transition_without_limitations(self) -> None:
        self.assertIn(Capability.NETWORK_TRANSITION, self.adapter.capabilities)
        self.assertEqual(self.adapter.capability_unavailable_reasons, {})

    def test_android_external_control_reuses_proven_uplink_interface(self) -> None:
        runner = ExternalControlRunner(self.runner.raw_directory.parent / "external-raw")
        runner.routing_ready_override = {
            "phase": "ready",
            "physical_interface": "wlan0",
            "physical_transport": "wifi",
            "vpn_interface": "tun0",
            "ipv4s": ["203.0.113.10", "203.0.113.11"],
            "port": 443,
        }
        runner.routing_rule_interface = "wlan0"
        runner.routing_phases["routing.json.ready"] = "ready"
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        runner.uplink_up = True
        runner.app_alive = True
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_UPLINK_IDENTITY_UNAVAILABLE"
        ):
            adapter._perform_external_control(
                "network_transition", time.monotonic() + 5.0
            )
        adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        adapter._perform_external_control("network_transition", time.monotonic() + 5.0)
        self.assertTrue(runner.uplink_up)
        transition_calls = [
            call
            for call in runner.calls
            if call[1:4] == ("shell", "sh", "-c")
            and "DobbyVPN uplink" in call[4]
        ]
        self.assertEqual(len(transition_calls), 1)
        script = shlex.split(transition_calls[0][4])[0]
        self.assertIn("ip -4 route show table all default", script)
        self.assertIn("transition_kind=$3", script)
        self.assertIn("interface=$4", script)
        self.assertNotIn("interfaces=$(printf", script)
        self.assertIn("while [ \"$(date +%s)\" -lt \"$down_deadline\" ]", script)
        self.assertIn("while [ \"$(date +%s)\" -lt \"$overall_deadline\" ]", script)
        self.assertIn("trap 'on_signal 143' 15", script)
        self.assertIn("case \",$flags,\"", script)
        self.assertIn("while IFS= read -r line", script)
        self.assertIn('wifi_status_output=$(cmd wifi status 2>&1)', script)
        self.assertIn(
            "wifi_state_output=$(printf '%s\\n' \"$wifi_status_output\" | sed -n '1p')",
            script,
        )
        self.assertIn("[ \"$wifi_state_output\" = \"Wifi is $1\" ]", script)
        self.assertIn('svc wifi disable', script)
        self.assertIn('svc wifi enable', script)
        self.assertIn('if network_state_is absent "$down_link" && ! route_is_usable "$down_routes"; then', script)
        self.assertIn('if network_state_is present "$restore_link" && route_is_usable "$restore_routes"; then', script)
        self.assertIn('ip link set dev "$interface" down', script)
        self.assertIn('ip link set dev "$interface" up', script)
        self.assertEqual(transition_calls[0][-1], "wlan0")
        self.assertEqual(transition_calls[0][-2], "wifi")
        self.assertEqual(len(transition_calls[0]), 10)
        self.assertGreaterEqual(int(transition_calls[0][-4]), 1)
        self.assertGreaterEqual(int(transition_calls[0][-3]), 1)

        ethernet_runner = ExternalControlRunner(
            self.runner.raw_directory.parent / "external-ethernet-raw"
        )
        ethernet_runner.routing_ready_override = {
            "phase": "ready",
            "physical_interface": "eth0",
            "physical_transport": "ethernet",
            "vpn_interface": "tun0",
            "ipv4s": ["203.0.113.10", "203.0.113.11"],
            "port": 443,
        }
        ethernet_runner.routing_rule_interface = "eth0"
        ethernet_runner.routing_phases["routing.json.ready"] = "ready"
        ethernet_adapter = AndroidHostedAdapter(
            runner=ethernet_runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        ethernet_adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        ethernet_adapter._perform_external_control(
            "network_transition", time.monotonic() + 5.0
        )
        ethernet_transition = next(
            call
            for call in ethernet_runner.calls
            if len(call) > 4 and "DobbyVPN uplink" in call[4]
        )
        self.assertEqual(ethernet_transition[-2:], ("ethernet", "eth0"))

    def test_android_external_control_reports_complete_primary_status(self) -> None:
        runner = ExternalControlRunner(self.runner.raw_directory.parent / "external-errors")
        runner.transition_failure = CommandResult(
            ("synthetic",),
            1,
            b"primary diagnostic\n",
            b"primary failure\nsecondary restore failure\n",
        )
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        runner.routing_phases["routing.json.ready"] = "ready"
        adapter._routing_proof("routing.json", time.monotonic() + 5.0)
        with self.assertRaisesRegex(
            ScenarioExecutionError, "ANDROID_UPLINK_TRANSITION_FAILED"
        ) as raised:
            adapter._perform_external_control(
                "network_transition", time.monotonic() + 5.0
            )
        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("command_stdout:\nprimary diagnostic", notes)
        self.assertIn("command_stderr:\nprimary failure\nsecondary restore failure", notes)

    def test_cleanup_failure_does_not_replace_product_failure(self) -> None:
        self.runner.observation = _observation("DRIVER_ERROR")
        self.runner.fail_cleanup = True
        with self.assertRaisesRegex(ScenarioExecutionError, "DRIVER_ERROR") as raised:
            FunctionalEngine().run(
                get_scenario("functional.core-connection"),
                self.adapter,
                _provenance(self.adapter),
                self.connection,
            )
        self.assertEqual(len(raised.exception.__notes__), 1)
        self.assertEqual(
            raised.exception.__notes__,
            ["android_cleanup_error=ANDROID_CLEANUP_FAILED"],
        )

    def test_cleanup_removes_owned_chain_left_by_killed_run(self) -> None:
        self.assertIsNone(
            self.adapter._cleanup_device((), time.monotonic() + 5.0)
        )
        cleanup = [
            call[1:] for call in self.runner.calls
            if call[1:3] == ("shell", "iptables")
        ]
        self.assertEqual(cleanup, [
            ("shell", "iptables", "-D", "OUTPUT", "-j", ROUTING_RULE_CHAIN),
            ("shell", "iptables", "-F", ROUTING_RULE_CHAIN),
            ("shell", "iptables", "-X", ROUTING_RULE_CHAIN),
            ("shell", "iptables", "-S"),
        ])

    def test_cleanup_reports_owned_chain_that_remains(self) -> None:
        self.runner.routing_cleanup_residual = True

        failure = self.adapter._cleanup_device((), time.monotonic() + 5.0)

        self.assertIsNotNone(failure)
        self.assertIn("ANDROID_ROUTING_RULE_CLEANUP_FAILED", str(failure))

    def test_cleanup_reports_routing_inventory_failure(self) -> None:
        self.runner.routing_cleanup_inventory_failure = True

        failure = self.adapter._cleanup_device((), time.monotonic() + 5.0)

        self.assertIsNotNone(failure)
        self.assertIn("ANDROID_ROUTING_RULE_CLEANUP_FAILED", str(failure))

    def test_cleanup_reports_routing_and_app_cleanup_failures_safely(self) -> None:
        self.runner.routing_cleanup_residual = True
        self.runner.fail_cleanup = True

        failure = self.adapter._cleanup_device((), time.monotonic() + 5.0)

        self.assertIsNotNone(failure)
        self.assertIn("ANDROID_ROUTING_RULE_CLEANUP_FAILED", str(failure))
        self.assertIn(
            "android_app_cleanup_error=ANDROID_CLEANUP_FAILED",
            "\n".join(failure.__notes__),
        )

    def test_missing_seam_inputs_are_rejected_and_headless_contract_is_explicit(self) -> None:
        with self.assertRaisesRegex(HostedAdapterError, "ANDROID_ADB_UNAVAILABLE"):
            AndroidHostedAdapter(
                runner=self.runner,
                profile=self.profile,
            )
        self.assertLessEqual(
            get_scenario("functional.start-stop-start").max_duration_seconds,
            30 * 60,
        )

    def test_android_ui_mode_is_restricted_to_named_coverage_lanes(self) -> None:
        with self.assertRaisesRegex(HostedAdapterError, "ANDROID_UI_MODE_INVALID"):
            AndroidHostedAdapter(
                runner=self.runner,
                profile=self.profile,
                adb=self.adb,
                ui_mode="headless",
            )

    def test_factory_wires_android_without_a_desktop_cli(self) -> None:
        adapter = adapter_for_platform(
            "android",
            cli=None,
            profile=self.profile,
            runner=self.runner,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
        )
        self.assertIsInstance(adapter, AndroidCompositeHostedAdapter)
        self.assertIsInstance(adapter.gui_auto, AndroidHostedAdapter)
        self.assertIsInstance(adapter.protocol_matrix, AndroidHostedAdapter)
        self.assertEqual(adapter.gui_auto.ui_mode, "gui-auto")
        self.assertEqual(adapter.protocol_matrix.ui_mode, "protocol-matrix")
        self.assertEqual(adapter.capabilities, self.adapter.capabilities)

    def test_factory_composite_routes_contiguous_gui_and_matrix_inventory(self) -> None:
        runner = FakeAndroidRunner(Path(self.directory.name) / "composite-raw")
        runner.observation = _observation(
            connections=[
                {"index": 0, "protocol": "OUTLINE"},
                {"index": 1, "protocol": "WIREGUARD"},
            ],
            connection={"index": 0, "protocol": "OUTLINE"},
        )
        adapter = adapter_for_platform(
            "android",
            profile=self.profile,
            runner=runner,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
        )
        self.assertIsInstance(adapter, AndroidCompositeHostedAdapter)
        sink_events: list[tuple[str, dict[str, object]]] = []
        adapter.set_progress_sink(lambda event, fields: sink_events.append((event, fields)))
        connections = adapter.discover_connections()
        self.assertEqual(
            connections,
            (
                ConnectionIdentity(0, "AUTO"),
                ConnectionIdentity(1, "OUTLINE"),
                ConnectionIdentity(2, "WIREGUARD"),
            ),
        )
        self.assertIs(
            adapter._external_to_internal[connections[0]][0], adapter.gui_auto
        )
        self.assertEqual(
            adapter._external_to_internal[connections[0]][1],
            ConnectionIdentity(0, "AUTO"),
        )
        self.assertIs(
            adapter._external_to_internal[connections[2]][0], adapter.protocol_matrix
        )
        self.assertEqual(
            adapter._external_to_internal[connections[2]][1],
            ConnectionIdentity(1, "WIREGUARD"),
        )

        runner.observation = _observation(
            connections=[{"index": 0, "protocol": "AUTO"}],
            connection={"index": 0, "protocol": "AUTO"},
            coverage_lane="gui-auto",
            gui_auto_verified=True,
            vpn_consent_handled=True,
        )
        adapter.select_connection(connections[0])
        gui_result = FunctionalEngine().run(
            get_scenario("functional.configure"),
            adapter,
            _provenance(adapter),
            connections[0],
        )
        self.assertEqual(gui_result.outcome, "passed")
        self.assertEqual(runner.staged_profile_payloads[-1][1], _GUI_PROFILE)
        gui_command = runner.command_payloads[-1]
        self.assertEqual(gui_command["coverage_lane"], "gui-auto")
        self.assertNotIn("profile_index", gui_command)

        runner.observation = _observation(
            connections=[
                {"index": 0, "protocol": "OUTLINE"},
                {"index": 1, "protocol": "WIREGUARD"},
            ],
            connection={"index": 1, "protocol": "WIREGUARD"},
        )
        adapter.select_connection(connections[2])
        matrix_result = FunctionalEngine().run(
            get_scenario("functional.configure"),
            adapter,
            _provenance(adapter),
            connections[2],
        )
        self.assertEqual(matrix_result.outcome, "passed")
        self.assertEqual(runner.staged_profile_payloads[-1][1], _PROFILE_BUNDLE)
        matrix_command = runner.command_payloads[-1]
        self.assertEqual(matrix_command["coverage_lane"], "protocol-matrix")
        self.assertEqual(matrix_command["profile_index"], 1)
        self.assertTrue(
            any(fields.get("coverage_lane") == "gui-auto" for _, fields in sink_events)
        )
        self.assertTrue(
            any(fields.get("coverage_lane") == "protocol-matrix" for _, fields in sink_events)
        )

        runner.calls.clear()
        adapter.reset(timeout_seconds=2.0)
        reset_calls = [
            call for call in runner.calls
            if call[1:] == ("shell", "am", "force-stop", "com.dobby.vpn")
        ]
        self.assertEqual(len(reset_calls), 2)
        adapter.finalize(timeout_seconds=2.0)

    def test_composite_discovery_and_finalization_retain_both_lane_failures(self) -> None:
        adapter = adapter_for_platform(
            "android",
            profile=self.profile,
            runner=self.runner,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
        )
        with (
            patch.object(
                adapter.gui_auto,
                "discover_connections",
                side_effect=RuntimeError("gui discovery boom"),
            ) as gui_discover,
            patch.object(
                adapter.protocol_matrix,
                "discover_connections",
                side_effect=RuntimeError("matrix discovery boom"),
            ) as matrix_discover,
            self.assertRaisesRegex(
                HostedAdapterError, "ANDROID_COMPOSITE_DISCOVERY_FAILED"
            ) as raised,
        ):
            adapter.discover_connections()
        gui_discover.assert_called_once()
        matrix_discover.assert_called_once()
        discovery_notes = "\n".join(raised.exception.__notes__)
        self.assertIn("gui-auto discovery failure=RuntimeError", discovery_notes)
        self.assertIn("protocol-matrix discovery failure=RuntimeError", discovery_notes)

        with (
            patch.object(
                adapter.gui_auto,
                "finalize",
                side_effect=RuntimeError("gui finalize boom"),
            ) as gui_finalize,
            patch.object(
                adapter.protocol_matrix,
                "finalize",
                side_effect=RuntimeError("matrix finalize boom"),
            ) as matrix_finalize,
            self.assertRaisesRegex(
                HostedAdapterError, "ANDROID_COMPOSITE_FINALIZE_FAILED"
            ) as raised,
        ):
            adapter.finalize()
        gui_finalize.assert_called_once()
        matrix_finalize.assert_called_once()
        finalize_notes = "\n".join(raised.exception.__notes__)
        self.assertIn("gui-auto finalize failure=RuntimeError", finalize_notes)
        self.assertIn("protocol-matrix finalize failure=RuntimeError", finalize_notes)

    def test_gui_auto_configure_contract_requires_visible_acceptance(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        configure = source.index('case "configure":')
        connect = source.index('case "connect":', configure)
        body = source[configure:connect]
        self.assertIn("configureThroughRenderedUI", body)
        self.assertIn("hasFollowingConnectionStart", body)
        self.assertIn("connectThroughRenderedUI", body)
        self.assertIn("disconnectThroughRenderedUI", body)
        self.assertIn("remainingTimeout", body)
        self.assertLess(body.index("configureThroughRenderedUI"), body.index("connectThroughRenderedUI"))
        self.assertLess(body.index("connectThroughRenderedUI"), body.index("disconnectThroughRenderedUI"))

    def test_hosted_real_ui_commits_profile_through_focused_ime(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        configure = source.index("private void configureThroughRenderedUI(")
        helper = source.index("private void injectProfileThroughNativeInput(", configure)
        helper_end = source.index("private JSONObject snapshotResult", helper)
        body = source[helper:helper_end]
        self.assertNotIn("ClipboardManager", source)
        self.assertNotIn("ClipData", source)
        self.assertIn("instrumentation.runOnMainSync", body)
        self.assertIn("activity.getCurrentFocus()", body)
        self.assertIn("focused instanceof EditText", body)
        self.assertIn("focused.onCreateInputConnection(editorInfo)", body)
        self.assertIn("String text = new String(profile, StandardCharsets.UTF_8);", source)
        self.assertNotIn("String text = new String(profile, StandardCharsets.UTF_8).trim();", source)
        self.assertIn("commitAndVerifyNativeInput(instrumentation, text, deadline)", body)
        self.assertNotIn("input.isFocused()", body)
        self.assertIn("foregroundActivity = ensureForegroundActivity();", body)
        self.assertIn("PROFILE_INPUT_CHUNK_CODE_UNITS = 4_096", source)
        self.assertIn("while (start < text.length())", body)
        self.assertIn("editor.setSelection(editor.length());", body)
        self.assertIn("String chunk = text.substring(start, end);", body)
        self.assertIn("connection.commitText(chunk, 1)", body)
        self.assertIn("if (!connection.finishComposingText())", body)
        self.assertIn("Character.isHighSurrogate(text.charAt(end - 1))", body)
        self.assertIn("Character.isLowSurrogate(text.charAt(end))", body)
        self.assertGreaterEqual(body.count("System.currentTimeMillis() >= deadline"), 2)
        self.assertIn('"ANDROID_UI_INPUT_COMMIT_FAILED"', body)
        self.assertIn('"ANDROID_UI_INPUT_FOCUS_LOST"', body)
        self.assertIn("waitForIdleBounded(uiDevice(), deadline)", body)
        self.assertNotIn('String[] lines = text.split("\\n", -1);', body)
        self.assertNotIn("instrumentation.sendKeySync", body)
        self.assertNotIn("KeyEvent", source)
        self.assertNotIn("AccessibilityNodeInfo.ACTION_PASTE", body)
        self.assertNotIn("KeyEvent.KEYCODE_V", body)
        self.assertNotIn("pressKeyCode(", body)
        self.assertNotIn("ANDROID_UI_PASTE_INCOMPLETE", body)
        self.assertNotIn("input.setText(text);", body)
        self.assertNotIn("input.setText(text);", source)
        self.assertNotIn("nativeEditorBufferMatches", body)
        self.assertNotIn("getText()", body)
        for diagnostic in (
            "ANDROID_UI_INPUT_REJECTED",
            "ANDROID_UI_INPUT_FIRST_CHUNK_ONLY",
            "ANDROID_UI_INPUT_TRUNCATED",
            "ANDROID_UI_INPUT_TRANSFORMED",
            "ANDROID_UI_INPUT_INCOMPLETE",
        ):
            self.assertNotIn(diagnostic, body)

    def test_hosted_real_ui_refreshes_activity_before_native_input(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        helper = source.index("private void injectProfileThroughNativeInput(")
        refresh = source.index("foregroundActivity = ensureForegroundActivity();", helper)
        self.assertGreater(refresh, helper)
        helper_end = source.index("private void commitAndVerifyNativeInput", helper)
        self.assertNotIn("input.isFocused()", source[helper:helper_end])
        self.assertIn("currently resumed Activity", source[helper:refresh])

    def test_hosted_real_ui_reacquires_native_editor_after_focus_handoff(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        self.assertIn("INPUT_REACQUIRE_TIMEOUT_MILLIS = 5_000L", source)
        resolver = source.index("private EditText resolveFocusedNativeInput(")
        resolver_end = source.index("private JSONObject snapshotResult", resolver)
        resolver_body = source[resolver:resolver_end]
        self.assertIn("findNativeInputView(activity.getWindow().getDecorView())", resolver_body)
        self.assertIn("editor.requestFocus()", resolver_body)
        self.assertIn("focused != editor", resolver_body)
        self.assertIn("editor.isFocused()", resolver_body)
        commit = source.index("private void commitAndVerifyNativeInput(")
        self.assertIn("focused.onCreateInputConnection(editorInfo)", source[commit:resolver])
        self.assertIn("reacquireDeadline", source[commit:resolver])
        self.assertIn('"ANDROID_UI_INPUT_FOCUS_LOST".equals(failure[0])', source[commit:resolver])
        self.assertIn("Thread.sleep(POLL_MILLIS)", source[commit:resolver])
        self.assertNotIn("input.setText(text);", source[commit:resolver])
        self.assertNotIn("ClipboardManager", source)
        self.assertNotIn("ClipData", source)

    def test_hosted_real_ui_skips_stale_editors_when_resolving_native_input(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        eligibility = source.index("private boolean isEligibleNativeInput(")
        eligibility_end = source.index("/** Commit the opaque profile", eligibility)
        eligibility_body = source[eligibility:eligibility_end]
        self.assertIn("editor.getVisibility() == View.VISIBLE", eligibility_body)
        self.assertIn("editor.isShown()", eligibility_body)
        self.assertIn("editor.isEnabled()", eligibility_body)
        self.assertIn("editor.isFocusable()", eligibility_body)
        self.assertIn("editor.getContext().getPackageName()", eligibility_body)

        resolver = source.index("private EditText resolveFocusedNativeInput(")
        resolver_end = source.index("private JSONObject snapshotResult", resolver)
        resolver_body = source[resolver:resolver_end]
        self.assertIn("findCurrentNativeInput(activity)", resolver_body)
        self.assertIn("findNativeInputView(activity.getWindow().getDecorView())", resolver_body)
        self.assertIn("editor.requestFocus()", resolver_body)
        self.assertIn("focused != editor", resolver_body)
        self.assertIn("editor.isFocused()", resolver_body)

        finder = source.index("private EditText findNativeInputView(View root)")
        finder_end = source.index("/** Commit the opaque profile", finder)
        finder_body = source[finder:finder_end]
        self.assertIn("isEligibleNativeInput(editor) ? editor : null", finder_body)
        self.assertIn("root instanceof ViewGroup", finder_body)
        self.assertIn("findNativeInputView(group.getChildAt(index))", finder_body)

        dismiss = source.index("private void forceHideNativeInput()")
        dismiss_end = source.index("private boolean isEligibleNativeInput", dismiss)
        dismiss_body = source[dismiss:dismiss_end]
        self.assertIn("findCurrentNativeInput(activity)", dismiss_body)
        self.assertIn("findNativeInputView(activity.getWindow().getDecorView())", dismiss_body)

    def test_hosted_real_ui_dismisses_native_editor_on_target_ui_thread(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        dismiss = source.index("private void hideNativeInput(")
        commit = source.index("/** Commit the opaque profile", dismiss)
        body = source[dismiss:commit]
        self.assertIn("INPUT_DISMISS_INITIAL_WAIT_MILLIS", body)
        self.assertIn("device.pressBack()", body)
        self.assertIn("waitForNativeInputGone(device", body)
        self.assertIn("forceHideNativeInput()", body)
        self.assertIn('"ANDROID_UI_INPUT_DISMISS_FAILED"', body)
        self.assertIn("InputMethodManager", body)
        self.assertIn("instrumentation.runOnMainSync", body)
        self.assertIn("activity.getWindow().getDecorView()", body)
        self.assertIn("manager.hideSoftInputFromWindow", body)
        self.assertIn("editor.clearFocus()", body)
        self.assertIn("editor.setVisibility(View.GONE)", body)
        self.assertIn("private EditText findNativeInputView(View root)", body)
        self.assertIn("root instanceof ViewGroup", body)
        self.assertNotIn("input.setText(text);", body)

    def test_android_activity_launch_does_not_wait_for_fyne_render_idle(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        start = source.index("private Activity ensureForegroundActivity()")
        end = source.index("private void acceptVpnConsent", start)
        body = source[start:end]
        self.assertIn("ActivityLifecycleMonitorRegistry", source)
        self.assertIn("Stage.RESUMED", body)
        self.assertIn("ACTIVITY_RESUME_TIMEOUT_MILLIS", body)
        self.assertIn("runOnMainSync", body)
        self.assertIn("context.startActivity(launch)", body)
        self.assertIn("Intent.FLAG_ACTIVITY_NEW_TASK", body)
        self.assertIn("findLiveFyneActivity()", body)
        self.assertIn("context.getClassLoader().loadClass", body)
        self.assertIn("activity.hasWindowFocus()", body)
        self.assertNotIn("Intent.FLAG_ACTIVITY_CLEAR_TASK", body)
        self.assertNotIn("Intent.FLAG_ACTIVITY_CLEAR_TOP", body)
        self.assertNotIn("startActivitySync(", body)
        self.assertNotIn("waitForIdleSync(", body)
        monitor_start = source.index("private Activity findResumedTargetActivity()")
        monitor_end = source.index("private void acceptVpnConsent", monitor_start)
        monitor_body = source[monitor_start:monitor_end]
        self.assertIn("AtomicReference<Activity>", monitor_body)
        self.assertIn("runOnMainSync", monitor_body)
        self.assertIn("getActivitiesInStage(Stage.RESUMED)", monitor_body)

    def test_android_rendered_drivers_disable_global_selector_wait(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        kotlin_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/kotlin/com/dobby/GoUiInstrumentedTest.kt"
        )
        for source in (
            java_path.read_text(encoding="utf-8"),
            kotlin_path.read_text(encoding="utf-8"),
        ):
            self.assertIn("Configurator.getInstance().setWaitForSelectorTimeout(0)", source)
            self.assertIn("@Before", source)

    def test_android_vpn_consent_retries_enabled_system_action_until_granted(self) -> None:
        java_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/java/com/dobby/GoUiHostedProfileTest.java"
        )
        source = java_path.read_text(encoding="utf-8")
        start = source.index("private void acceptVpnConsent(long timeout)")
        end = source.index("private Network awaitVpnNetwork", start)
        body = source[start:end]
        self.assertIn("findVpnConsentButton", body)
        self.assertIn('"android:id/button1"', body)
        self.assertIn('"com.android.vpndialogs:id/button1"', body)
        self.assertIn("button.isEnabled()", body)
        # UiAutomator's accessibility clickable bit is not a reliable
        # readiness signal for Android-owned VPN dialogs. The helper keeps
        # the stable selector and enabled-state assertion, then invokes the
        # supported UiObject2.click() action.
        self.assertNotIn("button.isClickable()", body)
        self.assertGreaterEqual(body.count("VpnService.prepare(context) == null"), 2)
        self.assertIn("grantDeadline", body)
        self.assertNotIn('By.text("Connect")', body)

    def test_android_renderer_accepts_ready_as_an_idle_state(self) -> None:
        kotlin_path = (
            Path(__file__).resolve().parents[3]
            / "android_module/app/src/androidTest/kotlin/com/dobby/GoUiInstrumentedTest.kt"
        )
        source = kotlin_path.read_text(encoding="utf-8")
        self.assertIn('waitForOneOf(arrayOf("Disconnected", "Ready"), 30_000)', source)
        self.assertIn('waitForOneOf(arrayOf("Disconnected", "Ready", "Error", "Failed"), 30_000)', source)
        self.assertNotIn('requireObject("Disconnected")', source)
        self.assertNotIn("dumpWindowHierarchy", source)
        self.assertNotIn("takeScreenshot", source)
        self.assertNotIn("lastTapDiagnostic", source)

    def test_android_source_sha_is_optional_and_omitted_when_absent(self) -> None:
        adapter = adapter_for_platform(
            "android",
            profile=self.profile,
            runner=self.runner,
            adb=self.adb,
            local_mode=True,
        )
        self.assertIsNone(adapter.source_sha)
        self.assertFalse(hasattr(adapter, "local_mode"))
        self.assertTrue(adapter.capabilities)
        command_file, _profile, _output = adapter.protocol_matrix._write_command(
            get_scenario("functional.configure")
        )
        command = json.loads(command_file.read_text(encoding="utf-8"))
        self.assertNotIn("source_sha", command)
        self.assertRegex(
            command["progress_file"],
            r"^android-hosted-[0-9a-f]+\.progress\.json$",
        )
        self.assertNotIn("profile", command["progress_file"])

    def test_finalizer_completes_the_shared_adapter_contract(self) -> None:
        self.adapter.finalize(12.5)
        with self.assertRaisesRegex(HostedAdapterError, "INVALID_FINALIZE_TIMEOUT"):
            self.adapter.finalize(0)

    def test_factory_uses_fixed_public_endpoints_for_android(self) -> None:
        adapter = adapter_for_platform(
            "android",
            profile=self.profile,
            runner=self.runner,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
        )
        self.assertEqual(adapter.identity_url, PUBLIC_IDENTITY_URL)
        self.assertEqual(adapter.download_url, PUBLIC_DOWNLOAD_URL)
        self.assertEqual(adapter.upload_url, PUBLIC_UPLOAD_URL)
        self.assertEqual(adapter.latency_url, PUBLIC_LATENCY_URL)
        self.assertTrue(adapter.capabilities)

    def test_binary_staging_disables_pty_allocation(self) -> None:
        runner = FakeAndroidRunner(Path(self.directory.name) / "input-raw")
        adapter = AndroidHostedAdapter(
            runner=runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url="https://latency.example.test/blob",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        connection = _select_connection(adapter)
        result = FunctionalEngine().run(
            get_scenario("functional.configure"), adapter, _provenance(adapter), connection
        )
        self.assertEqual(result.outcome, "passed")
        staged = [
            call for call in runner.calls
            if len(call) > 5 and call[1:4] == ("shell", "-T", "sh")
        ]
        self.assertGreaterEqual(len(staged), 2)
        self.assertTrue(all(call[2] == "-T" for call in staged))
        self.assertIn("mkdir -p /data/user/0/com.dobby.vpn/files", staged[0][-1])
        self.assertIn("cat > /data/user/0/com.dobby.vpn/files/", staged[0][-1])
        self.assertIn(".tmp", staged[0][-1])
        self.assertIn("mv -f", staged[0][-1])
        self.assertIn("chown", staged[0][-1])
        self.assertIn("restorecon", staged[0][-1])
        self.assertFalse(any("run-as" in call for call in runner.calls))
        # adb's shell transport joins argv elements before the remote shell
        # parses them.  The complete ``sh -c`` payload must therefore be
        # shell-quoted, otherwise ``sh -c`` receives only ``mkdir`` and the
        # Android toybox command fails with a missing operand.
        self.assertTrue(staged[0][-1].startswith("'"))
        self.assertTrue(staged[0][-1].endswith("'"))

    def test_missing_or_non_executable_adb_fails_closed(self) -> None:
        with self.assertRaisesRegex(HostedAdapterError, "ANDROID_ADB_UNAVAILABLE"):
            AndroidHostedAdapter(
                runner=self.runner,
                profile=self.profile,
                adb=Path(self.directory.name) / "missing-adb",
            )
        non_executable = Path(self.directory.name) / "non-executable-adb"
        non_executable.write_bytes(b"not executable\n")
        non_executable.chmod(0o600)
        with self.assertRaisesRegex(HostedAdapterError, "ANDROID_ADB_UNAVAILABLE"):
            AndroidHostedAdapter(
                runner=self.runner,
                profile=self.profile,
                adb=non_executable,
            )

    def test_android_endpoints_reject_query_fragment_and_userinfo(self) -> None:
        for invalid in (
            "https://identity.example.test/ip?token=x",
            "https://identity.example.test/ip#fragment",
            "https://user:password@identity.example.test/ip",
        ):
            with self.subTest(invalid=invalid):
                with self.assertRaisesRegex(HostedAdapterError, "IDENTITY_URL_INVALID"):
                    AndroidHostedAdapter(
                        runner=self.runner,
                        profile=self.profile,
                        adb=self.adb,
                        source_sha=_SOURCE_SHA,
                        identity_url=invalid,
                        latency_url="https://latency.example.test/blob",
                        download_url="https://download.example.test/blob",
                        upload_url="https://upload.example.test/blob",
                    )

    def test_android_measurement_endpoint_accepts_query(self) -> None:
        endpoint = "https://speed.cloudflare.com/__down?bytes=1048576"
        adapter = AndroidHostedAdapter(
            runner=self.runner,
            profile=self.profile,
            adb=self.adb,
            identity_url="https://identity.example.test/ip",
            latency_url=endpoint,
            download_url=endpoint,
            upload_url="https://speed.cloudflare.com/__up",
        )

        self.assertEqual(adapter.latency_url, endpoint)
        self.assertEqual(adapter.download_url, endpoint)

    def test_android_measurement_query_is_not_local_mode_specific(self) -> None:
        endpoint = "https://speed.cloudflare.com/__down?bytes=1048576"
        adapter = AndroidHostedAdapter(
            runner=self.runner,
            profile=self.profile,
            adb=self.adb,
            source_sha=_SOURCE_SHA,
            identity_url="https://identity.example.test/ip",
            latency_url=endpoint,
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        self.assertEqual(adapter.latency_url, endpoint)

    def test_android_supplied_source_sha_must_be_full_lowercase(self) -> None:
        for invalid in ("a" * 39, "A" * 40, "a" * 41):
            with self.subTest(invalid=invalid):
                with self.assertRaisesRegex(HostedAdapterError, "SOURCE_SHA_INVALID"):
                    AndroidHostedAdapter(
                        runner=self.runner,
                        profile=self.profile,
                        adb=self.adb,
                        source_sha=invalid,
                        identity_url="https://identity.example.test/ip",
                        latency_url="https://latency.example.test/blob",
                        download_url="https://download.example.test/blob",
                        upload_url="https://upload.example.test/blob",
                    )

    def test_android_adapter_rejects_unexpected_constructor_arguments(self) -> None:
        for argument, value in (
            ("network_interface", "eth0"),
            ("app_log", self.profile),
            ("service_log", self.profile),
        ):
            with self.subTest(argument=argument):
                with self.assertRaisesRegex(
                    HostedAdapterError, "ANDROID_ARGUMENT_UNEXPECTED"
                ):
                    AndroidHostedAdapter(
                        runner=self.runner,
                        profile=self.profile,
                        **{argument: value},
                    )

    def test_factory_rejects_android_desktop_arguments(self) -> None:
        values: dict[str, object] = {
            "cli": Path("/unexpected"),
            "service_pid": 1,
            "service_binary": Path("/unexpected"),
            "service_socket": Path("/unexpected"),
            "service_library_path": Path("/unexpected"),
            "service_pid_file": Path("/unexpected"),
            "service_identity_file": Path("/unexpected"),
            "network_interface": "eth0",
        }
        for argument, value in values.items():
            with self.subTest(argument=argument):
                with self.assertRaisesRegex(ValueError, f"unexpected {argument}"):
                    adapter_for_platform(
                        "android",
                        profile=self.profile,
                        runner=self.runner,
                        adb=self.adb,
                        **{argument: value},
                    )

    def test_factory_has_no_service_log_argument(self) -> None:
        with self.assertRaisesRegex(TypeError, "service_log"):
            adapter_for_platform(
                "android",
                profile=self.profile,
                runner=self.runner,
                adb=self.adb,
                service_log=self.profile,
            )

    def test_factory_rejects_a_desktop_lane_without_a_cli(self) -> None:
        with self.assertRaisesRegex(ValueError, "requires --cli"):
            adapter_for_platform(
                "linux",
                cli=None,
                profile=self.profile,
                runner=self.runner,
            )

    def test_parser_accepts_android_specific_inputs_without_a_cli(self) -> None:
        from torturer_checks.hosted.run import build_parser

        parsed = build_parser().parse_args([
            "--platform", "android", "--profile", str(self.profile),
            "--source-sha", _SOURCE_SHA,
            "--platform-version", "35",
            "--lane-timeout-seconds", "1800",
            "--output", str(Path(self.directory.name) / "result.json"),
            "--adb", "/synthetic/adb",
        ])
        self.assertIsNone(parsed.cli)

if __name__ == "__main__":
    unittest.main()
