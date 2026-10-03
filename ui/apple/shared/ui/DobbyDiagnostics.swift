import Foundation

private let previewBytes = 262_144

// The diagnostics worker serializes UI writes, previews, and exports.
func appendUIDiagnostic(_ message: String, to url: URL) throws {
    try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    if !FileManager.default.fileExists(atPath: url.path) {
        guard FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600]) else {
            throw DobbyClientError.diagnostics("Could not create \(url.path)")
        }
    }
    let record: [String: String] = [
        "schema": "dobby.log/v1", "timestamp": ISO8601DateFormatter().string(from: Date()),
        "source": "native-ui", "level": "ERROR", "event": "ui.failure", "message": message,
    ]
    var data = try JSONSerialization.data(withJSONObject: record)
    data.append(0x0A)
    let file = try FileHandle(forWritingTo: url)
    do {
        try file.seekToEnd()
        try file.write(contentsOf: data)
    } catch {
        let original = error
        do { try file.close() } catch {
            throw DobbyClientError.diagnostics("\(String(reflecting: original))\nClose: \(String(reflecting: error))")
        }
        throw original
    }
    try file.close()
}

func diagnosticPreview(paths: [URL]) -> (text: String, error: String) {
    var output: [String] = []
    var issues: [String] = []
    for url in paths {
        let file: FileHandle
        do { file = try FileHandle(forReadingFrom: url) } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            continue
        } catch { issues.append("\(url.path): \(String(reflecting: error))"); continue }
        do {
            let size = try file.seekToEnd()
            try file.seek(toOffset: size > UInt64(previewBytes) ? size - UInt64(previewBytes) : 0)
            let data = try file.read(upToCount: previewBytes) ?? Data()
            output.append("--- \(url.lastPathComponent) ---\n" + String(decoding: data, as: UTF8.self))
        } catch { issues.append("\(url.path): \(String(reflecting: error))") }
        do { try file.close() } catch { issues.append("\(url.path): \(String(reflecting: error))") }
    }
    return (output.joined(separator: "\n"), issues.joined(separator: "\n"))
}

// Copy bytes without decoding or following an actively growing log forever.
// Input failures are included in the export; destination failures fail the export.
func exportDiagnostics(paths: [URL], to url: URL, header: String) throws -> String {
    guard !paths.contains(where: { $0.standardizedFileURL == url.standardizedFileURL }) else {
        throw DobbyClientError.diagnostics("Choose an export destination outside the diagnostic input files.")
    }
    guard FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600]) else {
        throw DobbyClientError.diagnostics("Could not create \(url.path)")
    }
    let output: FileHandle
    do { output = try FileHandle(forWritingTo: url) } catch {
        let original = error
        do { try FileManager.default.removeItem(at: url) } catch {
            throw DobbyClientError.diagnostics("\(String(reflecting: original))\nCleanup: \(String(reflecting: error))")
        }
        throw original
    }
    var issues: [String] = []
    var failed = false
    do {
        try output.write(contentsOf: Data(header.utf8))
        for path in paths { issues += try copyDiagnostic(path, to: output) }
        let errors = issues.joined(separator: "\n")
        if !errors.isEmpty { try output.write(contentsOf: Data("\nCollection errors\n\(errors)\n".utf8)) }
    } catch {
        failed = true
        issues.insert(String(reflecting: error), at: 0)
    }
    do { try output.close() } catch { failed = true; issues.append("Close export: \(String(reflecting: error))") }
    // Read errors are valid partial diagnostics. A write/close failure must not
    // present a partial file as a completed export.
    if failed {
        do { try FileManager.default.removeItem(at: url) } catch { issues.append("Remove partial export: \(String(reflecting: error))") }
        throw DobbyClientError.diagnostics(issues.joined(separator: "\n"))
    }
    return issues.joined(separator: "\n")
}

private func copyDiagnostic(_ path: URL, to output: FileHandle) throws -> [String] {
    var issues: [String] = []
    let input: FileHandle
    do { input = try FileHandle(forReadingFrom: path) } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
        return []
    } catch { issues.append("\(path.path): \(String(reflecting: error))"); return issues }
    var copyError: Error?
    do {
        try output.write(contentsOf: Data("\n--- \(path.lastPathComponent) ---\n".utf8))
        var remaining: UInt64 = 0
        do { remaining = try input.seekToEnd(); try input.seek(toOffset: 0) } catch {
            issues.append("\(path.path): \(String(reflecting: error))")
            remaining = 0
        }
        while remaining > 0 {
            let data: Data
            do {
                data = try input.read(upToCount: Int(min(65_536, remaining))) ?? Data()
                guard !data.isEmpty else { throw DobbyClientError.diagnostics("Log shortened during export") }
            } catch { issues.append("\(path.path): \(String(reflecting: error))"); break }
            try output.write(contentsOf: data)
            remaining -= UInt64(data.count)
        }
    } catch { copyError = error }
    do { try input.close() } catch { issues.append("\(path.path): \(String(reflecting: error))") }
    if let copyError {
        issues.insert(String(reflecting: copyError), at: 0)
        throw DobbyClientError.diagnostics(issues.joined(separator: "\n"))
    }
    return issues
}
