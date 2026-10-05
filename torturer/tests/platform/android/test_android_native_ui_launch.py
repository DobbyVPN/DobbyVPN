from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_runner import local_vm_android


class AndroidNativeUiColdLaunchTests(unittest.TestCase):
    def _run_ui(
        self, start_output: bytes
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
                if label == "android-native-ui-cold-bare-link-start":
                    return subprocess.CompletedProcess(("adb",), 0, start_output, b"")
                if label == "android-native-ui":
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
