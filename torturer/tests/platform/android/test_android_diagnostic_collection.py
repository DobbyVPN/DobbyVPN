from __future__ import annotations

import os
from hashlib import sha256
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

from torturer_runner import local_vm_android
from torturer_contract.scenarios import select_scenarios


class AndroidDiagnosticCollectionTests(unittest.TestCase):
    def test_final_history_collected_before_uninstall_even_when_collection_fails(self) -> None:
        for collection_error in (None, OSError("final log unavailable")):
            with self.subTest(collection_error=collection_error), tempfile.TemporaryDirectory() as name:
                root = Path(name)
                actions = []

                def adb_call(_adb, _serial, arguments, **_kwargs):
                    actions.append(arguments)
                    output = b"device\n" if arguments == ["get-state"] else b""
                    if arguments[:4] == ["shell", "pm", "list", "packages"]:
                        output = f"package:{arguments[-1]}\n".encode()
                    return subprocess.CompletedProcess(("adb",), 0, output, b"")

                def collect(_adb, _serial, *, logs, **_kwargs):
                    self.assertEqual(actions[-1], ["shell", "am", "force-stop", local_vm_android.APP_PACKAGE])
                    self.assertNotIn(["uninstall", local_vm_android.APP_PACKAGE], actions)
                    self.assertEqual(logs, root / "logs" / "android-final")
                    if collection_error:
                        raise collection_error
                    (logs / "android-go-app-logs.jsonl").write_bytes(b"final VPN history\n")
                    return []

                runtime = {"adb": "adb", "serial": "emulator-5554", "installed_packages": [
                    local_vm_android.APP_PACKAGE, local_vm_android.COMPANION_PACKAGE,
                ]}
                with (
                    mock.patch.dict(os.environ, {"ADB_SERVER_SOCKET": "tcp:localhost:5037"}),
                    mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                    mock.patch.object(local_vm_android, "_collect_android_diagnostics", side_effect=collect) as collector,
                ):
                    if collection_error:
                        with self.assertRaisesRegex(Exception, "final log unavailable"):
                            local_vm_android.cleanup(root, runtime, root / "logs", 30)
                    else:
                        local_vm_android.cleanup(root, runtime, root / "logs", 30)
                        self.assertEqual((root / "logs/android-final/android-go-app-logs.jsonl").read_bytes(), b"final VPN history\n")
                collector.assert_called_once()
                for package in runtime["installed_packages"]:
                    self.assertIn(["uninstall", package], actions)


