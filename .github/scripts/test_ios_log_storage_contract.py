from pathlib import Path
import unittest


class IosNativeShellContractTests(unittest.TestCase):
    def setUp(self) -> None:
        self.root = Path(__file__).resolve().parents[2]

    def test_native_shell_contains_only_the_go_bridge_and_lifecycle_host(self) -> None:
        composition = (self.root / "swift_module/CommonDI/AppCompositionRoot.swift").read_text()
        bridge = (self.root / "swift_module/CommonDI/GoUIBridge.swift").read_text()
        header = (self.root / "swift_module/CommonDI/CommonDI.h").read_text()
        content_view = (self.root / "swift_module/iosApp/ContentView.swift").read_text()
        self.assertIn("IOSAppCompositionRoot.logsRepository", bridge)
        self.assertIn("IOSAppCompositionRoot.sessionShell", bridge)
        self.assertIn("dobby_ui_configure", header)
        self.assertIn("goLogFilePath", composition)
        self.assertIn("Dobby VPN", content_view)
        self.assertNotIn("createIosAppDependencies", composition)
        self.assertNotIn("import app", composition)
        self.assertNotIn("Compose", content_view)

    def test_logs_are_known_app_group_files_and_do_not_use_permission_guards(self) -> None:
        root = self.root / "swift_module/CommonDI"
        composition = (root / "AppCompositionRoot.swift").read_text()
        self.assertIn('sharedLogPath("app_logs.txt")', composition)
        self.assertIn('sharedLogPath("go_app_logs.jsonl")', composition)
        self.assertNotIn("chmod", composition)
        self.assertNotIn("DobbyVPNLogs", composition)
        self.assertNotIn("import app", composition)

    def test_simulator_packaging_does_not_require_apple_development_signing(self) -> None:
        script = (self.root / "go_module/scripts/package_ios_app.sh").read_text()
        self.assertIn("temporary self-signed certificate", script)
        self.assertIn("no Apple Development certificate", script)
        self.assertIn("iossimulator", script)
        self.assertIn("codesign --force --sign -", script)
        self.assertIn("com.apple.security.application-groups", script)
        self.assertIn("group.vpn.dobby.app", script)


if __name__ == "__main__":
    unittest.main()
