from __future__ import annotations

from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from torturer_runner import diagnostics, local_vm, local_vm_windows


class LocalVMLifecycleTests(unittest.TestCase):
    def test_windows_second_start_persists_cleanup_owner_before_discovery_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            logs = run_dir / "logs"
            binary = run_dir / "DobbyVPN.exe"
            binary.write_bytes(b"synthetic candidate")
            pid_file = run_dir / "service.pid"
            identity_file = run_dir / "service.identity"
            pid = 45678
            identity = f"{pid}|638999999999999999"
            pid_bytes = f"{pid}\n".encode("ascii")
            identity_bytes = f"{identity}\n".encode("ascii")
            pid_file.write_bytes(pid_bytes)
            identity_file.write_bytes(identity_bytes)
            user = r"EXAMPLE\InteractiveUser"
            local_vm._write_json(run_dir / "platform.json", {
                "platform": "windows",
                "status": "running",
                "runtime": {
                    "pid": pid,
                    "identity": identity,
                    "binary": str(binary),
                    "pid_file": str(pid_file),
                    "identity_file": str(identity_file),
                    "environment": {"DOBBYVPN_CONTROL_PIPE_USER": user},
                },
            })
            discovery_error = local_vm.LocalVMError("original network discovery failure")

            with (
                mock.patch.dict(local_vm_windows.os.environ, {
                    "DOBBYVPN_CONTROL_PIPE_USER": user,
                }),
                mock.patch.object(
                    local_vm_windows, "_discover_network_interface", side_effect=discovery_error,
                ),
            ):
                with self.assertRaises(local_vm.LocalVMError) as raised:
                    local_vm_windows.start(run_dir, {"service": str(binary)}, logs, 30)

            self.assertIs(raised.exception, discovery_error)
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            runtime = state["runtime"]
            self.assertIsNone(runtime["network_interface"])
            self.assertEqual(runtime["environment"]["DOBBYVPN_CONTROL_PIPE_USER"], user)
            self.assertEqual(runtime["environment"]["PROGRAMDATA"], str(run_dir / "ProgramData"))
            self.assertEqual(pid_file.read_bytes(), pid_bytes)
            self.assertEqual(identity_file.read_bytes(), identity_bytes)

            with mock.patch.object(local_vm_windows, "_powershell") as powershell:
                local_vm_windows.cleanup(run_dir, runtime, logs, 30)

            self.assertEqual(
                [call.kwargs["label"] for call in powershell.call_args_list],
                ["cleanup-service", "cleanup-routing"],
            )
            stop_call = powershell.call_args_list[0]
            self.assertEqual(stop_call.args[0], local_vm_windows._STOP_SCRIPT)
            cleanup_environment = stop_call.kwargs["environment"]
            self.assertEqual(cleanup_environment["DOBBYVPN_SERVICE_INTERACTIVE_OWNER"], user)
            self.assertEqual(cleanup_environment["DOBBYVPN_SERVICE_PID"], str(pid))
            self.assertEqual(cleanup_environment["DOBBYVPN_SERVICE_IDENTITY"], identity)
            self.assertEqual(cleanup_environment["DOBBYVPN_SERVICE_BINARY"], str(binary.resolve()))

    def test_windows_temp_failure_stops_before_source_checks_and_build(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile")
            args = local_vm.build_parser().parse_args([
                "prepare", "--platform", "windows", "--run-dir", str(run_dir),
                "--timeout", "90", "--source-checks",
            ])
            diagnostic = io.StringIO()
            with (
                mock.patch.object(local_vm, "_run_logged", side_effect=local_vm.LocalVMError(
                    "original temp permission failure")) as preflight,
                mock.patch.object(local_vm, "_run_platform_source_checks") as checks,
                mock.patch.object(local_vm, "_prepare_candidate") as build,
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.prepare(args), 1)
            checks.assert_not_called()
            build.assert_not_called()
            self.assertEqual(preflight.call_args.args[0][-1], "preflight-windows-temp")
            self.assertEqual(preflight.call_args.kwargs["timeout"], 30.0)
            self.assertIn("original temp permission failure", diagnostic.getvalue())
            state = json.loads((run_dir / "platform.json").read_text())
            self.assertEqual(state["status"], "failed")
            self.assertNotIn("source_checks_attempted", state)

    def test_windows_temp_preflight_precedes_source_checks_and_build(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile")
            args = local_vm.build_parser().parse_args([
                "prepare", "--platform", "windows", "--run-dir", str(run_dir),
                "--timeout", "90", "--source-checks",
            ])
            events = []
            with (
                mock.patch.object(local_vm, "_run_logged", side_effect=lambda *_args, **_kw:
                                  events.append("preflight")),
                mock.patch.object(local_vm, "_run_platform_source_checks", side_effect=lambda *_args:
                                  events.append("source-checks")),
                mock.patch.object(local_vm, "_prepare_candidate", side_effect=lambda *_args, **_kw:
                                  (events.append("build") or {"mode": "local-build"})),
            ):
                self.assertEqual(local_vm.prepare(args), 0)
            self.assertEqual(events, ["preflight", "source-checks", "build"])
            state = json.loads((run_dir / "platform.json").read_text())
            self.assertEqual(state["windows_temp_preflight"], "passed")

    def test_recovery_seams_cannot_replace_qualification_packages(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for mode in ("installed-package", "release-package"):
                with self.subTest(mode=mode), mock.patch.object(local_vm, "_run_logged") as build:
                    with self.assertRaisesRegex(local_vm.LocalVMError, "cannot replace"):
                        local_vm._prepare_recovery_stop_service(
                            root, "windows", {"mode": mode}, root / "logs", 30, "amd64", True,
                        )
                    build.assert_not_called()

    def test_tagged_candidate_is_restricted_to_explicit_recovery_case(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "source").mkdir()
            (root / "profile").write_text("synthetic profile")
            for tagged, selection in ((True, None), (False, "auto-recovery-stop")):
                with self.subTest(tagged=tagged, selection=selection):
                    local_vm._write_json(root / "platform.json", {
                        "status": "candidate-prepared", "platform": "windows", "suite": "full",
                        "candidate": {"mode": "local-build", "test_seams": tagged},
                    })
                    command = ["run", "--platform", "windows", "--suite", "full",
                               "--run-dir", str(root), "--timeout", "30"]
                    if selection:
                        command.extend(("--native-case", selection))
                    with mock.patch.object(local_vm, "_start_windows") as launch:
                        with self.assertRaisesRegex(local_vm.LocalVMError, "restricted"):
                            local_vm.run(local_vm.build_parser().parse_args(command))
                        launch.assert_not_called()

    def test_exact_release_preparation_validates_staged_source_before_install(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            source = run_dir / "source"
            source.mkdir()
            manifest = run_dir / "release.json"
            logs = run_dir / "logs"
            with (
                mock.patch.object(local_vm, "_validate_release_inputs", return_value=({}, {})) as validate,
                mock.patch.object(local_vm, "_install_macos_release", return_value=({"service": "candidate"}, {})) as install,
            ):
                result = local_vm._prepare_release_candidate(run_dir, "macos", manifest, logs, 30)
            validate.assert_called_once_with(run_dir, source, manifest, logs=logs)
            install.assert_called_once()
            self.assertEqual(result, {"service": "candidate", "mode": "release-package"})

    def test_installed_backend_collection_retains_generations_and_reports_failures(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            logs = root / "logs"
            payloads = {
                "backend.jsonl": b"current\xff\x00\r\n",
                "backend.jsonl.previous": b"previous\xfe\n",
                "backend.jsonl.stderr": b"native panic\n",
                "backend.jsonl.stderr.previous": b"previous stderr\n",
                "backend.jsonl.stdout": b"bootstrap stdout\n",
            }
            for name, payload in payloads.items():
                (source / name).write_bytes(payload)
            errors: list[str] = []
            diagnostics.collect_installed_backend_logs(source, logs, errors)
            self.assertEqual(errors, [])
            self.assertEqual(
                {path.name: path.read_bytes() for path in (logs / "installed-backend").iterdir()},
                payloads,
            )
            copy = diagnostics.shutil.copyfileobj

            def fail_stderr(source_file, destination_file, **kwargs):
                if Path(source_file.name).name == "backend.jsonl.stderr":
                    raise OSError("original copy failure")
                return copy(source_file, destination_file, **kwargs)

            copy_errors: list[str] = []
            diagnostic = io.StringIO()
            with (
                mock.patch.object(diagnostics.shutil, "copyfileobj", side_effect=fail_stderr),
                redirect_stderr(diagnostic),
            ):
                diagnostics.collect_installed_backend_logs(source, root / "copy-failure", copy_errors)
            self.assertEqual(len(copy_errors), 1)
            self.assertIn("original copy failure", diagnostic.getvalue())
            self.assertEqual(
                (root / "copy-failure/installed-backend/backend.jsonl.stdout").read_bytes(),
                payloads["backend.jsonl.stdout"],
            )
            (source / "backend.jsonl").unlink()
            diagnostics.collect_installed_backend_logs(source, root / "missing", errors)
            self.assertEqual(len(errors), 1)
            self.assertIn("backend.jsonl", errors[0])
            self.assertTrue((root / "missing/installed-backend/backend.jsonl.previous").is_file())

    def test_installed_log_collection_failure_does_not_skip_other_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            local_vm._write_json(run_dir / "platform.json", {
                "platform": "macos", "source_checks_attempted": True,
            })
            (run_dir / "installed.json").write_text("{}")
            labels: list[str] = []

            def command(*_args, **kwargs):
                labels.append(kwargs["label"])

            def collection(_directory, _logs, errors):
                self.assertIn("cleanup-installed-package", labels)
                labels.append("collection")
                errors.append("collect-installed-backend: original I/O failure")

            args = local_vm.build_parser().parse_args([
                "cleanup", "--platform", "macos", "--run-dir", str(run_dir), "--timeout", "30",
            ])
            diagnostic = io.StringIO()
            with (
                mock.patch.object(local_vm, "_cleanup_logged", side_effect=command),
                mock.patch.object(local_vm, "collect_installed_backend_logs", side_effect=collection),
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.cleanup(args), 1)
            self.assertEqual(labels[-2:], ["collection", "cleanup-source-caches"])
            self.assertIn("original I/O failure", diagnostic.getvalue())
            self.assertEqual(json.loads((run_dir / "platform.json").read_text())["status"], "cleanup-failed")

        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            local_vm._write_json(run_dir / "platform.json", {
                "platform": "windows", "source_checks_attempted": True,
            })
            # A completed package build can create ProgramData logs before
            # migration or installation fails and installed.json is written.
            package = run_dir / "output/desktop-package/desktop-package.json"
            package.parent.mkdir(parents=True)
            package.write_text("{}", encoding="utf-8")
            program_data_logs = run_dir / "ProgramData/DobbyVPN/Logs"
            program_data_logs.mkdir(parents=True)
            backend = b"backend bytes\x00\xff\r\n"
            stdout = b"service stdout\xfe\n"
            stderr = b"service stderr\xfd\n"
            (program_data_logs / "backend.jsonl").write_bytes(backend)
            (program_data_logs / "backend.jsonl.stdout").write_bytes(stdout)
            (program_data_logs / "backend.jsonl.stderr").write_bytes(stderr)

            labels: list[str] = []
            collect = local_vm.collect_installed_backend_logs
            copy = diagnostics.shutil.copyfileobj

            def collect_and_record(directory, logs, errors):
                labels.append("collect-installed-backend")
                collect(directory, logs, errors)

            def fail_stderr(source_file, destination_file, **kwargs):
                if Path(source_file.name).name == "backend.jsonl.stderr":
                    raise OSError("original backend copy failure")
                return copy(source_file, destination_file, **kwargs)

            def windows_cleanup(*_args):
                labels.append("cleanup-windows")

            def command(*_args, **kwargs):
                labels.append(kwargs["label"])

            args = local_vm.build_parser().parse_args([
                "cleanup", "--platform", "windows", "--run-dir", str(run_dir),
                "--timeout", "30",
            ])
            diagnostic = io.StringIO()
            with (
                mock.patch.dict(local_vm.os.environ, {"PROGRAMDATA": str(run_dir / "ProgramData")}),
                mock.patch.object(local_vm_windows, "cleanup", side_effect=windows_cleanup),
                mock.patch.object(local_vm, "_cleanup_logged", side_effect=command),
                mock.patch.object(
                    local_vm, "collect_installed_backend_logs", side_effect=collect_and_record
                ),
                mock.patch.object(diagnostics.shutil, "copyfileobj", side_effect=fail_stderr),
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.cleanup(args), 1)

            retained = run_dir / "logs/installed-backend"
            self.assertEqual((retained / "backend.jsonl").read_bytes(), backend)
            self.assertEqual((retained / "backend.jsonl.stdout").read_bytes(), stdout)
            self.assertEqual(
                labels,
                ["cleanup-windows", "collect-installed-backend", "cleanup-source-caches"],
            )
            self.assertFalse((run_dir / "installed.json").exists())
            self.assertIn("original backend copy failure", diagnostic.getvalue())
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["status"], "cleanup-failed")
            self.assertTrue(
                any("original backend copy failure" in error for error in state["cleanup_errors"])
            )

    def test_ios_cleanup_retains_nested_exception_details(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            local_vm._write_json(run_dir / "platform.json", {
                "platform": "ios-simulator",
                "status": "running",
                "runtime": {"udid": "synthetic-simulator"},
            })
            args = local_vm.build_parser().parse_args([
                "cleanup", "--platform", "ios-simulator", "--run-dir", str(run_dir),
                "--timeout", "30",
            ])
            failure = ExceptionGroup(
                "Disposable iOS Simulator cleanup failed",
                [OSError("synthetic keychain reset failure")],
            )
            diagnostic = io.StringIO()
            with (
                mock.patch("torturer_runner.local_vm_ios.cleanup", side_effect=failure),
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.cleanup(args), 1)

            state = json.loads((run_dir / "platform.json").read_text())
            self.assertEqual(state["status"], "cleanup-failed")
            self.assertIn("OSError: synthetic keychain reset failure", state["cleanup_errors"][0])
            self.assertIn("OSError: synthetic keychain reset failure", diagnostic.getvalue())

    def test_windows_case_only_full_run_reuses_the_tracked_backend(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary)
            (run_dir / "source").mkdir()
            (run_dir / "profile").write_text("synthetic profile\n", encoding="utf-8")
            local_vm._write_json(run_dir / "platform.json", {
                "platform": "windows",
                "suite": "full",
                "status": "candidate-prepared",
                "candidate": {"mode": "local-build", "ui_helper": str(run_dir / "helper.exe")},
            })
            args = local_vm.build_parser().parse_args([
                "run", "--platform", "windows", "--run-dir", str(run_dir),
                "--timeout", "30", "--suite", "full", "--native-case", "configure-tree",
            ])
            runtime = {"pid": 12345, "binary": str(run_dir / "backend.exe"), "environment": {}}
            with (
                mock.patch.object(local_vm, "_start_windows", return_value=runtime) as start,
                mock.patch.object(local_vm, "_prepare_desktop_ui_home", return_value=str(run_dir)),
                mock.patch.object(local_vm, "_native_ui_environment", return_value={}),
                mock.patch.object(local_vm, "_candidate_path", return_value=run_dir / "helper.exe"),
                mock.patch.object(local_vm, "_native_ui_command", return_value=["native-ui"]),
                mock.patch.object(local_vm, "_run_native_ui", return_value=mock.Mock(returncode=0)),
                mock.patch.object(local_vm, "_timed_call", side_effect=lambda _name, operation, **_kw: operation()),
            ):
                self.assertEqual(local_vm.run(args), 0)
            start.assert_called_once()
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["runtime"]["pid"], runtime["pid"])
            self.assertEqual(state["native_ui_status"], "passed")

    def test_prepare_parser_reaches_setup_and_keeps_failure_cleanup_state(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = Path(temporary) / "run"
            (run_dir / "source").mkdir(parents=True)
            (run_dir / "profile").write_text("synthetic profile\n", encoding="utf-8")
            runtime = {
                "pid": 12345,
                "binary": str(run_dir / "source" / "candidate" / "dobbyvpn"),
            }
            setup_called = False

            def fail_after_recording_runtime(run_dir: Path, platform: str, **_kwargs):
                nonlocal setup_called
                setup_called = True
                local_vm._write_json(
                    run_dir / "platform.json",
                    {
                        "platform": platform,
                        "status": "starting",
                        "runtime": runtime,
                    },
                )
                raise local_vm.LocalVMError("synthetic setup failure")

            prepare_args = local_vm.build_parser().parse_args(
                [
                    "prepare", "--platform", "linux", "--run-dir", str(run_dir),
                    "--timeout", "30", "--suite", "mini",
                ]
            )
            diagnostic = io.StringIO()
            with (
                mock.patch.object(
                    local_vm, "_prepare_candidate", side_effect=fail_after_recording_runtime,
                ),
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.prepare(prepare_args), 1)

            self.assertTrue(setup_called)
            self.assertIn("synthetic setup failure", diagnostic.getvalue())
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["status"], "failed")
            self.assertEqual(state["runtime"], runtime)

            cleanup_args = local_vm.build_parser().parse_args(
                ["cleanup", "--platform", "linux", "--run-dir", str(run_dir), "--timeout", "30"]
            )
            with (
                mock.patch.object(local_vm, "_cleanup_logged"),
                mock.patch.object(local_vm, "_stop_pid", return_value=True) as stop_pid,
                mock.patch.object(local_vm, "_cleanup_linux_runtime_state"),
            ):
                self.assertEqual(local_vm.cleanup(cleanup_args), 0)

            stop_pid.assert_called_once_with(
                runtime["pid"], runtime["binary"], 30.0,
                logs=run_dir / "logs", cwd=run_dir,
            )
            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["status"], "cleaned")


if __name__ == "__main__":
    unittest.main()
