import Combine
import DobbyDiagnosticFiles
@testable import DobbyNativeUI
import Foundation
import XCTest
import zlib

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
        let subscription = model.$logEntries.first { $0.contains { $0.message == "first line" } }.sink { _ in loaded.fulfill() }
        model.refreshLogs()
        await fulfillment(of: [loaded], timeout: 5)
        subscription.cancel()
        let text = String(repeating: "complete fresh diagnostic λ\n", count: 10000)
        try Data(text.utf8).write(to: file)
        let completed = expectation(description: "fresh export")
        model.prepareLogsExport { url in
            do {
                let output = String(decoding: try readGzip(url), as: UTF8.self)
                XCTAssertTrue(output.contains("DobbyVPN 1.5.3"))
                XCTAssertTrue(output.contains("Platform:"))
                XCTAssertTrue(output.hasSuffix(text), model.logsError)
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
                let output = String(decoding: try readGzip(url), as: UTF8.self)
                XCTAssertTrue(output.contains("available diagnostic"))
                XCTAssertTrue(output.contains(directory.path))
                XCTAssertFalse(model.logsError.isEmpty)
                try FileManager.default.removeItem(at: url)
            } catch { XCTFail(String(describing: error)) }
            completed.fulfill()
        }
        await fulfillment(of: [completed], timeout: 5)
    }
    func testMissingDiagnosticsAreNotCollectionFailures() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let missing = directory.appendingPathComponent("missing.jsonl")
        XCTAssertEqual(structuredPreview(paths: [missing], boundary: directory.appendingPathComponent("view")).error, "")
        XCTAssertEqual(try exportDiagnostics(paths: [missing], to: directory.appendingPathComponent("export.gz"), header: ""), "")
    }

    func testLargeExportPreservesEveryByteAndBoundsPreview() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let source = directory.appendingPathComponent("large.jsonl")
        let destination = directory.appendingPathComponent("export.gz")
        // Invalid UTF-8 at chunk boundaries must survive export unchanged.
        let block = Data((0..<65_536).map { UInt8(truncatingIfNeeded: $0) })
        XCTAssertTrue(FileManager.default.createFile(atPath: source.path, contents: nil))
        let writer = try FileHandle(forWritingTo: source)
        for _ in 0..<1024 { try writer.write(contentsOf: block) }
        try writer.close()
        let preview = structuredPreview(paths: [source], boundary: directory.appendingPathComponent("view"))
        XCTAssertTrue(preview.error.isEmpty)
        XCTAssertLessThan(preview.entries.map(\.message).joined().count, 263_000)
        let header = "test metadata\n"
        XCTAssertEqual(try exportDiagnostics(paths: [source], to: destination, header: header), "")
        guard let reader = gzopen(destination.path, "rb") else { return XCTFail("open gzip") }
        defer { XCTAssertEqual(gzclose(reader), Z_OK) }
        let prefix = Data((header + "\n--- large.jsonl ---\n").utf8)
        XCTAssertEqual(try readGzipChunk(reader, count: prefix.count), prefix)
        for _ in 0..<1024 { XCTAssertEqual(try readGzipChunk(reader, count: block.count), block) }
        XCTAssertTrue(try readGzipChunk(reader, count: 1).isEmpty)
    }

    func testRotationMigrationAndBothRetainedGenerations() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("native.jsonl")
        XCTAssertTrue(FileManager.default.createFile(atPath: file.path, contents: nil))
        let writer = try FileHandle(forWritingTo: file)
        try writer.truncate(atOffset: DiagnosticFiles.threshold - 1)
        try writer.seekToEnd()
        try writer.write(contentsOf: Data("\nlegacy-tail\n".utf8))
        try writer.close()
        // A legacy oversized file is streamed into complete-record generations.
        try DiagnosticFiles.append("first", event: "test", level: "INFO", source: "test", to: file)
        let previous = URL(fileURLWithPath: file.path + ".previous")
        let retained = try FileHandle(forReadingFrom: previous)
        XCTAssertEqual(try retained.seekToEnd(), DiagnosticFiles.threshold)
        try retained.close()
        XCTAssertTrue(try String(contentsOf: file, encoding: .utf8).hasPrefix("legacy-tail\n"))
        let next = try FileHandle(forWritingTo: file)
        try next.truncate(atOffset: DiagnosticFiles.threshold)
        try next.close()
        try DiagnosticFiles.append("second", event: "test", level: "INFO", source: "test", to: file)
        let old = try FileHandle(forReadingFrom: previous)
        XCTAssertEqual(try old.read(upToCount: 12), Data("legacy-tail\n".utf8))
        try old.close()
        XCTAssertTrue(try String(contentsOf: file, encoding: .utf8).contains("second"))
        // Keep this export small; the separate 64 MiB test checks streaming.
        try Data("retained prior\n".utf8).write(to: previous)
        let destination = directory.appendingPathComponent("both.gz")
        XCTAssertEqual(try exportDiagnostics(paths: [file], to: destination, header: ""), "")
        let exported = String(decoding: try readGzip(destination), as: UTF8.self)
        XCTAssertTrue(exported.contains("retained prior"))
        XCTAssertTrue(exported.contains("second"))
        XCTAssertThrowsError(try exportDiagnostics(paths: [file], to: destination, header: "overwrite"))
        XCTAssertEqual(String(decoding: try readGzip(destination), as: UTF8.self), exported)
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
                let text = String(decoding: try readGzip(url), as: UTF8.self)
                XCTAssertTrue(text.contains("error-0:"))
                XCTAssertTrue(text.contains("error-99:"))
                XCTAssertTrue(text.contains("diagnostic λ"))
                try FileManager.default.removeItem(at: url)
            } catch { XCTFail(String(describing: error)) }
            completed.fulfill()
        }
        await fulfillment(of: [completed], timeout: 10)
        let preview = structuredPreview(paths: client.diagnosticPaths, boundary: directory.appendingPathComponent("view"))
        XCTAssertFalse(preview.entries.map(\.message).joined().contains("error-0:"))
        XCTAssertTrue(preview.entries.map(\.message).joined().contains("error-99:"))
    }

}

