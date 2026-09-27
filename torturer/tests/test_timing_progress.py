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

from torturer_checks import local_vm
from torturer_checks.hosted import run as hosted_run
from torturer_checks.hosted.cli import HostedCLIAdapter, SubprocessRunner
from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_contract.functional.scenarios import ScenarioDefinition, ScenarioStep
from torturer_provider import render_service


def _json_events(output: str) -> list[dict[str, object]]:
    return [json.loads(line) for line in output.splitlines() if line.startswith("{")]


def _assert_utc_timestamp(test: unittest.TestCase, value: object) -> None:
    test.assertIsInstance(value, str)
    parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    test.assertIsNotNone(parsed.tzinfo)
    test.assertEqual(parsed.utcoffset().total_seconds(), 0)


class TimingProgressTests(unittest.TestCase):
    def test_hosted_progress_events_have_utc_timestamps(self) -> None:
        output = StringIO()
        with redirect_stdout(output):
            hosted_run._emit_progress_event("test-event", {"phase": "test"})

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
        adapter = HostedCLIAdapter.__new__(HostedCLIAdapter)
        adapter._selected_connection = None
        events: list[tuple[str, dict[str, object]]] = []
        adapter._progress_sink = lambda event, fields: events.append((event, fields))
        adapter.execute = lambda _step: {"configured": True}
        scenario = ScenarioDefinition(
            id="functional.timing",
            steps=(ScenarioStep("configure", "configure", 5),),
            required_capabilities=frozenset(),
            assertion_ids=("configure.accepted",),
            max_duration_seconds=5,
        )

        adapter.execute_scenario(scenario)

        self.assertEqual([event for event, _ in events], ["operation-start", "operation-finish"])
        self.assertGreaterEqual(events[1][1]["duration_seconds"], 0)

    def test_failed_hosted_operation_also_reports_duration(self) -> None:
        adapter = HostedCLIAdapter.__new__(HostedCLIAdapter)
        adapter._selected_connection = None
        events: list[tuple[str, dict[str, object]]] = []
        adapter._progress_sink = lambda event, fields: events.append((event, fields))

        def fail(_step):
            raise ScenarioExecutionError("ROUTING_PROBE_FAILED")

        adapter.execute = fail
        scenario = ScenarioDefinition(
            id="functional.timing",
            steps=(ScenarioStep("routing", "observe_routing_identity", 5),),
            required_capabilities=frozenset(),
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
            client_toml=lambda _url: "[profile]\nname = 'test'\n",
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
