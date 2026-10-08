from __future__ import annotations

import ctypes
from contextlib import contextmanager, nullcontext, redirect_stderr
from io import BytesIO, StringIO
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import ios_simulator_app
from torturer_runner.adapters import macos
from torturer_runner.ui import journey as native_ui
from torturer_runner.ui import smoke as native_ui_smoke
from torturer_runner.adapters.cli import CommandResult
from torturer_runner.windows_job import (
    WindowsJobCloseResult,
    _close_job,
)
from torturer_contract.engine import ScenarioExecutionError


def _desktop_profile_layout(platform: str) -> dict[str, object]:
    height = 560 if platform == "macos" else 640
    log_height = height - 310
    return {
        "ready": True,
        "window": {"x": 50, "y": 60, "width": 640, "height": height},
        "controls": {"x": 70, "y": 90, "width": 600, "height": 210},
        "profile_viewport": {"x": 90, "y": 180, "width": 560, "height": 100},
        "connection_action": {"x": 500, "y": 130, "width": 100, "height": 32},
        "logs": {"x": 70, "y": 310, "width": 600, "height": log_height},
        "scroll_position": 0,
        "visible_profile_actions": ["Profile 1 action", "Profile 2 action"],
    }


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


@contextmanager
def _native_ui_journey_args():
    with tempfile.TemporaryDirectory() as name:
        root = Path(name)
        paths = {name: root / name for name in ("cli", "ui", "profile", "native-helper")}
        for path in paths.values():
            path.write_text("test", encoding="utf-8")
        raw_logs = root / "logs"
        raw_logs.mkdir()
        args = SimpleNamespace(
            platform="windows",
            service_pipe="DobbyVPN.Control",
            service_socket=None,
            cli=paths["cli"],
            ui=paths["ui"],
            profile=paths["profile"],
            ui_helper=paths["native-helper"],
            raw_log_dir=raw_logs,
            output=root / "native-ui.json",
            service_pid=123,
            service_binary=paths["cli"],
            service_library_path=None,
            service_pid_file=None,
            service_identity_file=None,
            network_interface=None,
            routing_firewall_helper=None,
            timeout=5,
            native_cases=None,
        )
        yield root, args


def _windows_process_exited(pid: int, timeout_ms: int) -> bool:
    from ctypes import wintypes

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.OpenProcess.argtypes = (wintypes.DWORD, wintypes.BOOL, wintypes.DWORD)
    kernel32.OpenProcess.restype = wintypes.HANDLE
    kernel32.WaitForSingleObject.argtypes = (wintypes.HANDLE, wintypes.DWORD)
    kernel32.WaitForSingleObject.restype = wintypes.DWORD
    kernel32.CloseHandle.argtypes = (wintypes.HANDLE,)
    kernel32.CloseHandle.restype = wintypes.BOOL
    handle = kernel32.OpenProcess(0x00100000, False, pid)  # SYNCHRONIZE
    if not handle:
        error = ctypes.get_last_error()
        if error == 87:  # ERROR_INVALID_PARAMETER means the PID no longer exists.
            return True
        raise ctypes.WinError(error)
    try:
        result = kernel32.WaitForSingleObject(handle, timeout_ms)
        if result == 0:  # WAIT_OBJECT_0
            return True
        if result != 258:  # WAIT_TIMEOUT
            raise ctypes.WinError(ctypes.get_last_error())
        return False
    finally:
        kernel32.CloseHandle(handle)


