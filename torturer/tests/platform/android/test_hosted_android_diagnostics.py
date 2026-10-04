from __future__ import annotations

from torturer_runner.android_diagnostics import retained_log_sources

from contextlib import redirect_stderr
from hashlib import sha256
from io import BytesIO
import json
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.engine import ScenarioExecutionError
from torturer_runner.adapters.android import AndroidAdapter
from torturer_runner.adapters.cli import CommandResult


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class HostedAndroidFailureDiagnosticsTests(unittest.TestCase):
    def test_https_gui_lane_does_not_require_a_staged_profile_file(self):
        with tempfile.TemporaryDirectory() as name:
            profile = Path(name) / "absent-profile.toml"
            instrument = CommandResult(command=("adb", "instrument"), returncode=1,
                                       stdout=b"INSTRUMENTATION_FAILED: synthetic\n", stderr=b"")
            adapter = self.adapter_for(profile, instrument)
            adapter.ui_mode = "gui-auto"
            output = CommandResult(command=("adb", "cat"), returncode=0, stdout=b"{}", stderr=b"")
            failure = self.run_failing_phase(adapter, profile, output, BinaryStderr())
            self.assertIn("Android instrumentation failed", str(failure))
            adapter._run_instrumentation.assert_called_once()

    @staticmethod
    def adapter_for(profile: Path, instrument: CommandResult) -> AndroidAdapter:
        adapter = AndroidAdapter.__new__(AndroidAdapter)
        adapter.profile = profile
        adapter.ui_mode = "protocol-matrix"
        adapter._active_controls = ()
        adapter._run_instrumentation = mock.Mock(return_value=instrument)
        return adapter

    def run_failing_phase(
        self,
        adapter: AndroidAdapter,
        profile: Path,
        adb_result: CommandResult | BaseException,
        forwarded: BinaryStderr,
    ) -> BaseException:
        command_file = profile.parent / "phase.command.json"
        command_file.write_bytes(b"{}")
        output_name = "phase.observation.json"
        scenario = SimpleNamespace(id="android-diagnostic-phase")
        device_files: list[str] = []

        def adb(_arguments, _timeout, _failure_code, **_kwargs):
            if isinstance(adb_result, BaseException):
                raise adb_result
            return adb_result

        with (
            mock.patch.object(
                adapter,
                "_write_command",
                return_value=(command_file, "phase.profile", output_name),
            ),
            mock.patch.object(adapter, "_stage_private_file"),
            mock.patch.object(adapter, "_adb", side_effect=adb),
            redirect_stderr(forwarded),
        ):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._execute_phase(
                    scenario,
                    (),
                    time.monotonic() + 30,
                    device_files,
                )
        self.assertIn("phase.observation.json", device_files)
        return caught.exception

    def test_failed_instrumentation_forwards_exact_observation_bytes(self) -> None:
        instrument_stdout = b"INSTRUMENTATION_FAILED: test assertion\n"
        observation = b'{"command_output":"base64-diagnostics"}\x00\xff\n'
        instrument = CommandResult(
            command=("adb", "instrument"),
            returncode=1,
            stdout=instrument_stdout,
            stderr=b"instrumentation stderr\n",
        )
        output = CommandResult(
            command=("adb", "cat"),
            returncode=0,
            stdout=observation,
            stderr=b"observation cat stderr\n",
        )
        forwarded = BinaryStderr()

        with tempfile.TemporaryDirectory() as name:
            profile = Path(name) / "profile.toml"
            profile.write_bytes(b"synthetic profile\n")
            adapter = self.adapter_for(profile, instrument)
            failure = self.run_failing_phase(adapter, profile, output, forwarded)

        self.assertIn("Android instrumentation failed", str(failure))
        self.assertIn(observation, forwarded.buffer.getvalue())
        self.assertIn(b"[android-observation stdout]\n" + observation, forwarded.buffer.getvalue())
        self.assertIn(b"observation cat stderr\n", forwarded.buffer.getvalue())
        self.assertIn(b"instrumentation stderr\n", forwarded.buffer.getvalue())

    def test_observation_collection_error_is_attached_to_instrumentation_failure(self) -> None:
        instrument = CommandResult(
            command=("adb", "instrument"),
            returncode=1,
            stdout=b"INSTRUMENTATION_FAILED: original failure\n",
            stderr=b"original instrumentation stderr\n",
        )
        collection_error = ScenarioExecutionError(
            "ANDROID_OBSERVATION_UNAVAILABLE"
        )
        collection_error.stdout = b"cat stdout\x00\xff"
        collection_error.stderr = b"cat stderr diagnostic\n"
        collection_error.add_note("original collection detail")
        forwarded = BinaryStderr()

        with tempfile.TemporaryDirectory() as name:
            profile = Path(name) / "profile.toml"
            profile.write_bytes(b"synthetic profile\n")
            adapter = self.adapter_for(profile, instrument)
            failure = self.run_failing_phase(
                adapter, profile, collection_error, forwarded
            )

        self.assertIn("Android instrumentation failed", str(failure))
        notes = "\n".join(failure.__notes__)
        self.assertIn("ANDROID_OBSERVATION_UNAVAILABLE", notes)
        self.assertIn("original collection detail", notes)
        self.assertIn("cat stdout", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("cat stderr diagnostic", notes)

    def test_primary_and_both_cleanup_failures_keep_original_details(self) -> None:
        adapter = AndroidAdapter.__new__(AndroidAdapter)
        primary = ScenarioExecutionError("ANDROID_PRIMARY_SENTINEL")
        device_cleanup = ScenarioExecutionError("ANDROID_DEVICE_CLEANUP_SENTINEL")
        scratch_cleanup = ScenarioExecutionError("ANDROID_SCRATCH_CLEANUP_SENTINEL")
        device_cleanup.stderr = b"device cleanup stderr\x00\xff"
        scratch_cleanup.stdout = b"scratch cleanup stdout\n"
        scenario = SimpleNamespace(id="diagnostic", max_duration_seconds=30, steps=())

        with (
            mock.patch.object(adapter, "_execute_phase", side_effect=primary),
            mock.patch.object(adapter, "_cleanup_device", return_value=device_cleanup),
            mock.patch.object(adapter, "_cleanup_local_scratch", return_value=scratch_cleanup),
        ):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter.execute_scenario(scenario)

        self.assertIs(caught.exception, primary)
        notes = "\n".join(primary.__notes__)
        self.assertIn("ANDROID_DEVICE_CLEANUP_SENTINEL", notes)
        self.assertIn("device cleanup stderr", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("ANDROID_SCRATCH_CLEANUP_SENTINEL", notes)
        self.assertIn("scratch cleanup stdout", notes)

    def test_functional_failure_retains_app_logs_native_logs_and_full_logcat(self) -> None:
        go_logs = b'{"level":"error","message":"xray sentinel"}\x00\xff\n'
        native_logs = b'{"event":"native sentinel"}\x00\xfe\n'
        logcat = b"DobbyVpnService: state=FAILED\x00\xfd\nother-tag: full buffer\n"
        stderr = b"adb diagnostic\x00\xff\n"
        results = iter(
            (
                CommandResult(("adb",), 0, go_logs, stderr),
                CommandResult(("adb",), 0, native_logs, stderr),
                CommandResult(("adb",), 0, logcat, stderr),
            )
        )

        with tempfile.TemporaryDirectory() as name:
            raw = Path(name) / "logs"
            raw.mkdir()
            commands: list[tuple[str, ...]] = []
            adapter = AndroidAdapter.__new__(AndroidAdapter)
            adapter.runner = SimpleNamespace(raw_directory=raw)
            adapter.ui_mode = "protocol-matrix"
            adapter._selected_connection = SimpleNamespace(index=2, protocol="Xray")
            adapter._diagnostic_collection_sequence = 0
            primary = ScenarioExecutionError("XHTTP_FAILURE")

            def adb(arguments, *_args, **_kwargs):
                commands.append(arguments)
                if arguments[:4] == ("shell", "-T", "sh", "-c"):
                    return CommandResult(("adb",), 0, b"retained \xff\x00\n", stderr)
                return next(results)

            with mock.patch.object(
                adapter, "_adb", side_effect=adb
            ):
                adapter._collect_functional_failure_diagnostics(
                    primary,
                    "functional.core-connection",
                    time.monotonic() + 30,
                )

            self.assertEqual(str(primary), "XHTTP_FAILURE")
            self.assertEqual(
                commands,
                [
                    ("shell", "-T", "cat", "/data/user/0/com.dobby.vpn/files/diagnostics/go_app_logs.jsonl"),
                    ("shell", "-T", "cat", "/data/user/0/com.dobby.vpn/files/diagnostics/native_logs.jsonl"),
                    *(tuple(command) for _, _, command, _, _ in retained_log_sources()),
                    ("shell", "logcat", "-d", "-v", "raw"),
                ],
            )
            expected = {
                "go_app_logs.jsonl": go_logs,
                "native_logs.jsonl": native_logs,
                "logcat.txt": logcat,
                **{filename: b"retained \xff\x00\n" for _, _, _, filename, _ in retained_log_sources()},
            }
            for suffix, payload in expected.items():
                matches = list(raw.glob(f"*{suffix}"))
                self.assertEqual(len(matches), 1)
                self.assertEqual(matches[0].read_bytes(), payload)
                self.assertEqual(
                    matches[0].with_name(matches[0].name + ".stderr.log").read_bytes(),
                    stderr,
                )
            self.assertEqual(list(raw.glob("*logcat.txt"))[0].read_bytes(), logcat)

    def test_functional_diagnostic_collection_error_keeps_primary_and_partial_bytes(self) -> None:
        partial = b'{"event":"partial xray log"}\x00\xff'
        results = iter(
            (
                CommandResult(("adb",), 17, partial, b"go log read failed\x00\xff"),
                CommandResult(("adb",), 0, b'{"event":"native"}\n', b""),
                CommandResult(("adb",), 0, b"full logcat\n", b""),
            )
        )
        with tempfile.TemporaryDirectory() as name:
            raw = Path(name)
            adapter = AndroidAdapter.__new__(AndroidAdapter)
            adapter.runner = SimpleNamespace(raw_directory=raw)
            adapter.ui_mode = "gui-auto"
            adapter._selected_connection = None
            adapter._diagnostic_collection_sequence = 0
            primary = ScenarioExecutionError("XHTTP_FAILURE")
            with mock.patch.object(
                adapter, "_adb", side_effect=lambda arguments, *args, **kwargs: (
                    CommandResult(("adb",), 44, b"", b"")
                    if arguments[:4] == ("shell", "-T", "sh", "-c") else next(results)
                )
            ):
                adapter._collect_functional_failure_diagnostics(
                    primary,
                    "functional.core-connection",
                    time.monotonic() + 30,
                )

            self.assertEqual(str(primary), "XHTTP_FAILURE")
            notes = "\n".join(primary.__notes__)
            self.assertIn("ANDROID_GO_APP_LOG_COLLECTION_FAILED", notes)
            self.assertIn("go log read failed", notes)
            self.assertIn(r"\xff", notes)
            self.assertEqual(list(raw.glob("*go_app_logs.jsonl"))[0].read_bytes(), partial)


class HostedAndroidRoutingProofDiagnosticsTests(unittest.TestCase):
    def test_vpn_request_failure_keeps_code_and_complete_provider_detail(self) -> None:
        detail = (
            "java.net.SocketTimeoutException: request timed out\n"
            "\tat example.Probe.request(Probe.java:17)\n"
        )
        observation = {
            "phase": "blocked",
            "direct": {"error_code": "ANDROID_NETWORK_REQUEST_FAILED"},
            "vpn": {
                "error_code": "ANDROID_NETWORK_REQUEST_FAILED",
                "error_detail": detail,
            },
        }

        with self.assertRaises(ScenarioExecutionError) as caught:
            AndroidAdapter._assert_routing_blocked(observation)

        failure = caught.exception
        self.assertEqual(str(failure), "ANDROID_NETWORK_REQUEST_FAILED")
        self.assertIn("android_routing_phase=blocked", failure.__notes__)
        self.assertIn("android_routing_vpn_error_detail:\n" + detail, failure.__notes__)

    def test_vpn_request_success_still_passes_when_direct_request_is_blocked(self) -> None:
        observation = {
            "phase": "blocked",
            "direct": {"error_code": "ANDROID_NETWORK_REQUEST_FAILED"},
            "vpn": {"status": 200, "body": "203.0.113.7"},
        }

        self.assertEqual(
            AndroidAdapter._assert_routing_blocked(observation),
            "203.0.113.7",
        )


class AndroidRenderedScreenshotRetentionTests(unittest.TestCase):
    def test_same_label_from_distinct_commands_retains_both_exact_payloads(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            raw_directory = Path(name)
            adapter = AndroidAdapter.__new__(AndroidAdapter)
            adapter.runner = SimpleNamespace(raw_directory=raw_directory)
            frames = {
                "android-hosted-11111111111111111111111111111111": b"first command frame",
                "android-hosted-22222222222222222222222222222222": b"second command frame",
            }

            def pull(arguments, _timeout, _failure_code, **_kwargs):
                destination = Path(arguments[-1])
                destination.write_bytes(frames[destination.parent.name])
                return CommandResult(tuple(arguments), 0, b"", b"")

            adapter._adb = mock.Mock(side_effect=pull)
            remote = (
                "/data/user/0/com.dobby.vpn/cache/"
                "dobbyvpn-rendered-screenshots/0004-configure-surface.png"
            )
            retained = []
            for command_id, payload in frames.items():
                retained.append(adapter._pull_rendered_screenshot(
                    f"{command_id}.command.json",
                    remote,
                    "0004-configure-surface.png",
                    time.monotonic() + 30,
                    expected_bytes=len(payload),
                    expected_sha256=sha256(payload).hexdigest(),
                    expected_width=720,
                    expected_height=1280,
                ))

            self.assertNotEqual(retained[0], retained[1])
            self.assertEqual(
                [path.read_bytes() for path in retained],
                list(frames.values()),
            )
            self.assertEqual(
                len(list((raw_directory / "screenshots" / "android").glob("*/*.png"))),
                2,
            )


if __name__ == "__main__":
    unittest.main()
