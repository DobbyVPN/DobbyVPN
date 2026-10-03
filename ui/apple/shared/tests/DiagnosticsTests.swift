import Combine
import DobbyNativeUI
import Foundation
import XCTest

private final class DiagnosticClient: DobbySessionClient, @unchecked Sendable {
    let diagnosticPaths: [URL]
    let version = "1.5.3"
    let sourceCommit = String(repeating: "a", count: 40)
    init(paths: [URL]) { diagnosticPaths = paths }
    func call(_ method: String, parameters: [String: Any]) -> String {
        #"{"ok":true,"result":{"session_id":"test","state":"IDLE","primary_action":"START"}}"#
    }
}

final class DiagnosticsTests: XCTestCase {
    @MainActor
    func testExportReadsFreshCompleteFilesAndMetadata() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("backend.jsonl")
        try Data("first line\n".utf8).write(to: file)
        let model = DobbySessionViewModel(client: DiagnosticClient(paths: [file]))
        let loaded = expectation(description: "initial display")
        let subscription = model.$logs.first { $0.contains("first line") }.sink { _ in loaded.fulfill() }
        model.refreshLogs()
        await fulfillment(of: [loaded], timeout: 5)
        subscription.cancel()
        let text = String(repeating: "complete fresh diagnostic λ\n", count: 10000)
        try Data(text.utf8).write(to: file)
        let completed = expectation(description: "fresh export")
        model.prepareLogsExport { url in
            do {
                let output = try String(contentsOf: url, encoding: .utf8)
                XCTAssertTrue(output.contains("DobbyVPN 1.5.3"))
                XCTAssertTrue(output.contains("Platform:"))
                XCTAssertTrue(output.hasSuffix(text))
                try FileManager.default.removeItem(at: url)
            } catch { XCTFail(String(describing: error)) }
            completed.fulfill()
        }
        await fulfillment(of: [completed], timeout: 5)
    }

    @MainActor
    func testExportPreservesReadFailuresAndOtherAvailableFiles() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("backend.jsonl")
        try Data("available diagnostic\n".utf8).write(to: file)
        let model = DobbySessionViewModel(client: DiagnosticClient(paths: [directory, file]))
        let completed = expectation(description: "partial export reports error")
        model.prepareLogsExport { url in
            do {
                let output = try String(contentsOf: url, encoding: .utf8)
                XCTAssertTrue(output.contains("available diagnostic"))
                XCTAssertTrue(output.contains(directory.path))
                XCTAssertFalse(model.logsError.isEmpty)
                try FileManager.default.removeItem(at: url)
            } catch { XCTFail(String(describing: error)) }
            completed.fulfill()
        }
        await fulfillment(of: [completed], timeout: 5)
    }
}
