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
    iphonesimulator_sdk_version_command,
    xcodebuild_ui_test_command,
)


UDID = "A12B34C5-1234-5678-9ABC-123456789ABC"
BUNDLE = "vpn.dobby.app"
UI_TEST_SOURCE = Path(__file__).parents[3] / "swift_module" / "iosAppUITests" / "GoFyneUIInteractionTests.swift"
EXPORT_SOURCE = Path(__file__).parents[3] / "swift_module" / "CommonDI" / "ExportLogsInteractorImpl.swift"
PACKAGE_SOURCE = Path(__file__).parents[3] / "go_module" / "scripts" / "package_ios_app.sh"


class IOSSimulatorCommandsTest(unittest.TestCase):
    def test_package_lane_uses_shared_runtime_framework_validator(self) -> None:
        source = PACKAGE_SOURCE.read_text(encoding="utf-8")
        self.assertIn(
            'python3 "$go_root/scripts/ios_runtime_framework.py"',
            source,
        )
        self.assertNotIn("torturer_checks.ios_runtime_framework", source)
        self.assertIn("validation_architecture", source)

    def test_log_export_uses_the_existing_active_app_presenter(self) -> None:
        source = EXPORT_SOURCE.read_text(encoding="utf-8")
        self.assertIn("UIActivityViewController", source)
        self.assertIn("UIAlertController", source)
        self.assertIn('title: "Share"', source)
        self.assertIn('title: "Save"', source)
        self.assertIn('title: "Close"', source)
        self.assertIn("UIDocumentPickerViewController(forExporting:", source)
        self.assertIn("activeWindow(in: scene)", source)
        self.assertIn("topViewController(from: rootViewController)", source)
        self.assertIn("active.viewController.present(activityVC, animated: true)", source)
        self.assertIn("presenter.loadViewIfNeeded()", source)
        self.assertIn("guard let view = presenter.viewIfLoaded", source)
        self.assertIn("popover.sourceView = active.view", source)
        self.assertIn("if !Thread.isMainThread", source)
        self.assertIn("DispatchQueue.main.async", source)
        self.assertIn("completionWithItemsHandler", source)
        self.assertIn("presentationControllerDidDismiss", source)
        self.assertIn("permittedArrowDirections = []", source)
        self.assertIn("restorePresentationState()", source)
        self.assertNotIn("UIWindow(windowScene: scene)", source)
        self.assertNotIn("makeKeyAndVisible()", source)
        self.assertNotIn("LogExportOverlayViewController", source)
        self.assertNotIn("overlayWindow", source)
        self.assertNotIn("previousKeyWindow", source)
        self.assertIn("documentPickerWasCancelled", source)

    def test_go_fyne_journey_uses_rendered_software_keyboard_input(self) -> None:
        source = UI_TEST_SOURCE.read_text(encoding="utf-8")
        self.assertIn("Let XCTest own the first launch", source)
        setup = source.split("    func testGoFyneMiniJourney()", 1)[0]
        self.assertNotIn("app.terminate()", setup)
        self.assertIn(
            'let inputAfterSettings = element(named: "Connection configuration")',
            source,
        )
        self.assertIn("private func editConfiguration(", source)
        self.assertIn("var focusInput = input", source)
        self.assertIn("let editDeadline = Date().addingTimeInterval(90)", source)
        self.assertIn("func focusCurrentInput(until deadline: Date) -> Bool", source)
        self.assertIn("var needsFocus = false", source)
        self.assertIn("var unavailableKeyAttempts = 0", source)
        self.assertIn("func resetSoftwareKeyboard(until deadline: Date)", source)
        self.assertIn("resetSoftwareKeyboard(until: deadline)", source)
        self.assertIn("CGVector(dx: 0.98, dy: 0.06)", source)
        self.assertIn('NSPredicate(format: "exists == false")', source)
        self.assertIn("let keyboardTimeout = min(15", source)
        self.assertIn("unavailableKeyAttempts >= 3", source)
        self.assertIn("keyboard.frame.isEmpty", source)
        self.assertIn("let currentInput = focusInput", source)
        self.assertIn("currentInput.frame.isEmpty", source)
        self.assertIn("currentInput.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()", source)
        self.assertIn("app.keyboards.firstMatch", source)
        self.assertIn("keyboard.waitForExistence(timeout:", source)
        self.assertIn("app.keys.matching(predicate).firstMatch", source)
        self.assertNotIn("keyboard.keys.matching(predicate).firstMatch", source)
        self.assertIn("keyboardFrame.intersects(key.frame)", source)
        self.assertIn("key.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()", source)
        self.assertIn("label ==[c] %@ OR identifier ==[c] %@", source)
        self.assertIn("label CONTAINS[c] %@ OR identifier CONTAINS[c] %@", source)
        self.assertNotIn("app.typeText(", source)
        self.assertNotIn("app.typeKey(", source)
        self.assertNotIn("tapVisibleKeyboardKey", source)
        self.assertNotIn("typeUsingVisibleKeyboard", source)
        self.assertNotIn("Fyne input did not show the software keyboard", source)
        self.assertNotIn(
            'editConfiguration(inputAfterSettings, deleteCount: 1)\n        editConfiguration(inputAfterSettings, text: "bad")',
            source,
        )
        self.assertIn("dismissExportPrompt", source)
        self.assertIn("waitForNativeExportPrompt", source)
        self.assertIn("waitForDocumentPickerAndCancel", source)
        self.assertIn("save.tap()", source)
        self.assertNotIn("private enum ExportAction", source)
        self.assertNotIn("exerciseExportAction(", source)
        self.assertNotIn("share.tap()", source)
        self.assertNotIn("waitForShareDismissal", source)
        self.assertNotIn("nativeShareSurface", source)
        self.assertIn(
            'XCUIApplication(bundleIdentifier: "com.apple.DocumentManagerUICore.Service")',
            source,
        )
        self.assertIn("private func documentPickerControl(", source)
        self.assertIn("owner.descendants(matching: .any).matching(semanticLabel).firstMatch", source)
        self.assertIn("label ==[c] %@ OR identifier ==[c] %@", source)
        self.assertIn("value ==[c] %@", source)
        self.assertIn("value CONTAINS[c] %@", source)
        self.assertIn("label CONTAINS[c] %@ OR identifier CONTAINS[c] %@", source)
        self.assertIn('documentPickerControl(owner.1, named: "Cancel")', source)
        self.assertIn('documentPickerControl(owner.1, named: "Save")', source)
        self.assertIn("!cancel.frame.isEmpty, !save.frame.isEmpty", source)
        self.assertIn('surface.buttons["Share"]', source)
        self.assertIn('surface.buttons["Save"]', source)
        self.assertIn('surface.buttons["Close"]', source)
        self.assertIn("owner.1.navigationBars.firstMatch", source)
        self.assertIn("cancel.tap()", source)
        self.assertIn("owner: owner.1", source)
        self.assertIn("private func waitForNativeSurfaceDismissal(", source)
        self.assertIn("!dismissedElement.isHittable", source)
        self.assertIn("owner.state != .runningForeground", source)
        self.assertIn("app.wait(for: .runningForeground", source)
        self.assertIn("reopenedConnect.waitForExistence(timeout: 30)", source)
        self.assertIn("waitForEnabled(reopenedConnect)", source)
        self.assertIn("private func tapConnectExpectingFailure(_ connect: XCUIElement) -> Bool", source)
        self.assertIn("if waitForFailureState() {", source)
        self.assertIn(
            "connect.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()",
            source,
        )
        self.assertIn("tapConnectExpectingFailure(connect)", source)
        self.assertIn("tapConnectExpectingFailure(reopenedConnect)", source)
        self.assertIn('element(named: "Clear logs").waitForExistence(timeout: 30)', source)
        self.assertIn("reopened empty configuration did not produce a visible error", source)
        self.assertIn(
            "reopened empty-input error did not expose accessible connection details",
            source,
        )
        self.assertNotIn("reopenedConnect.tap()", source)
        self.assertIn('value.contains("bad")', source)
        self.assertNotIn("inlinesecretmustnotpersist", source)
        self.assertNotIn("clearInput(reopened", source)
        self.assertNotIn("invalid input did not produce a diagnostic state", source)

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
        self.assertEqual(
            simctl_launch_command(UDID, BUNDLE, console=True),
            ["xcrun", "simctl", "launch", "--console", "--terminate-running-process", UDID, BUNDLE],
        )
        self.assertEqual(simctl_terminate_command(UDID, BUNDLE)[-1], BUNDLE)
        self.assertEqual(
            iphonesimulator_sdk_version_command(),
            ["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"],
        )
        ui_command = xcodebuild_ui_test_command(UDID, "swift_module/iosApp.xcodeproj", "/tmp/ios-ui-tests")
        self.assertIn("-scheme", ui_command)
        self.assertIn("iosAppUITests", ui_command)
        self.assertEqual(ui_command[ui_command.index("-sdk") + 1], "iphonesimulator")
        self.assertIn("-only-testing:iosAppUITests/GoFyneUIInteractionTests", ui_command)
        self.assertIn("CODE_SIGNING_ALLOWED=YES", ui_command)
        self.assertIn("CODE_SIGN_IDENTITY=-", ui_command)
        with self.assertRaisesRegex(IOSSimulatorContractError, "UDID"):
            simctl_boot_command("not-a-device")
        with self.assertRaisesRegex(IOSSimulatorContractError, r"\.app"):
            simctl_install_command(UDID, "not-an-app")
        with self.assertRaisesRegex(IOSSimulatorContractError, r"\.xcodeproj"):
            xcodebuild_ui_test_command(UDID, "not-a-project", "/tmp/ios-ui-tests")


if __name__ == "__main__":
    unittest.main()
