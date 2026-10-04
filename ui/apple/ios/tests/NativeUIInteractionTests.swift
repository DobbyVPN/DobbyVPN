import XCTest

final class NativeUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    func testNativeConnectionAboutAndLogs() throws {
        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        attachScreenshot("startup")

        configuration.tap()
        configuration.typeText("invalidprofile")
        dismissConfigurationKeyboard()
        openAbout()
        app.buttons["Done"].tap()
        XCTAssertEqual(configuration.value as? String, "invalidprofile")
        XCTAssertFalse(app.buttons["VPN connection action"].isEnabled)
        XCTAssertTrue(app.staticTexts["Error"].waitForExistence(timeout: 30))
        attachScreenshot("failure")
        XCTAssertEqual(configuration.value as? String, "invalidprofile")

        configuration.tap()
        configuration.typeText("2")
        let editedConfiguration = try XCTUnwrap(configuration.value as? String)
        if editedConfiguration != "2" {
            XCTAssertEqual(editedConfiguration.count, "invalidprofile".count + 1)
            XCTAssertEqual(editedConfiguration.filter { $0 != "2" }, "invalidprofile")
        }
        dismissConfigurationKeyboard()

        let version = app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Version:")).firstMatch
        openAbout()
        XCTAssertTrue(version.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Source commit:")).firstMatch.exists)
        app.buttons["Done"].tap()
        XCTAssertEqual(configuration.value as? String, editedConfiguration)
        XCTAssertTrue(app.staticTexts["Logs"].exists)
        XCTAssertTrue(app.buttons["Share logs"].exists)
        XCTAssertTrue(app.buttons["Clear"].exists)
        app.buttons["Clear"].tap()
        XCTAssertFalse(app.buttons["Use configuration text…"].exists)
        XCTAssertTrue(app.textFields["Connection configuration"].exists)

        app.terminate()
        app.launch()
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        XCTAssertTrue(app.buttons["VPN connection action"].exists)
        attachScreenshot("reopened")
    }

    private func dismissConfigurationKeyboard() {
        app.buttons["Dismiss configuration keyboard"].tap()
        XCTAssertTrue(
            app.keyboards.firstMatch.waitForNonExistence(timeout: 10),
            "The software keyboard should be dismissed before continuing."
        )
    }

    private func openAbout() {
        let button = app.buttons["About"]
        XCTAssertTrue(button.waitForExistence(timeout: 10))
        button.tap()
        XCTAssertTrue(app.buttons["Done"].waitForExistence(timeout: 10))
    }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "dobbyvpn-ui-\(name)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
