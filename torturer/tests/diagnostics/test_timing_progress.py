from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime
from io import StringIO
import json
from pathlib import Path
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import local_vm
from torturer_runner import ios_simulator_app
from torturer_runner import lane
from torturer_runner.native_cases import IOS_LOGS_FREEZE_RESUME_CASE
from torturer_runner.adapters.cli import CLIAdapter, RoutingProofMixin, SubprocessRunner
from torturer_runner.adapters.windows import WindowsAdapter
from torturer_contract.engine import ScenarioExecutionError
from torturer_contract.scenarios import ScenarioDefinition, ScenarioStep
from disposable_vpn_server import render_service


def _json_events(output: str) -> list[dict[str, object]]:
    return [json.loads(line) for line in output.splitlines() if line.startswith("{")]


def _assert_utc_timestamp(test: unittest.TestCase, value: object) -> None:
    test.assertIsInstance(value, str)
    parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    test.assertIsNotNone(parsed.tzinfo)
    test.assertEqual(parsed.utcoffset().total_seconds(), 0)


class TimingProgressTests(unittest.TestCase):
    def test_windows_firewall_setup_does_not_consume_product_connect_deadline(self) -> None:
        clock = [100.0]
        setup_budgets: list[float] = []
        command_budgets: list[float] = []

        def prepare(budget: float) -> None:
            setup_budgets.append(budget)
            clock[0] += 37.0

        probe = SimpleNamespace(
            profile=Path("fixture"),
            _prepare_routing_probe=prepare,
            _selected_connection_index=lambda: "0",
            _remaining=lambda deadline, _failure: deadline - clock[0],
            _start_selected=lambda budget, _failure: command_budgets.append(budget),
            _connected=lambda _budget: True,
            _emit_progress=lambda *_args, **_kwargs: None,
        )
        with mock.patch("torturer_runner.adapters.cli.time.monotonic", side_effect=lambda: clock[0]):
            RoutingProofMixin._connect_with_routing_probe(probe, 40.0, setup_timeout=60.0)
            RoutingProofMixin._reconnect_with_routing_probe(probe, 30.0, setup_timeout=60.0)
        self.assertEqual(setup_budgets, [60.0, 60.0])
        self.assertEqual(command_budgets, [40.0, 30.0])

        adapter = object.__new__(WindowsAdapter)
        adapter._routing_proof_enabled = True
        with mock.patch.object(adapter, "_connect_with_routing_probe") as connect:
            adapter.execute(SimpleNamespace(operation="connect", timeout_seconds=40))
        connect.assert_called_once_with(40.0, setup_timeout=60.0)
        with mock.patch.object(adapter, "_reconnect_with_routing_probe") as reconnect:
            adapter.execute(SimpleNamespace(operation="reconnect", timeout_seconds=30))
        reconnect.assert_called_once_with(30.0, setup_timeout=60.0)

    def test_ios_build_stage_events_report_utc_duration_and_keep_failure_streams(self) -> None:
        contract = ios_simulator_app.PUBLIC_IOS_SIMULATOR_APP_CONTRACT

        class BuildRunner:
            def __init__(self, returncode: int) -> None:
                self.returncode = returncode

            def run(self, _command, *, cwd=None, timeout_seconds=None):
                del cwd, timeout_seconds
                if self.returncode == 0:
                    contract.app_path(work_dir).mkdir(parents=True, exist_ok=True)
                return ios_simulator_app.CommandResult(
                    self.returncode,
                    "build stdout\x00" if self.returncode else "",
                    "build stderr\xff" if self.returncode else "",
                )

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            work_dir = root / "work"
            (root / "candidate" / "ui" / "apple" / "ios" / "iosApp.xcodeproj").mkdir(
                parents=True
            )
            events_output = StringIO()
            with redirect_stdout(events_output):
                ios_simulator_app.prepare_ios_simulator_candidate(
                    candidate_root=root / "candidate",
                    work_dir=work_dir,
                    runner=BuildRunner(0),
                    contract=contract,
                    budget=ios_simulator_app.RunBudget(),
                )

            events = _json_events(events_output.getvalue())
            self.assertEqual(
                [(event["event"], event["stage"]) for event in events],
                [("stage-start", "build"), ("stage-finish", "build")],
            )
            for event in events:
                self.assertEqual(event["kind"], "dobbyvpn.ios_simulator.progress")
                _assert_utc_timestamp(self, event["timestamp_utc"])
            self.assertGreaterEqual(events[-1]["duration_seconds"], 0)
            self.assertEqual(events[-1]["status"], "succeeded")

            failed_output = StringIO()
            with redirect_stdout(failed_output):
                with self.assertRaises(ios_simulator_app.IOSSimulatorStageError) as caught:
                    ios_simulator_app.prepare_ios_simulator_candidate(
                        candidate_root=root / "candidate",
                        work_dir=root / "failed-work",
                        runner=BuildRunner(1),
                        contract=contract,
                        budget=ios_simulator_app.RunBudget(),
                    )

        self.assertEqual(caught.exception.stage, "package-ios-app")
        self.assertIn("build stdout\x00", "\n".join(caught.exception.__notes__))
        self.assertIn("build stderrÿ", "\n".join(caught.exception.__notes__))
        failed_events = _json_events(failed_output.getvalue())
        self.assertEqual(
            [(event["event"], event["stage"]) for event in failed_events],
            [("stage-start", "build"), ("stage-finish", "build")],
        )
        _assert_utc_timestamp(self, failed_events[-1]["timestamp_utc"])
        self.assertGreaterEqual(failed_events[-1]["duration_seconds"], 0)
        self.assertEqual(failed_events[-1]["status"], "failed")
        self.assertEqual(failed_events[-1]["error_type"], "IOSSimulatorStageError")

    def test_ios_simulator_lifecycle_stage_events(self) -> None:
        udid = "01234567-89ab-cdef-0123-456789abcdef"
        inventory = {
            "devices": {
                "com.apple.CoreSimulator.SimRuntime.iOS-17-5": [{
                    "isAvailable": True,
                    "name": "iPhone 15",
                    "udid": udid,
                }],
            },
        }

        class Runner:
            def run(self, command, *, cwd=None, timeout_seconds=None):
                del cwd, timeout_seconds
                arguments = list(command)
                if arguments[:4] == ["xcrun", "simctl", "list", "devices"]:
                    return ios_simulator_app.CommandResult(0, json.dumps(inventory), "")
                if arguments[:3] == ["xcrun", "--sdk", "iphonesimulator"]:
                    return ios_simulator_app.CommandResult(0, "17.5", "")
                if arguments[0] == "xcodebuild":
                    result_bundle = Path(
                        arguments[arguments.index("-resultBundlePath") + 1]
                    )
                    result_bundle.mkdir()
                return ios_simulator_app.CommandResult(0, "", "")

        contract = ios_simulator_app.PUBLIC_IOS_SIMULATOR_APP_CONTRACT
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            work_dir = root / "work"
            contract.app_path(work_dir).mkdir(parents=True)
            (root / "candidate" / "ui" / "apple" / "ios" / "iosApp.xcodeproj").mkdir(
                parents=True
            )
            events_output = StringIO()
            with (
                mock.patch.object(
                    ios_simulator_app,
                    "_read_simulator_hardware_keyboard_override",
                    return_value=False,
                ),
                mock.patch.object(
                    ios_simulator_app,
                    "_retain_xctest_screenshots",
                    return_value=root / "screenshots",
                ),
                mock.patch.object(
                    ios_simulator_app,
                    "_collect_ios_native_log",
                    return_value=None,
                ),
                redirect_stdout(events_output),
            ):
                evidence = ios_simulator_app.run_ios_simulator_app_contract(
                    candidate_root=root / "candidate",
                    work_dir=work_dir,
                    runner=Runner(),
                    native_cases=[IOS_LOGS_FREEZE_RESUME_CASE],
                )

        self.assertIsNone(evidence.native_log)
        self.assertEqual(
            evidence.native_log_collection_error,
            "app_logs.txt was absent; no native app log bytes were produced",
        )
        events = _json_events(events_output.getvalue())
        finish_events = [event for event in events if event["event"] == "stage-finish"]
        self.assertEqual(
            [event["stage"] for event in finish_events],
            ["boot", "install", "xctest", "cleanup"],
        )
        for event in events:
            _assert_utc_timestamp(self, event["timestamp_utc"])
        for event in finish_events:
            self.assertGreaterEqual(event["duration_seconds"], 0)
            self.assertEqual(event["status"], "succeeded")

    def test_lane_progress_events_have_utc_timestamps(self) -> None:
        output = StringIO()
        with redirect_stdout(output):
            lane._emit_progress_event("test-event", {"phase": "test"})

        event = _json_events(output.getvalue())[0]
        self.assertEqual(event["kind"], "dobbyvpn.functional.progress")
        self.assertEqual(event["event"], "test-event")
        _assert_utc_timestamp(self, event["timestamp_utc"])

    def test_hosted_commands_report_duration_and_keep_stream_bytes(self) -> None:
        stdout_bytes = b"stdout\x00\xff\n"
        stderr_bytes = b"stderr\x00\xfe\n"
        script = (
            "import os; "
            f"os.write(1, {stdout_bytes!r}); "
            f"os.write(2, {stderr_bytes!r})"
        )
        events: list[tuple[str, dict[str, object]]] = []
        with tempfile.TemporaryDirectory() as directory:
            runner = SubprocessRunner(Path(directory))
            runner.set_progress_sink(lambda event, fields: events.append((event, fields)))
            with redirect_stderr(StringIO()):
                result = runner.run(
                    [sys.executable, "-c", script], timeout_seconds=5,
                )

        self.assertEqual(result.stdout, stdout_bytes)
        self.assertEqual(result.stderr, stderr_bytes)
        self.assertEqual([event for event, _ in events], ["command-start", "command-finish"])
        self.assertEqual(events[0][1]["command_name"], Path(sys.executable).name)
        finish = events[1][1]
        self.assertEqual(finish["returncode"], 0)
        self.assertGreaterEqual(finish["duration_seconds"], 0)

    def test_hosted_semantic_operations_report_monotonic_duration(self) -> None:
        adapter = CLIAdapter.__new__(CLIAdapter)
        adapter._selected_connection = None
        events: list[tuple[str, dict[str, object]]] = []
        adapter._progress_sink = lambda event, fields: events.append((event, fields))
        adapter.execute = lambda _step: {"configured": True}
        scenario = ScenarioDefinition(
            id="functional.timing",
            steps=(ScenarioStep("configure", "configure", 5),),
            assertion_ids=("configure.accepted",),
            max_duration_seconds=5,
        )

        adapter.execute_scenario(scenario)

        self.assertEqual([event for event, _ in events], ["operation-start", "operation-finish"])
        self.assertGreaterEqual(events[1][1]["duration_seconds"], 0)

    def test_failed_hosted_operation_also_reports_duration(self) -> None:
        adapter = CLIAdapter.__new__(CLIAdapter)
        adapter._selected_connection = None
        events: list[tuple[str, dict[str, object]]] = []
        adapter._progress_sink = lambda event, fields: events.append((event, fields))

        def fail(_step):
            raise ScenarioExecutionError("ROUTING_PROBE_FAILED")

        adapter.execute = fail
        scenario = ScenarioDefinition(
            id="functional.timing",
            steps=(ScenarioStep("routing", "observe_routing_identity", 5),),
            assertion_ids=("routing.verified",),
            max_duration_seconds=5,
        )

        with self.assertRaises(ScenarioExecutionError):
            adapter.execute_scenario(scenario)

        self.assertEqual([event for event, _ in events], ["operation-start", "operation-error"])
        self.assertGreaterEqual(events[1][1]["duration_seconds"], 0)
        self.assertEqual(events[1][1]["code"], "ROUTING_PROBE_FAILED")

    def test_local_vm_command_timing_does_not_touch_logged_streams(self) -> None:
        stdout_bytes = b"candidate stdout\x00\xff\n"
        stderr_bytes = b"candidate stderr\x00\xfe\n"
        script = (
            "import os; "
            f"os.write(1, {stdout_bytes!r}); "
            f"os.write(2, {stderr_bytes!r})"
        )
        output = StringIO()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            logs = root / "logs"
            with redirect_stdout(output):
                result = local_vm._run_logged(
                    [sys.executable, "-c", script],
                    cwd=root,
                    logs=logs,
                    label="timing-probe",
                    timeout=5,
                )

            self.assertEqual(result.stdout, stdout_bytes)
            self.assertEqual(result.stderr, stderr_bytes)
            self.assertEqual((logs / "timing-probe.stdout.log").read_bytes(), stdout_bytes)
            self.assertEqual((logs / "timing-probe.stderr.log").read_bytes(), stderr_bytes)

        events = _json_events(output.getvalue())
        self.assertEqual([event["event"] for event in events], ["phase-start", "phase-finish"])
        self.assertEqual(events[0]["phase"], "command")
        self.assertEqual(events[1]["command_label"], "timing-probe")
        self.assertGreaterEqual(events[1]["duration_seconds"], 0)
        for event in events:
            _assert_utc_timestamp(self, event["timestamp_utc"])

    def test_render_service_acquire_and_cleanup_report_timing(self) -> None:
        ready = SimpleNamespace(
            handle=SimpleNamespace(service_id="service-123"),
            url="https://vpn.example.test",
        )

        class FakeController:
            def __init__(self, _api):
                self.released = False

            def acquire(self, *_args, **_kwargs):
                return ready

            def release(self, _ready):
                self.released = True

        class FakeAPI:
            def __init__(self, *_args, **_kwargs):
                self.deleted = False

            def list_services(self, _owner_id):
                return [SimpleNamespace(name="dobbyvpn-release-1-1", service_id="service-123")]

            def delete_service(self, _service_id):
                self.deleted = True

            def exists(self, _service_id):
                return False

        profile = SimpleNamespace(
            config_yaml=lambda _port: "service: test\n",
            client_toml=lambda _url: (
                "[[Outline]]\nDescription = 'synthetic Render profile'\n"
                "Server = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n"
                "\n[ExcludeIPs]\nIPs = []\n"
            ),
        )
        output = StringIO()
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.dict("os.environ", {"RENDER_API_TOKEN": "test-token"}), \
                mock.patch.object(render_service.OutlineWSSProfile, "random", return_value=profile), \
                mock.patch.object(render_service, "DisposableRenderController", FakeController), \
                mock.patch.object(render_service, "RenderAPI", FakeAPI), \
                redirect_stdout(output):
            start_args = SimpleNamespace(
                output=Path(directory), run_id="1", attempt="1", owner_id="owner",
                image_owner_id="image-owner",
                image_path="docker.io/dobbyvpn/outline@sha256:" + "a" * 64,
                image_digest="sha256:" + "a" * 64,
                region="oregon", timeout_seconds=1, poll_seconds=0.1, listen_port=10000,
            )
            self.assertEqual(render_service.start(start_args), 0)
            stop_args = SimpleNamespace(
                run_id="1", attempt="1", owner_id="owner", timeout_seconds=1,
            )
            self.assertEqual(render_service.stop(stop_args), 0)

        events = _json_events(output.getvalue())
        phases = [event["phase"] for event in events if event["event"] == "phase-finish"]
        self.assertEqual(
            phases,
            [
                "service-acquire",
                "profile-write",
                "service-discovery",
                "service-delete",
                "service-delete-verification",
            ],
        )
        for event in events:
            _assert_utc_timestamp(self, event["timestamp_utc"])
            if event["event"] == "phase-finish":
                self.assertGreaterEqual(event["duration_seconds"], 0)


if __name__ == "__main__":
    unittest.main()
