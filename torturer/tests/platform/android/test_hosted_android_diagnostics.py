from __future__ import annotations

from contextlib import redirect_stderr
from io import BytesIO
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.engine import ScenarioExecutionError
from torturer_runner.adapters.android import AndroidAdapter, _select_gui_profile
from torturer_runner.adapters.cli import CommandResult


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class AndroidGuiProfileSelectionTests(unittest.TestCase):
    def test_selects_one_supported_schema_v2_profile_and_adds_schema_header(self) -> None:
        raw = (
            b"schema_version = 2\n"
            b"[[profiles]]\nprotocol = 'TRUST_TUNNEL'\n"
            b"[profiles.config]\nendpoint = 'https://trust.invalid'\n"
            b"[[profiles]]\nprotocol = 'OUTLINE'\n"
            b"[profiles.config]\nServer = 'outline.invalid'\nPassword = 'synthetic'\nPort = 443\n"
            b"[[profiles]]\nprotocol = 'XRAY'\n"
            b"[profiles.config]\noutbounds = []\n"
        )
        start = raw.index(b"[[profiles]]\nprotocol = 'OUTLINE'")
        end = raw.index(b"[[profiles]]\nprotocol = 'XRAY'")

        selected = _select_gui_profile(raw)

        self.assertEqual(selected, b"schema_version = 2\n" + raw[start:end])
        self.assertLessEqual(len(selected), 64 * 1024)

    def test_header_inside_multiline_string_is_preserved_as_profile_content(self) -> None:
        profile = (
            b'[[profiles]]\nprotocol = "OUTLINE"\n'
            b'description = """Synthetic details include a header-like line.\n'
            b'[[profiles]]\nprotocol = "XRAY"\n"""\n'
            b'[profiles.config]\nServer = "outline.invalid"\n'
            b'Password = "synthetic"\nPort = 443\n'
        )
        raw = b"schema_version = 2\n" + profile

        self.assertEqual(
            _select_gui_profile(raw),
            b"schema_version = 2\n" + profile,
        )


class HostedAndroidFailureDiagnosticsTests(unittest.TestCase):
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
                    ("shell", "logcat", "-d", "-v", "raw"),
                ],
            )
            expected = {
                "go_app_logs.jsonl": go_logs,
                "native_logs.jsonl": native_logs,
                "logcat.txt": logcat,
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
                adapter, "_adb", side_effect=lambda *args, **kwargs: next(results)
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


if __name__ == "__main__":
    unittest.main()
