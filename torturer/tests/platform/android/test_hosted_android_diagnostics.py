from __future__ import annotations

from contextlib import redirect_stderr
from hashlib import sha256
from io import BytesIO
import json
from pathlib import Path
import tempfile
import time
import tomllib
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.engine import ScenarioExecutionError
from torturer_runner.adapters.android import AndroidAdapter, _select_gui_profile
from torturer_runner.adapters.cli import CommandResult
from disposable_vpn_server.outline import OutlineWSSProfile


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class AndroidGuiProfileSelectionTests(unittest.TestCase):
    def test_selects_one_profile_and_preserves_shared_exclusions(self) -> None:
        raw = (
            b"[ExcludeIPs]\nIPs = ['192.0.2.0/24']\n\n"
            b"[[TrustTunnel]]\nDescription = 'not selected'\n"
            b"[TrustTunnel.endpoint]\nhostname = 'trust.invalid'\n\n"
            b"[[Outline]]\nDescription = 'synthetic Outline'\n"
            b"Server = 'outline.invalid'\nPassword = 'synthetic'\nPort = 443\n\n"
            b"[[Xray]]\nDescription = 'synthetic Xray'\noutbounds = []\n"
        )

        selected = _select_gui_profile(raw)
        parsed = tomllib.loads(selected.decode("utf-8"))

        self.assertEqual(set(parsed), {"Outline", "ExcludeIPs"})
        self.assertEqual(parsed["Outline"][0]["Description"], "synthetic Outline")
        self.assertEqual(parsed["Outline"][0]["Server"], "outline.invalid")
        self.assertEqual(parsed["ExcludeIPs"], {"IPs": ["192.0.2.0/24"]})
        self.assertLessEqual(len(selected), 64 * 1024)

    def test_selects_xray_and_keeps_nested_configuration(self) -> None:
        raw = (
            b"[[Xray]]\n"
            b"Description = 'synthetic Xray'\n"
            b"log = { loglevel = 'info', output = { access = 'none' } }\n"
            b"outbounds = [{ tag = 'proxy', protocol = 'vless', settings = { vnext = ["
            b"{ address = 'xray.invalid', port = 443, users = ["
            b"{ id = 'synthetic-id', flow = 'vision', encryption = 'none' }] }] } }]\n"
            b"[Xray.extra]\nlabel = 'kept'\n\n"
            b"[ExcludeIPs]\nIPs = ['198.51.100.8/32']\n"
        )

        selected = _select_gui_profile(raw)
        parsed = tomllib.loads(selected.decode("utf-8"))

        self.assertEqual(set(parsed), {"Xray", "ExcludeIPs"})
        self.assertEqual(parsed["Xray"][0]["Description"], "synthetic Xray")
        self.assertEqual(
            parsed["Xray"][0]["outbounds"][0]["settings"]["vnext"][0]["users"][0]["id"],
            "synthetic-id",
        )
        self.assertEqual(parsed["Xray"][0]["extra"], {"label": "kept"})
        self.assertEqual(parsed["ExcludeIPs"], {"IPs": ["198.51.100.8/32"]})

    def test_generated_render_profile_uses_original_public_toml(self) -> None:
        profile = OutlineWSSProfile(web_path="/synthetic-path", secret="synthetic-secret")

        selected = _select_gui_profile(profile.client_toml("https://vpn.invalid").encode())
        parsed = tomllib.loads(selected.decode("utf-8"))

        self.assertEqual(set(parsed), {"Outline", "ExcludeIPs"})
        self.assertEqual(
            parsed["Outline"][0]["Description"],
            "DobbyVPN Torturer disposable Render service",
        )
        self.assertEqual(parsed["Outline"][0]["WebSocketPath"], "/synthetic-path")
        self.assertEqual(parsed["ExcludeIPs"], {"IPs": []})

    def test_header_inside_multiline_string_is_preserved_as_profile_content(self) -> None:
        profile = (
            b'[[Outline]]\nDescription = """Synthetic details include a header-like line.\n'
            b'[ExcludeIPs]\n'
            b'[[Xray]]\nDescription = "not an actual profile"\n"""\n'
            b'Server = "outline.invalid"\nPassword = "synthetic"\nPort = 443\n'
            b'\n[ExcludeIPs]\nIPs = ["203.0.113.0/24"]\n'
        )
        raw = profile

        self.assertEqual(
            _select_gui_profile(raw),
            profile,
        )

    def test_large_shared_exclusions_before_or_after_single_profile_are_preserved(self) -> None:
        ips = [f"198.18.{index >> 8}.{index & 255}/32" for index in range(13_954)]
        profile = (
            b'[[Outline]]\nDescription = "synthetic Outline"\n'
            b'Server = "outline.invalid"\nPassword = "synthetic"\nPort = 443\n'
        )
        exclusions = b"[ExcludeIPs]\nIPs = " + json.dumps(ips).encode("utf-8") + b"\n"
        cases = {
            "before-profile": exclusions + b"\n" + profile,
            "after-profile": profile + b"\n" + exclusions,
        }

        for placement, raw in cases.items():
            with self.subTest(placement=placement):
                selected = _select_gui_profile(raw)
                parsed = tomllib.loads(selected.decode("utf-8"))

                self.assertEqual(parsed["Outline"][0]["Description"], "synthetic Outline")
                self.assertEqual(parsed["ExcludeIPs"]["IPs"], ips)
                self.assertGreater(len(selected), 64 * 1024)
                self.assertLessEqual(len(selected), 1024 * 1024)

    def test_oversized_protocol_profile_is_still_rejected(self) -> None:
        raw = (
            b'[[Outline]]\nDescription = "'
            + b"x" * (64 * 1024)
            + b'"\nServer = "outline.invalid"\nPassword = "synthetic"\nPort = 443\n'
        )

        with self.assertRaisesRegex(ScenarioExecutionError, "ANDROID_GUI_PROFILE_UNAVAILABLE"):
            _select_gui_profile(raw)


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
