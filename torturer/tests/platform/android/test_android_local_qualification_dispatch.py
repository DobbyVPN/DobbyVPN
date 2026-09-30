from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.results import ConnectionIdentity
from torturer_runner import hosted, local_vm


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
            with mock.patch.object(local_vm, "_prepare_candidate", return_value=candidate):
                self.assertEqual(local_vm.prepare(args), 0)

            state = json.loads((run_dir / "platform.json").read_text(encoding="utf-8"))
            self.assertEqual(state["candidate"]["source_sha"], SOURCE_SHA)

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

            modes: list[str] = []
            factory_arguments: list[dict[str, object]] = []

            def adapter_for_platform(_platform, **kwargs):
                mode = kwargs["android_ui_mode"]
                modes.append(mode)
                factory_arguments.append(kwargs)
                return SimpleNamespace(ui_mode=mode)

            def execute_lane(_engine, scenarios, adapter, _provenance, **_kwargs):
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
            self.assertTrue(output["rendered_ui"]["passed"])
            self.assertEqual(output["rendered_ui"]["connection"]["protocol"], "AUTO")
            self.assertTrue(output["coverage"]["complete"])
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
