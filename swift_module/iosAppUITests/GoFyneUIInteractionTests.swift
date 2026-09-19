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
        let connect = element(named: "Connect")
        XCTAssertTrue(connect.waitForExistence(timeout: 10), "Fyne Connect control is not accessible")

        // A frame-anchored coordinate sends a real screen tap through Fyne's
        // GL view. Tapping the virtual accessibility element directly invokes
        // its static-text action instead and never focuses Fyne's Entry.
        XCTAssertFalse(input.frame.isEmpty, "Fyne configuration input has no tappable frame")
        input.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        typeUsingVisibleKeyboard("invalidprofile")

        // The input is intentionally not a VPN profile. This checks that the
        // visible Connect action is wired without treating Simulator UI as a
        // NetworkExtension success path.
        dismissSoftwareKeyboard()
        XCTAssertFalse(connect.frame.isEmpty, "Fyne Connect control has no tappable frame after keyboard dismissal")
        connect.tap()
        let visibleOutcome = ["Error", "Failed"].first { name in
            element(named: name).waitForExistence(timeout: 10)
        }
        XCTAssertNotNil(visibleOutcome, "Connect did not produce a new failure state")

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

    private func typeUsingVisibleKeyboard(_ text: String) {
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 10), "Fyne input did not show the software keyboard")
        for character in text {
            let lower = String(character).lowercased()
            let upper = String(character).uppercased()
            let lowerKey = keyboard.keys[lower]
            let upperKey = keyboard.keys[upper]
            let key = lowerKey.exists ? lowerKey : upperKey
            XCTAssertTrue(key.waitForExistence(timeout: 2), "software keyboard key is unavailable: \(character)")
            key.tap()
        }
    }

    private func dismissSoftwareKeyboard() {
        let keyboard = app.keyboards.firstMatch
        guard keyboard.waitForExistence(timeout: 2) else { return }

        // Recent Simulator keyboards expose a real hide action. Use it when
        // available; otherwise tap an empty point in the Go/Fyne GL view so
        // Fyne's normal focus-loss path resigns its native input responder.
        let hide = keyboard.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] %@ OR identifier CONTAINS[c] %@", "hide", "hide")
        ).firstMatch
        if hide.waitForExistence(timeout: 1) {
            hide.tap()
        }
        if keyboard.exists {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.06)).tap()
        }

        let gone = NSPredicate(format: "exists == false")
        let expectation = self.expectation(for: gone, evaluatedWith: keyboard)
        wait(for: [expectation], timeout: 5)
    }

}