private func readGzipChunk(_ reader: gzFile, count: Int) throws -> Data {
    var buffer = [UInt8](repeating: 0, count: count)
    let size = gzread(reader, &buffer, UInt32(count))
    guard size >= 0 else { throw NSError(domain: "GzipTest", code: Int(size)) }
    return Data(buffer.prefix(Int(size)))
}

private func readGzip(_ path: URL) throws -> Data {
    guard let reader = gzopen(path.path, "rb") else { throw NSError(domain: "GzipTest", code: 1) }
    defer { XCTAssertEqual(gzclose(reader), Z_OK) }
    var data = Data()
    while true {
        let next = try readGzipChunk(reader, count: 65_536)
        if next.isEmpty { return data }
        data.append(next)
    }
}

extension DiagnosticsTests {
    func testStructuredViewClearRotationAndPartialRecords() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let backend = directory.appendingPathComponent("backend.jsonl")
        let stderr = directory.appendingPathComponent("tunnel.stderr")
        let boundary = directory.appendingPathComponent("view.json")
        let late = #"{"timestamp":"2026-10-04T10:00:02Z","level":"WARN","message":"later","detail":"retained"}"#
        let early = #"{"timestamp":"2026-10-04T10:00:01Z","event":"stderr.capture","message":"capture"}"#
        try Data((late + "\n{\"timestamp\":").utf8).write(to: backend)
        try Data((early + "\nraw stack\n  frame\n").utf8).write(to: stderr)
        let preview = DiagnosticFiles.entries(paths: [backend, stderr], boundary: boundary)
        XCTAssertTrue(preview.error.isEmpty)
        XCTAssertEqual(preview.entries.map(\.message), ["Stderr capture initialized", "later", "raw stack", "  frame"])
        XCTAssertEqual(preview.entries[1].raw, late)
        XCTAssertTrue(preview.entries[0].source.contains("Tunnel stderr"))
        try DiagnosticFiles.clearView(paths: [backend, stderr], boundary: boundary)
        try FileManager.default.moveItem(at: backend, to: URL(fileURLWithPath: backend.path + ".previous"))
        try Data("new event\n".utf8).write(to: backend)
        let cleared = DiagnosticFiles.entries(paths: [backend, stderr], boundary: boundary)
        XCTAssertTrue(cleared.error.isEmpty)
        XCTAssertEqual(cleared.entries.map(\.message), ["new event"])
        XCTAssertEqual(try String(contentsOf: stderr, encoding: .utf8), early + "\nraw stack\n  frame\n")
    }
}
