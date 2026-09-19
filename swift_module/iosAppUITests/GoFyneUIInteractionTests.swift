import XCTest

/// UI-only proof for the installed Go/Fyne Simulator bundle.
///
/// This target deliberately does not import the Swift diagnostics host or
/// claim NetworkExtension/VPN success.  The app under test is the bundle that
/// the Simulator packaging contract installed (`vpn.dobby.app`); XCTest is
/// used only for accessibility lookup, typing, taps, and lifecycle.
final class GoFyneUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    func testAccessibleNavigationAndReopen() throws {
        let input = element(named: "Connection configuration")
        XCTAssertTrue(input.waitForExistence(timeout: 30), "Fyne configuration input is not accessible")

        let settings = element(named: "Settings")
        XCTAssertTrue(settings.waitForExistence(timeout: 10), "Fyne Settings control is not accessible")
        settings.tap()
        let back = element(named: "Back")
        XCTAssertTrue(back.waitForExistence(timeout: 10), "Fyne Back control is not accessible")
        back.tap()

        app.terminate()
        app.launch()
        XCTAssertTrue(
            element(named: "Connection configuration").waitForExistence(timeout: 30),
            "Go/Fyne UI did not reopen after termination"
        )
    }

    func testNativeTypingAndVisibleConnectOutcome() throws {
        let input = element(named: "Connection configuration")
        XCTAssertTrue(input.waitForExistence(timeout: 30), "Fyne configuration input is not accessible")
        // Fyne 2.8 exposes its canvas Entry as a positioned accessibility
        // element with a text role rather than a native UITextField. Tapping
        // that element focuses the real Entry; sending keyboard input through
        // the application then exercises the same path as the software
        // keyboard without pretending the proxy itself is editable.
        input.tap()
        app.typeText("ui-test-invalid-profile")

        // The input is intentionally not a VPN profile. This checks that the
        // visible Connect action is wired without treating Simulator UI as a
        // NetworkExtension success path.
        let connect = element(named: "Connect")
        XCTAssertTrue(connect.waitForExistence(timeout: 10), "Fyne Connect control is not accessible")
        connect.tap()
        let visibleOutcome = ["Error", "Failed", "Disconnected"].first { name in
            element(named: name).waitForExistence(timeout: 10)
        }
        XCTAssertNotNil(visibleOutcome, "Connect did not produce a visible UI outcome")

        app.terminate()
        app.launch()
        XCTAssertTrue(
            element(named: "Connection configuration").waitForExistence(timeout: 30),
            "Go/Fyne UI did not reopen after the editable-input interaction"
        )
    }

    private func element(named name: String) -> XCUIElement {
        let predicate = NSPredicate(format: "label == %@ OR identifier == %@", name, name)
        return app.descendants(matching: .any).matching(predicate).firstMatch
    }
}
