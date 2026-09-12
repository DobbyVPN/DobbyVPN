from __future__ import annotations

from pathlib import Path
import tempfile
import unittest

from torturer_checks.ios_simulator import (
    IOSSimulatorContractError,
    SimulatorApp,
    simctl_boot_command,
    simctl_bootstatus_command,
    simctl_install_command,
    simctl_launch_command,
    simctl_terminate_command,
)


UDID = "A12B34C5-1234-5678-9ABC-123456789ABC"
BUNDLE = "vpn.dobby.app"


class IOSSimulatorCommandsTest(unittest.TestCase):
    def test_app_evidence_is_only_a_path_and_build_label(self) -> None:
        app = SimulatorApp(Path("doBBYVPN.app"), BUNDLE, "arm64")
        self.assertEqual(app.app_path.name, "doBBYVPN.app")
        self.assertEqual(app.bundle_identifier, BUNDLE)
        self.assertEqual(app.architecture, "arm64")

    def test_simctl_arguments_validate_only_command_inputs(self) -> None:
        self.assertEqual(simctl_boot_command(UDID), ["xcrun", "simctl", "boot", UDID])
        self.assertEqual(simctl_bootstatus_command(UDID), ["xcrun", "simctl", "bootstatus", UDID, "-b"])
        self.assertEqual(simctl_install_command(UDID, "/tmp/Dobby VPN.app")[-1], "/tmp/Dobby VPN.app")
        self.assertEqual(
            simctl_launch_command(UDID, BUNDLE),
            ["xcrun", "simctl", "launch", "--terminate-running-process", UDID, BUNDLE],
        )
        self.assertEqual(simctl_terminate_command(UDID, BUNDLE)[-1], BUNDLE)
        with self.assertRaisesRegex(IOSSimulatorContractError, "UDID"):
            simctl_boot_command("not-a-device")
        with self.assertRaisesRegex(IOSSimulatorContractError, r"\.app"):
            simctl_install_command(UDID, "not-an-app")


if __name__ == "__main__":
    unittest.main()
