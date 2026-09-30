import XCTest

final class NativeUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    func testNativeConnectionSettingsAndLogs() throws {
        let configuration = app.textViews["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        attachScreenshot("startup")

        configuration.tap()
        configuration.typeText("invalidprofile")
        dismissConfigurationKeyboard()
        tapTab("Settings")
        tapTab("Connection")
        XCTAssertEqual(configuration.value as? String, "invalidprofile")
        app.buttons["VPN connection action"].tap()
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

        let settingsTab = app.tabBars.buttons["Settings"]
        let version = app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Version:")).firstMatch
        tapTab("Settings")
        if !version.waitForExistence(timeout: 3) && !settingsTab.isSelected {
            tapTab("Settings")
        }
        XCTAssertTrue(version.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Source commit:")).firstMatch
            .exists)

        tapTab("Connection")
        XCTAssertEqual(configuration.value as? String, editedConfiguration)
        tapTab("Logs")
        XCTAssertTrue(app.staticTexts["Logs"].waitForExistence(timeout: 10))
        app.buttons["Refresh"].tap()

        app.terminate()
        app.launch()
        XCTAssertTrue(app.textViews["Connection configuration"].waitForExistence(timeout: 30))
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

    private func tapTab(_ name: String) {
        let tab = app.tabBars.buttons[name]
        let hittable = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "isHittable == true"),
            object: tab
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [hittable], timeout: 10),
            .completed,
            "The \(name) tab should be hittable before it is tapped."
        )
        tab.tap()
    }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "dobbyvpn-ui-\(name)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
