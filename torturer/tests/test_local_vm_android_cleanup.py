from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_checks import local_vm_android as android


class AndroidCleanupTests(unittest.TestCase):
    def test_absent_app_and_present_companion_are_handled_independently(self):
        calls = []

        def run(command, **kwargs):
            calls.append(command)
            label = kwargs["label"]
            output = b"device\n" if label == "android-cleanup-state" else b"package:com.dobby.vpn.test\n"
            return subprocess.CompletedProcess(command, 0, output, b"")

        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with mock.patch.dict(android.os.environ, {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/adb"}), mock.patch.object(android, "_run_logged", side_effect=run):
                android.cleanup(root, {"adb": "adb", "serial": "test-device", "installed_packages": [android.APP_PACKAGE, android.COMPANION_PACKAGE]}, root / "logs", 10)
        uninstalls = [command for command in calls if "uninstall" in command]
        self.assertEqual(len(uninstalls), 1)
        self.assertEqual(uninstalls[0][-1], android.COMPANION_PACKAGE)

    def test_unavailable_adb_is_not_treated_as_absent_package(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with mock.patch.dict(android.os.environ, {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/adb"}), mock.patch.object(android, "_run_logged", side_effect=[subprocess.CompletedProcess([], 0, b"device\n", b""), RuntimeError("ADB failed")]):
                with self.assertRaisesRegex(RuntimeError, "ADB failed"):
                    android.cleanup(root, {"adb": "adb", "serial": "test-device", "installed_packages": [android.APP_PACKAGE]}, root / "logs", 10)
