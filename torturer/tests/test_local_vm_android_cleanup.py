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
        routing = [
            command for command in calls if "iptables -D OUTPUT" in command[-1]
        ]
        self.assertEqual(len(routing), 1)
        self.assertIn(android.ROUTING_RULE_CHAIN, routing[0][-1])
        self.assertIn("iptables -D OUTPUT", routing[0][-1])
        self.assertIn("inventory=$(iptables -S)", routing[0][-1])
        self.assertIn("printf", routing[0][-1])
        self.assertIn("$inventory", routing[0][-1])
        self.assertNotIn("already absent", routing[0][-1])
        self.assertIn("routing chain remains", routing[0][-1])
        probe = [
            command for command in calls if "dobbyvpn-probe-*" in command[-1]
        ]
        self.assertEqual(len(probe), 1)
        self.assertEqual(probe[0][-2:], ["-c", probe[0][-1]])

    def test_unavailable_adb_is_not_treated_as_absent_package(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            def run(command, **kwargs):
                if kwargs["label"] == "android-cleanup-state":
                    return subprocess.CompletedProcess(command, 0, b"device\n", b"")
                if kwargs["label"] == "android-cleanup-probe-com-dobby-vpn":
                    raise RuntimeError("ADB failed")
                return subprocess.CompletedProcess(command, 0, b"", b"")

            with mock.patch.dict(android.os.environ, {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/adb"}), mock.patch.object(android, "_run_logged", side_effect=run):
                with self.assertRaisesRegex(RuntimeError, "ADB failed"):
                    android.cleanup(root, {"adb": "adb", "serial": "test-device", "installed_packages": [android.APP_PACKAGE]}, root / "logs", 10)

    def test_app_probe_failure_still_attempts_companion_cleanup(self):
        calls = []

        def run(command, **kwargs):
            calls.append((kwargs["label"], command))
            label = kwargs["label"]
            if label == "android-cleanup-state":
                return subprocess.CompletedProcess(command, 0, b"device\n", b"")
            if label == "android-cleanup-probe-com-dobby-vpn":
                raise RuntimeError("app probe failed")
            if label == "android-cleanup-probe-com-dobby-vpn-test":
                return subprocess.CompletedProcess(
                    command, 0, b"package:com.dobby.vpn.test\n", b""
                )
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with mock.patch.dict(
                android.os.environ,
                {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/adb"},
            ), mock.patch.object(android, "_run_logged", side_effect=run):
                with self.assertRaisesRegex(RuntimeError, "app probe failed"):
                    android.cleanup(
                        root,
                        {
                            "adb": "adb",
                            "serial": "test-device",
                            "installed_packages": [
                                android.APP_PACKAGE,
                                android.COMPANION_PACKAGE,
                            ],
                        },
                        root / "logs",
                        10,
                    )

        labels = [label for label, _command in calls]
        self.assertIn("android-cleanup-uninstall-com-dobby-vpn-test", labels)

    def test_routing_cleanup_failure_does_not_skip_package_teardown(self):
        calls = []

        def run(command, **kwargs):
            calls.append((kwargs["label"], command))
            label = kwargs["label"]
            if label == "android-cleanup-state":
                return subprocess.CompletedProcess(command, 0, b"device\n", b"")
            if label == "android-cleanup-routing":
                raise RuntimeError("routing absence proof failed")
            if label == "android-cleanup-probe-com-dobby-vpn":
                return subprocess.CompletedProcess(
                    command, 0, b"package:com.dobby.vpn\n", b""
                )
            return subprocess.CompletedProcess(command, 0, b"", b"")

        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with mock.patch.dict(
                android.os.environ,
                {"ADB_SERVER_SOCKET": "localfilesystem:/tmp/adb"},
            ), mock.patch.object(android, "_run_logged", side_effect=run):
                with self.assertRaisesRegex(
                    RuntimeError, "android-cleanup-routing.*absence proof failed"
                ):
                    android.cleanup(
                        root,
                        {
                            "adb": "adb",
                            "serial": "test-device",
                            "installed_packages": [android.APP_PACKAGE],
                        },
                        root / "logs",
                        10,
                    )

        labels = [label for label, _command in calls]
        self.assertIn("android-cleanup-network-probe", labels)
        self.assertIn("android-cleanup-uninstall-com-dobby-vpn", labels)
