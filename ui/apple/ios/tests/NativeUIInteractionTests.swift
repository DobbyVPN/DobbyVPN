import XCTest
import UIKit

private struct RenderedLogAnchor {
    let detailIndex: Int
    let record: String
    let element: XCUIElement
}

private final class StableFrameTracker {
    private var previousFrames: [CGRect]?
    private var consecutiveStableSamples = 0

    func reset() {
        previousFrames = nil
        consecutiveStableSamples = 0
    }

    func observe(_ frames: [CGRect]) -> Bool {
        guard frames.allSatisfy({ !$0.isNull && $0.width > 0 && $0.height > 0 }) else {
            reset()
            return false
        }
        if let previousFrames,
           previousFrames.count == frames.count,
           zip(previousFrames, frames).allSatisfy({ pair in
               let (previous, current) = pair
               return abs(previous.minX - current.minX) <= 0.5
                   && abs(previous.minY - current.minY) <= 0.5
                   && abs(previous.width - current.width) <= 0.5
                   && abs(previous.height - current.height) <= 0.5
           }) {
            consecutiveStableSamples += 1
        } else {
            consecutiveStableSamples = 0
        }
        previousFrames = frames
        return consecutiveStableSamples >= 2
    }
}

final class NativeUIInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")
    private var capturesLogFreezeScreenshot = false

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    override func tearDownWithError() throws {
        if capturesLogFreezeScreenshot { attachScreenshot("logs-freeze-teardown") }
        try super.tearDownWithError()
    }

    func testNativeConnectionAboutAndLogs() throws {
        app.terminate()
        app.launch()

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
        XCTAssertTrue(
            app.staticTexts["Disconnected"].waitForExistence(timeout: 10),
            "An unsupported typed URL should remain disconnected without starting a fetch or connection"
        )
        attachScreenshot("invalid-source")
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
        let testBundle = Bundle(for: NativeUIInteractionTests.self)
        let expectedCommit = try XCTUnwrap(
            testBundle.object(forInfoDictionaryKey: "DobbyTestSourceCommit") as? String,
            "The XCTest bundle should contain the selected product revision; " +
                "bundle=\(testBundle.bundleURL.path) " +
                "identifier=\(testBundle.bundleIdentifier ?? "nil") " +
                "keys=\((testBundle.infoDictionary?.keys.sorted().joined(separator: ",")) ?? "none")"
        )
        XCTAssertEqual(commit, expectedCommit, "About should display the exact revision tested by this lane")
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
        XCTAssertEqual(XCTWaiter.wait(for: [populated], timeout: 10), .completed, "Retained records should be visible before clearing")
        let clearSentinel = "Paste an HTTPS subscription URL with a host"
        UIPasteboard.general.string = "http://example.invalid/clear-sentinel"
        let sentinelCount = occurrences(of: clearSentinel, in: logs.value as? String ?? "")
        app.buttons["Paste"].tap()
        XCTAssertTrue(waitForLogOccurrences(clearSentinel, atLeast: sentinelCount + 1, in: logs, timeout: 20))
        var expectedClearSentinelCount = sentinelCount + 1
        // Seed enough real log text through the existing Paste validation path
        // for the About/logs scroll check to exercise a genuine scroll range.
        for _ in 0..<7 {
            configuration.tap()
            configuration.typeText("x")
            dismissConfigurationKeyboard()
            expectedClearSentinelCount += 1
            app.buttons["Paste"].tap()
            XCTAssertTrue(waitForLogOccurrences(
                clearSentinel, atLeast: expectedClearSentinelCount, in: logs, timeout: 20
            ))
        }
        let renderedBeforeClear = try XCTUnwrap(logs.value as? String)
        let preClearRecords = renderedBeforeClear.components(separatedBy: "Details\n").filter { !$0.isEmpty }
        XCTAssertGreaterThanOrEqual(
            occurrences(of: clearSentinel, in: renderedBeforeClear),
            sentinelCount + 8,
            "The deterministic Paste rows should provide a real log scroll range"
        )
        XCTAssertFalse(preClearRecords.isEmpty, "There should be rendered records for Clear to remove")
        let clearAnchor = try XCTUnwrap(
            visibleLogAnchor(in: logs),
            "A rendered log row should anchor the reading position before Clear"
        )
        let clearAnchorOffsetBeforeScroll = clearAnchor.element.frame.minY - logs.frame.minY
        let clearAnchorOffset = scrollLogsAwayFromBottom(
            logs,
            anchor: clearAnchor,
            diagnosticPrefix: "logs-clear",
            // The prior 15% drag crossed the follow threshold briefly, then
            // decelerated back to the bottom. Use the measured 25% endpoint.
            dragEndY: 0.70,
            screenshotName: "logs-clear-scroll-attempt"
        )
        XCTAssertTrue(logs.value as? String == renderedBeforeClear,
                      "Scrolling to an older record should not change the rendered entries")
        XCTAssertTrue(elementIsVisible(clearAnchor.element, in: logs),
                      "The anchored rendered row should remain inside the log viewport")
        XCTAssertGreaterThan(clearAnchorOffset, clearAnchorOffsetBeforeScroll + 24,
                             "The reading position should be away from the bottom before Clear")
        app.buttons["Clear"].tap()
        let cleared = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let rendered = logs.value as? String else { return false }
            return rendered.isEmpty && preClearRecords.allSatisfy { !rendered.contains($0) }
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [cleared], timeout: 15), .completed,
                       "Clear should immediately empty the view and remove every record rendered before the action")
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
        XCTAssertFalse((reopenedLogs.value as? String ?? "").contains(clearAnchor.record),
                       "A specific rendered pre-Clear record must stay absent after relaunch")
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
        capturesLogFreezeScreenshot = true
        defer { XCUIDevice.shared.orientation = .portrait }
        let clipboard = "http://example.invalid/repeat-error"
        let validationError = "Paste an HTTPS subscription URL with a host"
        app.terminate()
        UIPasteboard.general.string = clipboard
        app.launchEnvironment = ["DOBBY_IOS_TEST_LOG_SCROLL_TRACE": "1"]
        app.launch()

        let configuration = app.textFields["Connection configuration"]
        XCTAssertTrue(configuration.waitForExistence(timeout: 30))
        let paste = app.buttons["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 10))
        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        let scrollTrace = app.descendants(matching: .any)
            .matching(identifier: "Log scroll diagnostics").firstMatch
        XCTAssertTrue(scrollTrace.waitForExistence(timeout: 10))
        let controls = app.descendants(matching: .any)
            .matching(identifier: "Connection controls").firstMatch
        let errorStatus = app.staticTexts["Error"]
        var expectedErrorCount = occurrences(of: validationError, in: logs.value as? String ?? "")
        paste.tap()
        XCTAssertTrue(errorStatus.waitForExistence(timeout: 10))
        expectedErrorCount += 1
        XCTAssertTrue(waitForLogOccurrences(
            validationError, atLeast: expectedErrorCount, in: logs, timeout: 20
        ))

        // The compact Simulator viewport needs enough records to produce a real scroll range.
        for _ in 0..<7 {
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
            return self.occurrences(of: validationError, in: text) >= 8
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [enoughEntries], timeout: 15), .completed)
        let beforeScroll = try XCTUnwrap(logs.value as? String)
        let errorsBeforeScroll = occurrences(of: validationError, in: beforeScroll)
        XCTAssertGreaterThanOrEqual(errorsBeforeScroll, 8)

        let latestLogText = try XCTUnwrap(logs.value as? String)
        XCTAssertTrue(latestLogText.hasSuffix("Details\n"), "The latest structured record should expose its Details link")
        let detailElements = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        let targetRecord = try XCTUnwrap(
            renderedLogRecord(containing: validationError, in: latestLogText),
            "The latest Paste validation record should remain in the frozen log view"
        )
        let targetDetailIndex = targetRecord.detailIndex
        let renderedErrorLines = targetRecord.record.components(separatedBy: "\n")
        XCTAssertTrue(renderedErrorLines.first?.hasSuffix(" · ERROR · App · native-ui") == true,
                      "The selected validation message should follow its timestamp, severity, and source header; got: \(targetRecord.record)")
        XCTAssertTrue(renderedErrorLines.contains(validationError),
                      "The Details link should belong to the selected standalone validation message")
        let details = detailElements.element(boundBy: targetDetailIndex)
        XCTAssertTrue(details.waitForExistence(timeout: 10),
                      "The Paste validation record's Details control should be exposed by the log text view")
        XCTAssertEqual(details.label, "Details", "The Paste validation record should be collapsed before tapping")
        for _ in 0..<8 {
            if details.isHittable { break }
            let start = logs.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.30))
            let end = logs.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.65))
            start.press(forDuration: 0.1, thenDragTo: end)
        }
        XCTAssertTrue(details.isHittable,
                      "The selected Paste validation Details control should be reachable after scrolling its row into view")
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

        let detailsCollapsed = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "label == %@", "Details"), object: details
        )
        details.tap()
        XCTAssertEqual(XCTWaiter.wait(for: [detailsCollapsed], timeout: 10), .completed,
                       "The original record should collapse before checking the reading position")
        attachScreenshot("logs-freeze-ready")
        let renderedBeforeFreeze = try XCTUnwrap(logs.value as? String)
        XCTAssertTrue(elementIsVisible(details, in: logs),
                      "The selected Paste record's Details link should remain in the rendered log viewport after collapsing")
        let positionAnchor = try XCTUnwrap(
            visibleLogAnchor(in: logs),
            "A recent collapsed record should remain visible above the bottom of the log viewport"
        )
        XCTAssertTrue(renderedBeforeFreeze.contains(positionAnchor.record),
                      "The anchor should identify a specific rendered log record")
        let anchorOffsetBeforeScroll = positionAnchor.element.frame.minY - logs.frame.minY
        let anchorOffsetAfterScroll = scrollLogsAwayFromBottom(
            logs,
            anchor: positionAnchor,
            diagnosticPrefix: "logs-freeze",
            dragEndY: 0.95
        )
        XCTAssertGreaterThan(anchorOffsetAfterScroll, anchorOffsetBeforeScroll + 24,
                             "The gesture should move the identifiable record away from the bottom")
        attachScreenshot("logs-freeze-scrolled")
        XCTAssertEqual(logs.value as? String, renderedBeforeFreeze,
                       "Scrolling should preserve the rendered log entries")
        let frozen = try XCTUnwrap(logs.value as? String)
        assertFrozenLogTextIsSelectable(in: logs, frozenText: frozen, message: validationError)

        let anchorOffsetBeforeConfiguration = positionAnchor.element.frame.minY - logs.frame.minY
        configuration.tap()
        configuration.typeText("x")
        dismissConfigurationKeyboard()
        XCTAssertEqual(logs.value as? String, frozen,
                       "Editing the configuration should not move the frozen log view")
        XCTAssertTrue(elementIsVisible(positionAnchor.element, in: logs),
                      "The same log record should remain visible after dismissing the configuration keyboard")
        XCTAssertEqual(
            positionAnchor.element.frame.minY - logs.frame.minY,
            anchorOffsetBeforeConfiguration,
            accuracy: 2,
            "Showing and dismissing the configuration keyboard must preserve the frozen reading position"
        )
        let anchorOffsetBeforeRefresh = positionAnchor.element.frame.minY - logs.frame.minY
        paste.tap()
        let refreshOpportunity = expectation(description: "A foreground log refresh elapses while scrolled up")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { refreshOpportunity.fulfill() }
        wait(for: [refreshOpportunity], timeout: 2.0)
        attachScreenshot("logs-freeze-after-refresh")
        XCTAssertEqual(logs.value as? String, frozen,
                       "The refreshed log source should stay frozen while the reader is away from the bottom")
        XCTAssertTrue(elementIsVisible(positionAnchor.element, in: logs),
                      "The same rendered record should remain visible after a refresh while scrolled up")
        XCTAssertEqual(
            positionAnchor.element.frame.minY - logs.frame.minY,
            anchorOffsetBeforeRefresh,
            accuracy: 2,
            "A log refresh must preserve the visible reading position"
        )

        openAbout()
        app.buttons["Done"].tap()
        XCTAssertEqual(logs.value as? String, frozen,
                       "Opening and dismissing About must not change the frozen log entries")
        XCTAssertTrue(elementIsVisible(positionAnchor.element, in: logs),
                      "The same rendered record should remain visible after About is dismissed")
        XCTAssertEqual(
            positionAnchor.element.frame.minY - logs.frame.minY,
            anchorOffsetBeforeRefresh,
            accuracy: 2,
            "About should preserve the visible reading position"
        )

        let readingAnchor = try XCTUnwrap(
            topmostVisibleLogAnchor(in: logs),
            "The current reading row should be identifiable before rotation"
        )
        let readingAnchorOffsetBeforeRotation = readingAnchor.element.frame.minY - logs.frame.minY
        XCUIDevice.shared.orientation = .landscapeLeft
        let landscape = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            self.app.frame.width > self.app.frame.height
        }, object: app)
        XCTAssertEqual(XCTWaiter.wait(for: [landscape], timeout: 15), .completed)
        XCTAssertEqual(logs.value as? String, frozen,
                       "A live rotation must not replace frozen log entries")
        XCTAssertTrue(frozen.contains(readingAnchor.record))
        XCTAssertTrue(frozen.contains(positionAnchor.record))
        let landscapeFrameTracker = StableFrameTracker()
        let landscapeLayout = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            guard self.app.frame.width > self.app.frame.height,
                  let anchor = self.detailElement(for: readingAnchor, in: logs), anchor.exists else {
                landscapeFrameTracker.reset()
                return false
            }
            return landscapeFrameTracker.observe([self.app.frame, logs.frame, anchor.frame])
        }, object: app)
        let landscapeLayoutResult = XCTWaiter.wait(for: [landscapeLayout], timeout: 12)
        let landscapeAnchor = detailElement(for: readingAnchor, in: logs)
        let landscapeVisible = landscapeAnchor.map { elementIsVisible($0, in: logs) } ?? false
        let landscapeGeometry = """
        stableFrames=\(landscapeLayoutResult == .completed)
        appFrame=\(app.frame)
        controlsFrame=\(controls.frame)
        textViewFrame=\(logs.frame)
        logHeaderFrame=\(app.staticTexts["Logs"].frame)
        preScrollDetailIndex=\(positionAnchor.detailIndex)
        readingAnchorDetailIndex=\(readingAnchor.detailIndex)
        anchorFrame=\(String(describing: landscapeAnchor?.frame))
        anchorVisible=\(landscapeVisible)
        """
        let geometryAttachment = XCTAttachment(string: landscapeGeometry)
        geometryAttachment.name = "log-freeze-landscape-geometry"
        geometryAttachment.lifetime = .keepAlways
        add(geometryAttachment)
        attachScreenshot("logs-freeze-landscape")
        XCTAssertEqual(landscapeLayoutResult, .completed,
                       "The log pane and rendered row should settle after rotation. Geometry: \(landscapeGeometry)")
        XCTAssertTrue(landscapeVisible,
                      "The current reading record should stay visible in landscape. Geometry: \(landscapeGeometry)")
        if let landscapeAnchor {
            let maximumVisibleOffset = max(0, logs.frame.height - landscapeAnchor.frame.height)
            XCTAssertEqual(
                landscapeAnchor.frame.minY - logs.frame.minY,
                min(max(readingAnchorOffsetBeforeRotation, 0), maximumVisibleOffset),
                accuracy: 2,
                "Landscape should preserve the current reading row's viewport position"
            )
        }
        XCUIDevice.shared.orientation = .portrait
        let portrait = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            self.app.frame.height > self.app.frame.width
        }, object: app)
        XCTAssertEqual(XCTWaiter.wait(for: [portrait], timeout: 15), .completed)
        XCTAssertEqual(logs.value as? String, frozen,
                       "Returning to portrait must keep the frozen log entries")
        XCTAssertTrue(frozen.contains(readingAnchor.record))
        attachScreenshot("logs-freeze-returned-to-portrait")
        let portraitAnchor = try XCTUnwrap(detailElement(for: readingAnchor, in: logs))
        XCTAssertTrue(elementIsVisible(portraitAnchor, in: logs),
                      "The current reading record should stay visible after returning to portrait")
        XCTAssertEqual(
            portraitAnchor.frame.minY - logs.frame.minY,
            readingAnchorOffsetBeforeRotation,
            accuracy: 2,
            "Returning to portrait should restore the current reading row's viewport position"
        )

        for _ in 0..<8 { logs.swipeUp() }
        let resumed = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let logs = element as? XCUIElement, let text = logs.value as? String else { return false }
            return self.occurrences(of: validationError, in: text) > errorsBeforeScroll
        }, object: logs)
        XCTAssertEqual(XCTWaiter.wait(for: [resumed], timeout: 10), .completed, "Returning to the bottom should resume new log entries")

        let traceAttachment = XCTAttachment(string: scrollTrace.value as? String ?? "<empty UIKit scroll trace>")
        traceAttachment.name = "dobbyvpn-ui-uikit-scroll-trace"
        traceAttachment.lifetime = .keepAlways
        add(traceAttachment)
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

    private func assertFrozenLogTextIsSelectable(
        in logs: XCUIElement,
        frozenText: String,
        message: String
    ) {
        // Skip rows clipped by the viewport: long-pressing their accessibility
        // centers can land on a Details link and expand the record.
        let messageLines = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", message))
            .allElementsBoundByIndex
        let viewportFrame = logs.frame.insetBy(dx: 1, dy: 1)
        guard let messageLine = messageLines.first(where: {
            $0.elementType == .textView && $0.isHittable && viewportFrame.contains($0.frame)
        }) else {
            let candidates = messageLines.enumerated().map { index, element in
                "\(index): hittable=\(element.isHittable), frame=\(element.frame)"
            }.joined(separator: "; ")
            XCTFail(
                "No fully visible non-link log message row was available for selection. " +
                    "Viewport: \(viewportFrame). Candidates: \(candidates)"
            )
            return
        }
        let selectedFrame = messageLine.frame
        messageLine.press(forDuration: 1.0)
        attachScreenshot("logs-freeze-selection-menu")
        XCTAssertEqual(logs.value as? String, frozenText,
                       "Long-pressing a message row must not expand Details or change frozen entries. " +
                           "Selected row: \(selectedFrame); viewport: \(viewportFrame)")
        let copyActionInApp = app.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", "Copy")).firstMatch
        let systemUI = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let copyActionInSystemUI = systemUI.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", "Copy")).firstMatch
        let copyAction: XCUIElement
        if copyActionInApp.waitForExistence(timeout: 3) {
            copyAction = copyActionInApp
        } else if copyActionInSystemUI.waitForExistence(timeout: 2) {
            copyAction = copyActionInSystemUI
        } else {
            XCTFail(
                "A long press on frozen log text should expose the native Copy action. " +
                    "App Copy visible: \(copyActionInApp.exists); system Copy visible: \(copyActionInSystemUI.exists). " +
                    "App hierarchy: \(app.debugDescription.prefix(5000)); " +
                    "system hierarchy: \(systemUI.debugDescription.prefix(2500))"
            )
            return
        }
        copyAction.tap()
        XCTAssertEqual(logs.value as? String, frozenText,
                       "Selecting and copying log text must not replace the frozen entries")
    }

    private func visibleLogAnchor(in logs: XCUIElement) -> RenderedLogAnchor? {
        guard let rendered = logs.value as? String, logs.frame.height > 0 else { return nil }
        let details = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        let detailCount = details.count
        // At the bottom-following position, only recent rows can intersect the viewport.
        // Querying every retained link makes XCUI traverse dozens of offscreen records.
        let latestDetails = (max(0, detailCount - 12)..<detailCount).reversed()
        let candidates = latestDetails.compactMap { index -> (anchor: RenderedLogAnchor, relativeY: CGFloat)? in
            let element = details.element(boundBy: index)
            guard element.label == "Details", elementIsVisible(element, in: logs),
                  let record = renderedLogRecord(atDetailIndex: index, in: rendered) else { return nil }
            let relativeY = (element.frame.midY - logs.frame.minY) / logs.frame.height
            guard (0.05...0.45).contains(relativeY) else { return nil }
            return (RenderedLogAnchor(detailIndex: index, record: record, element: element), relativeY)
        }
        return candidates.min {
            abs($0.relativeY - 0.20) < abs($1.relativeY - 0.20)
        }?.anchor
    }

    private func topmostVisibleLogAnchor(in logs: XCUIElement) -> RenderedLogAnchor? {
        guard let rendered = logs.value as? String, logs.frame.height > 0 else { return nil }
        let details = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        let candidates = (0..<details.count).compactMap { index -> RenderedLogAnchor? in
            let element = details.element(boundBy: index)
            guard element.label == "Details", elementIsVisible(element, in: logs),
                  let record = renderedLogRecord(atDetailIndex: index, in: rendered) else { return nil }
            return RenderedLogAnchor(detailIndex: index, record: record, element: element)
        }
        return candidates.min { $0.element.frame.minY < $1.element.frame.minY }
    }

    @discardableResult
    private func scrollLogsAwayFromBottom(
        _ logs: XCUIElement,
        anchor: RenderedLogAnchor,
        diagnosticPrefix: String,
        dragEndY: CGFloat = 0.60,
        screenshotName: String? = nil
    ) -> CGFloat {
        let initialAnchor = detailElement(for: anchor, in: logs) ?? anchor.element
        let initialAnchorFrame = initialAnchor.frame
        let initialViewportFrame = logs.frame
        let offsetBefore = initialAnchorFrame.minY - initialViewportFrame.minY
        // The freeze case needs a larger drag; its anchor is selected near the top.
        let start = logs.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.45))
        let end = logs.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: dragEndY))
        // A default-speed drag decelerates to the top of this short log pane,
        // which can move a valid visible anchor completely out of view.
        start.press(
            forDuration: 0.1,
            thenDragTo: end,
            withVelocity: .slow,
            thenHoldForDuration: 0.2
        )
        if let screenshotName = screenshotName { attachScreenshot(screenshotName) }
        let currentAnchor = detailElement(for: anchor, in: logs) ?? anchor.element
        let currentAnchorFrame = currentAnchor.frame
        let currentViewportFrame = logs.frame
        let offsetAfter = currentAnchorFrame.minY - currentViewportFrame.minY
        let movedPastFollowThreshold = offsetAfter > offsetBefore + 24
        let currentAnchorIsVisible = frameIsVisible(currentAnchorFrame, in: currentViewportFrame)
        let renderedLogText = logs.value as? String ?? ""
        let anchorRecordIsPresent = renderedLogText.contains(anchor.record)
        if !movedPastFollowThreshold {
            attachScrollFailureDiagnostics(
                "movement-at-or-below-24pt",
                anchor: anchor,
                beforeAnchorFrame: initialAnchorFrame,
                afterAnchorFrame: currentAnchorFrame,
                beforeViewportFrame: initialViewportFrame,
                afterViewportFrame: currentViewportFrame,
                offsetBefore: offsetBefore,
                offsetAfter: offsetAfter,
                anchorIsVisible: currentAnchorIsVisible,
                anchorRecordIsPresent: anchorRecordIsPresent,
                logs: logs,
                diagnosticPrefix: diagnosticPrefix
            )
        }
        if !currentAnchorIsVisible {
            attachScrollFailureDiagnostics(
                "anchor-not-visible",
                anchor: anchor,
                beforeAnchorFrame: initialAnchorFrame,
                afterAnchorFrame: currentAnchorFrame,
                beforeViewportFrame: initialViewportFrame,
                afterViewportFrame: currentViewportFrame,
                offsetBefore: offsetBefore,
                offsetAfter: offsetAfter,
                anchorIsVisible: currentAnchorIsVisible,
                anchorRecordIsPresent: anchorRecordIsPresent,
                logs: logs,
                diagnosticPrefix: diagnosticPrefix
            )
        }
        if !anchorRecordIsPresent {
            attachScrollFailureDiagnostics(
                "record-absent",
                anchor: anchor,
                beforeAnchorFrame: initialAnchorFrame,
                afterAnchorFrame: currentAnchorFrame,
                beforeViewportFrame: initialViewportFrame,
                afterViewportFrame: currentViewportFrame,
                offsetBefore: offsetBefore,
                offsetAfter: offsetAfter,
                anchorIsVisible: currentAnchorIsVisible,
                anchorRecordIsPresent: anchorRecordIsPresent,
                logs: logs,
                diagnosticPrefix: diagnosticPrefix
            )
        }
        XCTAssertGreaterThan(offsetAfter, offsetBefore + 24,
                             "The downward drag should move the reader more than the follow threshold")
        XCTAssertTrue(elementIsVisible(currentAnchor, in: logs),
                      "The same log row should remain visible after scrolling away from the bottom")
        XCTAssertTrue((logs.value as? String ?? "").contains(anchor.record),
                      "The anchored record should remain in the rendered log text")
        return offsetAfter
    }

    private func attachScrollFailureDiagnostics(
        _ failureCondition: String,
        anchor: RenderedLogAnchor,
        beforeAnchorFrame: CGRect,
        afterAnchorFrame: CGRect,
        beforeViewportFrame: CGRect,
        afterViewportFrame: CGRect,
        offsetBefore: CGFloat,
        offsetAfter: CGFloat,
        anchorIsVisible: Bool,
        anchorRecordIsPresent: Bool,
        logs: XCUIElement,
        diagnosticPrefix: String
    ) {
        let attachmentName = "\(diagnosticPrefix)-scroll-\(failureCondition)"
        attachScreenshot("\(attachmentName)-screenshot")
        let details = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        let visibleDetailsFrames = (0..<details.count).compactMap { index -> String? in
            let element = details.element(boundBy: index)
            let frame = element.frame
            guard element.label == "Details" || element.label == "Hide details",
                  !frame.isNull, frame.width > 0, frame.height > 0 else { return nil }
            let visibleFrame = frame.intersection(afterViewportFrame)
            guard !visibleFrame.isNull, visibleFrame.width > 0, visibleFrame.height > 0 else { return nil }
            return "\(element.label): frame=\(frame), visibleFrame=\(visibleFrame)"
        }
        let renderedLogText = logs.value as? String ?? "<no rendered log text>"
        let visibleFrames = visibleDetailsFrames.isEmpty
            ? "<no visible Details or Hide details descendants>"
            : visibleDetailsFrames.joined(separator: "\n")
        let scrollTrace = app.descendants(matching: .any)
            .matching(identifier: "Log scroll diagnostics").firstMatch
        let nativeScrollTrace = scrollTrace.exists
            ? scrollTrace.value as? String ?? "<empty UIKit scroll trace>"
            : "<UIKit scroll trace disabled>"
        let diagnosticText = """
        Case: \(attachmentName)
        Failed condition: \(failureCondition)

        Anchored record:
        \(anchor.record)

        Anchor frame before drag: \(beforeAnchorFrame)
        Anchor frame after drag: \(afterAnchorFrame)
        Viewport frame before drag: \(beforeViewportFrame)
        Viewport frame after drag: \(afterViewportFrame)
        Anchor offset before drag: \(offsetBefore)
        Anchor offset after drag: \(offsetAfter)
        Movement exceeded 24pt: \(offsetAfter > offsetBefore + 24)
        Anchor visible: \(anchorIsVisible)
        Anchored record present in rendered text: \(anchorRecordIsPresent)

        Visible Details / Hide details descendant frames:
        \(visibleFrames)

        UIKit scroll trace:
        \(nativeScrollTrace)

        Rendered log text:
        \(renderedLogText)
        """
        let attachment = XCTAttachment(string: diagnosticText)
        attachment.name = "dobbyvpn-ui-\(attachmentName)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func elementIsVisible(_ element: XCUIElement, in logs: XCUIElement) -> Bool {
        frameIsVisible(element.frame, in: logs.frame)
    }

    private func frameIsVisible(_ frame: CGRect, in viewportFrame: CGRect) -> Bool {
        guard !frame.isNull, frame.width > 0, frame.height > 0 else { return false }
        let visibleFrame = frame.intersection(viewportFrame)
        return !visibleFrame.isNull && visibleFrame.width > 0 && visibleFrame.height > 0
    }

    private func detailElement(for anchor: RenderedLogAnchor, in logs: XCUIElement) -> XCUIElement? {
        guard let rendered = logs.value as? String else { return nil }
        let details = logs.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@ OR label == %@", "Details", "Hide details"))
        for index in 0..<details.count {
            guard renderedLogRecord(atDetailIndex: index, in: rendered) == anchor.record else { continue }
            return details.element(boundBy: index)
        }
        return nil
    }

    private func renderedLogRecord(atDetailIndex index: Int, in text: String) -> String? {
        guard index >= 0 else { return nil }
        let lines = text.components(separatedBy: "\n")
        let links = lines.indices.filter { isDetailsControlLine(lines[$0]) }
        guard links.indices.contains(index) else { return nil }
        let linkIndex = links[index]
        guard let headerIndex = (0..<linkIndex).last(where: { isTimestampHeader(lines[$0]) }) else { return nil }
        return lines[headerIndex..<linkIndex].joined(separator: "\n").trimmingCharacters(in: .newlines)
    }

    private func renderedLogRecord(
        containing message: String,
        in text: String
    ) -> (detailIndex: Int, record: String)? {
        let lines = text.components(separatedBy: "\n")
        guard let messageIndex = lines.lastIndex(of: message),
              let headerIndex = (0..<messageIndex).last(where: { isTimestampHeader(lines[$0]) }) else { return nil }

        let nextHeader = ((messageIndex + 1)..<lines.count).first(where: { isTimestampHeader(lines[$0]) })
        let recordEnd = nextHeader ?? lines.count
        guard let linkIndex = ((messageIndex + 1)..<recordEnd).first(where: { isDetailsControlLine(lines[$0]) }) else {
            return nil
        }
        let detailIndex = lines[..<linkIndex].filter(isDetailsControlLine).count
        let record = lines[headerIndex..<linkIndex].joined(separator: "\n").trimmingCharacters(in: .newlines)
        return (detailIndex, record)
    }

    private func isTimestampHeader(_ line: String) -> Bool {
        line.range(
            of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z · "#,
            options: .regularExpression
        ) != nil
    }

    private func isDetailsControlLine(_ line: String) -> Bool {
        line == "Details" || line == "Hide details"
    }
}