class InformationRetentionTests(unittest.TestCase):
    def test_simulator_runner_forwards_exact_success_streams_once(self) -> None:
        stdout = b"success stdout\x00\xff"
        stderr = b"success stderr\xfe"
        process = mock.Mock()
        process.returncode = 0
        process.communicate.return_value = (stdout, stderr)
        forwarded = BinaryStderr()

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            redirect_stderr(forwarded),
        ):
            result = ios_simulator_app.SubprocessCommandRunner().run(("command",))

        self.assertEqual(result.stdout, "success stdout\x00\\xff")
        self.assertEqual(result.stderr, "success stderr\\xfe")
        self.assertEqual(forwarded.buffer.getvalue().count(stdout), 1)
        self.assertEqual(forwarded.buffer.getvalue().count(stderr), 1)

    def test_simulator_forwarding_failure_keeps_original_stream_bytes(self) -> None:
        stdout = b"success stdout\x00\xff"
        stderr = b"success stderr\xfe"
        process = mock.Mock()
        process.returncode = 0
        process.communicate.return_value = (stdout, stderr)

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch.object(ios_simulator_app, "emit_streams", side_effect=OSError("stderr closed")),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(("command",))

        self.assertEqual(caught.exception.stdout, stdout)
        self.assertEqual(caught.exception.stderr, stderr)
        self.assertIn("stderr closed", "\n".join(caught.exception.__notes__))

    def test_macos_routing_firewall_failures_retain_command_diagnostics(self) -> None:
        adapter = object.__new__(macos.MacOSAdapter)
        adapter.routing_firewall_helper = Path("/tmp/routing-firewall")
        adapter.network_interface = "en0"
        adapter._routing_probe_address = "198.51.100.9"
        adapter.runner = mock.Mock()

        for action in ("block", "remove"):
            with self.subTest(action=action):
                stdout = f"{action} helper stdout\x00\xff".encode("latin-1")
                stderr = f"{action} helper stderr\xfe".encode("latin-1")
                argv = ["sudo", "-n", str(adapter.routing_firewall_helper), f"routing-{action}"]
                if action == "block":
                    argv.extend((adapter.network_interface, adapter._routing_probe_address))
                result = CommandResult(
                    command=tuple(argv), returncode=1, stdout=stdout, stderr=stderr
                )
                adapter.runner.run.return_value = result
                with mock.patch("torturer_runner.adapters.cli.emit_streams") as emit:
                    with self.assertRaises(ScenarioExecutionError) as caught:
                        adapter._firewall(action, 5)

                self.assertEqual(
                    caught.exception.reason_code,
                    f"ROUTING_FIREWALL_{action.upper()}_FAILED",
                )
                notes = "\n".join(caught.exception.__notes__)
                self.assertIn(stdout.decode("utf-8", errors="backslashreplace"), notes)
                self.assertIn(stderr.decode("utf-8", errors="backslashreplace"), notes)
                emit.assert_called_once_with("command", stdout, stderr)
                adapter.runner.run.assert_called_with(argv, timeout_seconds=5)

    def test_macos_missing_socket_retains_service_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            log_payloads = {
                "service.log": b'backend log\x00\xff\n',
                "service.stdout.log": b'backend stdout\x01\xfe\n',
                "service.stderr.log": b'backend stderr\x02\xfd\n',
            }
            for filename, payload in log_payloads.items():
                (root / filename).write_bytes(payload)

            cli_failure = CommandResult(
                command=("dobby-cli", "snapshot"),
                returncode=1,
                stdout=b"",
                stderr=(
                    b"dobby-cli: operation failed error=dial unix "
                    b"/var/run/dobbyvpn/control.sock: connect: no such file or directory\n"
                ),
            )
            launchd = CommandResult(
                command=("launchctl", "print", "system/com.dobby.vpnservice"),
                returncode=0,
                stdout=b"state = running\nruns = 2\nlast exit code = 15\n",
                stderr=b"launchd diagnostic stderr\n",
            )
            runner = mock.Mock()
            runner.run.side_effect = (cli_failure, launchd)

            service = object.__new__(macos.MacOSServiceProcessController)
            service.runner = runner
            service.raw_directory = root
            service.control_socket = root / "control.sock"
            adapter = object.__new__(macos.MacOSAdapter)
            adapter.cli = Path("dobby-cli")
            adapter.runner = runner
            adapter.service = service

            forwarded = BinaryStderr()
            with redirect_stderr(forwarded):
                with self.assertRaises(ScenarioExecutionError) as caught:
                    adapter._command(("snapshot",), 5.0, "STATUS_FAILED")

            self.assertEqual(caught.exception.reason_code, "STATUS_FAILED")
            notes = "\n".join(caught.exception.__notes__)
            self.assertIn("no such file or directory", notes)
            self.assertIn("macos_launchd_print_returncode=0 timed_out=False", notes)
            self.assertIn("state = running\nruns = 2", notes)
            self.assertIn("macos_control_socket_lstat", notes)
            self.assertIn("exists=false", notes)
            labels = (
                "macos_backend_log_stdout",
                "macos_backend_stdout_stdout",
                "macos_backend_stderr_stderr",
            )
            for label, payload in zip(labels, log_payloads.values()):
                rendered = payload.decode("utf-8", errors="backslashreplace")
                self.assertIn(f"{label}:\n{rendered}", notes)
                self.assertIn(payload, forwarded.buffer.getvalue())
            self.assertIn(b"launchd diagnostic stderr", notes.encode())
            self.assertEqual(runner.run.call_count, 2)
            self.assertEqual(
                runner.run.call_args_list[1].args[0],
                macos._MACOS_LAUNCHD_PRINT,
            )
            self.assertEqual(
                runner.run.call_args_list[1].kwargs["timeout_seconds"],
                macos._MACOS_DIAGNOSTIC_TIMEOUT_SECONDS,
            )

    def test_native_ui_failure_result_keeps_completed_checks(self) -> None:
        with _native_ui_journey_args() as (_, args):
            output = args.output
            base = mock.Mock()
            base._snapshot.return_value = {"configured": True, "profiles": [{"index": 0}]}
            controller = mock.Mock()
            controller.bounded_by.side_effect = lambda _timeout: nullcontext()
            controller.configure.return_value = {"input_verified": True}
            action_error = native_ui.NativeUIJourneyError("connect failed")
            screenshot_error = OSError("failure screenshot unavailable")
            screenshot_error.add_note("secondary decoder cleanup detail")
            controller.connect.side_effect = action_error
            controller.capture.side_effect = [
                {"path": "configured.png", "width": 1, "height": 1},
                screenshot_error,
            ]
            smoke = SimpleNamespace(
                NativeUIController=mock.Mock(return_value=controller),
            )

            with (
                mock.patch.object(native_ui, "_ensure_directory"),
                mock.patch.object(native_ui, "SubprocessRunner"),
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "smoke", smoke),
                mock.patch.object(native_ui, "_exercise_subscription_controls", return_value={name: True for name in native_ui._REQUIRED_TRUE_CHECKS}),
                mock.patch("torturer_runner.subscription_fixture.SubscriptionFixture", return_value=mock.Mock(directory=args.profile.parent, start=mock.Mock(return_value="https://127.0.0.1:12345/subscription"))),
            ):
                with self.assertRaises(native_ui.NativeUIJourneyError) as caught:
                    native_ui.run_journey(args)

            failure = caught.exception
            self.assertEqual(failure.native_ui_checks, {"configure_native": True})
            self.assertIs(failure.__cause__, action_error)
            self.assertIn("OSError: failure screenshot unavailable", "\n".join(failure.__notes__))
            self.assertIn("secondary decoder cleanup detail", "\n".join(failure.__notes__))
            base.discover_connections.assert_not_called()
            controller.connect.assert_called_once()
            self.assertEqual(
                controller.capture.call_args_list,
                [mock.call("configured"), mock.call("failure-connect")],
            )
            base._snapshot.assert_not_called()
            controller.close_for_cleanup.assert_called_once()
            base.reset.assert_called_once()
            base.finalize.assert_called_once()

            with (
                mock.patch.object(native_ui, "build_parser", return_value=SimpleNamespace(
                    parse_args=lambda _argv: args,
                )),
                mock.patch.object(native_ui, "_ui_path_is_launchable", return_value=True),
                mock.patch.object(native_ui, "run_journey", side_effect=failure),
                redirect_stderr(StringIO()),
            ):
                self.assertEqual(native_ui.main([]), 1)

            self.assertEqual(
                json.loads(output.read_text(encoding="utf-8"))["checks"],
                {"configure_native": True},
            )

    def test_native_ui_milestone_screenshot_failure_fails_action(self) -> None:
        controller = mock.Mock()
        controller.bounded_by.side_effect = lambda _timeout: nullcontext()
        screenshot_error = OSError("configured screenshot could not be written")
        screenshot_error.add_note("decoder cleanup detail")
        controller.capture.side_effect = screenshot_error

        with self.assertRaises(native_ui.NativeUIJourneyError) as caught:
            native_ui._native_ui_action(
                controller,
                "configure",
                "native-input",
                30.0,
                lambda: {"input_verified": True},
                milestone="configured",
            )

        self.assertEqual(caught.exception.operation, "configure")
        self.assertEqual(caught.exception.stage, "native-input")
        self.assertIs(caught.exception.__cause__, screenshot_error)
        self.assertIn("milestone screenshot configured failed", str(caught.exception))
        self.assertIn("decoder cleanup detail", caught.exception.__notes__)
        controller.capture.assert_called_once_with("configured")

    def test_native_ui_unavailable_milestone_screenshot_fails_action(self) -> None:
        controller = mock.Mock()
        controller.bounded_by.side_effect = lambda _timeout: nullcontext()
        controller.capture.return_value = {"unavailable": "native window is closed"}

        with self.assertRaises(native_ui.NativeUIJourneyError) as caught:
            native_ui._native_ui_action(
                controller,
                "connect",
                "visible-connect",
                30.0,
                lambda: {"connected": True},
                milestone="connected",
            )

        self.assertEqual(caught.exception.operation, "connect")
        self.assertEqual(caught.exception.stage, "visible-connect")
        self.assertIn("native window is closed", str(caught.exception))
        controller.capture.assert_called_once_with("connected")

    def test_native_ui_successful_journey_captures_visible_milestones_only(self) -> None:
        with _native_ui_journey_args() as (root, args):
            base = mock.Mock()
            base._snapshot.return_value = {
                "configured": True,
                "profiles": [{"index": 0}],
                "active_profile": {"index": 0},
                "state": "CONNECTED",
                "generation": 1,
                "active_digest": "active-digest",
                "active_mode": "AUTO_SELECT",
                "active_index": 0,
                "digest": "active-digest",
                "source_url": "https://127.0.0.1:12345/subscription?cold=1",
            }
            inventory_restore = {"state": "checking"}

            def snapshot(*_args, **_kwargs):
                if inventory_restore["state"] == "empty":
                    inventory_restore["state"] = "loaded"
                    return {
                        "configured": False,
                        "profiles": [],
                        "active_profile": None,
                        "pending_target": None,
                        "state": "IDLE",
                        "source_url": "https://127.0.0.1:12345/subscription",
                    }
                if inventory_restore["state"] == "loaded":
                    inventory_restore["state"] = "done"
                    return {
                        "configured": True,
                        "profiles": [{"index": 0}, {"index": 1}],
                        "active_profile": None,
                        "pending_target": None,
                        "state": "CONFIGURED",
                        "source_url": "https://127.0.0.1:12345/subscription",
                    }
                return base._snapshot.return_value

            base._snapshot.side_effect = snapshot
            service_restarts = {"count": 0}

            def restart_service(_timeout):
                service_restarts["count"] += 1
                if service_restarts["count"] == 1:
                    inventory_restore["state"] = "empty"
                return {"process_loss_verified": True}

            base.restart_service_for_native_ui.side_effect = restart_service
            base._connected.side_effect = [False, True, True, True, False]
            base._cleanup_verified.return_value = True
            base.restart_service_for_native_ui.return_value = {"process_loss_verified": True}
            base.execute.side_effect = lambda step: {
                "observe_tunnel": {"tunnel_interface": True},
                "observe_routing_identity": {"routing_verified": True},
                "measure_stability": {"stability_verified": True, "stability_sample_count": 1},
                "measure_throughput": {
                    "latency_ms": 1.0, "download_mbps": 1.0, "upload_mbps": 1.0,
                },
            }[step.operation]
            controller = mock.Mock()
            controller.profile = root / "source.url"
            controller.cleared_record = "synthetic cleared UI record"
            controller._call.return_value = {"text": "new post-clear event"}
            controller.bounded_by.side_effect = lambda _timeout: nullcontext()
            controller.start.side_effect = lambda: controller.capture("startup")
            controller.configure.return_value = {"input_verified": True}
            controller.connect.return_value = {"status": "Connected"}
            controller.disconnect.return_value = {"status": "Disconnected"}
            controller.cold_deep_link.return_value = {"status": "Connected"}
            controller.about.side_effect = lambda: (
                controller.capture("about"),
                {"about_version": True, "about_source_commit": True},
            )[1]
            controller.close.return_value = {"closed": True}
            controller.reopen.side_effect = lambda: (
                controller.start(), {"status": "Connected"}
            )[1]
            controller.recover_after_process_loss.return_value = {
                "status": "Connected", "reconnecting_seen": True,
            }
            controller.capture.side_effect = lambda milestone: {
                "path": str(root / "screenshots" / f"{milestone}.png"),
                "width": 1,
                "height": 1,
            }
            smoke = SimpleNamespace(NativeUIController=mock.Mock(return_value=controller))
            native_checks = {name: True for name in native_ui._REQUIRED_TRUE_CHECKS}
            if args.platform == "windows":
                native_checks["rendered_stderr_capture_label"] = True
                native_checks["windows_rendered_log_palette"] = True
                native_checks["windows_text_size_150_layout"] = True

            palette_log = args.raw_log_dir / "service.log"
            palette_log.touch()

            def palette_check(_ui, _log_path, _timeout, current_checks):
                current_checks["windows_rendered_log_palette"] = True

            with (
                mock.patch.object(native_ui, "_ensure_directory"),
                mock.patch.object(native_ui, "SubprocessRunner"),
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "smoke", smoke),
                mock.patch.object(native_ui, "_exercise_subscription_controls", return_value=native_checks),
                mock.patch.object(native_ui, "_exercise_windows_rendered_log_palette", side_effect=palette_check),
                mock.patch.dict(os.environ, {"DOBBY_LOG_PATH": str(palette_log)}, clear=False),
                mock.patch(
                    "torturer_runner.subscription_fixture.SubscriptionFixture",
                    return_value=mock.Mock(
                        directory=args.profile.parent,
                        start=mock.Mock(return_value="https://127.0.0.1:12345/subscription"),
                        control_stats=mock.Mock(side_effect=[
                            {"subscription_gets": 1, "in_flight_gets": 0, "max_in_flight_gets": 1},
                            {"subscription_gets": 2, "in_flight_gets": 0, "max_in_flight_gets": 1},
                            {"subscription_gets": 2, "in_flight_gets": 0, "max_in_flight_gets": 1},
                            {"subscription_gets": 3, "in_flight_gets": 0, "max_in_flight_gets": 1},
                            {"subscription_gets": 3, "in_flight_gets": 0, "max_in_flight_gets": 1},
                        ]),
                    ),
                ),
            ):
                result = native_ui.run_journey(args)

            self.assertTrue(result["complete"])
            milestones = [call.args[0] for call in controller.capture.call_args_list]
            self.assertNotIn("closed", milestones)
            self.assertEqual(milestones.count("about"), 1)
            self.assertTrue({
                "startup", "configured", "connected", "disconnected", "reconnected",
                "about", "reopened", "process-recovered",
            }.issubset(milestones))
            controller.cold_deep_link.assert_called_once_with(
                "https://127.0.0.1:12345/subscription?cold=1"
            )

    def test_simulator_second_timeout_streams_reach_primary_failure(self) -> None:
        initial_stdout = b"initial\x00\xff"
        terminated_stdout = initial_stdout + b" after terminate"
        final_stdout = terminated_stdout + b" after cleanup"
        final_stderr = b"cleanup stderr"
        process = mock.Mock()
        process.pid = 123
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(("command",), 0.1, output=initial_stdout),
            subprocess.TimeoutExpired(("command",), 1.0, output=terminated_stdout),
            subprocess.TimeoutExpired(
                ("command",), 1.0, output=final_stdout, stderr=final_stderr
            ),
        ]
        forwarded = BinaryStderr()

        with (
            mock.patch.object(ios_simulator_app.subprocess, "Popen", return_value=process),
            mock.patch("bounded_process.terminate_process_group"),
            redirect_stderr(forwarded),
        ):
            with self.assertRaises(ios_simulator_app.IOSSimulatorAppContractError) as caught:
                ios_simulator_app.SubprocessCommandRunner().run(
                    ("command",), timeout_seconds=0.1
                )

        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("after cleanup", notes)
        self.assertIn("cleanup stderr", notes)
        self.assertIn(final_stdout, forwarded.buffer.getvalue())
        self.assertIn(final_stderr, forwarded.buffer.getvalue())

    def test_windows_job_close_result_has_explicit_outcome_and_diagnostics(self) -> None:
        class FakeJob:
            closed = False
            diagnostics: list[str] = []

            def close(self, *, deadline=None):
                self.diagnostics.append("api=CloseHandle winerror=5")

        result = _close_job(FakeJob(), stage="test")

        self.assertIsInstance(result, WindowsJobCloseResult)
        self.assertFalse(isinstance(result, tuple))
        self.assertTrue(result.failed)
        self.assertIn("api=CloseHandle winerror=5", result.diagnostics)
        self.assertTrue(any("job-still-attached" in item for item in result.diagnostics))

    def test_ios_ui_log_collection_preserves_raw_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            container = root / "container"
            (container / "tmp").mkdir(parents=True)
            (container / "tmp/app_logs.txt").write_bytes(b"native lifecycle\n")
            payload = b"UI diagnostic \xff\x00\r\n"
            retained_names = ("ui_diagnostics.jsonl", "app_logs.txt.previous", "go_app_logs.jsonl.stderr", "go_app_logs.jsonl.stderr.previous")
            for filename in retained_names:
                (container / "tmp" / filename).write_bytes(payload)
            with mock.patch.object(ios_simulator_app, "_require_success", return_value=SimpleNamespace(stdout=str(container))):
                ios_simulator_app._collect_ios_native_log(
                    mock.Mock(), SimpleNamespace(udid="11111111-1111-1111-1111-111111111111"),
                    SimpleNamespace(bundle_identifier="vpn.dobby.app"), root / "work", budget=mock.Mock(),
                )
            ios_simulator_app.retain_ios_diagnostics(root / "work", root / "collected")
            for filename in retained_names:
                self.assertEqual((root / "collected" / filename).read_bytes(), payload)

    def test_ios_ui_log_collection_reports_absent_native_log_without_placeholder(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            container = root / "container"
            temporary = container / "tmp"
            temporary.mkdir(parents=True)
            payloads = {
                "ui_diagnostics.jsonl": b'{"event":"ui.failure"}\n',
                "go_tunnel_logs.jsonl.stderr": b'{"event":"stderr.capture"}\n',
            }
            for filename, payload in payloads.items():
                (temporary / filename).write_bytes(payload)

            with mock.patch.object(
                ios_simulator_app,
                "_require_success",
                return_value=SimpleNamespace(stdout=str(container)),
            ):
                native_log = ios_simulator_app._collect_ios_native_log(
                    mock.Mock(),
                    SimpleNamespace(udid="11111111-1111-1111-1111-111111111111"),
                    SimpleNamespace(bundle_identifier="vpn.dobby.app"),
                    root / "work",
                    budget=mock.Mock(),
                )

            collected = root / "work/diagnostics/ios-simulator"
            self.assertIsNone(native_log)
            self.assertFalse((collected / "app-native.log").exists())
            for filename, payload in payloads.items():
                self.assertEqual((collected / filename).read_bytes(), payload)

    def test_native_ui_log_collection_preserves_raw_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            controller = object.__new__(native_ui_smoke.NativeUIController)
            controller.platform = "windows"
            controller.logs = root / "collected"
            controller._windows_wer_started_at_utc = None
            controller._windows_wer_dump_dir = None
            controller.logs.mkdir()
            source = root / "DobbyVPN/Logs/ui_diagnostics.jsonl"
            with mock.patch.dict(os.environ, {"LOCALAPPDATA": str(root)}):
                controller.collect_diagnostics()
                self.assertEqual(list(controller.logs.iterdir()), [])
                source.parent.mkdir(parents=True)
                source.write_bytes(b"original \xff\x00 diagnostic\r\n")
                previous = source.with_name(source.name + ".previous")
                previous.write_bytes(b"previous \xfe\x00 diagnostic\r\n")
                controller.collect_diagnostics()
                for retained in (source, previous):
                    self.assertEqual((controller.logs / retained.name).read_bytes(), retained.read_bytes())

    @unittest.skipIf(os.name == "nt", "macOS account home uses POSIX pwd")
    def test_macos_native_collection_uses_account_home_with_disposable_home(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            account_home = root / "account"
            source = account_home / "Library/Logs/DobbyVPN/ui_diagnostics.jsonl"
            source.parent.mkdir(parents=True)
            source.write_bytes(b"native original cause\xff\r\n")
            controller = object.__new__(native_ui_smoke.NativeUIController)
            controller.platform = "macos"
            controller.logs = root / "collected"
            controller._windows_wer_started_at_utc = None
            controller._windows_wer_dump_dir = None
            controller.logs.mkdir()
            with (
                mock.patch.dict(os.environ, {"HOME": str(root / "disposable")}),
                mock.patch("pwd.getpwuid", return_value=SimpleNamespace(pw_dir=str(account_home))),
            ):
                controller.collect_diagnostics()
            self.assertEqual((controller.logs / source.name).read_bytes(), source.read_bytes())

    def test_macos_clear_checks_selectable_logs_and_preserved_reading_position(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            profile = root / "profile.txt"
            profile.write_text("https://example.invalid/subscription", encoding="utf-8")
            controller = object.__new__(native_ui_smoke.NativeUIController)
            controller.platform = "macos"
            controller.profile = profile
            controller.cleared_record = None
            initial = "2026 · INFO · Backend\nready\nDetails\n"
            log_texts = iter((initial, initial, initial, initial, initial, initial + "new record\n", ""))
            positions = iter((
                {"visible_range_start": 0, "visible_range_end": 100},
                {"visible_range_start": 0, "visible_range_end": 100},
                {"visible_range_start": 0, "visible_range_end": 100},
                {"visible_range_start": 0, "visible_range_end": 100},
            ))
            operations: list[str] = []

            def call(operation: str, **fields: object) -> dict:
                operations.append(operation)
                if operation == "logs":
                    return {"ready": True, "text": next(log_texts)}
                if operation == "select-log-text":
                    return {"ready": True, "selected": initial}
                if operation == "log-position":
                    return {"ready": True, **next(positions)}
                if operation == "resize-window":
                    return {"ready": True, "width": 900, "height": 700}
                if operation == "profile-list-layout":
                    return _desktop_profile_layout(controller.platform)
                return {"ready": True}

            def wait(predicate, message: str) -> None:
                if not predicate():
                    raise AssertionError(message)

            controller._call = call
            controller._wait = wait
            controller._click = lambda _name: None
            controller.failing_subscription = lambda _url: {}
            controller.snapshot = lambda: {"status": "Disconnected"}

            result = controller.clear_logs()

            self.assertEqual(result, {"status": "Disconnected"})
            self.assertEqual(controller.cleared_record, "2026 · INFO · Backend")
            self.assertIn("select-log-text", operations)
            self.assertIn("log-position", operations)
            self.assertEqual(operations.count("profile-list-layout"), 1)
            self.assertEqual(operations.count("resize-window"), 2)
            self.assertTrue(controller.log_resize_verified)
            self.assertEqual(profile.read_text(encoding="utf-8"), "https://example.invalid/subscription")

    def test_windows_clear_rejects_a_reading_position_change_while_frozen(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            profile = root / "profile.txt"
            profile.write_text("https://example.invalid/subscription", encoding="utf-8")
            controller = object.__new__(native_ui_smoke.NativeUIController)
            controller.platform = "windows"
            controller.profile = profile
            controller.cleared_record = None
            structured = "2026 · INFO · Backend · ready"
            initial = structured + "\nDetails\n"
            log_texts = iter((initial, initial, initial))
            positions = iter((0.0, 25.0))

            def call(operation: str, **fields: object) -> dict:
                if operation == "logs":
                    return {
                        "ready": True,
                        "text": next(log_texts),
                        "entries": [
                            {"text": "2026-10-06T00:00:00Z · INFO · Backend stderr\nStderr capture initialized", "foreground": 0},
                            {"text": structured, "foreground": 0},
                        ],
                        "expansion_verified": True,
                        "expanded_record": '{"message":"ready"}',
                    }
                if operation == "select-log-text":
                    return {"ready": True, "selected": structured}
                if operation == "log-position":
                    position = next(positions)
                    return {"ready": True, "vertical_scroll_percent": position,
                            "visible_first_record": "record-at-" + str(position)}
                if operation == "resize-window":
                    return {"ready": True, "left": 50, "top": 60, "width": 900, "height": 700}
                if operation == "profile-list-layout":
                    return _desktop_profile_layout(controller.platform)
                return {"ready": True}

            def wait(predicate, message: str) -> None:
                if not predicate():
                    raise AssertionError(message)

            controller._call = call
            controller._wait = wait
            controller._click = lambda _name: None
            controller.failing_subscription = lambda _url: {}
            controller.snapshot = lambda: {"status": "Disconnected"}

            with self.assertRaisesRegex(native_ui_smoke.NativeUISmokeError, "reading position changed"):
                controller.clear_logs()

    def test_windows_clear_preserves_selection_and_position_while_frozen(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            profile = root / "profile.txt"
            profile.write_text("https://example.invalid/subscription", encoding="utf-8")
            controller = object.__new__(native_ui_smoke.NativeUIController)
            controller.platform = "windows"
            controller.profile = profile
            controller.cleared_record = None
            structured = "2026 · INFO · Backend · ready"
            initial = structured + "\nDetails\n"
            capture = "2026-10-06T00:00:00Z · INFO · Backend stderr\nStderr capture initialized"
            log_texts = iter((initial, initial, initial, initial, initial, initial + "new record\n", ""))
            positions = iter((25.0, 25.0, 25.0, 25.0))
            selected = iter((structured, structured))
            operations: list[str] = []

            def call(operation: str, **fields: object) -> dict:
                operations.append(operation)
                if operation == "logs":
                    return {
                        "ready": True,
                        "text": next(log_texts),
                        "entries": [{"text": capture, "foreground": 1}, {"text": structured, "foreground": 1}],
                        "expansion_verified": True,
                        "expanded_record": '{"message":"ready"}',
                    }
                if operation == "select-log-text":
                    return {"ready": True, "selected": next(selected)}
                if operation == "log-position":
                    position = next(positions)
                    return {"ready": True, "vertical_scroll_percent": position,
                            "visible_first_record": "record-at-" + str(position)}
                if operation == "resize-window":
                    return {"ready": True, "left": 50, "top": 60, "width": 900, "height": 700}
                if operation == "profile-list-layout":
                    return _desktop_profile_layout(controller.platform)
                return {"ready": True}

            def wait(predicate, message: str) -> None:
                if not predicate():
                    raise AssertionError(message)

            controller._call = call
            controller._wait = wait
            controller._click = lambda _name: None
            controller.failing_subscription = lambda _url: {}
            controller.snapshot = lambda: {"status": "Disconnected"}

            result = controller.clear_logs()

            self.assertEqual(result, {"status": "Disconnected"})
            self.assertEqual(controller.cleared_record, structured)
            self.assertEqual(operations.count("select-log-text"), 2)
            self.assertEqual(operations.count("log-position"), 4)
            self.assertEqual(operations.count("resize-window"), 2)
            self.assertEqual(operations.count("profile-list-layout"), 1)
            self.assertTrue(controller.log_resize_verified)
            self.assertEqual(profile.read_text(encoding="utf-8"), "https://example.invalid/subscription")

    def test_native_windows_helper_uses_existing_job_boundary(self) -> None:
        process = mock.Mock()
        completed = subprocess.CompletedProcess(
            ["native-helper"], 0, stdout=b'{"ready":true}', stderr=b""
        )
        controller = object.__new__(native_ui_smoke.NativeUIController)
        controller.platform = "windows"
        controller.executable = Path("D:/DobbyVPN.exe")
        controller.helper = Path("D:/NativeUI.exe")
        controller._timeout = 15.0
        controller._deadline = None
        controller.pid = None
        controller.identity = None

        with (
            mock.patch.object(native_ui_smoke, "_native_run", return_value=completed) as run,
            mock.patch.object(native_ui_smoke.time, "monotonic", return_value=100.0),
            mock.patch.object(
                native_ui_smoke,
                "popen_with_windows_job",
                return_value=process,
            ) as popen_with_job,
            mock.patch.object(
                native_ui_smoke,
                "terminate_windows_job",
                return_value=SimpleNamespace(
                    process_tree_proven=True,
                    active_processes=0,
                    diagnostics=(),
                ),
            ) as terminate_job,
            mock.patch.object(
                native_ui_smoke,
                "close_windows_job",
                return_value=WindowsJobCloseResult(),
            ) as close_job,
        ):
            response = controller._call("type", source="profile.ovpn")

            self.assertEqual(response, {"ready": True})
            self.assertIn("popen_factory", run.call_args.kwargs)
            self.assertIn("terminate", run.call_args.kwargs)
            self.assertIn("close_boundary", run.call_args.kwargs)
            self.assertEqual(run.call_args.kwargs["cleanup_timeout_seconds"], 2.0)

            popen_factory = run.call_args.kwargs["popen_factory"]
            self.assertIs(popen_factory(["native-helper"]), process)
            popen_with_job.assert_called_once_with(
                subprocess.Popen,
                ["native-helper"],
                stage="native-ui-helper",
                deadline=110.0,
            )

            run.call_args.kwargs["terminate"](process, 112.0)
            terminate_job.assert_called_once_with(
                process,
                deadline=112.0,
                stage="native-ui-helper-timeout",
            )
            run.call_args.kwargs["close_boundary"](process, 114.0)
            close_job.assert_called_once_with(
                process,
                stage="native-ui-helper-cleanup",
                deadline=114.0,
            )

            terminate_job.return_value = SimpleNamespace(
                process_tree_proven=False,
                active_processes=1,
                diagnostics=(),
            )
            with self.assertRaises(native_ui_smoke.WindowsJobError) as termination_error:
                run.call_args.kwargs["terminate"](process, 115.0)
            self.assertIn("active_processes=1", str(termination_error.exception))

            close_job.return_value = WindowsJobCloseResult(
                diagnostics=("api=CloseHandle winerror=5",), failed=True
            )
            with self.assertRaises(native_ui_smoke.WindowsJobError) as close_error:
                run.call_args.kwargs["close_boundary"](process, 116.0)
            self.assertIn("CloseHandle winerror=5", str(close_error.exception))

    @unittest.skipUnless(os.name == "nt", "requires a Windows Job Object")
    def test_native_helper_job_ends_descendant_holding_capture_pipes(self) -> None:
        from torturer_runner.process_capture import run_finite_capture

        cleanup_seconds = 2.0
        spawn, terminate, close = native_ui_smoke._windows_job_capture_callbacks(time.monotonic() + 3.0)
        processes = []
        child_script = (
            "import sys,time; "
            "print('child-stdout-marker', flush=True); "
            "print('child-stderr-marker', file=sys.stderr, flush=True); time.sleep(60)"
        )
        parent_script = (
            "import subprocess,sys,time\n"
            "child=subprocess.Popen([sys.executable, '-c', sys.argv[1]])\n"
            "print(f'parent-child-pid={child.pid}', flush=True)\n"
            "time.sleep(1)\n"
            "print('parent-stderr-marker', file=sys.stderr, flush=True)\n"
        )
        command = [sys.executable, "-c", parent_script, child_script]

        def capture_spawn(arguments, **popen_kwargs):
            process = spawn(arguments, **popen_kwargs)
            processes.append(process)
            return process

        try:
            with self.assertRaises(subprocess.TimeoutExpired) as caught:
                run_finite_capture(
                    command,
                    timeout_seconds=3.0,
                    popen_factory=capture_spawn,
                    terminate=terminate,
                    close_boundary=close,
                    termination_grace_seconds=1.0,
                    cleanup_timeout_seconds=cleanup_seconds,
                )
            process = processes[0]
            stdout, stderr = caught.exception.stdout or b"", caught.exception.stderr or b""
            match = re.search(rb"parent-child-pid=(\d+)", stdout)
            if match is None:
                self.fail(f"child PID missing from captured stdout: {stdout!r}")
            child_pid = int(match.group(1))
            self.assertTrue(all(marker in stdout for marker in (
                b"child-stdout-marker", b"parent-child-pid="
            )))
            self.assertTrue(all(marker in stderr for marker in (
                b"child-stderr-marker", b"parent-stderr-marker"
            )))
            self.assertFalse(getattr(caught.exception, "__notes__", ()))
            self.assertTrue(_windows_process_exited(child_pid, int(cleanup_seconds * 1000)))
            self.assertTrue(all(not reader.is_alive() for reader in (
                process.stdout_thread, process.stderr_thread
            )))
        finally:
            for process in processes:
                close(process, time.monotonic() + cleanup_seconds)


if __name__ == "__main__":
    unittest.main()
