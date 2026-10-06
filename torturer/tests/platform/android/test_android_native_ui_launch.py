from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import time
from types import SimpleNamespace
from unittest import mock

from torturer_runner import local_vm_android
from torturer_runner.adapters.android import AndroidAdapter, _MAIN_ACTIVITY, _PACKAGE_NAME


class AndroidNativeUiColdLaunchTests(unittest.TestCase):
    def test_first_rendered_configure_records_one_process_cold_import_baseline(self) -> None:
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
            adapter._subscription_fixture = SimpleNamespace(
                url="https://127.0.0.1:54432/subscription",
                control_url="https://127.0.0.1:54432/control",
                control_key="fixture-key",
                control_stats=lambda: {"subscription_gets": 3},
            )
            configure = SimpleNamespace(
                id="functional-configure", operation="configure", timeout_seconds=30
            )

            first, _, _ = adapter._write_command(
                SimpleNamespace(), steps=(configure,)
            )
            second, _, _ = adapter._write_command(
                SimpleNamespace(), steps=(configure,)
            )

            first_command = json.loads(first.read_text(encoding="utf-8"))
            second_command = json.loads(second.read_text(encoding="utf-8"))
            self.assertTrue(first_command["process_cold_import"])
            self.assertEqual(first_command["process_cold_import_request_count"], 3)
            self.assertNotIn("process_cold_import", second_command)

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
    ) -> tuple[
        subprocess.CompletedProcess[bytes] | None,
        Exception | None,
        list[tuple[str, list[str]]],
    ]:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            logs = root / "logs"
            logs.mkdir()
            calls: list[tuple[str, list[str]]] = []

            def adb_call(_adb, _serial, arguments, *, label, **_kwargs):
                calls.append((label, arguments))
                if label in {
                    "android-native-ui-cold-bare-link-start",
                    "android-clear-boundary-process-restart-launch",
                }:
                    return subprocess.CompletedProcess(("adb",), 0, start_output, b"")
                if label in {
                    "android-native-ui",
                    "android-clear-boundary-process-restart-test",
                }:
                    return subprocess.CompletedProcess(("adb",), 0, b"instrumentation output\n", b"")
                return subprocess.CompletedProcess(("adb",), 0, b"", b"")

            with (
                mock.patch.dict(os.environ, {"ADB_SERVER_SOCKET": "tcp:localhost:5037"}),
                mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                mock.patch.object(
                    local_vm_android,
                    "parse_instrumentation_result",
                    return_value=SimpleNamespace(succeeded=True),
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
                    return None, error, calls
                return result, None, calls

    def test_force_stop_then_implicit_bare_link_start_precedes_instrumentation(self) -> None:
        result, error, calls = self._run_ui(
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
        result, error, calls = self._run_ui(
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

    def test_missing_foreground_marker_stops_before_instrumentation(self) -> None:
        for output in (b"Status: ok\n", b"Complete\n"):
            with self.subTest(output=output):
                result, error, calls = self._run_ui(output)
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
