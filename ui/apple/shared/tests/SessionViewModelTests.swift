import Combine
@testable import DobbyNativeUI
import Foundation
import XCTest

final class SessionViewModelTests: XCTestCase {
    @MainActor
    func testTypedEditsWaitForDebounceAndOnlyConfigureLatestURL() async throws {
        let fixture = try ViewModelFixture()
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        let first = "https://example.invalid/first"
        let latest = "https://example.invalid/latest"
        model.sourceChanged(first)
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty, "Typed input must still be debounced")

        model.sourceChanged(latest)
        let latestEditAt = Date()
        try await Task.sleep(nanoseconds: 350_000_000)
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty, "A later edit restarts the debounce interval")
        let configured = await waitUntil(timeout: 2) { fixture.client.startedConfigureSources.count == 1 }
        XCTAssertTrue(configured)
        XCTAssertGreaterThanOrEqual(Date().timeIntervalSince(latestEditAt), 0.38)
        XCTAssertEqual(fixture.client.startedConfigureSources, [latest])
        await waitForSnapshot(model) { $0.sourceURL == latest }
        XCTAssertEqual(model.sourceText, latest)
        XCTAssertTrue(model.inventoryReady)
    }

    @MainActor
    func testPasteIsImmediateFencesOldResultAndLeavesStopAndLogsResponsive() async throws {
        let fixture = try ViewModelFixture()
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        let oldSource = "https://example.invalid/old"
        let pastedSource = "https://example.invalid/pasted"
        fixture.client.holdConfigure(oldSource)
        fixture.client.holdConfigure(pastedSource)
        model.sourceChanged(oldSource, immediate: true)
        let oldStarted = await waitUntil(timeout: 2) { fixture.client.startedConfigureSources == [oldSource] }
        XCTAssertTrue(oldStarted)

        model.paste(pastedSource)
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(model.sourceText, pastedSource)
        model.stop()
        let stopped = await waitUntil(timeout: 1) { fixture.client.stopCount == 1 }
        XCTAssertTrue(stopped, "Stop must use its session worker while Configure is held")

        let logsLoaded = expectation(description: "diagnostics refresh while Configure is held")
        let logsSubscription = model.$logEntries.first { $0.contains { $0.message == "during configure" } }
            .sink { _ in logsLoaded.fulfill() }
        model.refreshLogs()
        await fulfillment(of: [logsLoaded], timeout: 2)
        logsSubscription.cancel()

        fixture.client.releaseConfigure(oldSource)
        let pastedStarted = await waitUntil(timeout: 0.35) {
            fixture.client.startedConfigureSources == [oldSource, pastedSource]
        }
        XCTAssertTrue(pastedStarted, "Paste must start the next serialized Configure without a typing debounce")
        XCTAssertEqual(model.sourceText, pastedSource, "The old Configure result must not restore its superseded URL")
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)

        fixture.client.releaseConfigure(pastedSource)
        await waitForSnapshot(model) { $0.sourceURL == pastedSource }
        XCTAssertEqual(model.sourceText, pastedSource)
        XCTAssertTrue(model.inventoryReady)
        XCTAssertEqual(fixture.client.completedConfigureSources, [oldSource, pastedSource])
    }

    @MainActor
    func testRestoredURLLoadsOnceWhenSnapshotHasNoInventory() async throws {
        let source = "https://example.invalid/restored"
        let fixture = try ViewModelFixture(initialSource: source, inventoryConfigured: false)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)

        await waitForSnapshot(model) { $0.configured && $0.sourceURL == source }
        XCTAssertEqual(fixture.client.startedConfigureSources, [source])
        XCTAssertEqual(model.sourceText, source)

        model.refreshSnapshot()
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source], "Unchanged Snapshot polling must not fetch the restored URL again")
    }

    @MainActor
    private func waitForSnapshot(
        _ model: DobbySessionViewModel,
        matching predicate: @escaping (DobbySessionSnapshot) -> Bool
    ) async {
        let received = expectation(description: "matching snapshot")
        let subscription = model.$snapshot.first(where: predicate).sink { _ in received.fulfill() }
        await fulfillment(of: [received], timeout: 5)
        subscription.cancel()
    }
}

