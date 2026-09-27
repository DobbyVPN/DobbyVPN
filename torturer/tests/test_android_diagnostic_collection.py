from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

from torturer_checks import local_vm_android
from torturer_contract.functional.scenarios import select_scenarios, suite_set


class AndroidDiagnosticCollectionTests(unittest.TestCase):
    def test_collects_native_file_and_complete_logcat_bytes(self) -> None:
        native = b'{"event":"native-sentinel"}\n'
        logcat = b"fallback sentinel\x00\xff\n"
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            results = iter(
                (
                    subprocess.CompletedProcess(("adb",), 0, native, b""),
                    subprocess.CompletedProcess(("adb",), 0, logcat, b""),
                )
            )
            with mock.patch.object(
                local_vm_android, "_adb_call", side_effect=lambda *args, **kwargs: next(results)
            ):
                errors = local_vm_android._collect_android_diagnostics(
                    "adb",
                    "emulator-5554",
                    run_dir=root,
                    logs=root,
                    timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )

            self.assertEqual(errors, [])
            self.assertEqual((root / "android-native-logs.jsonl").read_bytes(), native)
            self.assertEqual((root / "android-logcat.txt").read_bytes(), logcat)

    def test_collection_failures_fail_without_replacing_instrumentation_result(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            results = iter(
                (
                    subprocess.CompletedProcess(("adb",), 17, b"native out", b"native err"),
                    subprocess.CompletedProcess(("adb",), 19, b"logcat out", b"logcat err"),
                )
            )
            with mock.patch.object(
                local_vm_android, "_adb_call", side_effect=lambda *args, **kwargs: next(results)
            ):
                errors = local_vm_android._collect_android_diagnostics(
                    "adb",
                    "emulator-5554",
                    run_dir=root,
                    logs=root,
                    timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )

        primary = subprocess.CompletedProcess(
            ("am", "instrument"), 1, b"original assertion bytes\x00\xff", b"original stderr\n"
        )
        result = local_vm_android._with_collection_errors(
            primary,
            instrumentation_succeeded=False,
            collection_errors=errors,
        )
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, primary.stdout)
        self.assertTrue(result.stderr.startswith(primary.stderr))
        self.assertIn(b"ANDROID_NATIVE_LOG_COLLECTION_FAILED", result.stderr)
        self.assertIn(b"ANDROID_LOGCAT_COLLECTION_FAILED", result.stderr)
        self.assertIn(b"adb exited 17", result.stderr)
        self.assertIn(b"adb exited 19", result.stderr)
        self.assertIn(b"native err", result.stderr)
        self.assertIn(b"logcat err", result.stderr)

    def test_successful_instrumentation_still_fails_on_collection_error(self) -> None:
        primary = subprocess.CompletedProcess(
            ("am", "instrument"), 0, b"OK (1 test)\nINSTRUMENTATION_CODE: -1\n", b""
        )
        result = local_vm_android._with_collection_errors(
            primary,
            instrumentation_succeeded=True,
            collection_errors=["ANDROID_LOGCAT_COLLECTION_FAILED: unavailable"],
        )
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, primary.stdout)
        self.assertIn(b"ANDROID_LOGCAT_COLLECTION_FAILED", result.stderr)

    def test_instrumentation_timeout_collects_diagnostics_and_keeps_timeout_primary(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            logs = root / "logs"
            logs.mkdir()
            primary = local_vm_android._error("android-native-ui: command timed out")
            primary.add_note("android-native-ui_stdout:\npartial instrumentation output")
            native = b'{"event":"timeout-native-sentinel"}\n'
            calls: list[str] = []

            def adb_call(_adb, _serial, _arguments, *, label, **_kwargs):
                calls.append(label)
                if label == "android-native-ui":
                    raise primary
                if label == "android-complete-throwable-self-test":
                    return subprocess.CompletedProcess(("adb",), 0, b"", b"")
                if label == "android-native-ui-app-start":
                    return subprocess.CompletedProcess(("adb",), 0, b"Complete\nStatus: ok\n", b"")
                if label == "android-native-diagnostics":
                    return subprocess.CompletedProcess(("adb",), 0, native, b"")
                if label == "android-logcat-diagnostics":
                    return subprocess.CompletedProcess(("adb",), 19, b"logcat out", b"logcat err")
                return subprocess.CompletedProcess(("adb",), 0, b"", b"")

            with (
                mock.patch.dict(os.environ, {"ADB_SERVER_SOCKET": "tcp:localhost:5037"}),
                mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                mock.patch.object(
                    local_vm_android,
                    "parse_instrumentation_result",
                    return_value=SimpleNamespace(succeeded=True),
                ),
                mock.patch.object(local_vm_android, "validate_complete_throwable_report"),
            ):
                with self.assertRaises(type(primary)) as caught:
                    local_vm_android.run_ui(
                        root,
                        {"adb": "adb", "serial": "emulator-5554"},
                        logs,
                        timeout=30,
                    )

            self.assertIs(caught.exception, primary)
            self.assertIn(
                "android-native-ui_stdout:\npartial instrumentation output",
                caught.exception.__notes__,
            )
            self.assertIn("ANDROID_LOGCAT_COLLECTION_FAILED", caught.exception.__notes__[-1])
            self.assertEqual(
                calls[-3:],
                [
                    "android-native-ui",
                    "android-native-diagnostics",
                    "android-logcat-diagnostics",
                ],
            )
            self.assertEqual((logs / "android-native-logs.jsonl").read_bytes(), native)


class DisabledFunctionalScenarioTests(unittest.TestCase):
    def test_network_transition_is_not_in_a_qualification_suite(self) -> None:
        for suite in ("mini", "full"):
            self.assertNotIn(
                "functional.network-transition",
                {scenario.id for scenario in suite_set(suite)},
            )

    def test_network_transition_manual_selection_fails_explicitly(self) -> None:
        with self.assertRaisesRegex(
            ValueError, "disabled until further notice.*functional.network-transition"
        ):
            select_scenarios(scenario_ids=["functional.network-transition"])


if __name__ == "__main__":
    unittest.main()
