import XCTest

final class IOSAppUITests: XCTestCase {
    func testConnectionSettingsNavigationAndText() {
        let app = XCUIApplication()
        app.launch()

        func element(label: String) -> XCUIElement {
            app.descendants(matching: .any)
                .matching(NSPredicate(format: "label == %@", label))
                .firstMatch
        }

        let input = element(label: "dobby.subscription.input")
        XCTAssertTrue(
            input.waitForExistence(timeout: 20),
            "subscription input is missing:\n\(app.debugDescription)",
        )
        input.tap()
        input.typeText("https://example.invalid/simulator-smoke")

        let settings = element(label: "dobby.settings.nav")
        XCTAssertTrue(settings.waitForExistence(timeout: 10), "settings navigation is missing")
        settings.tap()

        let version = element(label: "dobby.build.version")
        XCTAssertTrue(version.waitForExistence(timeout: 10), "build version is missing")
        XCTAssertFalse((version.value as? String ?? "").isEmpty, "build version is empty")

        let connection = element(label: "dobby.connection.nav")
        XCTAssertTrue(connection.waitForExistence(timeout: 10), "connection navigation is missing")
        connection.tap()

        XCTAssertEqual(
            input.value as? String,
            "https://example.invalid/simulator-smoke",
            "navigation discarded the entered URL",
        )
    }
}
