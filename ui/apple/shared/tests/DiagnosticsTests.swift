import Combine
@testable import DobbyNativeUI
import Foundation
import XCTest

private final class DiagnosticClient: DobbySessionClient, @unchecked Sendable {
    let diagnosticPaths: [URL]
    let version = "1.5.3"
    let sourceCommit = String(repeating: "a", count: 40)
    let uiDiagnosticPath: URL
    init(paths: [URL]) {
        uiDiagnosticPath = paths.last!.deletingLastPathComponent().appendingPathComponent("ui.jsonl")
        diagnosticPaths = paths + [uiDiagnosticPath]
    }
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
    func testLargeExportPreservesEveryByteAndBoundsPreview() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let source = directory.appendingPathComponent("large.jsonl")
        let destination = directory.appendingPathComponent("export.txt")
        // Invalid UTF-8 at chunk boundaries must survive export unchanged.
        let block = Data((0..<65_536).map { UInt8(truncatingIfNeeded: $0) })
        XCTAssertTrue(FileManager.default.createFile(atPath: source.path, contents: nil))
        let writer = try FileHandle(forWritingTo: source)
        for _ in 0..<1024 { try writer.write(contentsOf: block) }
        try writer.close()
        let preview = diagnosticPreview(paths: [source])
        XCTAssertTrue(preview.error.isEmpty)
        XCTAssertLessThan(preview.text.count, 263_000)
        let header = "test metadata\n"
        XCTAssertEqual(try exportDiagnostics(paths: [source], to: destination, header: header), "")
        let reader = try FileHandle(forReadingFrom: destination)
        let prefix = Data((header + "\n--- large.jsonl ---\n").utf8)
        XCTAssertEqual(try reader.read(upToCount: prefix.count), prefix)
        for _ in 0..<1024 { XCTAssertEqual(try reader.read(upToCount: block.count), block) }
        XCTAssertEqual(try reader.read(upToCount: 1)?.count ?? 0, 0)
        try reader.close()
    }

    @MainActor
    func testUIErrorsSurviveModelReplacementAndExportCompletely() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let client = DiagnosticClient(paths: [directory.appendingPathComponent("missing.jsonl")])
        var model: DobbySessionViewModel? = DobbySessionViewModel(client: client)
        for index in 0..<100 {
            model?.reportLogsError("error-\(index):" + String(repeating: "diagnostic λ", count: 1000))
        }
        let saved = expectation(description: "all writes precede export")
        model?.prepareLogsExport { url in
            do { try FileManager.default.removeItem(at: url) } catch { XCTFail(String(describing: error)) }
            saved.fulfill()
        }
        await fulfillment(of: [saved], timeout: 10)
        model = nil
        let replacement = DobbySessionViewModel(client: client)
        let completed = expectation(description: "persisted history")
        replacement.prepareLogsExport { url in
            do {
                let text = try String(contentsOf: url, encoding: .utf8)
                XCTAssertTrue(text.contains("error-0:"))
                XCTAssertTrue(text.contains("error-99:"))
                XCTAssertTrue(text.contains("diagnostic λ"))
                try FileManager.default.removeItem(at: url)
            } catch { XCTFail(String(describing: error)) }
            completed.fulfill()
        }
        await fulfillment(of: [completed], timeout: 10)
        let preview = diagnosticPreview(paths: client.diagnosticPaths)
        XCTAssertFalse(preview.text.contains("error-0:"))
        XCTAssertTrue(preview.text.contains("error-99:"))
    }

}
