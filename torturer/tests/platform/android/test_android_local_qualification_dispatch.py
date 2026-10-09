from __future__ import annotations

from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.results import ConnectionIdentity
from torturer_runner import hosted, local_vm, local_vm_android


SOURCE_SHA = "a" * 40


class AndroidLocalQualificationDispatchTests(unittest.TestCase):
    def test_prepare_persists_source_sha_with_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile", encoding="utf-8")
            args = local_vm.build_parser().parse_args([
                "prepare", "--platform", "android", "--run-dir", str(run_dir),
                "--timeout", "60", "--source-sha", SOURCE_SHA,
                "--source-tree", "b" * 40,
            ])
            candidate = {
                "mode": "local-build",
                "app": str(run_dir / "app.apk"),
                "test_companion": str(run_dir / "test.apk"),
            }
            with (
                mock.patch.object(local_vm_android, "preflight"),
                mock.patch.object(local_vm, "_prepare_candidate", return_value=candidate),
            ):
                self.assertEqual(local_vm.prepare(args), 0)

            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["candidate"]["source_sha"], SOURCE_SHA)
            self.assertEqual(state["android_preflight"], "passed")

    def test_prepare_runs_android_preflight_before_source_checks_and_build(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile", encoding="utf-8")
            args = local_vm.build_parser().parse_args([
                "prepare", "--platform", "android", "--run-dir", str(run_dir),
                "--timeout", "60", "--source-checks",
            ])
            candidate = {
                "mode": "local-build",
                "app": str(run_dir / "app.apk"),
                "test_companion": str(run_dir / "test.apk"),
            }
            events: list[str] = []

            def preflight(**_kwargs):
                events.append("preflight")

            def source_checks(*_args, **_kwargs):
                events.append("source-checks")

            def build(*_args, **_kwargs):
                events.append("build")
                return candidate

            with (
                mock.patch.object(local_vm_android, "preflight", side_effect=preflight),
                mock.patch.object(local_vm, "_run_platform_source_checks", side_effect=source_checks),
                mock.patch.object(local_vm, "_prepare_candidate", side_effect=build),
            ):
                self.assertEqual(local_vm.prepare(args), 0)

            self.assertEqual(events, ["preflight", "source-checks", "build"])
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["android_preflight"], "passed")

    def test_failed_android_preflight_stops_before_checks_and_candidate_build(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile", encoding="utf-8")
            args = local_vm.build_parser().parse_args([
                "prepare", "--platform", "android", "--run-dir", str(run_dir),
                "--timeout", "60", "--source-checks",
            ])
            diagnostic = io.StringIO()
            with (
                mock.patch.object(
                    local_vm_android, "preflight",
                    side_effect=local_vm.LocalVMError("synthetic unavailable device"),
                ) as preflight,
                mock.patch.object(local_vm, "_run_platform_source_checks") as source_checks,
                mock.patch.object(local_vm, "_prepare_candidate") as build,
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.prepare(args), 1)

            preflight.assert_called_once()
            source_checks.assert_not_called()
            build.assert_not_called()
            self.assertIn("synthetic unavailable device", diagnostic.getvalue())
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["status"], "failed")
            self.assertNotIn("source_checks_attempted", state)

    def test_android_preflight_rejects_missing_device_boot_or_system_service(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            environment = {"ADB_SERVER_SOCKET": "tcp:localhost:5037"}
            failures = ["device", "boot", "package", "activity", "surfaceflinger"]
            for failure in failures:
                with self.subTest(failure=failure):
                    calls: list[tuple[list[str], dict[str, object]]] = []

                    def adb_call(_adb, _serial, arguments, **kwargs):
                        calls.append((arguments, kwargs))
                        if arguments == ["get-state"]:
                            if failure == "device":
                                return subprocess.CompletedProcess(
                                    ["adb"], 1, b"offline\n", b"device offline\n",
                                )
                            return subprocess.CompletedProcess(["adb"], 0, b"device\n", b"")
                        if arguments == ["shell", "getprop", "sys.boot_completed"]:
                            value = b"0\n" if failure == "boot" else b"1\n"
                            return subprocess.CompletedProcess(["adb"], 0, value, b"")
                        service = arguments[-1]
                        if failure == service.lower():
                            return subprocess.CompletedProcess(
                                ["adb"], 0,
                                f"Service {service}: not found\n".encode(), b"",
                            )
                        return subprocess.CompletedProcess(
                            ["adb"], 0, f"Service {service}: found\n".encode(), b"",
                        )

                    with (
                        mock.patch.object(
                            local_vm_android, "_adb_and_environment",
                            return_value=("/sdk/platform-tools/adb", "emulator-5554", environment),
                        ),
                        mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                    ):
                        with self.assertRaises(local_vm.LocalVMError) as caught:
                            local_vm_android.preflight(
                                run_dir=root, logs=root / "logs", timeout=30,
                            )

                    if failure == "device":
                        self.assertIn("device is unavailable", str(caught.exception))
                        self.assertEqual([call[0] for call in calls], [["get-state"]])
                    elif failure == "boot":
                        self.assertIn("boot is incomplete", str(caught.exception))
                        self.assertEqual([call[0] for call in calls], [
                            ["get-state"], ["shell", "getprop", "sys.boot_completed"],
                        ])
                    else:
                        service = {
                            "package": "package", "activity": "activity",
                            "surfaceflinger": "SurfaceFlinger",
                        }[failure]
                        self.assertIn(
                            f"Android prerequisite service is unavailable: {service}",
                            str(caught.exception),
                        )
                        self.assertEqual(calls[-1][0], ["shell", "service", "check", {
                            "package": "package", "activity": "activity",
                            "surfaceflinger": "SurfaceFlinger",
                        }[failure]])
                    self.assertTrue(all(call[1]["check"] is False for call in calls))
                    self.assertTrue(all(call[1]["timeout"] <= 5.0 for call in calls))

    def test_android_preflight_checks_live_local_socket_before_adb(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            environment = {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/dobbyvpn-adb.sock"}
            events: list[str] = []
            responses = [
                subprocess.CompletedProcess(["adb"], 0, b"device\n", b""),
                subprocess.CompletedProcess(["adb"], 0, b"1\n", b""),
                *(subprocess.CompletedProcess(
                    ["adb"], 0, f"Service {service}: found\n".encode(), b"",
                ) for service in ("package", "activity", "SurfaceFlinger")),
            ]

            def adb_call(*_args, **_kwargs):
                events.append("adb")
                return responses.pop(0)

            with (
                mock.patch.object(
                    local_vm_android, "_adb_and_environment",
                    return_value=("/sdk/platform-tools/adb", "emulator-5554", environment),
                ),
                mock.patch.object(local_vm_android, "_adb_call", side_effect=adb_call),
                mock.patch.object(local_vm_android.socket, "socket") as socket_factory,
            ):
                connection = socket_factory.return_value.__enter__.return_value
                connection.connect.side_effect = lambda _path: events.append("socket")
                local_vm_android.preflight(run_dir=root, logs=root / "logs", timeout=30)

            self.assertEqual(events, ["socket", "adb", "adb", "adb", "adb", "adb"])
            socket_factory.assert_called_once_with(
                local_vm_android.socket.AF_UNIX, local_vm_android.socket.SOCK_STREAM,
            )
            connection.settimeout.assert_called_once_with(2.0)
            connection.connect.assert_called_once_with("/tmp/dobbyvpn-adb.sock")

    def test_unavailable_local_android_socket_keeps_original_exception_and_skips_adb(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            environment = {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/dobbyvpn-adb.sock"}
            missing = FileNotFoundError("synthetic missing socket")
            with (
                mock.patch.object(
                    local_vm_android, "_adb_and_environment",
                    return_value=("/sdk/platform-tools/adb", "emulator-5554", environment),
                ),
                mock.patch.object(local_vm_android, "_adb_call") as adb_call,
                mock.patch.object(local_vm_android.socket, "socket") as socket_factory,
            ):
                socket_factory.return_value.__enter__.return_value.connect.side_effect = missing
                with self.assertRaisesRegex(
                    local_vm.LocalVMError, "synthetic missing socket",
                ) as caught:
                    local_vm_android.preflight(
                        run_dir=root, logs=root / "logs", timeout=30,
                    )

            self.assertIs(caught.exception.__cause__, missing)
            adb_call.assert_not_called()

    def test_local_android_run_executes_hosted_auto_and_profile_matrix(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            (run_dir / "source" / "torturer").mkdir(parents=True)
            (run_dir / "profile").write_text("synthetic profile", encoding="utf-8")
            (run_dir / "logs").mkdir()
            state = {
                "platform": "android",
                "suite": "mini",
                "status": "candidate-prepared",
                "candidate": {
                    "mode": "local-build",
                    "app": str(run_dir / "app.apk"),
                    "test_companion": str(run_dir / "test.apk"),
                    "source_sha": SOURCE_SHA,
                },
            }
            (run_dir / "platform.json").write_text(json.dumps(state), encoding="utf-8")
            logged_commands: list[list[str]] = []

            def run_logged(command, **_kwargs):
                logged_commands.append(command)
                return subprocess.CompletedProcess(command, 0, b"", b"")

            run_args = local_vm.build_parser().parse_args([
                "run", "--platform", "android", "--run-dir", str(run_dir),
                "--timeout", "120", "--suite", "mini",
            ])
            with (
                mock.patch.object(
                    local_vm, "_start_android",
                    return_value={"adb": "/sdk/platform-tools/adb", "serial": "emulator-5554"},
                ),
                mock.patch(
                    "torturer_runner.local_vm_android.run_ui",
                    return_value=subprocess.CompletedProcess(["instrument"], 0, b"", b""),
                ),
                mock.patch.object(local_vm, "_run_logged", side_effect=run_logged),
            ):
                self.assertEqual(local_vm.run(run_args), 0)

            self.assertEqual(len(logged_commands), 1)
            command = logged_commands[0]
            self.assertEqual(command[1:3], ["-m", "torturer_runner.hosted"])
            self.assertIn(("--source-sha", SOURCE_SHA), list(zip(command, command[1:])))
            self.assertIn(("--profile", str(run_dir / "profile")), list(zip(command, command[1:])))
            self.assertIn(("--adb", "/sdk/platform-tools/adb"), list(zip(command, command[1:])))
            self.assertIn(("--app-apk", str(run_dir / "app.apk")), list(zip(command, command[1:])))
            self.assertIn(
                ("--test-companion-apk", str(run_dir / "test.apk")),
                list(zip(command, command[1:])),
            )

            modes: list[str] = []
            factory_arguments: list[dict[str, object]] = []
            prelude_events: list[str] = []

            def adapter_for_platform(_platform, **kwargs):
                mode = kwargs["android_ui_mode"]
                modes.append(mode)
                factory_arguments.append(kwargs)
                adapter = SimpleNamespace(ui_mode=mode)
                if mode == "gui-auto":
                    adapter.run_unchanged_consent_selection = lambda **_kwargs: (
                        prelude_events.append("consent-grant-selection")
                        or {
                            "passed": True,
                            "source_verified": True,
                            "digest_verified": True,
                            "profile_identity_verified": True,
                            "mode": "PROFILE_INDEX",
                            "index": 1,
                            "protocol": "XRAY",
                            "generation": 2,
                            "generation_advanced": True,
                            "disconnect_clean": True,
                        }
                    )
                return adapter

            def execute_lane(_engine, scenarios, adapter, _provenance, **_kwargs):
                prelude_events.append(f"lane-{adapter.ui_mode}")
                if adapter.ui_mode == "gui-auto":
                    connections = (ConnectionIdentity(index=0, protocol="AUTO"),)
                else:
                    connections = (
                        ConnectionIdentity(index=0, protocol="OUTLINE"),
                        ConnectionIdentity(index=1, protocol="XRAY"),
                        ConnectionIdentity(index=2, protocol="OUTLINE"),
                        ConnectionIdentity(index=3, protocol="XRAY"),
                    )
                results = [
                    {
                        "connection": connection.to_dict(),
                        "scenario": {"id": scenario.id},
                        "outcome": "passed",
                    }
                    for connection in connections
                    for scenario in scenarios
                ]
                return connections, results

            with (
                mock.patch.object(hosted, "adapter_for_platform", side_effect=adapter_for_platform),
                mock.patch.object(hosted, "_execute_lane", side_effect=execute_lane),
            ):
                self.assertEqual(hosted.main(command[3:]), 0)

            output = json.loads((run_dir / "logs" / "functional.json").read_text(encoding="utf-8"))
            self.assertEqual(modes, ["gui-auto", "protocol-matrix"])
            self.assertEqual([item["source_sha"] for item in factory_arguments], [SOURCE_SHA, SOURCE_SHA])
            self.assertEqual([item["profile"] for item in factory_arguments], [run_dir / "profile"] * 2)
            self.assertEqual(factory_arguments[0]["app_apk"], run_dir / "app.apk")
            self.assertEqual(
                factory_arguments[0]["test_companion_apk"], run_dir / "test.apk"
            )
            self.assertEqual(
                prelude_events,
                ["consent-grant-selection", "lane-gui-auto", "lane-protocol-matrix"],
            )
            self.assertTrue(output["rendered_ui"]["passed"])
            self.assertTrue(output["rendered_ui"]["consent_grant_selection"]["passed"])
            self.assertEqual(output["rendered_ui"]["connection"]["protocol"], "AUTO")
            self.assertTrue(output["coverage"]["complete"])
            self.assertTrue(output["coverage"]["rendered_ui_consent_grant_selection_passed"])
            self.assertEqual(output["coverage"]["connection_count"], 4)

    def test_focused_android_diagnostic_keeps_local_runner_and_source_identity(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            run_dir = Path(name)
            descriptor = {
                "mode": "local-build",
                "app": "/candidate/app.apk",
                "test_companion": "/candidate/test.apk",
                "source_sha": SOURCE_SHA,
                "runtime": {"adb": "/sdk/platform-tools/adb"},
            }
            command = local_vm._functional_command(
                run_dir, descriptor, "android", 60, ["functional.configure"], "mini",
            )

        self.assertEqual(command[1:3], ["-m", "torturer_runner.functional"])
        self.assertIn(("--source-sha", SOURCE_SHA), list(zip(command, command[1:])))


if __name__ == "__main__":
    unittest.main()