@MainActor
private func waitUntil(timeout: TimeInterval, condition: @escaping () -> Bool) async -> Bool {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if condition() { return true }
        try? await Task.sleep(nanoseconds: 10_000_000)
    }
    return condition()
}

private struct ViewModelFixture {
    let directory: URL
    let client: ViewModelTestClient

    init(initialSource: String = "", inventoryConfigured: Bool = true) throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        self.directory = directory
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let backend = directory.appendingPathComponent("backend.jsonl")
        try Data("during configure\n".utf8).write(to: backend)
        self.client = ViewModelTestClient(
            diagnosticPaths: [backend],
            uiDiagnosticPath: directory.appendingPathComponent("ui.jsonl"),
            initialSource: initialSource,
            inventoryConfigured: inventoryConfigured
        )
    }
}

private final class ViewModelTestClient: DobbySessionClient, @unchecked Sendable {
    let diagnosticPaths: [URL]
    let uiDiagnosticPath: URL
    let version = "test"
    let sourceCommit = String(repeating: "a", count: 40)

    private let condition = NSCondition()
    private var sequence: Int64 = 1
    private var configuredSource: String
    private var inventoryConfigured: Bool
    private var heldSources = Set<String>()
    private var releasedSources = Set<String>()
    private var startedSources: [String] = []
    private var completedSources: [String] = []
    private var activeConfigures = 0
    private var maxConcurrentConfigures = 0
    private var stops = 0

    init(diagnosticPaths: [URL], uiDiagnosticPath: URL, initialSource: String, inventoryConfigured: Bool) {
        self.diagnosticPaths = diagnosticPaths
        self.uiDiagnosticPath = uiDiagnosticPath
        self.configuredSource = initialSource
        self.inventoryConfigured = inventoryConfigured
    }

    var startedConfigureSources: [String] {
        condition.lock(); defer { condition.unlock() }
        return startedSources
    }

    var completedConfigureSources: [String] {
        condition.lock(); defer { condition.unlock() }
        return completedSources
    }

    var maximumConcurrentConfigures: Int {
        condition.lock(); defer { condition.unlock() }
        return maxConcurrentConfigures
    }

    var stopCount: Int {
        condition.lock(); defer { condition.unlock() }
        return stops
    }

    func holdConfigure(_ source: String) {
        condition.lock(); defer { condition.unlock() }
        heldSources.insert(source)
    }

    func releaseConfigure(_ source: String) {
        condition.lock(); defer { condition.unlock() }
        releasedSources.insert(source)
        condition.broadcast()
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        switch method {
        case "Snapshot":
            condition.lock()
            let currentSequence = sequence
            let source = configuredSource
            let configured = inventoryConfigured
            condition.unlock()
            return response(["ok": true, "result": [
                "session_id": "test-session", "sequence": currentSequence, "generation": 1,
                "state": "CONNECTED", "primary_action": "STOP", "configured": configured,
                "source_url": source, "source_error": "", "digest": "digest",
                "active_digest": "digest", "active_mode": "AUTO_SELECT", "can_switch": true,
            ]])
        case "Configure":
            let source = parameters["source"] as? String ?? ""
            condition.lock()
            startedSources.append(source)
            activeConfigures += 1
            maxConcurrentConfigures = max(maxConcurrentConfigures, activeConfigures)
            condition.broadcast()
            while heldSources.contains(source) && !releasedSources.contains(source) {
                condition.wait()
            }
            activeConfigures -= 1
            completedSources.append(source)
            configuredSource = source
            inventoryConfigured = true
            sequence += 1
            let resultSequence = sequence
            condition.broadcast()
            condition.unlock()
            return response(["ok": true, "result": ["sequence": resultSequence]])
        case "Stop":
            condition.lock(); stops += 1; condition.broadcast(); condition.unlock()
            return response(["ok": true, "result": [:] as [String: Any]])
        default:
            return response(["ok": true, "result": [:] as [String: Any]])
        }
    }
}

private func response(_ value: [String: Any]) -> String {
    let data = try! JSONSerialization.data(withJSONObject: value)
    return String(decoding: data, as: UTF8.self)
}
