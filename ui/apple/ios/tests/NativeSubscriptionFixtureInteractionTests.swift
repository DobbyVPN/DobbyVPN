import Foundation
import XCTest

/// Rendered subscription coverage supplied by the local Torturer fixture.
/// The Simulator runner supplies a run-owned HTTPS fixture for this test.
final class NativeSubscriptionFixtureInteractionTests: XCTestCase {
    private let app = XCUIApplication(bundleIdentifier: "vpn.dobby.app")
    private let environment = ProcessInfo.processInfo.environment
    private var subscriptionURL = ""

    override func setUpWithError() throws {
        continueAfterFailure = false

        let urlKey = "DOBBY_IOS_TEST_SUBSCRIPTION_URL"
        let markerKey = "DOBBY_IOS_TEST_FIXTURE_REQUIRED"
        let seedKey = "DOBBY_SIMULATOR_TEST_SEED_STDERR_CAPTURE"
        guard environment[markerKey] == "1" else {
            throw NSError(
                domain: "DobbyVPN.iOSFixture",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: "Selected rendered subscription coverage requires the Torturer HTTPS fixture"]
            )
        }

        guard let source = environment[urlKey],
              let components = URLComponents(string: source),
              components.scheme?.lowercased() == "https",
              components.host == "127.0.0.1",
              components.user == nil,
              components.password == nil,
              environment[seedKey] == "1" else {
            throw NSError(
                domain: "DobbyVPN.iOSFixture",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: "Selected fixture test requires a loopback HTTPS URL and stderr seed option"]
            )
        }

        // The Torturer marker is created only after it has installed the
        // fixture CA in this run-owned Simulator. URLSession in the app still
        // performs its normal system-trust evaluation.
        subscriptionURL = source
        UIPasteboard.general.string = source
        app.launchEnvironment = [
            markerKey: "1",
            seedKey: "1",
        ]
        app.launch()
    }

    func testAutomaticProfilesAndFixtureRequestCounts() throws {
        guard #available(iOS 16.4, *) else {
            throw XCTSkip("Cold and warm URL activation checks require iOS 16.4 or newer")
        }

        let sourceField = app.textFields["Connection configuration"]
        XCTAssertTrue(sourceField.waitForExistence(timeout: 30))
        let paste = app.buttons["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 10), "The fixture URL should be available through the native Paste control")
        let pasteStartedAt = ProcessInfo.processInfo.systemUptime
        paste.tap()
        waitForSourceField(sourceField, value: subscriptionURL)

        let failedStateElement = app.descendants(matching: .any)
            .matching(identifier: "Simulator test session state").firstMatch
        XCTAssertTrue(failedStateElement.waitForExistence(timeout: 10))
        let failedRequest = XCTNSPredicateExpectation(
            predicate: NSPredicate { element, _ in
                guard let element = element as? XCUIElement,
                      let state = Self.sessionState(from: element) else { return false }
                return (state["request_count"] as? NSNumber)?.intValue == 1
            },
            object: failedStateElement
        )
        XCTAssertEqual(XCTWaiter.wait(for: [failedRequest], timeout: 15), .completed)
        let retry = app.buttons["Retry"]
        XCTAssertTrue(retry.waitForExistence(timeout: 15), "A failed load should offer Retry")
        attachScreenshot("subscription-failure")
        let failedState = try XCTUnwrap(Self.sessionState(from: failedStateElement))
        let requestStartedAt = try XCTUnwrap(
            (failedState["request_started_at_uptime"] as? NSNumber)?.doubleValue,
            "The Simulator test client should report when it started the Paste request"
        )
        let pasteRequestElapsed = requestStartedAt - pasteStartedAt
        XCTAssertLessThan(
            pasteRequestElapsed, 1.5,
            "Explicit Paste should start the request immediately; Configure began after \(pasteRequestElapsed)s"
        )
        XCTAssertGreaterThanOrEqual(pasteRequestElapsed, 0)
        assertFailedLoadWithoutConnection(failedState)
        XCTAssertTrue(
            app.staticTexts.matching(NSPredicate(format: "label CONTAINS[c] %@", "Subscription request failed")).firstMatch.exists,
            "The failed load should explain why profiles did not appear"
        )
        assertNoAutomaticRetry(after: failedState, from: failedStateElement)

        retry.tap()
        let pasteStateElement = try waitForLoadedInventory(source: subscriptionURL)
        let pasteState = try XCTUnwrap(Self.sessionState(from: pasteStateElement))
        assertRetriedLoadWithoutConnection(pasteState, source: subscriptionURL)

        let autoAction = app.buttons.matching(identifier: "VPN connection action").firstMatch
        XCTAssertTrue(autoAction.waitForExistence(timeout: 10))
        XCTAssertEqual(autoAction.label, "Auto connect")
        XCTAssertTrue(autoAction.isHittable)
        XCTAssertTrue(app.staticTexts["Simulator fixture profile 1"].waitForExistence(timeout: 10))
        attachScreenshot("subscription-profiles")

        let controls = app.scrollViews.matching(identifier: "Connection controls").firstMatch
        XCTAssertTrue(controls.exists)
        let profiles = controls.scrollViews.firstMatch
        XCTAssertTrue(profiles.waitForExistence(timeout: 10), "A separate bounded profile-list viewport should be present")
        XCTAssertLessThanOrEqual(profiles.frame.height, 220, "The profile list should remain bounded")

        // The inner list can extend below the clipped outer controls viewport
        // on the compact Simulator. Drag through the blank trailing margin
        // above the child list so hit-testing cannot scroll the child to its
        // tail while we bring the complete viewport into view.
        for _ in 0..<4 {
            let parentViewport = controls.frame
            let profileViewport = profiles.frame
            if profileViewport.maxY <= parentViewport.maxY + 1 { break }

            let startPoint = CGPoint(
                x: parentViewport.maxX - 4,
                y: profileViewport.minY - 4
            )
            let scrollDistance = profileViewport.maxY - parentViewport.maxY + 8
            let endPoint = CGPoint(x: startPoint.x, y: startPoint.y - scrollDistance)
            XCTAssertTrue(parentViewport.contains(startPoint), "The parent-scroll start should lie in the visible Connection controls viewport")
            XCTAssertTrue(parentViewport.contains(endPoint), "The parent-scroll end should lie in the visible Connection controls viewport")
            XCTAssertFalse(profileViewport.contains(startPoint), "The parent-scroll start must stay outside the nested profile list")
            XCTAssertFalse(profileViewport.contains(endPoint), "The parent-scroll end must stay outside the nested profile list")

            let start = controls.coordinate(withNormalizedOffset: CGVector(
                dx: (startPoint.x - parentViewport.minX) / parentViewport.width,
                dy: (startPoint.y - parentViewport.minY) / parentViewport.height
            ))
            let end = controls.coordinate(withNormalizedOffset: CGVector(
                dx: (endPoint.x - parentViewport.minX) / parentViewport.width,
                dy: (endPoint.y - parentViewport.minY) / parentViewport.height
            ))
            start.press(forDuration: 0.05, thenDragTo: end)
        }
        let controlsViewport = controls.frame
        let profileViewport = profiles.frame
        XCTAssertGreaterThanOrEqual(
            profileViewport.minY, controlsViewport.minY - 1,
            "The profile list should start inside the visible Connection controls viewport; profiles=\(profileViewport), controls=\(controlsViewport)"
        )
        XCTAssertLessThanOrEqual(
            profileViewport.maxY, controlsViewport.maxY + 1,
            "The profile list should end inside the visible Connection controls viewport; profiles=\(profileViewport), controls=\(controlsViewport)"
        )
        XCTAssertTrue(profiles.isHittable, "The complete profile viewport should be available for row gestures")
        attachScreenshot("subscription-profiles-visible")

        // Walk through the list in measured, low-speed steps. The default
        // XCTest drag velocity can carry the nested ScrollView past several
        // rows even when the finger moves only about one row. Count a profile
        // only after its description, protocol and Connect action are hittable.
        let names = app.staticTexts.matching(
            NSPredicate(format: "label MATCHES %@", #"Simulator fixture profile (?:[1-9]|1[01])|Profile 12"#)
        )
        var encountered: [Int] = []
        var traversalObservations: [String] = []
        var previousGeometry: String?
        var consecutiveNoMovement = 0
        for step in 0..<50 {
            let viewport = profiles.frame
            var visibleRows: [(index: Int, name: XCUIElement, frame: CGRect)] = []
            for element in names.allElementsBoundByIndex {
                guard let index = Self.profileIndex(from: element.label) else { continue }
                let frame = element.frame
                guard frame.intersects(viewport) else { continue }
                visibleRows.append((index, element, frame))
            }

            var rowObservations: [String] = []
            let geometry = visibleRows.map { row in
                "\(row.index)@\(Int((row.frame.minY * 2).rounded()) / 2)"
            }.joined(separator: ",")
            for row in visibleRows {
                let index = row.index
                let protocolLabel = app.staticTexts.matching(identifier: "Profile \(index) protocol").firstMatch
                let connect = app.buttons.matching(identifier: "Profile \(index) action").firstMatch
                let nameIsHittable = row.name.isHittable
                let protocolIsHittable = protocolLabel.isHittable
                let actionIsHittable = connect.isHittable
                rowObservations.append(
                    "p\(index) name=\(Self.frameDescription(row.frame))/\(nameIsHittable) " +
                    "protocol=\(Self.frameDescription(protocolLabel.frame))/\(protocolIsHittable) " +
                    "action=\(Self.frameDescription(connect.frame))/\(actionIsHittable)"
                )
                guard nameIsHittable, protocolIsHittable, actionIsHittable,
                      !encountered.contains(index) else { continue }
                encountered.append(index)
                XCTAssertEqual(protocolLabel.label, "Profile \(index) protocol · OUTLINE")
                XCTAssertEqual(connect.label, "Connect")
            }
            traversalObservations.append(
                "step=\(step) viewport=\(Self.frameDescription(viewport)) " +
                "rows=[\(rowObservations.joined(separator: "; "))]"
            )
            if encountered.count == 12 { break }
            if geometry == previousGeometry {
                consecutiveNoMovement += 1
            } else {
                consecutiveNoMovement = 0
            }
            previousGeometry = geometry
            if consecutiveNoMovement >= 2 { break }

            let start = profiles.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.80))
            let end = profiles.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.72))
            start.press(
                forDuration: 0.05,
                thenDragTo: end,
                withVelocity: XCUIGestureVelocity(80),
                thenHoldForDuration: 0.10
            )
        }
        XCTAssertEqual(
            encountered,
            Array(1...12),
            "Rendered profiles should appear in source order. Scroll measurements: \(traversalObservations.joined(separator: " | "))"
        )
        XCTAssertTrue(app.staticTexts["Profile 12"].isHittable)
        XCTAssertTrue(app.buttons["Profile 12 action"].isHittable)

        let logs = app.textViews["Connection logs"]
        XCTAssertTrue(logs.waitForExistence(timeout: 10))
        XCTAssertTrue(logs.isHittable)
        XCTAssertGreaterThan(logs.frame.height, 100, "The bounded profile list should leave a useful log viewport")
        XCTAssertTrue(app.buttons["Clear"].isHittable)
        XCTAssertTrue(app.buttons["Share logs"].isHittable)

        let captureVisible = XCTNSPredicateExpectation(
            predicate: NSPredicate { element, _ in
                guard let logs = element as? XCUIElement,
                      let rendered = logs.value as? String else { return false }
                return rendered.contains("Stderr capture initialized")
            },
            object: logs
        )
        XCTAssertEqual(XCTWaiter.wait(for: [captureVisible], timeout: 10), .completed)
        let renderedLogs = try XCTUnwrap(logs.value as? String)
        let completeCaptureRecord = #"(?m)^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}) · INFO · Tunnel stderr · tunnel\nStderr capture initialized(?:\n|$)"#
        XCTAssertNotNil(
            renderedLogs.range(of: completeCaptureRecord, options: .regularExpression),
            "The rendered record should show its timestamp, INFO severity, Tunnel stderr source and initialization message: \(renderedLogs)"
        )

        let deepLink = try importLink(for: subscriptionURL)
        app.terminate()
        app.open(deepLink)
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 15))
        let coldSource = app.textFields["Connection configuration"]
        XCTAssertTrue(coldSource.waitForExistence(timeout: 15))
        waitForSourceField(coldSource, value: subscriptionURL)
        let coldStateElement = try waitForLoadedInventory(source: subscriptionURL)
        let coldState = try XCTUnwrap(Self.sessionState(from: coldStateElement))
        assertOneLoadWithoutConnection(coldState, source: subscriptionURL)

        for _ in 0..<2 {
            let previous = try XCTUnwrap(Self.sessionState(from: coldStateElement))
            XCUIDevice.shared.system.open(deepLink)
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 15))
            let repeatedSource = app.textFields["Connection configuration"]
            XCTAssertTrue(repeatedSource.waitForExistence(timeout: 10))
            waitForSourceField(repeatedSource, value: subscriptionURL)
            assertNoDuplicateLoad(after: previous, from: coldStateElement)
        }
    }

    private func waitForSourceField(_ field: XCUIElement, value: String) {
        let updated = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value == %@", value), object: field
        )
        XCTAssertEqual(XCTWaiter.wait(for: [updated], timeout: 15), .completed)
    }

    private static func frameDescription(_ frame: CGRect) -> String {
        String(format: "(%.1f,%.1f,%.1f,%.1f)", frame.minX, frame.minY, frame.width, frame.height)
    }

    private func waitForLoadedInventory(source: String) throws -> XCUIElement {
        let stateElement = app.descendants(matching: .any)
            .matching(identifier: "Simulator test session state").firstMatch
        XCTAssertTrue(stateElement.waitForExistence(timeout: 10))
        let loaded = XCTNSPredicateExpectation(
            predicate: NSPredicate { element, _ in
                guard let element = element as? XCUIElement,
                      let state = Self.sessionState(from: element) else { return false }
                return state["configured"] as? Bool == true
                    && state["source_url"] as? String == source
                    && (state["profile_count"] as? NSNumber)?.intValue == 12
            },
            object: stateElement
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [loaded], timeout: 30), .completed,
            "Automatic loading did not produce the fixture profiles; state=\(stateElement.value ?? "nil")"
        )
        return stateElement
    }

    private func assertOneLoadWithoutConnection(_ state: [String: Any], source: String) {
        XCTAssertEqual(state["source_url"] as? String, source)
        XCTAssertEqual((state["sequence"] as? NSNumber)?.intValue, 1)
        XCTAssertEqual((state["generation"] as? NSNumber)?.intValue, 0)
        XCTAssertEqual((state["configure_requests"] as? NSNumber)?.intValue, 1)
        XCTAssertEqual((state["request_count"] as? NSNumber)?.intValue, 1)
        XCTAssertEqual((state["start_requests"] as? NSNumber)?.intValue, 0)
    }

    private func assertFailedLoadWithoutConnection(_ state: [String: Any]) {
        XCTAssertEqual(state["configured"] as? Bool, false)
        XCTAssertEqual((state["profile_count"] as? NSNumber)?.intValue, 0)
        XCTAssertEqual((state["sequence"] as? NSNumber)?.intValue, 0)
        XCTAssertEqual((state["generation"] as? NSNumber)?.intValue, 0)
        XCTAssertEqual((state["configure_requests"] as? NSNumber)?.intValue, 1)
        XCTAssertEqual((state["request_count"] as? NSNumber)?.intValue, 1)
        XCTAssertEqual((state["start_requests"] as? NSNumber)?.intValue, 0)
    }

    private func assertNoAutomaticRetry(after previous: [String: Any], from element: XCUIElement) {
        let duplicate = XCTNSPredicateExpectation(
            predicate: NSPredicate { object, _ in
                guard let element = object as? XCUIElement,
                      let current = Self.sessionState(from: element) else { return false }
                return (current["configure_requests"] as? NSNumber)?.intValue
                    != (previous["configure_requests"] as? NSNumber)?.intValue
                    || (current["request_count"] as? NSNumber)?.intValue
                    != (previous["request_count"] as? NSNumber)?.intValue
            },
            object: element
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [duplicate], timeout: 1.6), .timedOut,
            "A failed URL should wait for the user's Retry action instead of fetching again"
        )
        XCTAssertTrue(app.buttons["Retry"].exists)
        let current = Self.sessionState(from: element)
        XCTAssertEqual(current?["configure_requests"] as? NSNumber, previous["configure_requests"] as? NSNumber)
        XCTAssertEqual(current?["request_count"] as? NSNumber, previous["request_count"] as? NSNumber)
    }

    private func assertRetriedLoadWithoutConnection(_ state: [String: Any], source: String) {
        XCTAssertEqual(state["source_url"] as? String, source)
        XCTAssertEqual(state["sequence"] as? NSNumber, 1)
        XCTAssertEqual(state["generation"] as? NSNumber, 0)
        XCTAssertEqual(state["configure_requests"] as? NSNumber, 2)
        XCTAssertEqual(state["request_count"] as? NSNumber, 2)
        XCTAssertEqual(state["start_requests"] as? NSNumber, 0)
    }

    private func assertNoDuplicateLoad(after previous: [String: Any], from element: XCUIElement) {
        let duplicate = XCTNSPredicateExpectation(
            predicate: NSPredicate { object, _ in
                guard let element = object as? XCUIElement,
                      let current = Self.sessionState(from: element) else { return false }
                return (current["configure_requests"] as? NSNumber)?.intValue
                    != (previous["configure_requests"] as? NSNumber)?.intValue
                    || (current["request_count"] as? NSNumber)?.intValue
                    != (previous["request_count"] as? NSNumber)?.intValue
                    || (current["generation"] as? NSNumber)?.intValue
                    != (previous["generation"] as? NSNumber)?.intValue
            },
            object: element
        )
        XCTAssertEqual(
            XCTWaiter.wait(for: [duplicate], timeout: 1.6), .timedOut,
            "Repeated warm delivery should not configure, fetch or connect again"
        )
        let current = Self.sessionState(from: element)
        XCTAssertEqual(current?["session_id"] as? String, previous["session_id"] as? String)
        XCTAssertEqual(current?["sequence"] as? NSNumber, previous["sequence"] as? NSNumber)
        XCTAssertEqual(current?["configure_requests"] as? NSNumber, previous["configure_requests"] as? NSNumber)
        XCTAssertEqual(current?["request_count"] as? NSNumber, previous["request_count"] as? NSNumber)
        XCTAssertEqual(current?["generation"] as? NSNumber, previous["generation"] as? NSNumber)
        XCTAssertEqual(current?["start_requests"] as? NSNumber, previous["start_requests"] as? NSNumber)
    }

    private func importLink(for source: String) throws -> URL {
        var components = URLComponents()
        components.scheme = "dobbyvpn"
        components.host = "import"
        components.queryItems = [URLQueryItem(name: "url", value: source)]
        return try XCTUnwrap(components.url)
    }

    private func attachScreenshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "dobbyvpn-ui-\(name)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private static func sessionState(from element: XCUIElement) -> [String: Any]? {
        guard let value = element.value as? String,
              let data = value.data(using: .utf8) else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }

    private static func profileIndex(from label: String) -> Int? {
        guard let range = label.range(of: #"[0-9]+$"#, options: .regularExpression) else { return nil }
        return Int(label[range])
    }
}
