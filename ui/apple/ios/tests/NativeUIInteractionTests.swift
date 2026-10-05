import XCTest
import UIKit

final class NativeUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    func testNativeConnectionAboutAndLogs() throws {
        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        assertLogLayout()
        attachScreenshot("startup")

        configuration.tap()
        configuration.typeText("invalidprofile")
        assertLogLayout()
        attachScreenshot("keyboard")
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
        XCTAssertEqual(version.label, "Version: 1.5.4")
        let fullCommit = app.staticTexts["About source commit metadata"]
        XCTAssertTrue(fullCommit.exists)
        let commitPrefix = "Source commit: "
        XCTAssertTrue(fullCommit.label.hasPrefix(commitPrefix))
        let commit = String(fullCommit.label.dropFirst(commitPrefix.count))
        XCTAssertNotNil(commit.range(of: "^[0-9a-fA-F]{40}$", options: .regularExpression))
        XCTAssertTrue(app.staticTexts["Commit: \(commit.prefix(12))"].exists)
        let sourceLink = app.descendants(matching: .any)
            .matching(identifier: "About source link").firstMatch
        XCTAssertTrue(sourceLink.waitForExistence(timeout: 10))
        XCTAssertEqual(sourceLink.value as? String, "https://github.com/DobbyVPN/DobbyVPN/tree/\(commit)")
        app.buttons["Done"].tap()
        XCTAssertEqual(configuration.value as? String, editedConfiguration)
        XCTAssertTrue(app.staticTexts["Logs"].exists)
        XCTAssertTrue(app.buttons["Share logs"].exists)
        XCTAssertTrue(app.buttons["Clear"].exists)
        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        let populated = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value != %@", ""), object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [populated], timeout: 10), .completed, "The prior error should be visible before clearing")
        app.buttons["Clear"].tap()
        let emptied = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", ""), object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [emptied], timeout: 10), .completed, "Clear should empty the rendered log view")
        if #available(iOS 16.4, *) {
            var invalidImport = URLComponents()
            invalidImport.scheme = "dobbyvpn"
            invalidImport.host = "import"
            invalidImport.queryItems = [URLQueryItem(name: "url", value: "http://example.invalid")]
            XCUIDevice.shared.system.open(try XCTUnwrap(invalidImport.url))
            let resumed = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value CONTAINS %@", "INVALID_ARGUMENT"), object: logs)
            XCTAssertEqual(XCTWaiter.wait(for: [resumed], timeout: 15), .completed, "A new error should appear after Clear resumes following")
        }
        XCTAssertFalse(app.buttons["Use configuration text…"].exists)
        XCTAssertTrue(app.textFields["Connection configuration"].exists)

        app.terminate()
        app.launch()
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        XCTAssertTrue(app.buttons["VPN connection action"].exists)
        attachScreenshot("reopened")
        try verifyColdAndWarmImports()

    }

    func testLargeTextKeepsLogsAndControlsVisible() {
        app.terminate()
        app.launchArguments = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        app.launch()
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        assertLogLayout()
        XCTAssertTrue(app.buttons["VPN connection action"].exists)
        XCTAssertTrue(app.buttons["Clear"].isHittable)
        attachScreenshot("large-text")
    }

    func testPasteReadsClipboardOnlyAfterTapAndRejectsNonHTTPSInput() {
        let clipboard = "http://example.invalid/not-a-subscription"
        app.terminate()
        UIPasteboard.general.string = clipboard
        app.launch()

        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let paste = app.buttons["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 10))
        XCTAssertNotEqual(configuration.value as? String, clipboard, "Clipboard availability must not read its contents")

        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        let previousRejections = occurrences(
            of: "Paste an HTTPS subscription URL with a host", in: logs.value as? String ?? ""
        )
        paste.tap()
        XCTAssertTrue(waitForLogOccurrences(
            "Paste an HTTPS subscription URL with a host",
            atLeast: previousRejections + 1,
            in: logs,
            timeout: 10
        ))
        XCTAssertNotEqual(configuration.value as? String, clipboard, "Non-HTTPS clipboard text must be rejected")
    }

    func testValidNativePasteAcceptedOnTap() {
        let clipboard = "https://example.invalid/native-paste"
        app.terminate()
        UIPasteboard.general.string = clipboard
        app.launch()

        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let paste = app.buttons["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 10))
        paste.tap()
        let pasted = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", clipboard), object: configuration)
        XCTAssertEqual(XCTWaiter.wait(for: [pasted], timeout: 10), .completed, "A valid HTTPS clipboard value should be accepted on tap")
        XCTAssertFalse(app.buttons["VPN connection action"].isEnabled)
    }

    func testLogsFreezeAndResumeAtBottom() throws {
        let clipboard = "http://example.invalid/repeat-error"
        let validationError = "Paste an HTTPS subscription URL with a host"
        app.terminate()
        UIPasteboard.general.string = clipboard
        app.launch()

        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let paste = app.buttons["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 10))
        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        let errorStatus = app.staticTexts["Error"]
        var expectedErrorCount = occurrences(of: validationError, in: logs.value as? String ?? "")
        paste.tap()
        XCTAssertTrue(errorStatus.waitForExistence(timeout: 10))
        expectedErrorCount += 1
        XCTAssertTrue(waitForLogOccurrences(
            validationError, atLeast: expectedErrorCount, in: logs, timeout: 10
        ))

        for _ in 0..<8 {
            configuration.tap()
            configuration.typeText("x")
            dismissConfigurationKeyboard()
            XCTAssertTrue(errorStatus.waitForNonExistence(timeout: 5))
            paste.tap()
            XCTAssertTrue(errorStatus.waitForExistence(timeout: 5))
            expectedErrorCount += 1
            XCTAssertTrue(waitForLogOccurrences(
                validationError, atLeast: expectedErrorCount, in: logs, timeout: 5
            ))
        }

        let enoughEntries = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let text = logs.value as? String else { return false }
            return self.occurrences(of: validationError, in: text) >= 6
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [enoughEntries], timeout: 15), .completed)
        let beforeScroll = try XCTUnwrap(logs.value as? String)
        let errorsBeforeScroll = occurrences(of: validationError, in: beforeScroll)
        XCTAssertGreaterThanOrEqual(errorsBeforeScroll, 6)

        logs.swipeDown()
        let frozen = try XCTUnwrap(logs.value as? String)
        configuration.tap()
        configuration.typeText("x")
        dismissConfigurationKeyboard()
        XCTAssertTrue(errorStatus.waitForNonExistence(timeout: 5))
        paste.tap()
        XCTAssertTrue(errorStatus.waitForExistence(timeout: 5))
        let changedWhileScrolledUp = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value != %@", frozen), object: logs
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [changedWhileScrolledUp], timeout: 1.5), .timedOut,
            "New records should not replace the rendered text while scrolled up"
        )

        logs.swipeUp()
        logs.swipeUp()
        logs.swipeUp()
        let resumed = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let text = logs.value as? String else { return false }
            return self.occurrences(of: validationError, in: text) > errorsBeforeScroll
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [resumed], timeout: 10), .completed, "Returning to the bottom should resume new log entries")
    }

    private func verifyColdAndWarmImports() throws {
        guard #available(iOS 16.4, *) else { XCTFail("URL activation checks require iOS 16.4 or newer"); return }
        let cold = "https://example.invalid/cold?a=%2F"
        let warm = "https://example.invalid/warm"
        func link(_ source: String) throws -> URL {
            var components = URLComponents()
            components.scheme = "dobbyvpn"
            components.host = "import"
            components.queryItems = [URLQueryItem(name: "url", value: source)]
            return try XCTUnwrap(components.url)
        }
        func expectSource(_ value: String) {
            let field = app.textFields["Connection configuration"]
            XCTAssertTrue(field.waitForExistence(timeout: 15))
            let result = XCTWaiter.wait(for: [XCTNSPredicateExpectation(
                predicate: NSPredicate(format: "value == %@", value), object: field
            )], timeout: 15)
            XCTAssertEqual(result, .completed)
            XCTAssertFalse(app.buttons["VPN connection action"].isEnabled)
        }
        app.terminate()
        app.open(try link(cold))
        expectSource(cold)
        XCUIDevice.shared.system.open(try link(warm))
        expectSource(warm)
        XCUIDevice.shared.system.open(try link(warm))
        expectSource(warm)
        XCUIDevice.shared.system.open(try XCTUnwrap(URL(string: "dobbyvpn://")))
        expectSource(warm)
        XCUIDevice.shared.system.open(try XCTUnwrap(URL(string: "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid&url=duplicate")))
        expectSource(warm)
    }

    private func assertLogLayout() {
        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        XCTAssertGreaterThanOrEqual(logs.frame.height, 60)
        XCTAssertGreaterThanOrEqual(logs.frame.minY, app.staticTexts["Logs"].frame.maxY)
        XCTAssertLessThanOrEqual(logs.frame.maxY, app.frame.maxY)
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

    private func occurrences(of needle: String, in text: String) -> Int {
        text.components(separatedBy: needle).count - 1
    }

    private func waitForLogOccurrences(
        _ message: String,
        atLeast count: Int,
        in logs: XCUIElement,
        timeout: TimeInterval
    ) -> Bool {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let text = logs.value as? String else { return false }
            return self.occurrences(of: message, in: text) >= count
        }, object: logs)
        return XCTWaiter.wait(for: [expectation], timeout: timeout) == .completed
    }
}
