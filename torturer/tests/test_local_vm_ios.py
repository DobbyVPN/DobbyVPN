import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from torturer_checks import local_vm, local_vm_ios


class LocalSimulatorTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "source").mkdir()
        self.logs = self.root / "logs"
        self.logs.mkdir()
        local_vm._write_json(self.root / "platform.json", {"schema": 1, "platform": "ios-simulator", "runtime": {}})

    def test_uses_existing_lifecycle_with_native_architecture_and_no_git_labels(self):
        commands = []

        def execute(runner, command, **kwargs):
            commands.append(command)
            return local_vm_ios.ios.CommandResult(0, "")

        def lifecycle(**kwargs):
            self.assertNotIn("repository", kwargs)
            self.assertNotIn("commit_sha", kwargs)
            runner = kwargs["runner"]
            runner.run(["xcrun", "simctl", "boot", "device-1"], timeout_seconds=5)
            runner.run(["xcrun", "simctl", "install", "device-1", "fixture.app"], timeout_seconds=5)
            runner.run(["xcrun", "simctl", "shutdown", "device-1"], timeout_seconds=5)
            return SimpleNamespace(simulator=SimpleNamespace(udid="device-1"))

        with patch.object(local_vm_ios.platform, "machine", return_value="x86_64"), \
                patch.object(local_vm_ios.ios.SubprocessCommandRunner, "run", autospec=True, side_effect=execute), \
                patch.object(local_vm_ios.ios, "prepare_ios_simulator_candidate", autospec=True) as prepare, \
                patch.object(local_vm_ios.ios, "run_ios_simulator_app_contract", autospec=True, side_effect=lifecycle) as run_contract:
            runtime = local_vm_ios.run(self.root, self.logs, 300, None)
        self.assertEqual(prepare.call_args.kwargs["contract"].architecture, "amd64")
        self.assertNotIn("mode", prepare.call_args.kwargs)
        self.assertNotIn("mode", run_contract.call_args.kwargs)
        self.assertNotIn("existing_app", run_contract.call_args.kwargs)
        self.assertFalse(runtime["installed"])
        self.assertIn(["xcrun", "simctl", "uninstall", "device-1", "vpn.dobby.app"], commands)
        self.assertTrue((self.logs / "simulator.json").exists())

    def test_simulator_never_uses_vpn_candidate_builder_or_profile(self):
        args = local_vm.build_parser().parse_args(["run", "--platform", "ios-simulator", "--run-dir", str(self.root), "--timeout", "300", "--suite", "mini"])
        with patch.object(local_vm, "_prepare_candidate") as build, patch.object(local_vm, "_start_ios", return_value={"udid": "fixture"}):
            self.assertEqual(local_vm.run(args), 0)
        build.assert_not_called()

    def test_shutdown_is_idempotent_and_scoped_to_recorded_device(self):
        inventory = {"devices": {"runtime": [{"udid": "device-1", "state": "Shutdown"}, {"udid": "other", "state": "Booted"}]}}
        with patch.object(local_vm, "_run_logged", return_value=subprocess.CompletedProcess([], 0, json.dumps(inventory).encode(), b"")) as run:
            local_vm_ios.cleanup(self.root, {"udid": "device-1"}, self.logs, 30)
        self.assertEqual(run.call_count, 1)

    def test_shutdown_error_is_not_reported_clean(self):
        inventory = {"devices": {"runtime": [{"udid": "device-1", "state": "Booted"}]}}
        with patch.object(local_vm, "_run_logged", side_effect=[subprocess.CompletedProcess([], 0, json.dumps(inventory).encode(), b""), local_vm.LocalVMError("shutdown failed")]):
            with self.assertRaisesRegex(local_vm.LocalVMError, "shutdown failed"):
                local_vm_ios.cleanup(self.root, {"udid": "device-1"}, self.logs, 30)


if __name__ == "__main__":
    unittest.main()
