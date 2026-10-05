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
        XCTAssertTrue(app.buttons["Clear"].isHittable, "The log controls should remain reachable with the keyboard open")
        XCTAssertTrue(app.buttons["Share logs"].isHittable, "Log export should remain reachable with the keyboard open")
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
        let clearSentinel = "Paste an HTTPS subscription URL with a host"
        UIPasteboard.general.string = "http://example.invalid/clear-sentinel"
        let sentinelCount = occurrences(of: clearSentinel, in: logs.value as? String ?? "")
        app.buttons["Paste"].tap()
        XCTAssertTrue(waitForLogOccurrences(clearSentinel, atLeast: sentinelCount + 1, in: logs, timeout: 20))
        app.buttons["Clear"].tap()
        let emptied = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", ""), object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [emptied], timeout: 15), .completed,
                       "Clear should leave the rendered log view empty")
        let configurationAfterClear = app.textFields["Connection configuration"]
        configurationAfterClear.tap()
        configurationAfterClear.typeText("x")
        dismissConfigurationKeyboard()
        app.buttons["Paste"].tap()
        XCTAssertTrue(waitForLogOccurrences(clearSentinel, atLeast: 1, in: logs, timeout: 15),
                      "Following should resume and render the first record written after Clear")
        XCTAssertFalse(app.buttons["Use configuration text…"].exists)
        XCTAssertTrue(app.textFields["Connection configuration"].exists)

        app.terminate()
        app.launch()
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        XCTAssertTrue(app.buttons["VPN connection action"].exists)
        let reopenedLogs = app.textViews["Connection logs"]
        XCTAssertTrue(reopenedLogs.waitForExistence(timeout: 10))
        let reopenedPostClearRecord = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value CONTAINS %@", clearSentinel), object: reopenedLogs
        )
        XCTAssertEqual(XCTWaiter.wait(for: [reopenedPostClearRecord], timeout: 15), .completed,
                       "The post-Clear record should remain available after relaunch")
        XCTAssertEqual(occurrences(of: clearSentinel, in: reopenedLogs.value as? String ?? ""), 1,
                       "Relaunch must retain the post-Clear record without restoring its earlier copies")
        attachScreenshot("reopened")
        try verifyColdAndWarmImports()

    }

    func testLargeTextKeepsLogsAndControlsVisible() {
        app.terminate()
        app.launchArguments = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        UIPasteboard.general.string = "https://example.invalid/large-text"
        app.launch()
        app.launchArguments = []
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        assertLogLayout()
        let controls = app.scrollViews["Connection controls"]
        XCTAssertTrue(controls.waitForExistence(timeout: 10), "The connection controls should have a scrollable viewport")
        let paste = app.buttons["Paste"]
        if !paste.isHittable { controls.swipeDown() }
        XCTAssertTrue(paste.isHittable, "Paste should remain reachable at the largest accessibility text size")
        let connectionAction = app.buttons["VPN connection action"]
        for _ in 0..<8 {
            if connectionAction.isHittable { break }
            controls.swipeUp()
        }
        XCTAssertTrue(connectionAction.isHittable,
                      "The main connection action should remain reachable by scrolling at the largest accessibility text size")
        XCTAssertTrue(app.buttons["Clear"].isHittable)
        XCTAssertTrue(app.buttons["Share logs"].isHittable)
        attachScreenshot("large-text")
    }

    func testCompactLandscapeKeepsConnectionControlsAndLogsReachable() {
        app.terminate()
        UIPasteboard.general.string = "https://example.invalid/landscape"
        XCUIDevice.shared.orientation = .landscapeLeft
        defer { XCUIDevice.shared.orientation = .portrait }
        app.launch()

        let rotated = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            self.app.frame.width > self.app.frame.height
        }, object: app)
        XCTAssertEqual(XCTWaiter.wait(for: [rotated], timeout: 15), .completed,
                       "The Simulator should render the compact landscape layout")
        XCTAssertTrue(app.textFields["Connection configuration"].waitForExistence(timeout: 30))
        assertLogLayout()

        let action = app.buttons["VPN connection action"]
        if !action.isHittable {
            let controls = app.scrollViews["Connection controls"]
            XCTAssertTrue(controls.exists, "The controls should remain in a scrollable viewport on a short screen")
            controls.swipeUp()
        }
        XCTAssertTrue(action.isHittable, "The main connection action should remain reachable after scrolling the controls")
        let logs = app.textViews["Connection logs"]
        XCTAssertGreaterThanOrEqual(logs.frame.height, 50, "The log viewer should retain usable height in landscape")
        XCTAssertTrue(app.buttons["Clear"].isHittable)
        XCTAssertTrue(app.buttons["Share logs"].isHittable)
        XCTAssertLessThanOrEqual(logs.frame.maxY, app.frame.maxY)
        attachScreenshot("compact-landscape")
    }

    func testEmptyAndNonTextClipboardDoesNotFillSubscriptionField() {
        app.terminate()
        UIPasteboard.general.items = []
        app.launch()

        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let original = configuration.value as? String ?? ""
        let paste = app.buttons["Paste"]
        if #available(iOS 16.0, *) {
            XCTAssertTrue(paste.waitForExistence(timeout: 10), "The system Paste control should be available with an empty clipboard")
            if paste.isEnabled { paste.tap() }
            XCTAssertEqual(configuration.value as? String ?? "", original,
                           "An empty clipboard should not change the subscription field")
        } else {
            XCTAssertFalse(paste.exists, "The legacy Paste action should stay hidden when no text is available")
        }

        app.terminate()
        UIPasteboard.general.setData(Data("synthetic image payload".utf8), forPasteboardType: "public.png")
        app.launch()
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let nonTextOriginal = configuration.value as? String ?? ""
        let nonTextPaste = app.buttons["Paste"]
        if #available(iOS 16.0, *) {
            XCTAssertTrue(nonTextPaste.waitForExistence(timeout: 10), "The system Paste control should accept only compatible text")
            if nonTextPaste.isEnabled { nonTextPaste.tap() }
            XCTAssertEqual(configuration.value as? String ?? "", nonTextOriginal,
                           "A non-text clipboard item should not fill the subscription field")
        } else {
            XCTAssertFalse(nonTextPaste.exists, "The legacy Paste action should stay hidden for non-text clipboard items")
        }
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
        defer { XCUIDevice.shared.orientation = .portrait }
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
            validationError, atLeast: expectedErrorCount, in: logs, timeout: 20
        ))

        for _ in 0..<3 {
            configuration.tap()
            configuration.typeText("x")
            dismissConfigurationKeyboard()
            paste.tap()
            XCTAssertTrue(errorStatus.waitForExistence(timeout: 5))
            expectedErrorCount += 1
            XCTAssertTrue(waitForLogOccurrences(
                validationError, atLeast: expectedErrorCount, in: logs, timeout: 20
            ))
        }

        let enoughEntries = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let text = logs.value as? String else { return false }
            return self.occurrences(of: validationError, in: text) >= 4
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [enoughEntries], timeout: 15), .completed)
        let beforeScroll = try XCTUnwrap(logs.value as? String)
        let errorsBeforeScroll = occurrences(of: validationError, in: beforeScroll)
        XCTAssertGreaterThanOrEqual(errorsBeforeScroll, 4)

        let latestLogText = try XCTUnwrap(logs.value as? String)
        XCTAssertTrue(latestLogText.hasSuffix("Details\n"), "The latest structured record should expose its Details link")
        let detailElements = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        let targetError = try XCTUnwrap(
            latestLogText.range(of: validationError, options: .backwards),
            "The latest Paste validation record should remain in the frozen log view"
        )
        let textBeforeTarget = String(latestLogText[..<targetError.lowerBound])
        let targetDetailIndex = occurrences(of: "Details\n", in: textBeforeTarget) +
            occurrences(of: "Hide details\n", in: textBeforeTarget)
        let details = detailElements.element(boundBy: targetDetailIndex)
        XCTAssertTrue(details.waitForExistence(timeout: 10),
                      "The Paste validation record's Details control should be exposed by the log text view")
        XCTAssertEqual(details.label, "Details", "The Paste validation record should be collapsed before tapping")
        XCTAssertTrue(details.isHittable, "The Paste validation Details control should be reachable at the log tail")
        details.tap()
        let detailsExpanded = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "label == %@", "Hide details"),
            object: details
        )
        XCTAssertEqual(XCTWaiter.wait(for: [detailsExpanded], timeout: 10), .completed,
                       "Tapping the Paste validation Details control should expand that record")
        let expandedRecord = XCTNSPredicateExpectation(
            predicate: NSPredicate(
                format: "value CONTAINS %@ OR value CONTAINS %@",
                "\"schema\":\"dobby.log/v1\"",
                "\"schema\":\"dobby.log\\/v1\""
            ),
            object: logs
        )
        XCTAssertEqual(XCTWaiter.wait(for: [expandedRecord], timeout: 10), .completed,
                       "Expanding a log entry should show its original structured record")
        let expandedText = try XCTUnwrap(logs.value as? String)
        XCTAssertTrue(
            expandedText.contains("\"message\":\"\(validationError)\""),
            "The expanded original record must belong to the selected Paste validation message"
        )
        XCTAssertTrue(expandedText.contains("\"event\":\"ui.failure\""))
        XCTAssertTrue(expandedText.contains("\"source\":\"native-ui\""))
        for _ in 0..<6 { logs.swipeUp() }
        let frozen = try XCTUnwrap(logs.value as? String)
        openAbout()
        app.buttons["Done"].tap()
        XCTAssertEqual(logs.value as? String, frozen,
                       "Opening and dismissing About must not change the frozen log entries")
        configuration.tap()
        configuration.typeText("x")
        dismissConfigurationKeyboard()
        paste.tap()
        XCTAssertTrue(errorStatus.waitForExistence(timeout: 5))
        let changedWhileScrolledUp = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value != %@", frozen), object: logs
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [changedWhileScrolledUp], timeout: 1.5), .timedOut,
            "New records should not replace the rendered text while scrolled up"
        )

        XCUIDevice.shared.orientation = .landscapeLeft
        let landscape = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            self.app.frame.width > self.app.frame.height
        }, object: app)
        XCTAssertEqual(XCTWaiter.wait(for: [landscape], timeout: 15), .completed)
        XCTAssertEqual(logs.value as? String, frozen,
                       "A live rotation must not replace frozen log entries")
        XCUIDevice.shared.orientation = .portrait
        let portrait = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            self.app.frame.height > self.app.frame.width
        }, object: app)
        XCTAssertEqual(XCTWaiter.wait(for: [portrait], timeout: 15), .completed)
        XCTAssertEqual(logs.value as? String, frozen,
                       "Returning to portrait must keep the frozen log entries")

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
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 15),
                          "Import should leave the DobbyVPN app in the foreground")
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
        let invalidImportFeedback = app.staticTexts["Deep link import guidance"]
        XCTAssertTrue(invalidImportFeedback.waitForExistence(timeout: 10),
                      "An invalid deep link should keep actionable import guidance visible despite connection errors")
        XCTAssertTrue(invalidImportFeedback.label.contains("Use dobbyvpn://import?url="))
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
