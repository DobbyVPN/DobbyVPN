from __future__ import annotations

import subprocess
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / "go_module" / "scripts" / "package_mobile_ui.sh"


class MobileUIPackageScriptTests(unittest.TestCase):
    def test_script_declares_the_supported_renderer_targets_and_boundary(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")
        for target in ("android/arm64", "android/amd64", "ios", "iossimulator"):
            self.assertIn(target, source)
        self.assertIn("./cmd/dobbyui", source)
        self.assertIn("--tags accessibility", source)
        self.assertIn("--app-id com.dobby.vpn", source)
        self.assertIn("--icon \"$script_root/assets/logo.png\"", source)
        self.assertIn("native lifecycle boundaries", source)
        self.assertIn("second VPN runtime", source)

    def test_script_rejects_missing_arguments_before_running_go(self) -> None:
        result = subprocess.run(
            ["bash", str(SCRIPT)],
            check=False,
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("usage:", result.stderr)

    def test_script_rejects_unknown_target_before_running_go(self) -> None:
        result = subprocess.run(
            ["bash", str(SCRIPT), "linux", "/tmp/dobby-vpn-ui"],
            check=False,
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("unsupported mobile target", result.stderr)


if __name__ == "__main__":
    unittest.main()
