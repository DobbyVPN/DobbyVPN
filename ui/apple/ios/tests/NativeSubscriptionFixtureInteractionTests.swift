import Foundation
import XCTest

/// Rendered subscription coverage supplied by the local Torturer fixture.
/// Ordinary CI has no disposable HTTPS fixture and reports this test as skipped.
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
            let partialFixture = environment[urlKey] != nil || environment[seedKey] != nil
            if partialFixture {
                throw NSError(
                    domain: "DobbyVPN.iOSFixture",
                    code: 1,
                    userInfo: [NSLocalizedDescriptionKey: "Fixture URL or seed was supplied without DOBBY_IOS_TEST_FIXTURE_REQUIRED=1"]
                )
            }
            throw XCTSkip("Torturer did not provide the disposable HTTPS subscription fixture; rendered subscription coverage was skipped.")
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
        paste.tap()
        waitForSourceField(sourceField, value: subscriptionURL)

        let pasteStateElement = try waitForLoadedInventory(source: subscriptionURL)
        let pasteState = try XCTUnwrap(Self.sessionState(from: pasteStateElement))
        assertOneLoadWithoutConnection(pasteState, source: subscriptionURL)

        let autoAction = app.buttons.matching(identifier: "VPN connection action").firstMatch
        XCTAssertTrue(autoAction.waitForExistence(timeout: 10))
        XCTAssertEqual(autoAction.label, "Auto connect")
        XCTAssertTrue(autoAction.isHittable)
        XCTAssertTrue(app.staticTexts["Simulator fixture profile 1"].waitForExistence(timeout: 10))

        let controls = app.scrollViews.matching(identifier: "Connection controls").firstMatch
        XCTAssertTrue(controls.exists)
        let profiles = controls.scrollViews.firstMatch
        XCTAssertTrue(profiles.waitForExistence(timeout: 10), "A separate bounded profile-list viewport should be present")
        XCTAssertLessThanOrEqual(profiles.frame.height, 220, "The profile list should remain bounded")

        // Move in short increments so every source-ordered row passes through
        // the viewport. Validate each rendered description, protocol and
        // Connect action while the logs remain visible below the controls.
        let names = app.staticTexts.matching(
            NSPredicate(format: "label MATCHES %@", #"Simulator fixture profile [0-9]+"#)
        )
        var encountered: [Int] = []
        for _ in 0..<50 {
            for element in names.allElementsBoundByIndex where element.isHittable {
                guard let index = Self.profileIndex(from: element.label), !encountered.contains(index) else { continue }
                encountered.append(index)
                let protocolLabel = app.staticTexts.matching(identifier: "Profile \(index) protocol").firstMatch
                let connect = app.buttons.matching(identifier: "Profile \(index) action").firstMatch
                XCTAssertEqual(protocolLabel.label, "Profile \(index) protocol · OUTLINE")
                XCTAssertEqual(connect.label, "Connect")
                XCTAssertTrue(connect.isHittable)
            }
            if encountered.count == 12 { break }
            let start = profiles.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.78))
            let end = profiles.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.70))
            start.press(forDuration: 0.05, thenDragTo: end)
        }
        XCTAssertEqual(encountered, Array(1...12), "Rendered profiles should appear in source order")
        XCTAssertTrue(app.staticTexts["Simulator fixture profile 12"].isHittable)
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