class AndroidScreenshotCollectionTests(unittest.TestCase):
    def test_clear_process_restart_case_requires_one_success_frame(self) -> None:
        label = "logs-clear-before-process-restart"
        remote = f"{local_vm_android._SCREENSHOT_ROOT}{label}.png"
        payload = b"clear-before-process-restart screenshot"
        marker = (
            f"DOBBY_UI_SCREENSHOT label={label} path={remote} bytes={len(payload)} "
            f"sha256={sha256(payload).hexdigest()} width=720 height=1280\n"
        ).encode()

        with tempfile.TemporaryDirectory() as name:
            work = Path(name)

            def adb_call(_adb, _serial, arguments, **_kwargs):
                Path(arguments[2]).write_bytes(payload)
                return subprocess.CompletedProcess(("adb",), 0, b"pulled", b"")

            with mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call):
                local_vm_android._collect_rendered_screenshots(
                    "adb", "emulator-5554", marker, succeeded=True,
                    native_cases=["logs-clear-process-restart"],
                    run_dir=work, logs=work / "logs", timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )
                with self.assertRaisesRegex(Exception, "must be logs-clear-before-process-restart"):
                    local_vm_android._collect_rendered_screenshots(
                        "adb", "emulator-5554", marker.replace(
                            label.encode(), b"small-screen-log-viewport"
                        ), succeeded=True,
                        native_cases=["logs-clear-process-restart"],
                        run_dir=work, logs=work / "logs", timeout=5,
                        environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                    )
                with self.assertRaisesRegex(Exception, "must be logs-clear-before-process-restart"):
                    local_vm_android._collect_rendered_screenshots(
                        "adb", "emulator-5554", marker + marker, succeeded=True,
                        native_cases=["logs-clear-process-restart"],
                        run_dir=work, logs=work / "logs", timeout=5,
                        environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                    )

    def test_failure_checkpoints_and_installed_icon_are_retained(self) -> None:
        labels = (
            "startup", "about-metadata", "landscape-large-font",
            "small-screen-scroll-failure", "failure",
        )
        root = Path("/data/user/0/com.dobby.vpn/files/dobbyvpn-rendered-screenshots/")
        payloads: dict[str, bytes] = {}
        records = []
        for label in labels:
            path = str(root / f"{label}.png")
            payload = f"png-payload-{label}".encode()
            payloads[path] = payload
            records.append(
                b"INSTRUMENTATION_STATUS: stream=DOBBY_UI_SCREENSHOT "
                + f"label={label} path={path} bytes={len(payload)} "
                  f"sha256={sha256(payload).hexdigest()} width=720 height=1280".encode()
            )

        icon_path = str(root / "installed-launcher-artwork.png")
        icon = b"installed launcher PNG payload"
        payloads[icon_path] = icon
        records.append(
            b"INSTRUMENTATION_STATUS: stream=DOBBY_INSTALLED_LAUNCHER_ARTWORK "
            + f"path={icon_path} bytes={len(icon)} sha256={sha256(icon).hexdigest()} "
              "width=512 height=512 sampled_colors=135".encode()
        )
        stdout = b"\n".join(records) + b"\n"

        with tempfile.TemporaryDirectory() as name:
            work = Path(name)
            pulled: list[str] = []

            def adb_call(_adb, _serial, arguments, **_kwargs):
                self.assertEqual(arguments[0], "pull")
                remote, destination = arguments[1:]
                Path(destination).write_bytes(payloads[remote])
                pulled.append(Path(remote).name)
                return subprocess.CompletedProcess(("adb",), 0, b"pulled", b"")

            with mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call):
                local_vm_android._collect_rendered_screenshots(
                    "adb", "emulator-5554", stdout, succeeded=False,
                    run_dir=work, logs=work / "logs", timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )
                local_vm_android._collect_launcher_artwork(
                    "adb", "emulator-5554", stdout, succeeded=False,
                    run_dir=work, logs=work / "logs", timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )

            expected = [*(f"{label}.png" for label in labels), "installed-launcher-artwork.png"]
            self.assertEqual(pulled, expected)
            for filename in expected:
                self.assertEqual(
                    (work / "logs/screenshots/android" / filename).read_bytes(),
                    payloads[str(root / filename)],
                )

    def test_success_requires_the_full_local_screenshot_sequence(self) -> None:
        labels = (
            "startup", "about-metadata", "landscape-large-font", "failure-state", "reopened",
        )
        with tempfile.TemporaryDirectory() as name:
            work = Path(name)
            payload = b"complete test screenshot"
            records = []
            payloads = {}
            for label in labels:
                remote = f"{local_vm_android._SCREENSHOT_ROOT}{label}.png"
                payloads[remote] = payload + label.encode()
                content = payloads[remote]
                records.append(
                    b"DOBBY_UI_SCREENSHOT "
                    + f"label={label} path={remote} bytes={len(content)} "
                      f"sha256={sha256(content).hexdigest()} width=720 height=1280".encode()
                )
            stdout = b"\n".join(records) + b"\n"

            def adb_call(_adb, _serial, arguments, **_kwargs):
                Path(arguments[2]).write_bytes(payloads[arguments[1]])
                return subprocess.CompletedProcess(("adb",), 0, b"pulled", b"")

            with mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call):
                local_vm_android._collect_rendered_screenshots(
                    "adb", "emulator-5554", stdout, succeeded=True,
                    run_dir=work, logs=work / "logs", timeout=5,
                    environment={"ADB_SERVER_SOCKET": "tcp:localhost:5037"},
                )

    def test_collects_complete_app_logs_and_unfiltered_logcat_bytes(self) -> None:
        native = b'{"event":"native-sentinel"}\n'
        go = b'{"source":"go","message":"xray sentinel"}\x00\xff\n'
        logcat = b"all tags sentinel\x00\xff\n"
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            commands: list[list[str]] = []
            results = iter(
                (
                    subprocess.CompletedProcess(("adb",), 0, native, b""),
                    subprocess.CompletedProcess(("adb",), 0, go, b""),
                    subprocess.CompletedProcess(("adb",), 0, logcat, b""),
                )
            )

            def adb_call(_adb, _serial, arguments, **kwargs):
                commands.append(arguments)
                if arguments[:4] == ["shell", "-T", "sh", "-c"]:
                    return subprocess.CompletedProcess(("adb",), 0, b"retained \xff\x00\n", b"")
                return next(results)

            with mock.patch.object(
                local_vm_android, "_adb_call", side_effect=adb_call
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
            self.assertEqual((root / "android-go-app-logs.jsonl").read_bytes(), go)
            self.assertEqual((root / "android-logcat.txt").read_bytes(), logcat)
            self.assertEqual(commands[-1], ["shell", "logcat", "-d", "-v", "raw"])
            for _, _, command, filename, _ in local_vm_android.retained_log_sources():
                self.assertIn(command, commands)
                self.assertEqual((root / filename).read_bytes(), b"retained \xff\x00\n")

    def test_collection_failures_fail_without_replacing_instrumentation_result(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            results = iter(
                (
                    subprocess.CompletedProcess(("adb",), 17, b"native out", b"native err"),
                    subprocess.CompletedProcess(("adb",), 18, b"go out", b"go err"),
                    subprocess.CompletedProcess(("adb",), 19, b"logcat out", b"logcat err"),
                )
            )
            with mock.patch.object(
                local_vm_android, "_adb_call", side_effect=lambda _adb, _serial, arguments, **kwargs: (
                    subprocess.CompletedProcess(("adb",), 44, b"", b"")
                    if arguments[:4] == ["shell", "-T", "sh", "-c"] else next(results)
                )
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
        self.assertIn(b"ANDROID_GO_APP_LOG_COLLECTION_FAILED", result.stderr)
        self.assertIn(b"ANDROID_LOGCAT_COLLECTION_FAILED", result.stderr)
        self.assertIn(b"adb exited 17", result.stderr)
        self.assertIn(b"adb exited 18", result.stderr)
        self.assertIn(b"adb exited 19", result.stderr)
        self.assertIn(b"native err", result.stderr)
        self.assertIn(b"go err", result.stderr)
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
            go = b'{"event":"timeout-go-sentinel"}\n'
            calls: list[str] = []
            def adb_call(_adb, _serial, arguments, *, label, **_kwargs):
                calls.append(label)
                if arguments[:4] == ["shell", "-T", "sh", "-c"]:
                    return subprocess.CompletedProcess(("adb",), 44, b"", b"")
                if label == "android-native-ui":
                    raise primary
                if label == "android-native-ui-cold-bare-link-start":
                    return subprocess.CompletedProcess(("adb",), 0, b"Complete\nStatus: ok\n", b"")
                if label == "android-native-diagnostics":
                    return subprocess.CompletedProcess(("adb",), 0, native, b"")
                if label == "android-go-app-diagnostics":
                    return subprocess.CompletedProcess(("adb",), 0, go, b"")
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
                calls[-10:],
                [
                    "android-native-ui",
                    "android-native-diagnostics",
                    "android-go-app-diagnostics",
                    *(label for _, label, _, _, _ in local_vm_android.retained_log_sources()),
                    "android-logcat-diagnostics",
                ],
            )
            self.assertEqual((logs / "android-native-logs.jsonl").read_bytes(), native)
            self.assertEqual((logs / "android-go-app-logs.jsonl").read_bytes(), go)
            for _, _, _, filename, _ in local_vm_android.retained_log_sources():
                self.assertFalse((logs / filename).exists())


class DisabledFunctionalScenarioTests(unittest.TestCase):
    def test_network_transition_is_not_in_a_qualification_suite(self) -> None:
        for suite in ("mini", "full"):
            self.assertNotIn(
                "functional.network-transition",
                {scenario.id for scenario in select_scenarios(suite=suite)},
            )

    def test_network_transition_manual_selection_fails_explicitly(self) -> None:
        with self.assertRaisesRegex(
            ValueError, "disabled until further notice.*functional.network-transition"
        ):
            select_scenarios(scenario_ids=["functional.network-transition"])


if __name__ == "__main__":
    unittest.main()
