from __future__ import annotations

import contextlib
import copy
import base64
from concurrent.futures import ThreadPoolExecutor
import gc
import io
import json
import os
import re
import signal
import socket
from pathlib import Path
import subprocess
import tempfile
import sys
import time
import traceback
import unittest
import warnings
from unittest import mock

import torturer_checks.hosted.run as hosted_run
from torturer_checks.hosted.cli import (
    CommandResult,
    HostedAdapterError,
    HostedCLIAdapter,
    SubprocessRunner,
)
from torturer_checks.hosted.linux import (
    LinuxHostedAdapter,
    LinuxServiceProcessController,
    _LINUX_PROCESS_STAT_SCRIPT,
    _SERVICE_LAUNCH_SCRIPT,
    _parse_linux_process_census,
)
from torturer_checks.hosted.factory import (
    PUBLIC_DOWNLOAD_URL,
    PUBLIC_IDENTITY_URL,
    PUBLIC_UPLOAD_URL,
    adapter_for_platform,
)
from torturer_checks.hosted.macos import MacOSHostedAdapter
from torturer_checks.hosted.windows import WindowsHostedAdapter
from torturer_checks.hosted.run import (
    _coverage_contract,
    _finalize_adapter,
    _qualification_exit_code,
    _run_scenarios,
    _select_scenarios,
    build_parser,
)
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.engine import (
    CapabilityUnavailable,
    FunctionalEngine,
    ScenarioExecutionError,
)
from torturer_contract.functional.scenarios import (
    ScenarioStep,
    get_scenario,
    suite_set,
    test_set as canonical_test_set,
)


class FakeRunner:
    def __init__(self) -> None:
        self.calls: list[tuple[str, ...]] = []
        self.timeouts: list[float] = []
        self.connected = False
        self.external_calls = 0
        self.restore_identity = True
        self.baseline_ip = b"198.51.100.10\n"
        self.disconnected_ip = b"198.51.100.10\n"

    def run(self, command, *, timeout_seconds):
        argv = tuple(command)
        self.calls.append(argv)
        self.timeouts.append(float(timeout_seconds))
        if argv[0] == "curl":
            return CommandResult(argv, 0, b"0.25\t1000000\t200\n", b"curl diagnostic\n")
        if argv[0] == "sudo":
            return CommandResult(argv, 0, b"sudo diagnostic\n", b"")
        if argv[:2] == ("sh", "-c") and "systemctl show" in argv[2]:
            return CommandResult(argv, 0, b"", b"")
        operation = argv[1]
        if operation == "profile-inventory":
            return CommandResult(
                argv, 0, b'{"profiles":[{"index":0,"protocol":"OUTLINE"}]}\n', b""
            )
        if operation == "check-config":
            return CommandResult(argv, 0, b"profiles=1 source=file\n", b"")
        if operation == "connect-profile":
            self.connected = True
            return CommandResult(argv, 0, b"CONNECTED\n", b"")
        if operation == "status":
            state = b"Connected" if self.connected else b"Disconnected"
            return CommandResult(argv, 0, b'{"code":%d,"state":"%s"}\n' % (2 if self.connected else 0, state), b"")
        if operation == "external-ip":
            self.external_calls += 1
            if self.external_calls == 1:
                value = self.baseline_ip
            elif not self.connected and self.restore_identity:
                value = self.disconnected_ip
            else:
                value = b"203.0.113.10\n"
            return CommandResult(argv, 0, value, b"")
        if operation == "disconnect":
            self.connected = False
            return CommandResult(argv, 0, b"DISCONNECTED\n", b"")
        raise AssertionError(argv)


class SequencedIdentityRunner(FakeRunner):
    """Return exact external identities in order, then repeat the last one."""

    def __init__(
        self, identities: list[bytes], *, disconnected_status_delay: int = 0
    ) -> None:
        super().__init__()
        if not identities:
            raise ValueError("identity sequence must not be empty")
        self.identities = identities
        self.disconnected_status_delay = disconnected_status_delay

    def run(self, command, *, timeout_seconds):
        argv = tuple(command)
        if (
            len(argv) > 1
            and argv[1] == "status"
            and not self.connected
            and self.disconnected_status_delay > 0
        ):
            self.calls.append(argv)
            self.timeouts.append(float(timeout_seconds))
            self.disconnected_status_delay -= 1
            return CommandResult(argv, 0, b'{"code":2,"state":"Connected"}\n', b"")
        if len(argv) > 1 and argv[1] == "external-ip":
            self.calls.append(argv)
            self.timeouts.append(float(timeout_seconds))
            index = min(self.external_calls, len(self.identities) - 1)
            self.external_calls += 1
            return CommandResult(argv, 0, self.identities[index], b"")
        return super().run(command, timeout_seconds=timeout_seconds)


def _connection(adapter) -> ConnectionIdentity:
    connections = adapter.discover_connections()
    adapter.select_connection(connections[0])
    return connections[0]


class _DiscoverableAdapter:
    def discover_connections(self, timeout_seconds: float = 30.0):
        del timeout_seconds
        return (ConnectionIdentity(0, "OUTLINE"),)

    def select_connection(self, connection: ConnectionIdentity) -> None:
        self.selected_connection = connection


class HostedCLIAdapterTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory(prefix="hosted-cli-adapter-")
        root = Path(self.directory.name)
        self.cli = root / "dobby-cli"
        self.cli.write_bytes(b"synthetic executable")
        os.chmod(self.cli, 0o700)
        self.profile = root / "profile.toml"
        self.profile.write_text("[[Outline]]\nPassword = \"synthetic\"\n", encoding="utf-8")
        os.chmod(self.profile, 0o600)
        self.runner = FakeRunner()
        self.runner.raw_directory = root / "runner-raw"
        self.adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
        self.connection = _connection(self.adapter)
        self.runner.calls.clear()
        self.runner.timeouts.clear()
        self.addCleanup(self.directory.cleanup)

    def test_common_operations_are_observations_not_assertions(self) -> None:
        self.assertTrue({Capability.CONFIGURE, Capability.CONNECT, Capability.TUNNEL_INTERFACE,
                         Capability.ROUTING_IDENTITY, Capability.DISCONNECT,
                         Capability.RESOURCE_CLEANUP} <= self.adapter.capabilities)
        scenario = get_scenario("functional.start-stop-start")
        engine = FunctionalEngine()
        result = engine.run(scenario, self.adapter, _provenance(self.adapter), self.connection)
        self.assertEqual(result.outcome, "passed")
        self.assertTrue(any(call[1] == "connect-profile" for call in self.runner.calls))
        self.adapter.reset()
        self.assertFalse(self.runner.connected)

    def test_command_failure_reports_complete_separate_streams(self) -> None:
        command = (str(self.cli), "connect-profile", str(self.profile), "0")
        cases = (
            (
                CommandResult(command, 23, b"connect stdout\n", b"connect stderr\n"),
                "CONNECT_FAILED",
            ),
            (
                CommandResult(
                    command,
                    124,
                    b"timeout stdout\n",
                    b"timeout stderr\n",
                    timed_out=True,
                ),
                "COMMAND_TIMEOUT",
            ),
        )
        for result, expected_code in cases:
            with self.subTest(expected_code=expected_code):
                with (
                    mock.patch.object(self.runner, "run", return_value=result),
                    self.assertRaisesRegex(
                        ScenarioExecutionError, expected_code
                    ) as caught,
                ):
                    self.adapter._command(
                        command[1:], timeout=5.0, failure="CONNECT_FAILED"
                    )
                self.assertEqual(
                    caught.exception.__notes__,
                    [
                        f"command_returncode={result.returncode}",
                        f"command_timed_out={result.timed_out}",
                        f"command_stdout:\n{result.stdout.decode()}",
                        f"command_stderr:\n{result.stderr.decode()}",
                    ],
                )

    def test_command_failure_retains_runner_cause_and_safe_notes(self) -> None:
        underlying = RuntimeError("underlying command failure")
        runner_error = HostedAdapterError("COMMAND_UNAVAILABLE")
        runner_error.add_note("command_returncode=-1")
        runner_error.add_note("command_timed_out=False")
        runner_error.stdout = b"runner stdout\n"
        runner_error.stderr = b"runner stderr\n"
        runner_error.add_note("command_stdout:\nrunner stdout\n")
        runner_error.add_note("command_stderr:\nrunner stderr\n")

        class RaisingRunner:
            def run(self, command, *, timeout_seconds):
                del command, timeout_seconds
                raise runner_error from underlying

        adapter = HostedCLIAdapter(
            cli=self.cli, profile=self.profile, runner=RaisingRunner()
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "COMMAND_UNAVAILABLE"
        ) as caught:
            adapter._command(
                ("connect-profile", str(self.profile), "0"),
                timeout=5.0,
                failure="CONNECT_FAILED",
            )
        self.assertIs(caught.exception.__cause__, runner_error)
        self.assertIs(caught.exception.__cause__.__cause__, underlying)
        self.assertTrue(any("command_error=HostedAdapterError" in note for note in caught.exception.__notes__))
        self.assertEqual(caught.exception.__cause__.__notes__, runner_error.__notes__)
        formatted = "".join(traceback.format_exception(caught.exception))
        for note in runner_error.__notes__:
            self.assertGreaterEqual(formatted.count(note), 1)

    def test_linux_external_ip_probe_reports_bounded_curl_failure(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
        )
        adapter._routing_probe_host = "probe.example"
        adapter._routing_probe_address = "203.0.113.8"
        result = CommandResult(
            ("curl", "--fail", "https://probe.example/api/v0/ip"),
            6,
            b"partial curl stdout\n",
            b"curl: (6) Could not resolve host\n",
        )
        with (
            mock.patch.object(self.runner, "run", return_value=result),
            self.assertRaisesRegex(ScenarioExecutionError, "ROUTING_PROBE_FAILED") as caught,
        ):
            adapter._probe_external_ip(5.0)
        self.assertEqual(
            caught.exception.__notes__,
            [
                f"command_returncode={result.returncode}",
                f"command_timed_out={result.timed_out}",
                f"command_stdout:\n{result.stdout.decode()}",
                f"command_stderr:\n{result.stderr.decode()}",
            ],
        )

    def test_external_ip_probe_reports_bounded_curl_failure(self) -> None:
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
        )
        result = CommandResult(
            ("curl", "--fail", "https://probe.example/api/v0/ip"),
            6,
            b"partial curl stdout\n",
            b"curl: (6) Could not resolve host\n",
        )
        with (
            mock.patch.object(self.runner, "run", return_value=result),
            self.assertRaisesRegex(
                ScenarioExecutionError, "EXTERNAL_IDENTITY_FAILED"
            ) as caught,
        ):
            adapter._external_ip(5.0)
        self.assertEqual(
            caught.exception.__notes__,
            [
                f"command_returncode={result.returncode}",
                f"command_timed_out={result.timed_out}",
                f"command_stdout:\n{result.stdout.decode()}",
                f"command_stderr:\n{result.stderr.decode()}",
            ],
        )

    def test_linux_routing_resolution_failure_reports_bounded_getent_status(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
        )
        result = CommandResult(
            ("/usr/bin/getent", "ahostsv4", "probe.example"),
            2,
            b"getent stdout\n",
            b"getent: Name or service not known\n",
        )
        with (
            mock.patch.object(self.runner, "run", return_value=result),
            self.assertRaisesRegex(
                ScenarioExecutionError, "ROUTING_PROBE_RESOLUTION_FAILED"
            ) as caught,
        ):
            adapter._resolve_routing_probe(5.0)
        self.assertEqual(
            caught.exception.__notes__,
            [
                f"command_returncode={result.returncode}",
                f"command_timed_out={result.timed_out}",
                f"command_stdout:\n{result.stdout.decode()}",
                f"command_stderr:\n{result.stderr.decode()}",
            ],
        )

    def test_linux_network_transition_retries_routing_probe_failure(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            network_interface="eth0",
        )
        probe_failure = ScenarioExecutionError("ROUTING_PROBE_FAILED")
        with (
            mock.patch.object(
                adapter,
                "_privileged",
                return_value=CommandResult((), 0, b"", b""),
            ),
            mock.patch.object(adapter, "_connected", return_value=True),
            mock.patch.object(
                adapter,
                "_routing_verified",
                side_effect=[probe_failure, True],
            ) as routing_verified,
            mock.patch(
                "torturer_checks.hosted.linux.time.monotonic",
                side_effect=[0.0] * 10 + [1.0, 1.0, 2.0],
            ),
            mock.patch("torturer_checks.hosted.linux.time.sleep"),
        ):
            result = adapter._network_transition(30.0)
        self.assertEqual(result, {"network_transition_verified": True})
        self.assertEqual(routing_verified.call_count, 2)

    def test_linux_network_transition_raises_latest_routing_probe_failure_at_deadline(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            network_interface="eth0",
        )
        first_failure = ScenarioExecutionError("ROUTING_PROBE_FAILED")
        first_failure.add_note("command_stdout:\nfirst stdout\n")
        last_failure = ScenarioExecutionError("ROUTING_PROBE_FAILED")
        last_failure.add_note("command_stdout:\nlast stdout\n")
        with (
            mock.patch.object(
                adapter,
                "_privileged",
                return_value=CommandResult((), 0, b"", b""),
            ),
            mock.patch.object(adapter, "_connected", return_value=True),
            mock.patch.object(
                adapter,
                "_routing_verified",
                side_effect=[first_failure, last_failure],
            ),
            mock.patch(
                "torturer_checks.hosted.linux.time.monotonic",
                side_effect=[0.0] * 10 + [1.0, 1.0, 30.0],
            ),
            mock.patch("torturer_checks.hosted.linux.time.sleep"),
        ):
            with self.assertRaisesRegex(
                ScenarioExecutionError, "ROUTING_PROBE_FAILED"
            ) as caught:
                adapter._network_transition(30.0)
        self.assertIs(caught.exception, last_failure)
        self.assertEqual(
            caught.exception.__notes__, ["command_stdout:\nlast stdout\n"]
        )

    def test_desktop_inventory_and_profile_selection_are_fully_dynamic(self) -> None:
        protocols = ("OUTLINE", "XRAY", "TRUST_TUNNEL", "OUTLINE")

        class DynamicRunner(FakeRunner):
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if len(argv) > 1 and argv[1] == "profile-inventory":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    payload = {
                        "profiles": [
                            {"index": index, "protocol": protocol}
                            for index, protocol in enumerate(protocols)
                        ]
                    }
                    return CommandResult(argv, 0, json.dumps(payload).encode(), b"")
                if len(argv) > 1 and argv[1] == "check-config":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"profiles=4 source=file\n", b"")
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = DynamicRunner()
        adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=runner)
        connections = adapter.discover_connections()
        self.assertEqual(tuple(item.protocol for item in connections), protocols)
        for connection in connections:
            adapter.select_connection(connection)
            self.assertTrue(adapter._configure(5))
            adapter.execute(
                ScenarioStep(id="connect", operation="connect", timeout_seconds=5)
            )
            adapter.execute(
                ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=5)
            )
        selected_indices = [
            call[-1] for call in runner.calls if len(call) > 1 and call[1] == "connect-profile"
        ]
        self.assertEqual(selected_indices, ["0", "1", "2", "3"])

    def test_local_mode_uses_fixed_public_endpoints(self) -> None:
        adapter = adapter_for_platform(
            "linux",
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            local_mode=True,
        )
        self.assertEqual(adapter.identity_url, PUBLIC_IDENTITY_URL)
        self.assertEqual(adapter.download_url, PUBLIC_DOWNLOAD_URL)
        self.assertEqual(adapter.upload_url, PUBLIC_UPLOAD_URL)
        self.assertTrue(Capability.TRAFFIC_MEASUREMENT in adapter.capabilities)

    def test_desktop_identity_probe_can_use_configured_public_endpoint(self) -> None:
        class IdentityRunner(FakeRunner):
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[0] == "curl":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"203.0.113.42\n", b"identity diagnostic\n")
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = IdentityRunner()
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            identity_url="https://identity.example.test/ip",
        )
        self.assertEqual(adapter._external_ip(5), "203.0.113.42")
        self.assertEqual(runner.calls[0][0], "curl")
        self.assertIn("--show-error", runner.calls[0])
        self.assertNotIn("--silent", runner.calls[0])

    def test_stability_uses_repeated_tunneled_https_samples(self) -> None:
        runner = FakeRunner()
        runner.connected = True
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            identity_url="https://identity.example.test/ip",
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with mock.patch("torturer_checks.hosted.cli.time.sleep"):
            result = adapter._stability(15)
        self.assertTrue(result["stability_verified"])
        self.assertEqual(
            [call[1] for call in runner.calls if len(call) > 1 and call[0] != "curl"],
            ["status"] * 5,
        )
        curl_calls = [call for call in runner.calls if call[0] == "curl"]
        self.assertEqual(len(curl_calls), 5)
        self.assertTrue(all(call[-1] == "https://download.example.test/blob" for call in curl_calls))

    def test_stability_reports_http_measurement_failure(self) -> None:
        class ServiceUnavailableRunner(FakeRunner):
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[0] == "curl":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"0.01\t0\t503\n", b"service unavailable\n")
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = ServiceUnavailableRunner()
        runner.connected = True
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            download_url=PUBLIC_DOWNLOAD_URL,
            upload_url=PUBLIC_UPLOAD_URL,
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "MEASUREMENT_SERVICE_UNAVAILABLE"
        ) as raised:
            adapter._stability(5)
        self.assertIn("stability_sample=1/5", raised.exception.__notes__)

    def test_routing_identity_waits_for_convergence_without_classifying_baseline_as_tunnel(self) -> None:
        baseline = b"198.51.100.10\n"
        tunneled = b"203.0.113.10\n"
        runner = SequencedIdentityRunner([baseline, baseline, tunneled])
        adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=runner)
        _connection(adapter)

        adapter.execute(ScenarioStep(id="connect", operation="connect", timeout_seconds=5))
        with mock.patch("torturer_checks.hosted.cli.time.sleep") as sleep:
            result = adapter.execute(
                ScenarioStep(
                    id="routing",
                    operation="observe_routing_identity",
                    timeout_seconds=5,
                )
            )

        self.assertEqual(result, {"routing_verified": True})
        self.assertEqual(adapter._tunneled_ips, {"203.0.113.10"})
        self.assertEqual(runner.external_calls, 3)
        sleep.assert_called_once()

    def test_disconnect_waits_for_status_and_route_restoration(self) -> None:
        baseline = b"198.51.100.10\n"
        tunneled = b"203.0.113.10\n"
        runner = SequencedIdentityRunner(
            [baseline, tunneled, tunneled, baseline],
            disconnected_status_delay=1,
        )
        adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=runner)
        _connection(adapter)

        adapter.execute(ScenarioStep(id="connect", operation="connect", timeout_seconds=5))
        adapter.execute(
            ScenarioStep(
                id="routing",
                operation="observe_routing_identity",
                timeout_seconds=5,
            )
        )
        with mock.patch("torturer_checks.hosted.cli.time.sleep") as sleep:
            result = adapter.execute(
                ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=5)
            )

        self.assertEqual(result, {"disconnect_clean": True})
        self.assertEqual(runner.external_calls, 4)
        self.assertEqual(sum(call[1] == "status" for call in runner.calls), 3)
        self.assertEqual(sleep.call_count, 2)

    def test_routing_convergence_returns_false_at_exact_deadline(self) -> None:
        runner = SequencedIdentityRunner([b"198.51.100.10\n"])
        adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=runner)
        adapter._baseline_ip = "198.51.100.10"

        with (
            mock.patch(
                "torturer_checks.hosted.cli.time.monotonic",
                side_effect=[100.0, 100.1, 105.0],
            ),
            mock.patch("torturer_checks.hosted.cli.time.sleep") as sleep,
        ):
            self.assertFalse(adapter._wait_for_routing_verified(5.0))

        self.assertEqual(adapter._tunneled_ips, set())
        self.assertEqual(runner.external_calls, 1)
        sleep.assert_not_called()

    def test_routing_convergence_retries_transient_probe_failure(self) -> None:
        for reason in ("EXTERNAL_IDENTITY_FAILED", "ROUTING_INTERFACE_UNAVAILABLE"):
            with self.subTest(reason=reason):
                adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
                failure = ScenarioExecutionError(reason)
                failure.add_note("command_stderr=b'temporary routing failure'")
                with (
                    mock.patch.object(
                        adapter, "_routing_verified", side_effect=[failure, True]
                    ) as routing_verified,
                    mock.patch("torturer_checks.hosted.cli.time.sleep") as sleep,
                ):
                    self.assertTrue(adapter._wait_for_routing_verified(5.0))
                self.assertEqual(routing_verified.call_count, 2)
                sleep.assert_called_once()

    def test_routing_convergence_preserves_last_probe_failure_at_deadline(self) -> None:
        adapter = HostedCLIAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
        failure = ScenarioExecutionError("EXTERNAL_IDENTITY_FAILED")
        failure.add_note("command_stderr=b'temporary DNS failure'")
        with (
            mock.patch.object(adapter, "_routing_verified", side_effect=failure),
            mock.patch(
                "torturer_checks.hosted.cli.time.monotonic",
                side_effect=[100.0, 100.1, 105.0],
            ),
            mock.patch("torturer_checks.hosted.cli.time.sleep"),
            self.assertRaisesRegex(
                ScenarioExecutionError, "EXTERNAL_IDENTITY_FAILED"
            ) as caught,
        ):
            adapter._wait_for_routing_verified(5.0)
        self.assertIs(caught.exception, failure)
        self.assertEqual(
            caught.exception.__notes__, ["command_stderr=b'temporary DNS failure'"]
        )

    def test_factory_uses_fixed_public_endpoints_for_every_desktop(self) -> None:
        for platform, expected_type in (
            ("linux", LinuxHostedAdapter),
            ("windows", WindowsHostedAdapter),
            ("macos", MacOSHostedAdapter),
        ):
            with self.subTest(platform=platform):
                adapter = adapter_for_platform(
                    platform,
                    cli=self.cli,
                    profile=self.profile,
                    runner=FakeRunner(),
                )
                self.assertIsInstance(adapter, expected_type)
                self.assertEqual(adapter.identity_url, PUBLIC_IDENTITY_URL)
                self.assertEqual(adapter.download_url, PUBLIC_DOWNLOAD_URL)
                self.assertEqual(adapter.upload_url, PUBLIC_UPLOAD_URL)

    def test_windows_and_macos_use_the_same_canonical_cli_scenarios(self) -> None:
        scenario = get_scenario("functional.start-stop-start")
        for adapter_class, expected_id in (
            (WindowsHostedAdapter, "hosted-windows-cli"),
            (MacOSHostedAdapter, "hosted-macos-cli"),
        ):
            runner = FakeRunner()
            adapter = adapter_class(cli=self.cli, profile=self.profile, runner=runner)
            self.assertEqual(adapter.adapter_id, expected_id)
            result = FunctionalEngine().run(scenario, adapter, _provenance(adapter), _connection(adapter))
            self.assertEqual(result.outcome, "passed")

    def test_hosted_desktop_unsafe_gaps_have_platform_reason_codes(self) -> None:
        windows = WindowsHostedAdapter(cli=self.cli, profile=self.profile, runner=FakeRunner())
        macos = MacOSHostedAdapter(cli=self.cli, profile=self.profile, runner=FakeRunner())
        linux = LinuxHostedAdapter(cli=self.cli, profile=self.profile, runner=FakeRunner())
        self.assertEqual(
            windows.capability_unavailable_reasons[Capability.NETWORK_TRANSITION],
            "HOSTED_WINDOWS_UPLINK_TOGGLE_UNSUPPORTED",
        )
        self.assertEqual(
            macos.capability_unavailable_reasons[Capability.NETWORK_TRANSITION],
            "HOSTED_MACOS_UPLINK_TOGGLE_UNSUPPORTED",
        )
        self.assertEqual(
            linux.capability_unavailable_reasons[Capability.NETWORK_TRANSITION],
            "HOSTED_LINUX_INTERFACE_REQUIRED",
        )

    def test_reconnect_reuses_public_cli_and_leaves_clean_baseline(self) -> None:
        self.assertIn(Capability.RECONNECT, self.adapter.capabilities)
        result = FunctionalEngine().run(
            get_scenario("functional.start-stop-start"),
            self.adapter,
            _provenance(self.adapter),
            self.connection,
        )
        self.assertEqual(result.outcome, "passed")
        self.assertTrue(result.cleanup["verified"])
        reconnect_index = next(
            index for index, call in enumerate(self.runner.calls)
            if call[1] == "connect-profile" and index > 4
        )
        disconnects = [
            index for index, call in enumerate(self.runner.calls) if call[1] == "disconnect"
        ]
        self.assertEqual(len(disconnects), 2)
        self.assertLess(disconnects[0], reconnect_index)
        self.assertGreater(disconnects[1], reconnect_index)
        self.assertFalse(self.runner.connected)

    def test_reconnect_operation_is_bounded_and_returns_observations(self) -> None:
        observations = self.adapter.execute(
            ScenarioStep(id="reconnect", operation="reconnect", timeout_seconds=5)
        )
        self.assertEqual(observations, {"restart_verified": True, "reconnect_completed": True})
        self.assertTrue(self.runner.connected)

    def test_cleanup_scenario_proves_disconnect_and_cleanup(self) -> None:
        scenario = get_scenario("functional.start-stop-start")
        engine = FunctionalEngine()
        result = engine.run(scenario, self.adapter, _provenance(self.adapter), self.connection)
        self.assertEqual(result.outcome, "passed")
        self.assertTrue(result.cleanup["verified"])

    def test_disconnect_accepts_a_rotated_non_tunnel_host_identity(self) -> None:
        self.runner.disconnected_ip = b"198.51.100.11\n"
        scenario = get_scenario("functional.start-stop-start")
        result = FunctionalEngine().run(
            scenario, self.adapter, _provenance(self.adapter), self.connection
        )
        self.assertEqual(result.outcome, "passed")
        self.assertTrue(result.cleanup["verified"])
        self.assertNotEqual(self.runner.baseline_ip, self.runner.disconnected_ip)

    def test_disconnect_fails_when_status_is_clean_but_identity_is_not_restored(self) -> None:
        self.runner.restore_identity = False
        scenario = get_scenario("functional.start-stop-start")

        class Clock:
            now = 100.0

            def monotonic(self) -> float:
                return self.now

            def sleep(self, seconds: float) -> None:
                self.now += seconds

        clock = Clock()
        with (
            mock.patch(
                "torturer_checks.hosted.cli.time.monotonic", side_effect=clock.monotonic
            ),
            mock.patch("torturer_checks.hosted.cli.time.sleep", side_effect=clock.sleep),
        ):
            result = FunctionalEngine().run(
                scenario, self.adapter, _provenance(self.adapter), self.connection
            )
        self.assertEqual(result.outcome, "failed")
        self.assertEqual(result.failure_code, "ASSERTION_FAILED")
        self.assertFalse(
            any(
                assertion.id == "disconnect.clean" and assertion.passed
                for assertion in result.assertions
            )
        )

    def test_linux_network_transition_requires_and_uses_explicit_interface(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli, profile=self.profile, runner=self.runner, network_interface="eth0"
        )
        _connection(adapter)
        self.assertIn(Capability.NETWORK_TRANSITION, adapter.capabilities)
        adapter.execute(ScenarioStep(id="connect", operation="connect", timeout_seconds=5))
        adapter.execute(ScenarioStep(id="tunnel", operation="observe_tunnel", timeout_seconds=5))
        adapter.execute(ScenarioStep(id="routing", operation="observe_routing_identity", timeout_seconds=5))
        result = adapter.execute(ScenarioStep(id="network", operation="network_transition", timeout_seconds=5))
        self.assertEqual(result, {"network_transition_verified": True})
        self.assertTrue(
            any(
                call[:2] == ("sh", "-c")
                and "systemctl show" in call[2]
                for call in self.runner.calls
            )
        )

    def test_linux_local_routing_uses_firewall_and_tunnel_counters(self) -> None:
        class RoutingProbeRunner(FakeRunner):
            def __init__(self) -> None:
                super().__init__()
                self.firewall_active = False
                self.rx_bytes = 100
                self.tx_bytes = 200

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[:2] == ("/usr/bin/getent", "ahostsv4"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"8.8.8.8 STREAM probe.example\n", b"")
                if argv[0] == "sudo" and len(argv) >= 4 and argv[3] in {"block", "remove"}:
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    self.firewall_active = argv[3] == "block"
                    return CommandResult(argv, 0, b"", b"")
                if argv[0] == "curl":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    if self.firewall_active and not self.connected:
                        return CommandResult(argv, 7, b"", b"blocked")
                    if self.connected:
                        self.rx_bytes += 500
                        self.tx_bytes += 300
                    return CommandResult(argv, 0, b'"198.51.100.10"', b"")
                if argv[:4] == ("/usr/sbin/ip", "-o", "route", "get"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    interface = "dobby233" if self.connected else "eth0"
                    return CommandResult(
                        argv, 0, f"8.8.8.8 dev {interface} src 192.0.2.1\n".encode(), b""
                    )
                if argv[:2] == ("/usr/bin/cat", "/sys/class/net/dobby233/statistics/rx_bytes"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, f"{self.rx_bytes}\n".encode(), b"")
                if argv[:2] == ("/usr/bin/cat", "/sys/class/net/dobby233/statistics/tx_bytes"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, f"{self.tx_bytes}\n".encode(), b"")
                return super().run(command, timeout_seconds=timeout_seconds)

        # The unprivileged Torturer process cannot stat the root-owned fixed
        # helper; sudo validates and executes the exact provisioned path.
        helper = Path("/var/lib/dobbyvpn-tests-linux/runner/routing-probe-firewall")
        runner = RoutingProbeRunner()
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            identity_url="https://probe.example/api/v0/ip",
            local_mode=True,
            network_interface="eth0",
            routing_firewall_helper=helper,
        )
        _connection(adapter)

        adapter.execute(
            ScenarioStep(id="connect", operation="connect", timeout_seconds=20)
        )
        routing = adapter.execute(
            ScenarioStep(
                id="routing", operation="observe_routing_identity", timeout_seconds=10
            )
        )
        disconnected = adapter.execute(
            ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=10)
        )

        self.assertEqual(routing, {"routing_verified": True})
        self.assertEqual(disconnected, {"disconnect_clean": True})
        self.assertFalse(runner.firewall_active)

    def test_linux_network_transition_is_unavailable_without_exact_interface(self) -> None:
        adapter = LinuxHostedAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
        self.assertNotIn(Capability.NETWORK_TRANSITION, adapter.capabilities)
        with self.assertRaises(CapabilityUnavailable):
            adapter.execute(
                ScenarioStep(id="network", operation="network_transition", timeout_seconds=5)
            )

    def test_macos_local_routing_uses_pf_and_utun_counters(self) -> None:
        class MacRoutingProbeRunner(FakeRunner):
            def __init__(self) -> None:
                super().__init__()
                self.firewall_active = False
                self.received_bytes = 100
                self.sent_bytes = 200

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                self.timeouts.append(float(timeout_seconds))
                if argv[:5] == ("/usr/bin/dscacheutil", "-q", "host", "-a", "name"):
                    return CommandResult(
                        argv, 0, b"name: probe.example\nip_address: 8.8.8.8\n", b""
                    )
                if argv[:4] == (
                    "sudo", "-n", "/fixed/network-transition", "routing-remove"
                ):
                    self.firewall_active = False
                    return CommandResult(argv, 0, b"", b"")
                if argv[:4] == (
                    "sudo", "-n", "/fixed/network-transition", "routing-block"
                ):
                    self.firewall_active = True
                    return CommandResult(argv, 0, b"Token : 123\n", b"")
                if argv[0] == "curl":
                    if self.firewall_active and not self.connected:
                        return CommandResult(argv, 7, b"", b"blocked")
                    if self.connected:
                        self.received_bytes += 500
                        self.sent_bytes += 300
                    return CommandResult(argv, 0, b'"198.51.100.10"', b"")
                if argv[:3] == ("/sbin/route", "-n", "get"):
                    interface = "utun233" if self.connected else "en0"
                    return CommandResult(
                        argv, 0, f"route to: 8.8.8.8\ninterface: {interface}\n".encode(), b""
                    )
                if argv[:2] == ("/usr/sbin/netstat", "-bI"):
                    output = (
                        "Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\n"
                        f"utun233 1200 <Link#5> 1 0 {self.received_bytes} "
                        f"2 0 {self.sent_bytes} 0\n"
                    )
                    return CommandResult(argv, 0, output.encode(), b"")
                self.calls.pop()
                self.timeouts.pop()
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = MacRoutingProbeRunner()
        adapter = MacOSHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            identity_url="https://probe.example/api/v0/ip",
            local_mode=True,
            network_interface="en0",
            routing_firewall_helper=Path("/fixed/network-transition"),
        )
        _connection(adapter)

        adapter.execute(
            ScenarioStep(id="connect", operation="connect", timeout_seconds=20)
        )
        routing = adapter.execute(
            ScenarioStep(
                id="routing", operation="observe_routing_identity", timeout_seconds=10
            )
        )
        disconnected = adapter.execute(
            ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=10)
        )

        self.assertEqual(routing, {"routing_verified": True})
        self.assertEqual(disconnected, {"disconnect_clean": True})
        self.assertFalse(runner.firewall_active)
        self.assertTrue(any(call[:3] == ("/sbin/route", "-n", "get") for call in runner.calls))
        self.assertTrue(any(call[:2] == ("/usr/sbin/netstat", "-bI") for call in runner.calls))

    def test_macos_network_transition_uses_fixed_helper_and_proves_recovery(self) -> None:
        class MacNetworkRunner(FakeRunner):
            def __init__(self) -> None:
                super().__init__()
                self.raw_directory = Path(self_test.directory.name) / "macos-network-raw"
                self.interface_up = True

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[:4] == ("sudo", "-n", "/fixed/network-transition", "arm"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    Path(argv[5]).write_text("label=synthetic\n", encoding="ascii")
                    self.interface_up = False
                    return CommandResult(argv, 0, b"repair scheduled\n", b"")
                if argv[:4] == ("sudo", "-n", "/fixed/network-transition", "finish"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    Path(argv[4]).write_text(
                        "label=synthetic\nrestore_status=0\n", encoding="ascii"
                    )
                    self.interface_up = True
                    return CommandResult(argv, 0, b"restore_status=0\n", b"")
                if argv[:2] == ("/sbin/ifconfig", "en0"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    flags = "UP,BROADCAST,RUNNING" if self.interface_up else "BROADCAST"
                    return CommandResult(
                        argv, 0, f"en0: flags=8863<{flags}> mtu 1500\n".encode(), b""
                    )
                return super().run(command, timeout_seconds=timeout_seconds)

        self_test = self
        runner = MacNetworkRunner()
        runner.connected = True
        adapter = MacOSHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="en0",
            network_transition_helper=Path("/fixed/network-transition"),
        )
        self.assertIn(Capability.NETWORK_TRANSITION, adapter.capabilities)
        with mock.patch.object(adapter, "_wait_for_routing_verified", return_value=True):
            result = adapter.execute(
                ScenarioStep(id="network", operation="network_transition", timeout_seconds=30)
            )
        self.assertEqual(result, {"network_transition_verified": True})
        operations = [call[3] if call[:3] == ("sudo", "-n", "/fixed/network-transition") else call[0] for call in runner.calls]
        self.assertEqual(operations[:5], ["arm", "/sbin/ifconfig", "finish", "/sbin/ifconfig", str(self.cli)])
        self.assertEqual(list(runner.raw_directory.iterdir()), [])

    def test_macos_network_transition_requires_interface_and_helper(self) -> None:
        for interface, helper in (("en0", None), (None, Path("/fixed/helper"))):
            runner = FakeRunner()
            runner.raw_directory = Path(self.directory.name) / f"macos-missing-{interface}"
            adapter = MacOSHostedAdapter(
                cli=self.cli,
                profile=self.profile,
                runner=runner,
                local_mode=True,
                network_interface=interface,
                network_transition_helper=helper,
            )
            self.assertNotIn(Capability.NETWORK_TRANSITION, adapter.capabilities)
            with self.assertRaises(CapabilityUnavailable):
                adapter.execute(
                    ScenarioStep(id="network", operation="network_transition", timeout_seconds=5)
                )

    def test_macos_network_restore_failure_is_secondary_to_observation_failure(self) -> None:
        class FailingMacNetworkRunner(FakeRunner):
            def __init__(self) -> None:
                super().__init__()
                self.raw_directory = Path(self_test.directory.name) / "macos-network-failure-raw"

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                self.timeouts.append(float(timeout_seconds))
                if argv[:4] == ("sudo", "-n", "/fixed/network-transition", "arm"):
                    Path(argv[5]).write_text("label=synthetic\n", encoding="ascii")
                    return CommandResult(argv, 0, b"scheduled\n", b"")
                if argv[:2] == ("/sbin/ifconfig", "en0"):
                    return CommandResult(
                        argv, 0, b"en0: flags=8863<UP,BROADCAST,RUNNING> mtu 1500\n", b""
                    )
                if argv[:4] == ("sudo", "-n", "/fixed/network-transition", "finish"):
                    return CommandResult(
                        argv, 19, b"restore stdout\n", b"ifconfig: restore failed\n"
                    )
                raise AssertionError(argv)

        self_test = self
        runner = FailingMacNetworkRunner()
        adapter = MacOSHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="en0",
            network_transition_helper=Path("/fixed/network-transition"),
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "NETWORK_LOSS_NOT_OBSERVED"
        ) as caught:
            adapter._network_transition(30)
        notes = "\n".join(caught.exception.__notes__)
        self.assertIn(
            "network_uplink_restoration_command_stdout:\nrestore stdout", notes
        )
        self.assertIn(
            "network_uplink_restoration_command_stderr:\nifconfig: restore failed",
            notes,
        )

    def test_windows_local_routing_uses_firewall_and_tunnel_counters(self) -> None:
        class WindowsRoutingProbeRunner(FakeRunner):
            def __init__(self) -> None:
                super().__init__()
                self.firewall_active = False
                self.received_bytes = 100
                self.sent_bytes = 200
                self.connected_probe_failures = 1

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[0].lower().startswith("powershell"):
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    match = re.search(
                        r"FromBase64String\('([A-Za-z0-9+/=]+)'\)", argv[-1]
                    )
                    if match is None:
                        raise AssertionError(argv)
                    script = base64.b64decode(match.group(1)).decode("utf-8")
                    if "GetHostAddresses" in script:
                        return CommandResult(argv, 0, b"8.8.8.8\n", b"")
                    if "New-NetFirewallRule" in script:
                        self.firewall_active = True
                        return CommandResult(argv, 0, b"", b"")
                    if "Remove-NetFirewallRule" in script:
                        self.firewall_active = False
                        return CommandResult(argv, 0, b"", b"")
                    if "Find-NetRoute" in script:
                        interface = 42 if self.connected else 7
                        return CommandResult(
                            argv,
                            0,
                            json.dumps({"interface_index": interface}).encode() + b"\n",
                            b"",
                        )
                    if "Get-NetAdapterStatistics" in script:
                        return CommandResult(
                            argv,
                            0,
                            json.dumps({
                                "interface_index": 42,
                                "received_bytes": self.received_bytes,
                                "sent_bytes": self.sent_bytes,
                            }).encode() + b"\n",
                            b"",
                        )
                    raise AssertionError(script)
                if argv[0] == "curl":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    if self.firewall_active and not self.connected:
                        return CommandResult(argv, 7, b"", b"blocked")
                    if self.connected:
                        if self.connected_probe_failures:
                            self.connected_probe_failures -= 1
                            return CommandResult(argv, 7, b"", b"not ready")
                        self.received_bytes += 500
                        self.sent_bytes += 300
                    return CommandResult(argv, 0, b'"198.51.100.10"\n', b"")
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = WindowsRoutingProbeRunner()
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            identity_url="https://probe.example/api/v0/ip",
            local_mode=True,
            network_interface="7",
        )
        _connection(adapter)

        adapter.execute(
            ScenarioStep(id="connect", operation="connect", timeout_seconds=20)
        )
        routing = adapter.execute(
            ScenarioStep(
                id="routing", operation="observe_routing_identity", timeout_seconds=10
            )
        )
        disconnected = adapter.execute(
            ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=10)
        )

        self.assertEqual(routing, {"routing_verified": True})
        self.assertEqual(disconnected, {"disconnect_clean": True})
        self.assertFalse(runner.firewall_active)

    def test_linux_explicit_interface_uses_mini_and_keeps_network_transition_diagnostic(self) -> None:
        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            network_interface="eth0",
        )
        selected = _select_scenarios(None, platform="linux")
        self.assertEqual(len(selected), 4)
        self.assertEqual(
            {scenario.id for scenario in selected},
            {scenario.id for scenario in suite_set("mini")},
        )
        network_result = FunctionalEngine().run(
            get_scenario("functional.network-transition"),
            adapter,
            _provenance(adapter),
            _connection(adapter),
        )
        self.assertEqual(network_result.outcome, "passed")

    def test_windows_local_network_transition_uses_measured_interface_index(self) -> None:
        class WindowsNetworkRunner(FakeRunner):
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[0].lower().startswith("powershell"):
                    for repair in self.raw_directory.glob('windows-uplink-repair*.ps1'):
                        payload = repair.read_text()
                        self_test.assertNotIn('Unregister-ScheduledTask', payload)
                        self_test.assertNotIn('Remove-Item', payload)
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"", b"")
                return super().run(command, timeout_seconds=timeout_seconds)

        self_test = self
        runner = WindowsNetworkRunner()
        runner.raw_directory = Path(self.directory.name) / "windows-network-raw"
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="7",
        )
        _connection(adapter)
        self.assertIn(Capability.NETWORK_TRANSITION, adapter.capabilities)
        self.assertNotIn(
            Capability.NETWORK_TRANSITION,
            adapter.capability_unavailable_reasons,
        )
        with mock.patch.object(adapter, "_prepare_routing_probe"), mock.patch.object(
            adapter, "_routing_verified", return_value=True
        ), mock.patch("torturer_checks.hosted.windows.time.sleep"):
            adapter.execute(ScenarioStep(id="connect", operation="connect", timeout_seconds=5))
            adapter.execute(ScenarioStep(id="tunnel", operation="observe_tunnel", timeout_seconds=5))
            adapter.execute(ScenarioStep(id="routing", operation="observe_routing_identity", timeout_seconds=5))
            result = adapter.execute(
                ScenarioStep(id="network", operation="network_transition", timeout_seconds=30)
            )
            adapter._network_transition(30)
        self.assertEqual(result, {"network_transition_verified": True})
        self.assertEqual(list(runner.raw_directory.glob('*.raw.log')), [])
        powershell_calls = [
            call for call in runner.calls if call[0].lower().startswith("powershell")
        ]
        self.assertEqual(len(powershell_calls), 12)
        decoded_scripts = []
        for call in powershell_calls:
            match = re.search(r"FromBase64String\('([A-Za-z0-9+/=]+)'\)", call[-1])
            self.assertIsNotNone(match)
            decoded_scripts.append(base64.b64decode(match.group(1)).decode("utf-8"))
        self.assertTrue(
            any("New-ScheduledTaskAction" in script for script in decoded_scripts)
        )
        self.assertTrue(
            any("| Disable-NetAdapter" in script for script in decoded_scripts)
        )
        self.assertTrue(
            any("|\n  Enable-NetAdapter" in script for script in decoded_scripts)
        )
        self.assertTrue(
            any(
                'while ($true)' in script
                and 'if ($adapter.Status -eq "Up") { break }' in script
                and "Start-Sleep -Milliseconds 100" in script
                for script in decoded_scripts
            )
        )
        self.assertFalse(
            any("Disable-NetAdapter -InterfaceIndex" in script for script in decoded_scripts)
        )
        self.assertFalse(
            any("Enable-NetAdapter -InterfaceIndex" in script for script in decoded_scripts)
        )
        self.assertTrue(
            any("-ExecutionPolicy Bypass" in script for script in decoded_scripts)
        )
        self.assertFalse(any(runner.raw_directory.glob("windows-uplink-repair-*.ps1")))

    def test_windows_network_transition_uses_shared_transient_routing_wait(self) -> None:
        runner = FakeRunner()
        runner.raw_directory = Path(self.directory.name) / "windows-network-retry"
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="7",
        )
        with mock.patch.object(
            adapter,
            "_network_command",
            return_value=CommandResult((), 0, b"", b""),
        ), mock.patch.object(
            adapter, "_connected", return_value=True
        ), mock.patch.object(
            adapter, "_wait_for_routing_verified", return_value=True
        ) as routing_wait, mock.patch(
            "torturer_checks.hosted.windows.time.monotonic", return_value=0.0
        ), mock.patch("torturer_checks.hosted.windows.time.sleep"):
            result = adapter._network_transition(30.0)

        self.assertEqual(result, {"network_transition_verified": True})
        routing_wait.assert_called_once()

    def test_windows_network_transition_retains_repair_pair_when_restore_fails(self) -> None:
        runner = FakeRunner()
        runner.raw_directory = Path(self.directory.name) / "windows-network-restore-failure"
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="7",
        )
        calls: list[str] = []
        restore_error = ScenarioExecutionError("NETWORK_UP_FAILED")

        def network_command(script, *args):
            del args
            calls.append(script)
            if "Enable-NetAdapter" in script:
                raise restore_error
            return CommandResult((), 0, b"", b"")

        with mock.patch.object(adapter, "_network_command", side_effect=network_command):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._network_transition(30.0)

        self.assertIs(caught.exception, restore_error)
        self.assertFalse(any("Unregister-ScheduledTask" in script for script in calls))
        self.assertEqual(
            len(list(runner.raw_directory.glob("windows-uplink-repair-*.ps1"))), 1
        )

    def test_windows_network_transition_preserves_primary_when_task_cleanup_fails(self) -> None:
        runner = FakeRunner()
        runner.raw_directory = Path(self.directory.name) / "windows-network-cleanup-failure"
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            local_mode=True,
            network_interface="7",
        )
        calls: list[str] = []
        primary_error = ScenarioExecutionError("NETWORK_DOWN_FAILED")
        cleanup_error = ScenarioExecutionError("NETWORK_REPAIR_CLEANUP_FAILED")

        def network_command(script, *args):
            del args
            calls.append(script)
            if "Disable-NetAdapter" in script:
                raise primary_error
            if "Unregister-ScheduledTask" in script:
                raise cleanup_error
            return CommandResult((), 0, b"", b"")

        with mock.patch.object(adapter, "_network_command", side_effect=network_command):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._network_transition(30.0)

        self.assertIs(caught.exception, primary_error)
        self.assertIn(
            "network_repair_cleanup_error=ScenarioExecutionError",
            caught.exception.__notes__,
        )
        self.assertTrue(any("Unregister-ScheduledTask" in script for script in calls))
        self.assertEqual(
            len(list(runner.raw_directory.glob("windows-uplink-repair-*.ps1"))), 1
        )

    def test_windows_network_command_preserves_native_failure(self) -> None:
        runner = mock.Mock()
        runner.run.side_effect = lambda command, **kwargs: CommandResult(
            tuple(command), 1, b'complete stdout', b'Unregister-ScheduledTask: task missing'
        )
        adapter = WindowsHostedAdapter(
            cli=self.cli, profile=self.profile, runner=runner,
            local_mode=True, network_interface='7',
        )
        with self.assertRaises(ScenarioExecutionError) as caught:
            adapter._network_command('native cleanup', 5, 'NETWORK_REPAIR_CLEANUP_FAILED')
        notes = '\n'.join(caught.exception.__notes__)
        self.assertIn('command_returncode=1', notes)
        self.assertIn('command_stdout:\ncomplete stdout', notes)
        self.assertIn('command_stderr:\nUnregister-ScheduledTask: task missing', notes)

    def test_windows_routing_retries_a_bounded_probe_timeout(self) -> None:
        adapter = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
            local_mode=True,
            network_interface="7",
        )
        adapter._routing_firewall_active = True
        adapter._routing_probe_address = "8.8.8.8"
        with (
            mock.patch.object(adapter, "_route_interface", return_value="42"),
            mock.patch.object(
                adapter,
                "_interface_counters",
                side_effect=[(100, 200), (100, 200), (101, 201)],
            ),
            mock.patch.object(
                adapter,
                "_probe_external_ip",
                side_effect=[
                    ScenarioExecutionError("COMMAND_TIMEOUT"),
                    "198.51.100.10",
                ],
            ) as probe,
            mock.patch("torturer_checks.hosted.cli.time.sleep"),
        ):
            self.assertTrue(adapter._wait_for_routing_verified(5.0))
        self.assertEqual(probe.call_count, 2)

    def test_windows_native_connect_prepares_routing_probe_only_in_local_mode(self) -> None:
        enabled = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
            local_mode=True,
            network_interface="7",
        )
        with (
            mock.patch.object(enabled, "_prepare_routing_probe") as prepare,
            mock.patch.object(enabled, "_capture_baseline") as baseline,
        ):
            enabled.prepare_native_connect(5.0)
        prepare.assert_called_once_with(5.0)
        baseline.assert_not_called()

        disabled = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            identity_url="https://probe.example/api/v0/ip",
        )
        with (
            mock.patch.object(disabled, "_prepare_routing_probe") as prepare,
            mock.patch.object(disabled, "_capture_baseline") as baseline,
        ):
            disabled.prepare_native_connect(5.0)
        prepare.assert_not_called()
        baseline.assert_called_once_with(5.0)

    def test_windows_network_interface_is_local_only_and_numeric(self) -> None:
        with self.assertRaisesRegex(HostedAdapterError, "NETWORK_INTERFACE_INVALID"):
            WindowsHostedAdapter(
                cli=self.cli,
                profile=self.profile,
                runner=self.runner,
                local_mode=True,
                network_interface="Ethernet; Remove-Item",
            )
        hosted = WindowsHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            network_interface="7",
        )
        self.assertNotIn(Capability.NETWORK_TRANSITION, hosted.capabilities)

    def test_hosted_runner_can_select_a_bounded_canonical_subset(self) -> None:
        parser = build_parser()
        options = {
            option
            for action in parser._actions
            for option in action.option_strings
        }
        self.assertIn("--scenario", options)
        self.assertNotIn("--scenario-id", options)
        parsed = parser.parse_args([
            "--platform", "linux", "--cli", str(self.cli), "--profile", str(self.profile),
            "--source-sha", "a" * 40,
            "--platform-version", "24.04",
            "--lane-timeout-seconds", "1800",
            "--output", str(self.directory.name + "/result.json"), "--scenario",
            "functional.configure", "--scenario", "functional.start-stop-start",
        ])
        self.assertEqual(parsed.scenario_ids, ["functional.configure", "functional.start-stop-start"])

    def test_hosted_lane_timeout_is_required_and_strict(self) -> None:
        arguments = [
            "--platform", "linux",
            "--profile", str(self.profile),
            "--source-sha", "a" * 40,
            "--platform-version", "24.04",
            "--output", str(self.directory.name + "/result.json"),
        ]
        with self.assertRaises(SystemExit):
            build_parser().parse_args(arguments)
        for value in ("0", "-1", "nan", "inf"):
            with self.subTest(value=value), self.assertRaises(SystemExit):
                build_parser().parse_args(arguments + ["--lane-timeout-seconds", value])
        parsed = build_parser().parse_args(
            arguments + ["--lane-timeout-seconds", "1800"]
        )
        self.assertEqual(parsed.lane_timeout_seconds, 1800.0)

    def test_linux_candidate_identity_probes_share_one_absolute_deadline(self) -> None:
        class ProbeRunner:
            def __init__(self) -> None:
                self.timeouts: list[float] = []

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.timeouts.append(float(timeout_seconds))
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(argv, 0, b"/synthetic/service\n", b"")
                if argv[2:4] == ("sh", "-c") and argv[-1] == "731":
                    return CommandResult(
                        argv,
                        0,
                        b"731 (service) S 1 306 306 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12345\n",
                        b"",
                    )
                raise AssertionError(argv)

        controller = object.__new__(LinuxServiceProcessController)
        controller.pid = 731
        controller.binary = Path("/synthetic/service")
        controller.runner = ProbeRunner()
        controller._initial_identity = ("12345", 306)
        controller._replacement_identity = None
        with mock.patch(
            "torturer_checks.hosted.linux.time.monotonic",
            side_effect=[100.0, 100.0, 101.0],
        ):
            controller._verify_candidate_pid(5.0)
        self.assertEqual(controller.runner.timeouts, [5.0, 4.0])

    def test_linux_restart_recomputes_timeout_between_verify_and_kill(self) -> None:
        controller = object.__new__(LinuxServiceProcessController)
        controller.pid = 731
        controller._verify_candidate_pid = mock.Mock()
        controller._sudo = mock.Mock(
            return_value=CommandResult(("sudo",), 0, b"", b"")
        )
        controller._wait_dead = mock.Mock()
        controller._start = mock.Mock()
        with mock.patch(
            "torturer_checks.hosted.linux.time.monotonic",
            side_effect=[100.0, 100.0, 101.0, 102.0, 103.0, 104.0],
        ):
            controller.restart_after_loss(5.0)
        self.assertEqual(
            controller._verify_candidate_pid.call_args_list,
            [mock.call(5.0, deadline=105.0), mock.call(4.0, deadline=105.0)],
        )
        self.assertEqual(controller._sudo.call_args.args[1], 3.0)
        self.assertEqual(controller._wait_dead.call_args.args[0], 2.0)
        self.assertEqual(controller._start.call_args.args[0], 1.0)

    def test_linux_process_census_rejects_duplicates_and_accepts_extra_fields(self) -> None:
        valid = "731 1 731 S\n"
        self.assertEqual(len(_parse_linux_process_census(valid)), 1)
        kernel_thread = _parse_linux_process_census("2 0 0 S\n")[2]
        self.assertEqual(kernel_thread.process_group, 0)
        with self.assertRaises(ValueError):
            _parse_linux_process_census(valid + valid)
        self.assertEqual(
            _parse_linux_process_census("731 1 731 S diagnostic\n")[731].pid,
            731,
        )
        with self.assertRaises(ValueError):
            _parse_linux_process_census("731 1 731 R+\n")

    def test_linux_liveness_does_not_turn_a_ps_probe_error_into_absence(self) -> None:
        controller = object.__new__(LinuxServiceProcessController)
        controller.pid = 731
        controller._sudo = mock.Mock(
            side_effect=(
                CommandResult(("sudo",), 0, b"", b""),
                CommandResult(("sudo",), 1, b"", b""),
            )
        )
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PROBE_FAILED"):
            controller._alive(1.0)
        controller._sudo = mock.Mock(
            side_effect=(
                CommandResult(("sudo",), 1, b"", b"permission denied\n"),
                CommandResult(("sudo",), 1, b"", b""),
            )
        )
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PROBE_FAILED"):
            controller._alive(1.0)

    def test_linux_partial_restart_commits_identity_before_readiness(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-partial"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-partial-raw"
        raw_directory.mkdir(mode=0o700)
        (raw_directory / "service-scratch.marker").touch(mode=0o600)

        class Launcher:
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[2:5] == ("sh", "-c", _SERVICE_LAUNCH_SCRIPT):
                    return CommandResult(argv, 0, b"794\n", b"")
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(
                        argv, 0, (str(binary.resolve()) + "\n").encode(), b""
                    )
                raise AssertionError(argv)

        controller = object.__new__(LinuxServiceProcessController)
        controller.pid = 793
        controller.binary = binary
        controller.socket = root / "partial.sock"
        controller.library_path = None
        controller.pid_file = root / "partial.pid"
        controller.runner = Launcher()
        controller.raw_directory = raw_directory
        controller.service_log = raw_directory / ".service-test.log"
        controller._restart_number = 0
        controller._initial_identity = None
        controller._replacement_identity = None
        controller._replacement_tree = ()
        events: list[str] = []
        controller._candidate_process_identity = mock.Mock(
            return_value=("12399", 794)
        )
        controller._verify_candidate_pid = mock.Mock(
            side_effect=lambda _timeout, **_kwargs: (events.append("verify"), ("12399", 794))[1]
        )
        controller._write_pid = mock.Mock(side_effect=lambda _pid: events.append("write"))
        controller._alive = mock.Mock(return_value=False)
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_RESTART_EXITED"):
            controller._start(5.0)
        controller._verify_candidate_pid.assert_called_once()
        self.assertEqual(controller._replacement_identity, ("12399", 794))
        self.assertEqual(events, ["verify", "write"])

    def test_linux_process_identity_probe_requires_explicit_absence_marker(self) -> None:
        controller = object.__new__(LinuxServiceProcessController)
        controller.runner = mock.Mock()
        controller.runner.run.return_value = CommandResult(
            ("sudo",), 1, b"", b""
        )
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_TREE_PROBE_FAILED"):
            controller._read_process_stat(731, 1.0)
        controller.runner.run.return_value = CommandResult(
            ("sudo",), 2, b"service_probe_absent\n", b""
        )
        self.assertIsNone(controller._read_process_stat(731, 1.0))

    def test_linux_process_stat_probe_rechecks_a_vanished_proc_record(self) -> None:
        failed_read = _LINUX_PROCESS_STAT_SCRIPT.index(
            'if ! record=$(cat "$path"); then'
        )
        vanished = _LINUX_PROCESS_STAT_SCRIPT.index(
            'if [ ! -e "$path" ]; then', failed_read
        )
        absent = _LINUX_PROCESS_STAT_SCRIPT.index(
            "printf 'service_probe_absent\\n'", vanished
        )
        probe_error = _LINUX_PROCESS_STAT_SCRIPT.index(
            "printf 'service_probe_error\\n'", absent
        )
        self.assertLess(failed_read, vanished)
        self.assertLess(vanished, absent)
        self.assertLess(absent, probe_error)

    def test_linux_initial_identity_sidecar_rejects_same_binary_pid_reuse(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-initial-identity"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-initial-identity-raw"
        raw_directory.mkdir(mode=0o700)
        identity_file = root / "service.identity"
        identity_file.write_text("731|12345|306\n", encoding="ascii")
        identity_file.chmod(0o600)

        class ReusedPIDRunner:
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(argv, 0, (str(binary) + "\n").encode(), b"")
                if argv[2:4] == ("sh", "-c"):
                    return CommandResult(
                        argv,
                        0,
                        b"731 (service) S 1 306 306 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 99999\n",
                        b"",
                    )
                raise AssertionError(argv)

        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_PID_NOT_CANDIDATE"):
            LinuxServiceProcessController(
                pid=731,
                binary=binary,
                socket=root / "service-initial-identity.sock",
                library_path=None,
                pid_file=root / "service-initial-identity.pid",
                identity_file=identity_file,
                runner=ReusedPIDRunner(),
                raw_directory=raw_directory,
            )

    def test_hosted_runner_resets_after_every_selected_scenario(self) -> None:
        scenarios = _select_scenarios([
            "functional.core-connection",
            "functional.start-stop-start",
        ])
        results = _run_scenarios(
            FunctionalEngine(), scenarios, self.adapter, _provenance(self.adapter),
            self.connection,
        )
        self.assertEqual(len(results), 2)

    def _linux_coverage_fixture(self):
        selected = suite_set("mini")
        connections = (ConnectionIdentity(0, "OUTLINE"),)
        results = [
            {
                "connection": connections[0].to_dict(),
                "scenario": {"id": scenario.id},
                "outcome": "passed",
            }
            for scenario in selected
        ]
        coverage = _coverage_contract(
            "linux",
            connections,
            selected,
            results,
        )
        return selected, results, coverage

    def test_all_mini_scenarios_are_required_hosted_coverage(self) -> None:
        _, _, coverage = self._linux_coverage_fixture()
        self.assertEqual(_qualification_exit_code(coverage), 0)
        self.assertEqual(coverage["status"], "complete")

    def test_unexpected_missing_capability_cannot_become_a_coverage_pass(self) -> None:
        selected, results, _ = self._linux_coverage_fixture()
        unexpected = next(
            item for item in results
            if item["scenario"]["id"] == "functional.core-connection"
        )
        unexpected.update({"outcome": "unavailable", "failure": {"code": "UNEXPECTED_GAP"}})
        coverage = _coverage_contract(
            "linux",
            (self.connection,),
            selected,
            results,
        )
        self.assertEqual(coverage["status"], "coverage-contract-failed")
        self.assertEqual(
            _qualification_exit_code(coverage),
            2,
        )

    def test_coverage_summary_has_no_expected_unavailable_scenarios(self) -> None:
        _, _, coverage = self._linux_coverage_fixture()
        self.assertEqual(coverage["status"], "complete")
        self.assertTrue(coverage["complete"])
        self.assertEqual(coverage["actual_unavailable"], [])

    def test_unavailable_result_cannot_be_supported_by_hosted_allowlist(self) -> None:
        selected, results, _ = self._linux_coverage_fixture()
        results = [dict(item) for item in results]
        supported = next(
            item for item in results
            if item["scenario"]["id"] == "functional.core-connection"
        )
        supported["outcome"] = "unavailable"
        supported["failure"] = {"code": "UNEXPECTED_GAP"}
        coverage = _coverage_contract(
            "linux", (self.connection,), selected, results,
        )
        self.assertEqual(coverage["status"], "coverage-contract-failed")
        self.assertEqual(_qualification_exit_code(coverage), 2)

    def test_hosted_subset_or_missing_result_cannot_qualify(self) -> None:
        selected, results, _ = self._linux_coverage_fixture()
        subset = _coverage_contract("linux", (self.connection,), selected[:1], results[:1])
        missing = _coverage_contract("linux", (self.connection,), selected, results[:-1])
        self.assertEqual(subset["status"], "coverage-contract-failed")
        self.assertEqual(missing["status"], "coverage-contract-failed")

    def test_all_passed_test_set_is_complete_even_when_platform_has_possible_gaps(self) -> None:
        selected = suite_set("mini")
        results = [
            {
                "connection": self.connection.to_dict(),
                "scenario": {"id": scenario.id},
                "outcome": "passed",
            }
            for scenario in selected
        ]
        coverage = _coverage_contract(
            "linux",
            (self.connection,),
            selected,
            results,
        )
        self.assertEqual(coverage["status"], "complete")
        self.assertTrue(coverage["complete"])
        self.assertEqual(coverage["actual_unavailable"], [])
        self.assertEqual(_qualification_exit_code(coverage), 0)

    def test_hosted_runner_accepts_all_feasible_cli_scenarios_in_one_lane(self) -> None:
        selected = _select_scenarios([
            "functional.core-connection",
            "functional.start-stop-start",
        ])
        self.assertEqual(len(selected), 2)

    def test_unsupported_capabilities_are_explicitly_unavailable(self) -> None:
        scenario = get_scenario("functional.network-transition")
        engine = FunctionalEngine()
        result = engine.run(scenario, self.adapter, _provenance(self.adapter), self.connection)
        self.assertEqual(result.outcome, "unavailable")
        self.assertEqual(result.failure_code, "HOSTED_RUNNER_UPLINK_TOGGLE_UNSUPPORTED")

    def test_throughput_probe_keeps_curl_diagnostics_visible(self) -> None:
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        metrics = adapter._throughput(5)
        self.assertEqual(metrics["latency_ms"], 250.0)
        curl_calls = [call for call in self.runner.calls if call[0] == "curl"]
        self.assertEqual(len(curl_calls), 2)
        self.assertNotIn("--silent", curl_calls[0])
        self.assertIn("--show-error", curl_calls[0])
        self.assertIn("--output", curl_calls[0])
        self.assertEqual(curl_calls[0][curl_calls[0].index("--output") + 1], os.devnull)
        self.assertIn("--upload-file", curl_calls[1])
        self.assertIn("--request", curl_calls[1])
        self.assertEqual(curl_calls[1][curl_calls[1].index("--request") + 1], "POST")
        self.assertIn("--output", curl_calls[1])

    def test_http_measurement_status_is_separate_from_request_failure(self) -> None:
        class ServiceUnavailableRunner(FakeRunner):
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[0] == "curl":
                    self.calls.append(argv)
                    self.timeouts.append(float(timeout_seconds))
                    return CommandResult(argv, 0, b"0.01\t0\t503\n", b"service unavailable\n")
                return super().run(command, timeout_seconds=timeout_seconds)

        runner = ServiceUnavailableRunner()
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            download_url=PUBLIC_DOWNLOAD_URL,
            upload_url=PUBLIC_UPLOAD_URL,
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "MEASUREMENT_SERVICE_UNAVAILABLE"
        ):
            adapter._throughput(5)

    def test_connect_commands_share_one_step_deadline(self) -> None:
        with mock.patch(
            "torturer_checks.hosted.cli.time.monotonic",
            side_effect=[100.0, 101.0, 102.0],
        ):
            self.adapter.execute(
                ScenarioStep(id="connect", operation="connect", timeout_seconds=10)
            )
        self.assertEqual(self.runner.timeouts, [9.0, 8.0])

    def test_throughput_commands_share_one_step_deadline(self) -> None:
        adapter = HostedCLIAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
            download_url="https://download.example.test/blob",
            upload_url="https://upload.example.test/blob",
        )
        with mock.patch(
            "torturer_checks.hosted.cli.time.monotonic",
            side_effect=[100.0, 101.0, 102.0],
        ):
            adapter._throughput(10)
        self.assertEqual(self.runner.timeouts, [9.0, 8.0])

    def test_linux_process_loss_commands_share_one_step_deadline(self) -> None:
        class FakeService:
            def __init__(self) -> None:
                self.timeouts: list[float] = []

            def restart_after_loss(self, timeout: float, *, deadline: float | None = None) -> None:
                self.timeouts.append(timeout)

        adapter = LinuxHostedAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
        _connection(adapter)
        self.runner.calls.clear()
        self.runner.timeouts.clear()
        service = FakeService()
        adapter.service = service  # type: ignore[assignment]
        adapter._baseline_ip = "198.51.100.10"
        self.runner.external_calls = 1
        with (
            mock.patch(
                "torturer_checks.hosted.linux.time.monotonic",
                side_effect=[100.0, 101.0, 102.0, 103.0, 104.0],
            ),
            mock.patch.object(
                adapter, "_wait_for_routing_verified", return_value=True
            ) as wait_for_routing,
        ):
            result = adapter._process_loss(10)
        self.assertEqual(result, {"process_loss_verified": True})
        self.assertEqual(service.timeouts, [9.0])
        self.assertEqual(self.runner.timeouts, [8.0, 7.0])
        wait_for_routing.assert_called_once_with(6.0)

    def test_linux_process_loss_rejects_expired_deadline_instead_of_using_minimum(self) -> None:
        class FakeService:
            def __init__(self) -> None:
                self.timeouts: list[float] = []

            def restart_after_loss(self, timeout: float, *, deadline: float | None = None) -> None:
                self.timeouts.append(timeout)

        adapter = LinuxHostedAdapter(cli=self.cli, profile=self.profile, runner=self.runner)
        service = FakeService()
        adapter.service = service  # type: ignore[assignment]
        with mock.patch(
            "torturer_checks.hosted.linux.time.monotonic",
            side_effect=[100.0, 100.02],
        ):
            with self.assertRaisesRegex(ScenarioExecutionError, "PROCESS_LOSS_TIMEOUT"):
                adapter._process_loss(0.01)
        self.assertEqual(service.timeouts, [])
        self.assertEqual(self.runner.calls, [])

    def test_linux_adapter_finalization_stops_the_deliberately_restarted_service(self) -> None:
        class FakeService:
            def __init__(self) -> None:
                self.timeouts: list[float] = []

            def stop_restarted_service(self, timeout: float, *, deadline: float | None = None) -> None:
                self.timeouts.append(timeout)

            def cleanup_scratch(self) -> None:
                pass

        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
        )
        service = FakeService()
        adapter.service = service  # type: ignore[assignment]
        adapter.finalize(12.5)
        self.assertEqual(service.timeouts, [12.5])

    def test_linux_adapter_finalization_preserves_service_failure_when_scratch_cleanup_fails(self) -> None:
        class FakeService:
            def stop_restarted_service(self, timeout: float, *, deadline: float | None = None) -> None:
                raise HostedAdapterError("SERVICE_STOP_FAILED")

            def cleanup_scratch(self) -> None:
                raise ScenarioExecutionError("SERVICE_SCRATCH_CLEANUP_FAILED")

        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
        )
        adapter.service = FakeService()  # type: ignore[assignment]

        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_STOP_FAILED") as raised:
            adapter.finalize(12.5)

        self.assertEqual(
            raised.exception.__notes__,
            [
                "service_scratch_cleanup_error="
                "ScenarioExecutionError: SERVICE_SCRATCH_CLEANUP_FAILED"
            ],
        )

    def test_linux_adapter_finalization_reports_scratch_only_failure(self) -> None:
        class FakeService:
            def stop_restarted_service(self, timeout: float, *, deadline: float | None = None) -> None:
                pass

            def cleanup_scratch(self) -> None:
                raise ScenarioExecutionError("SERVICE_SCRATCH_CLEANUP_FAILED")

        adapter = LinuxHostedAdapter(
            cli=self.cli,
            profile=self.profile,
            runner=self.runner,
        )
        adapter.service = FakeService()  # type: ignore[assignment]

        with self.assertRaisesRegex(
            ScenarioExecutionError, "SERVICE_SCRATCH_CLEANUP_FAILED"
        ):
            adapter.finalize(12.5)

    def test_hosted_run_finalization_uses_the_remaining_lane_budget(self) -> None:
        class FakeAdapter:
            def __init__(self) -> None:
                self.timeouts: list[float] = []

            def finalize(self, *, timeout_seconds: float, deadline: float | None = None) -> None:
                self.timeouts.append(timeout_seconds)

        adapter = FakeAdapter()
        with mock.patch(
            "torturer_checks.hosted.run.time.monotonic",
            return_value=100.0,
        ):
            _finalize_adapter(adapter, 112.5)
        self.assertEqual(adapter.timeouts, [12.5])

    def test_hosted_run_passes_the_canonical_absolute_deadline_to_finalizer(self) -> None:
        class DeadlineAdapter:
            def __init__(self) -> None:
                self.deadlines: list[float | None] = []

            def finalize(self, *, timeout_seconds: float, deadline: float | None) -> None:
                self.deadlines.append(deadline)

        adapter = DeadlineAdapter()
        with mock.patch(
            "torturer_checks.hosted.run.time.monotonic",
            return_value=100.0,
        ):
            _finalize_adapter(adapter, 112.5)
        self.assertEqual(adapter.deadlines, [112.5])

    def test_hosted_run_rejects_an_adapter_without_a_finalizer(self) -> None:
        with self.assertRaisesRegex(ValueError, "ADAPTER_FINALIZER_UNAVAILABLE"):
            _finalize_adapter(object(), None)

    def test_hosted_run_main_finalizes_adapter_before_returning(self) -> None:
        root = Path(self.directory.name)
        raw_directory = root / "main-finalize-raw"
        raw_directory.mkdir(mode=0o700)
        output = root / "main-finalize-result.json"

        class MainAdapter(_DiscoverableAdapter):
            adapter_id = "hosted-linux-cli"
            adapter_version = "v2"
            capabilities = frozenset({Capability.CONFIGURE})
            capability_unavailable_reasons = {}

            def __init__(self) -> None:
                self.runner = object()
                self.finalized: list[float] = []

            def finalize(self, *, timeout_seconds: float, deadline: float | None = None) -> None:
                self.finalized.append(timeout_seconds)

        adapter = MainAdapter()
        arguments = [
            "--platform", "linux",
            "--profile", str(self.profile),
            "--source-sha", "a" * 40,
            "--platform-version", "24.04",
            "--lane-timeout-seconds", "200",
            "--output", str(output),
            "--raw-log-dir", str(raw_directory),
            "--scenario", "functional.configure",
        ]
        with (
            mock.patch.object(hosted_run, "adapter_for_platform", return_value=adapter),
            mock.patch.object(hosted_run, "_run_connection_matrix", return_value=[]),
            mock.patch.object(
                hosted_run,
                "_coverage_contract",
                return_value={"status": "complete"},
            ),
            mock.patch.object(hosted_run, "_qualification_exit_code", return_value=0),
        ):
            code = hosted_run.main(arguments)

        self.assertEqual(code, 0)
        self.assertEqual(len(adapter.finalized), 1)
        self.assertGreater(adapter.finalized[0], 0)
        self.assertTrue(output.is_file())

    def test_hosted_run_success_path_finalizer_failure_propagates(self) -> None:
        root = Path(self.directory.name)
        raw_directory = root / "main-finalizer-failure-raw"
        raw_directory.mkdir(mode=0o700)
        output = root / "main-finalizer-failure-result.json"

        class MainAdapter(_DiscoverableAdapter):
            adapter_id = "hosted-linux-cli"
            adapter_version = "v2"
            capabilities = frozenset({Capability.CONFIGURE})
            capability_unavailable_reasons = {}
            runner = object()

            def finalize(self, *, timeout_seconds: float, deadline: float | None) -> None:
                raise ValueError("PRIVATE_SECRET=must-not-escape")

        arguments = [
            "--platform", "linux", "--profile", str(self.profile),
            "--source-sha", "a" * 40, "--platform-version", "24.04",
            "--lane-timeout-seconds", "200",
            "--output", str(output), "--raw-log-dir", str(raw_directory),
            "--scenario", "functional.configure",
        ]
        with (
            mock.patch.object(hosted_run, "adapter_for_platform", return_value=MainAdapter()),
            mock.patch.object(hosted_run, "_run_connection_matrix", return_value=[]),
            mock.patch.object(
                hosted_run, "_coverage_contract",
                return_value={"status": "complete"},
            ),
            mock.patch.object(hosted_run, "_qualification_exit_code", return_value=0),
            self.assertRaisesRegex(ValueError, "PRIVATE_SECRET=must-not-escape"),
        ):
            hosted_run.main(arguments)

    def test_hosted_run_lane_clock_starts_before_slow_preflight(self) -> None:
        root = Path(self.directory.name)
        raw_directory = root / "main-preflight-raw"
        raw_directory.mkdir(mode=0o700)
        output = root / "main-preflight-result.json"

        class MainAdapter(_DiscoverableAdapter):
            adapter_id = "hosted-linux-cli"
            adapter_version = "v2"
            capabilities = frozenset({Capability.CONFIGURE})
            capability_unavailable_reasons = {}

            def __init__(self) -> None:
                self.runner = object()
                self.finalized: list[float] = []

            def finalize(self, *, timeout_seconds: float, deadline: float | None = None) -> None:
                self.finalized.append(timeout_seconds)

        adapter = MainAdapter()
        scenario_deadlines: list[float] = []

        def run_scenarios(*_args, deadline: float | None = None, **_kwargs):
            scenario_deadlines.append(float(deadline))
            return []

        clock = [100.0]

        def monotonic() -> float:
            return clock[0]

        def slow_preflight(*_args, **_kwargs) -> str:
            clock[0] = 130.0
            return "c" * 40

        arguments = [
            "--platform", "linux",
            "--profile", str(self.profile),
            "--source-sha", "a" * 40,
            "--platform-version", "24.04",
            "--lane-timeout-seconds", "200",
            "--output", str(output),
            "--raw-log-dir", str(raw_directory),
            "--scenario", "functional.configure",
        ]
        with (
            mock.patch.object(hosted_run.time, "monotonic", side_effect=monotonic),
            mock.patch.object(hosted_run, "_full_sha", side_effect=slow_preflight),
            mock.patch.object(hosted_run, "adapter_for_platform", return_value=adapter),
            mock.patch.object(hosted_run, "_run_connection_matrix", side_effect=run_scenarios),
            mock.patch.object(
                hosted_run,
                "_coverage_contract",
                return_value={"status": "complete"},
            ),
            mock.patch.object(hosted_run, "_qualification_exit_code", return_value=0),
        ):
            code = hosted_run.main(arguments)

        self.assertEqual(code, 0)
        self.assertEqual(scenario_deadlines, [300.0])
        self.assertEqual(adapter.finalized, [30.0])

    def test_hosted_run_failure_propagates_and_reports_safe_finalizer_error(self) -> None:
        root = Path(self.directory.name)
        raw_directory = root / "main-catch-raw"
        raw_directory.mkdir(mode=0o700)
        output = root / "main-catch-result.json"

        class MainAdapter(_DiscoverableAdapter):
            adapter_id = "hosted-linux-cli"
            adapter_version = "v2"
            capabilities = frozenset({Capability.CONFIGURE})
            capability_unavailable_reasons = {}

            def __init__(self) -> None:
                self.runner = object()
                self.finalized: list[float] = []

            def finalize(self, *, timeout_seconds: float, deadline: float | None = None) -> None:
                self.finalized.append(timeout_seconds)
                raise ValueError("profile=/runner/input/profile.toml")

        adapter = MainAdapter()
        scenario_deadlines: list[float] = []

        def fail_scenarios(*_args, deadline: float | None = None, **_kwargs):
            scenario_deadlines.append(float(deadline))
            raise ScenarioExecutionError("SCENARIO_FAILED synthetic detail")

        arguments = [
            "--platform", "linux",
            "--profile", str(self.profile),
            "--source-sha", "a" * 40,
            "--platform-version", "24.04",
            "--lane-timeout-seconds", "200",
            "--output", str(output),
            "--raw-log-dir", str(raw_directory),
            "--scenario", "functional.configure",
        ]
        with (
            mock.patch.object(hosted_run, "adapter_for_platform", return_value=adapter),
            mock.patch.object(hosted_run, "_run_connection_matrix", side_effect=fail_scenarios),
            self.assertRaises(ScenarioExecutionError) as raised,
        ):
            hosted_run.main(arguments)

        self.assertEqual(str(raised.exception), "SCENARIO_FAILED synthetic detail")
        self.assertEqual(len(adapter.finalized), 1)
        self.assertEqual(len(scenario_deadlines), 1)
        self.assertAlmostEqual(adapter.finalized[0], 30.0, delta=0.5)
        self.assertEqual(len(raised.exception.__notes__), 1)
        self.assertEqual(
            raised.exception.__notes__,
            ["adapter_finalization_error=ValueError"],
        )

    def test_linux_service_finalization_verifies_and_stops_the_exact_restart(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-finalize"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        pid_file = root / "service-finalize.pid"
        raw_directory = root / "service-finalize-raw"
        raw_directory.mkdir(mode=0o700)

        class ServiceRunner:
            def __init__(self) -> None:
                self.alive = True
                self.calls: list[tuple[str, ...]] = []

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                if argv[2:4] == ("sh", "-c") and argv[-1] == "789":
                    return CommandResult(
                        argv,
                        0 if self.alive else 2,
                        b"789 (service) S 1 300 300 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12345\n" if self.alive else b"service_probe_absent\n",
                        b"",
                    )
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(
                        argv,
                        0 if self.alive else 1,
                        (str(binary.resolve()) + "\n").encode() if self.alive else b"",
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0 if self.alive else 1, b"", b"")
                if argv[2:4] == ("ps", "-o"):
                    return CommandResult(
                        argv,
                        0 if self.alive else 1,
                        b"S\n" if self.alive else b"",
                        b"",
                    )
                if argv[2:4] == ("ps", "-axo"):
                    return CommandResult(
                        argv,
                        0,
                        b"789 1 300 S\n" if self.alive else b"",
                        b"",
                    )
                if argv[2:4] == ("kill", "-TERM"):
                    self.alive = False
                    return CommandResult(argv, 0, b"", b"")
                raise AssertionError(argv)

        runner = ServiceRunner()
        controller = LinuxServiceProcessController(
            pid=789,
            binary=binary,
            socket=root / "service-finalize.sock",
            library_path=None,
            pid_file=pid_file,
            runner=runner,
            raw_directory=raw_directory,
        )
        controller._restart_number = 1
        controller._replacement_identity = ("12345", 300)
        controller.stop_restarted_service(10.0)

        self.assertFalse(runner.alive)
        self.assertIn(("sudo", "-n", "kill", "-TERM", "--", "-300"), runner.calls)
        self.assertNotIn(("sudo", "-n", "kill", "-KILL", "--", "-300"), runner.calls)

    def test_linux_service_finalization_escalates_a_resistant_restart_to_kill(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-finalize-resistant"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-finalize-resistant-raw"
        raw_directory.mkdir(mode=0o700)

        class ServiceRunner:
            def __init__(self) -> None:
                self.alive = True
                self.calls: list[tuple[str, ...]] = []

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                if argv[2:4] == ("sh", "-c") and argv[-1] == "790":
                    return CommandResult(
                        argv,
                        0 if self.alive else 2,
                        b"790 (service) S 1 301 301 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12346\n" if self.alive else b"service_probe_absent\n",
                        b"",
                    )
                if argv[2:4] == ("sh", "-c") and argv[-1] == "7910":
                    return CommandResult(
                        argv,
                        0 if self.alive else 2,
                        b"7910 (worker) S 790 301 301 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12347\n" if self.alive else b"service_probe_absent\n",
                        b"",
                    )
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(
                        argv,
                        0,
                        (str(binary.resolve()) + "\n").encode(),
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0 if self.alive else 1, b"", b"")
                if argv[2:4] == ("ps", "-o"):
                    return CommandResult(argv, 0 if self.alive else 1, b"S\n" if self.alive else b"", b"")
                if argv[2:4] == ("ps", "-axo"):
                    return CommandResult(
                        argv,
                        0,
                        b"790 1 301 S\n7910 790 301 S\n" if self.alive else b"",
                        b"",
                    )
                if argv[2:4] == ("kill", "-TERM"):
                    return CommandResult(argv, 0, b"", b"")
                if argv[2:4] == ("kill", "-KILL"):
                    self.alive = False
                    return CommandResult(argv, 0, b"", b"")
                raise AssertionError(argv)

        runner = ServiceRunner()
        controller = LinuxServiceProcessController(
            pid=790,
            binary=binary,
            socket=root / "service-finalize-resistant.sock",
            library_path=None,
            pid_file=root / "service-finalize-resistant.pid",
            runner=runner,
            raw_directory=raw_directory,
        )
        controller._restart_number = 1
        controller._replacement_identity = ("12346", 301)
        with mock.patch.object(
            controller,
            "_wait_replacement_tree",
            side_effect=[HostedAdapterError("SERVICE_FINALIZE_TIMEOUT"), ()],
        ):
            controller.stop_restarted_service(10.0)

        self.assertFalse(runner.alive)
        self.assertIn(("sudo", "-n", "kill", "-TERM", "--", "-301"), runner.calls)
        self.assertIn(("sudo", "-n", "kill", "-KILL", "--", "-301"), runner.calls)

    def test_linux_service_finalization_refuses_a_reused_pid(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-finalize-identity"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-finalize-identity-raw"
        raw_directory.mkdir(mode=0o700)

        class ServiceRunner:
            def __init__(self) -> None:
                self.readlinks = 0
                self.calls: list[tuple[str, ...]] = []

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                if argv[2:4] == ("readlink", "-f"):
                    self.readlinks += 1
                    return CommandResult(
                        argv,
                        0,
                        (str(binary.resolve()) + "\n").encode(),
                        b"",
                    )
                if argv[2:4] == ("sh", "-c") and argv[-1] == "791":
                    return CommandResult(
                        argv,
                        0,
                        b"791 (service) S 1 302 302 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 99999\n",
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0, b"", b"")
                if argv[2:4] == ("ps", "-o"):
                    return CommandResult(argv, 0, b"S\n", b"")
                raise AssertionError(argv)

        runner = ServiceRunner()
        controller = LinuxServiceProcessController(
            pid=791,
            binary=binary,
            socket=root / "service-finalize-identity.sock",
            library_path=None,
            pid_file=root / "service-finalize-identity.pid",
            runner=runner,
            raw_directory=raw_directory,
        )
        controller._restart_number = 1
        controller._replacement_identity = ("12345", 302)
        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_PID_NOT_CANDIDATE"):
            controller.stop_restarted_service(10.0)

        self.assertFalse(any(call[2:4] in {("kill", "-TERM"), ("kill", "-KILL")} for call in runner.calls))

    def test_linux_service_finalization_does_not_pass_on_root_disappearance(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-finalize-root-gone"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-finalize-root-gone-raw"
        raw_directory.mkdir(mode=0o700)

        class ServiceRunner:
            def __init__(self) -> None:
                self.alive = True

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(argv, 0, (str(binary.resolve()) + "\n").encode(), b"")
                if argv[2:4] == ("sh", "-c") and argv[-1] == "793":
                    return CommandResult(
                        argv,
                        0 if self.alive else 2,
                        b"793 (service) S 1 306 306 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12351\n"
                        if self.alive else b"service_probe_absent\n",
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0 if self.alive else 1, b"", b"")
                if argv[2:4] == ("ps", "-o"):
                    return CommandResult(
                        argv,
                        0 if self.alive else 1,
                        b"S\n" if self.alive else b"",
                        b"",
                    )
                raise AssertionError(argv)

        runner = ServiceRunner()
        controller = LinuxServiceProcessController(
            pid=793,
            binary=binary,
            socket=root / "service-finalize-root-gone.sock",
            library_path=None,
            pid_file=root / "service-finalize-root-gone.pid",
            runner=runner,
            raw_directory=raw_directory,
        )
        controller._restart_number = 1
        controller._replacement_identity = ("12351", 306)
        runner.alive = False
        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_TREE_PROBE_FAILED"):
            controller.stop_restarted_service(10.0)

    def test_linux_service_finalization_fails_closed_on_timed_out_liveness_probe(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service-finalize-timeout"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        raw_directory = root / "service-finalize-timeout-raw"
        raw_directory.mkdir(mode=0o700)

        class ServiceRunner:
            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(argv, 0, (str(binary.resolve()) + "\n").encode(), b"")
                if argv[2:4] == ("sh", "-c") and argv[-1] == "792":
                    return CommandResult(
                        argv,
                        0,
                        b"792 (service) S 1 303 303 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12347\n",
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0, b"", b"", timed_out=True)
                raise AssertionError(argv)

        controller = LinuxServiceProcessController(
            pid=792,
            binary=binary,
            socket=root / "service-finalize-timeout.sock",
            library_path=None,
            pid_file=root / "service-finalize-timeout.pid",
            runner=ServiceRunner(),
            raw_directory=raw_directory,
        )
        controller._restart_number = 1
        controller._replacement_identity = ("12347", 303)
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PROBE_FAILED"):
            controller.stop_restarted_service(10.0)

    def test_linux_restart_launcher_tracks_exact_child_pid(self) -> None:
        root = Path(self.directory.name)
        binary = root / "service"
        binary.write_bytes(b"synthetic service")
        binary.chmod(0o700)
        library = root / "library"
        library.mkdir()
        socket_path = root / "control.sock"
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(str(socket_path))
        listener.listen(1)
        self.addCleanup(listener.close)
        pid_file = root / "service.pid"
        raw_directory = root / "service-raw"
        raw_directory.mkdir(mode=0o700)
        previous_log = raw_directory / "legacy-service.log"
        previous_log.write_bytes(b"pre-existing scratch marker\n")
        previous_log.chmod(0o600)

        class ServiceRunner:
            def __init__(self) -> None:
                self.calls: list[tuple[str, ...]] = []

            def run(self, command, *, timeout_seconds):
                argv = tuple(command)
                self.calls.append(argv)
                if len(argv) >= 5 and argv[2:5] == ("sh", "-c", _SERVICE_LAUNCH_SCRIPT):
                    return CommandResult(argv, 0, b"456\n", b"")
                if argv[2:4] == ("readlink", "-f"):
                    return CommandResult(
                        argv,
                        0,
                        (str(binary.resolve()) + "\n").encode(),
                        b"",
                    )
                if argv[2:4] == ("sh", "-c") and argv[-1] == "123":
                    return CommandResult(
                        argv,
                        0,
                        b"123 (service) S 1 303 303 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12348\n",
                        b"",
                    )
                if argv[2:4] == ("sh", "-c") and argv[-1] == "456":
                    return CommandResult(
                        argv,
                        0,
                        b"456 (service) S 1 304 304 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12349\n",
                        b"",
                    )
                if argv[2:4] == ("kill", "-0"):
                    return CommandResult(argv, 0, b"", b"")
                if argv[2:4] == ("ps", "-o"):
                    return CommandResult(argv, 0, b"S\n", b"")
                raise AssertionError(argv)

        runner = ServiceRunner()
        controller = LinuxServiceProcessController(
            pid=123,
            binary=binary,
            socket=socket_path,
            library_path=library,
            pid_file=pid_file,
            runner=runner,
            raw_directory=raw_directory,
        )
        with mock.patch(
            "torturer_checks.hosted.linux.time.monotonic",
            return_value=100.0,
        ):
            controller._start(10.0)

        self.assertEqual(controller.pid, 456)
        self.assertEqual(pid_file.read_text(encoding="ascii"), "456\n")
        launcher = next(
            call
            for call in runner.calls
            if len(call) >= 5 and call[2:5] == ("sh", "-c", _SERVICE_LAUNCH_SCRIPT)
        )
        self.assertEqual(
            launcher[:5],
            ("sudo", "-n", "sh", "-c", _SERVICE_LAUNCH_SCRIPT),
        )
        self.assertEqual(launcher[5], "dobbyvpn-service")
        self.assertEqual(launcher[6], str(binary))
        self.assertEqual(launcher[7], str(socket_path))
        self.assertEqual(launcher[9], str(library))
        launched_log = Path(launcher[10])
        self.assertEqual(launched_log.parent, raw_directory)
        self.assertTrue(launched_log.name.startswith(".service-"))
        self.assertNotEqual(launched_log, previous_log)
        self.assertEqual(launcher[11], str(raw_directory.parent))
        self.assertEqual(launcher[12], "0")
        self.assertEqual(previous_log.read_bytes(), b"pre-existing scratch marker\n")
        controller.cleanup_scratch()
        self.assertFalse(launched_log.exists())

    def test_subprocess_runner_keeps_stdout_and_stderr_in_memory(self) -> None:
        raw = Path(self.directory.name) / "raw"
        runner = SubprocessRunner(raw)
        result = runner.run(
            ("python3", "-c", "import sys; sys.stdout.buffer.write(b'198.51.100.10\\n'); sys.stderr.buffer.write(b'err\\n')"),
            timeout_seconds=5,
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b"198.51.100.10\n")
        self.assertEqual(result.stderr, b"err\n")
        self.assertEqual(list(raw.iterdir()), [])

    def test_subprocess_runner_never_persists_raw_command_output(self) -> None:
        raw = Path(self.directory.name) / "no-raw-output"
        runner = SubprocessRunner(raw)
        result = runner.run(
            (sys.executable, "-c", "print('passed'); print('private', file=__import__('sys').stderr)"),
            timeout_seconds=5,
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), b"passed")
        self.assertEqual(result.stderr.strip(), b"private")
        self.assertEqual(list(raw.iterdir()), [])

    def test_subprocess_runner_forwards_complete_redacted_streams(self) -> None:
        raw = Path(self.directory.name) / "complete-output-raw"
        runner = SubprocessRunner(raw)
        secret = "profile-secret-value"
        runner.register_sensitive_values(secret)
        beginning = "beginning-marker"
        middle = "middle-marker"
        ending = "ending-marker"
        stdout = f"{beginning}\n" + ("x" * 4096) + f"\n{middle}\n{secret}\n{ending}\n"
        stderr = f"stderr-begin\n" + ("y" * 4096) + f"\nstderr-end\n"
        script = (
            "import sys; "
            f"sys.stdout.write({stdout!r}); sys.stderr.write({stderr!r})"
        )
        with mock.patch("sys.stderr", new_callable=io.StringIO) as diagnostics:
            result = runner.run((sys.executable, "-c", script), timeout_seconds=5)
        self.assertEqual(result.stdout.decode(), stdout)
        self.assertEqual(result.stderr.decode(), stderr)
        rendered = diagnostics.getvalue()
        self.assertIn(beginning, rendered)
        self.assertIn(middle, rendered)
        self.assertIn(ending, rendered)
        self.assertIn("stderr-begin", rendered)
        self.assertIn("stderr-end", rendered)
        self.assertNotIn(secret, rendered)
        self.assertIn("[REDACTED]", rendered)
        self.assertEqual(list(raw.iterdir()), [])

    def test_subprocess_runner_does_not_synthesize_an_application_log(self) -> None:
        raw = Path(self.directory.name) / "application-log-raw"
        raw.mkdir(mode=0o700)
        app_log = raw / "app.log"
        app_log.write_bytes(b"product-owned application event\n")
        executable = Path(sys.executable).resolve()
        runner = SubprocessRunner(raw)
        result = runner.run(
            (
                str(executable),
                "-c",
                "import sys; sys.stdout.buffer.write(b'out'); sys.stderr.buffer.write(b'err')",
            ),
            timeout_seconds=5,
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(app_log.read_bytes(), b"product-owned application event\n")
        self.assertEqual(list(raw.glob("*.raw.log")), [])

        other = Path("/bin/echo")
        if other.is_file():
            runner.run((str(other), "not-application"), timeout_seconds=5)
            self.assertEqual(app_log.read_bytes(), b"product-owned application event\n")

    @unittest.skipUnless(
        os.name == "posix" and Path("/proc").is_dir(),
        "Linux subreaper assertion requires procfs",
    )
    def test_subprocess_runner_preserves_stdin_and_nonzero_status_in_memory(self) -> None:
        raw = Path(self.directory.name) / "stdin-status-raw"
        runner = SubprocessRunner(raw)
        result = runner.run(
            (
                sys.executable,
                "-c",
                "import sys; data=sys.stdin.buffer.read(); sys.stdout.buffer.write(data); raise SystemExit(23)",
            ),
            timeout_seconds=5,
            input_bytes=b"private-stdin-marker\n",
        )
        self.assertEqual(result.returncode, 23)
        self.assertEqual(result.stdout, b"private-stdin-marker\n")
        self.assertEqual(list(raw.glob("*.raw.log")), [])

    @unittest.skipUnless(
        os.name == "posix" and Path("/proc").is_dir(),
        "Linux subreaper assertion requires procfs",
    )
    def test_subprocess_runner_preserves_sigkill_status(self) -> None:
        raw = Path(self.directory.name) / "sigkill-status-raw"
        runner = SubprocessRunner(raw)
        result = runner.run(
            (
                sys.executable,
                "-c",
                "import os,signal; os.kill(os.getpid(), signal.SIGKILL)",
            ),
            timeout_seconds=5,
        )
        self.assertEqual(result.returncode, -signal.SIGKILL)
        self.assertEqual(list(raw.glob("*.raw.log")), [])

    @unittest.skipUnless(
        os.name == "posix" and Path("/proc").is_dir(),
        "Linux subreaper assertion requires procfs",
    )
    def test_subprocess_runner_missing_command_reports_bounded_error(self) -> None:
        raw = Path(self.directory.name) / "missing-command-raw"
        runner = SubprocessRunner(raw)
        missing = "/dobbyvpn/synthetic-command-does-not-exist"
        with self.assertRaisesRegex(HostedAdapterError, "COMMAND_UNAVAILABLE") as caught:
            runner.run((missing,), timeout_seconds=5)
        notes = "\n".join(caught.exception.__notes__)
        self.assertNotIn(missing, notes)
        self.assertIn("command_returncode=-1", caught.exception.__notes__)
        self.assertEqual(list(raw.iterdir()), [])

    def test_subprocess_runner_timeout_terminates_the_command_group(self) -> None:
        raw = Path(self.directory.name) / "timeout-raw"
        runner = SubprocessRunner(raw)
        child_pid_path = raw / "child.pid"
        script = (
            "import subprocess,sys,time; "
            "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)']); "
            "open(sys.argv[1], 'w').write(str(child.pid)); time.sleep(30)"
        )
        with self.assertRaisesRegex(HostedAdapterError, "COMMAND_TIMEOUT"):
            runner.run((sys.executable, "-c", script, str(child_pid_path)), timeout_seconds=0.3)
        child_pid = int(child_pid_path.read_text(encoding="ascii"))
        self.assertEqual(list(raw.glob("*.raw.log")), [])
        deadline = time.monotonic() + 2.0
        while time.monotonic() < deadline:
            try:
                os.kill(child_pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.01)
        else:
            os.kill(child_pid, signal.SIGKILL)
            self.fail("command-group descendant survived timeout")

    def test_subprocess_runner_timeout_reports_primary_and_cleanup_error_safely(self) -> None:
        raw = Path(self.directory.name) / "cleanup-error-raw"
        runner = SubprocessRunner(raw)

        class Process:
            pid = 1234
            returncode = -9

            def __init__(self) -> None:
                self.calls = 0

            def communicate(self, **_kwargs):
                self.calls += 1
                if self.calls == 1:
                    raise subprocess.TimeoutExpired(("synthetic",), 0.1, output=b"out", stderr=b"err")
                return b"out-final", b"err-final"

        process = Process()
        with (
            mock.patch("torturer_checks.hosted.cli.popen_with_windows_job", return_value=process),
            mock.patch(
                "torturer_checks.hosted.cli._terminate_process",
                side_effect=OSError("synthetic kill failure"),
            ),
        ):
            with self.assertRaisesRegex(HostedAdapterError, "COMMAND_TIMEOUT") as caught:
                runner.run(("synthetic",), timeout_seconds=1.0)
        self.assertTrue(any("termination" in note for note in caught.exception.__notes__))
        self.assertIn("command_returncode=124", caught.exception.__notes__)
        self.assertIn("command_timed_out=True", caught.exception.__notes__)
        self.assertIn("command_stdout:\nout-final", caught.exception.__notes__)
        self.assertIn("command_stderr:\nerr-final", caught.exception.__notes__)
        self.assertEqual(list(raw.iterdir()), [])

    def test_subprocess_runner_does_not_allocate_raw_files_for_reused_directory(self) -> None:
        raw = Path(self.directory.name) / "reused-raw"
        runner = SubprocessRunner(raw)
        runner.run((sys.executable, "-c", "print('first')"), timeout_seconds=5)
        runner.run((sys.executable, "-c", "print('second')"), timeout_seconds=5)
        self.assertEqual(list(raw.glob("*.raw.log")), [])

    def test_subprocess_runner_keeps_concurrent_results_in_memory(
        self,
    ) -> None:
        raw = Path(self.directory.name) / "concurrent-raw"
        runner = SubprocessRunner(raw)
        slow_started = raw / "slow.started"
        slow_release = raw / "slow.release"
        slow_script = (
            "from pathlib import Path\n"
            "import sys, time\n"
            "started, release = map(Path, sys.argv[1:])\n"
            "started.touch()\n"
            "while not release.exists():\n"
            "    time.sleep(0.005)\n"
            "sys.stdout.buffer.write(b'slow-stdout\\n')\n"
            "sys.stderr.buffer.write(b'slow-stderr\\n')\n"
        )
        fast_script = (
            "import sys; sys.stdout.buffer.write(b'fast-stdout\\n'); "
            "sys.stderr.buffer.write(b'fast-stderr\\n')"
        )

        with ThreadPoolExecutor(max_workers=2) as workers:
            slow_future = workers.submit(
                runner.run,
                (sys.executable, "-c", slow_script, str(slow_started), str(slow_release)),
                timeout_seconds=5,
            )
            try:
                deadline = time.monotonic() + 2
                while not slow_started.exists() and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertTrue(slow_started.exists())

                fast_result = workers.submit(
                    runner.run,
                    (sys.executable, "-c", fast_script),
                    timeout_seconds=5,
                ).result()
                self.assertEqual(fast_result.stdout, b"fast-stdout\n")
                self.assertEqual(fast_result.stderr, b"fast-stderr\n")
            finally:
                slow_release.touch()
            slow_result = slow_future.result()

        self.assertEqual(slow_result.stdout, b"slow-stdout\n")
        self.assertEqual(slow_result.stderr, b"slow-stderr\n")
        self.assertEqual(list(raw.glob("*.raw.log")), [])

    def test_throughput_urls_reject_fragment_and_userinfo(self) -> None:
        for invalid in (
            "https://download.example.test/blob#fragment",
            "https://user:password@download.example.test/blob",
        ):
            with self.subTest(invalid=invalid):
                with self.assertRaisesRegex(HostedAdapterError, "DOWNLOAD_URL_INVALID"):
                    HostedCLIAdapter(
                        cli=self.cli,
                        profile=self.profile,
                        runner=self.runner,
                        download_url=invalid,
                        upload_url="https://upload.example.test/blob",
                    )
def _provenance(adapter: HostedCLIAdapter):
    from torturer_contract.functional.results import RunProvenance
    return RunProvenance(
        platform="linux",
        platform_version="24.04",
        architecture="amd64",
    )


if __name__ == "__main__":
    unittest.main()
