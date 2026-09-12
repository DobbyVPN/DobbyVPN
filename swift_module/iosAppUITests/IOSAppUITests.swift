import XCTest

final class IOSAppUITests: XCTestCase {
    func testConnectionSettingsNavigationAndText() {
        let app = XCUIApplication()
        app.launch()

        let input = app.descendants(matching: .any)
            .matching(identifier: "dobby.subscription.input")
            .firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 20), "subscription input is missing")
        input.tap()
        input.typeText("https://example.invalid/simulator-smoke")

        let settings = app.descendants(matching: .any)
            .matching(identifier: "dobby.settings.nav")
            .firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 10), "settings navigation is missing")
        settings.tap()

        let version = app.descendants(matching: .any)
            .matching(identifier: "dobby.build.version")
            .firstMatch
        XCTAssertTrue(version.waitForExistence(timeout: 10), "build version is missing")
        XCTAssertFalse((version.value as? String ?? "").isEmpty, "build version is empty")

        let connection = app.descendants(matching: .any)
            .matching(identifier: "dobby.connection.nav")
            .firstMatch
        XCTAssertTrue(connection.waitForExistence(timeout: 10), "connection navigation is missing")
        connection.tap()

        XCTAssertEqual(
            input.value as? String,
            "https://example.invalid/simulator-smoke",
            "navigation discarded the entered URL",
        )
    }
}
