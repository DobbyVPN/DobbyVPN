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
        app.buttons["Dismiss configuration keyboard"].tap()
        app.tabBars.buttons["Settings"].tap()
        app.tabBars.buttons["Connection"].tap()
        XCTAssertEqual(configuration.value as? String, "invalidprofile")
        app.buttons["VPN connection action"].tap()
        XCTAssertTrue(app.staticTexts["Error"].waitForExistence(timeout: 30))
        attachScreenshot("failure")

        configuration.tap()
        configuration.typeText("2")
        let editedConfiguration = try XCTUnwrap(configuration.value as? String)
        XCTAssertEqual(editedConfiguration.count, "invalidprofile".count + 1)
        XCTAssertEqual(editedConfiguration.filter { $0 != "2" }, "invalidprofile")
        app.buttons["Dismiss configuration keyboard"].tap()

        let settingsTab = app.tabBars.buttons["Settings"]
        let version = app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Version:")).firstMatch
        settingsTab.tap()
        if !version.waitForExistence(timeout: 3) && !settingsTab.isSelected {
            settingsTab.tap()
        }
        XCTAssertTrue(version.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Source commit:")).firstMatch
            .exists)

        app.tabBars.buttons["Connection"].tap()
        XCTAssertEqual(configuration.value as? String, editedConfiguration)
        app.tabBars.buttons["Logs"].tap()
        XCTAssertTrue(app.staticTexts["Logs"].waitForExistence(timeout: 10))
        app.buttons["Refresh"].tap()

        app.terminate()
        app.launch()
        XCTAssertTrue(app.textViews["Connection configuration"].waitForExistence(timeout: 30))
        XCTAssertTrue(app.buttons["VPN connection action"].exists)
        attachScreenshot("reopened")
    }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "dobbyvpn-ui-\(name)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
