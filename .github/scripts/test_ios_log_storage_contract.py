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
        self.assertIn("provisioning-free Simulator app cannot receive", script)
        self.assertIn('codesign --force --sign - "$app"', script)
        self.assertNotIn("simulator_entitlements", script)
        self.assertNotIn('Add :DobbyKeychainAccessGroup string vpn.dobby.app', script)
        self.assertIn('if [[ "$device" == 1 ]]; then', script)
        self.assertIn('cp -R "$tunnel_product" "$app/PlugIns/tunnel.appex"', script)
        self.assertIn('rm -rf "$app/Frameworks/CommonDI.framework" "$app/PlugIns/tunnel.appex"', script)
        self.assertIn("install_name_tool -add_rpath '@executable_path/Frameworks'", script)
        self.assertIn('if [[ -n "${SOURCE_COMMIT:-}" ]]', script)
        self.assertIn("source commit unavailable; using the local-candidate sentinel", script)

    def test_ui_test_target_is_simulator_only(self) -> None:
        project = (self.root / "swift_module/iosApp.xcodeproj/project.pbxproj").read_text()
        target_start = project.index('E00000000000000000000009 /* Debug */')
        target_end = project.index('/* End XCBuildConfiguration section */', target_start)
        target = project[target_start:target_end]
        self.assertEqual(target.count("SDKROOT = iphonesimulator;"), 2)
        self.assertEqual(target.count("SUPPORTED_PLATFORMS = iphonesimulator;"), 2)
        self.assertNotIn("SDKROOT = iphoneos;", target)

    def test_fyne_ui_test_types_through_the_real_native_input_view(self) -> None:
        ui_test = (self.root / "swift_module/iosAppUITests/GoFyneUIInteractionTests.swift").read_text()
        self.assertIn("input.frame.isEmpty", ui_test)
        self.assertIn("input.coordinate(withNormalizedOffset:", ui_test)
        self.assertIn("typeUsingVisibleKeyboard", ui_test)
        self.assertIn("app.keyboards.firstMatch", ui_test)
        self.assertIn("key.tap()", ui_test)
        self.assertIn("dismissSoftwareKeyboard", ui_test)
        self.assertIn("connect.tap()", ui_test)
        self.assertIn('["Error", "Failed"]', ui_test)
        self.assertNotIn('["Error", "Failed", "Disconnected"]', ui_test)
        self.assertNotIn("input.typeText(", ui_test)


if __name__ == "__main__":
    unittest.main()
