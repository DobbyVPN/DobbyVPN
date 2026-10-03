from __future__ import annotations

from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from torturer_runner import local_vm


class LocalVMLifecycleTests(unittest.TestCase):
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
            local_vm._collect_installed_backend_logs(source, logs, errors)
            self.assertEqual(errors, [])
            self.assertEqual(
                {path.name: path.read_bytes() for path in (logs / "installed-backend").iterdir()},
                payloads,
            )
            copy = local_vm.shutil.copyfileobj

            def fail_stderr(source_file, destination_file, **kwargs):
                if Path(source_file.name).name == "backend.jsonl.stderr":
                    raise OSError("original copy failure")
                return copy(source_file, destination_file, **kwargs)

            copy_errors: list[str] = []
            diagnostic = io.StringIO()
            with (
                mock.patch.object(local_vm.shutil, "copyfileobj", side_effect=fail_stderr),
                redirect_stderr(diagnostic),
            ):
                local_vm._collect_installed_backend_logs(source, root / "copy-failure", copy_errors)
            self.assertEqual(len(copy_errors), 1)
            self.assertIn("original copy failure", diagnostic.getvalue())
            self.assertEqual(
                (root / "copy-failure/installed-backend/backend.jsonl.stdout").read_bytes(),
                payloads["backend.jsonl.stdout"],
            )
            (source / "backend.jsonl").unlink()
            local_vm._collect_installed_backend_logs(source, root / "missing", errors)
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
                mock.patch.object(local_vm, "_collect_installed_backend_logs", side_effect=collection),
                redirect_stderr(diagnostic),
            ):
                self.assertEqual(local_vm.cleanup(args), 1)
            self.assertEqual(labels[-2:], ["collection", "cleanup-source-caches"])
            self.assertIn("original I/O failure", diagnostic.getvalue())
            self.assertEqual(json.loads((run_dir / "platform.json").read_text())["status"], "cleanup-failed")

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
