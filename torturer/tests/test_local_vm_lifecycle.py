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
