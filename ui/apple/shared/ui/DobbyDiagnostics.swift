import Foundation
import Darwin
#if canImport(DobbyDiagnosticFiles)
import DobbyDiagnosticFiles
#else
import IOSIntegration
#endif

func appendUIDiagnostic(
    _ message: String,
    to url: URL,
    event: String = "ui.failure",
    level: String = "ERROR",
    excludedPaths: [URL] = []
) throws {
    #if os(iOS)
    try DiagnosticFiles.append(message, event: event, level: level, source: "native-ui", to: url, rotating: false)
#else
    try DiagnosticFiles.append(message, event: event, level: level, source: "native-ui", to: url, legacyDesktop: true, legacyExcludedPaths: excludedPaths.map(\.path))
#endif
}

func exportDiagnostics(paths: [URL], to url: URL, header: String, checkCancellation: () throws -> Void = {}) throws -> String {
    try DiagnosticFiles.export(paths: paths, to: url, header: header, checkCancellation: checkCancellation)
}

final class DobbyUploadCancellation: @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    func cancel() { lock.lock(); cancelled = true; lock.unlock() }
    func check() throws {
        lock.lock(); let value = cancelled; lock.unlock()
        if value { throw CancellationError() }
    }
}

typealias DobbyLogEntry = DiagnosticFiles.Entry

func isShownOnScreen(_ entry: DobbyLogEntry) -> Bool { entry.level != "DEBUG" && entry.level != "TRACE" }

/// The last parsed log view. It is read and written only on the diagnostics
/// worker queue, which serialises every access.
final class LogViewCache: @unchecked Sendable {
    var signature: String?
    var result: (entries: [DobbyLogEntry], error: String)?

    /// The entries to show. Unchanged files produce the same entries, so they are
    /// not read and parsed again. A result that carried an error is never reused.
    func preview(paths: [URL], boundary: URL) -> (entries: [DobbyLogEntry], error: String) {
        let current = logViewSignature(paths: paths, boundary: boundary)
        if let cached = result, signature == current { return cached }
        let fresh = structuredPreview(paths: paths, boundary: boundary)
        let shown = (entries: fresh.entries.filter(isShownOnScreen), error: fresh.error)
        signature = fresh.error.isEmpty ? current : nil
        result = fresh.error.isEmpty ? shown : nil
        return shown
    }
}

func structuredPreview(paths: [URL], boundary: URL) -> (entries: [DobbyLogEntry], error: String) {
    DiagnosticFiles.entries(paths: paths, boundary: boundary)
}
func logViewSignature(paths: [URL], boundary: URL) -> String {
    DiagnosticFiles.viewSignature(paths: paths, boundary: boundary)
}
func clearDiagnosticView(paths: [URL], boundary: URL) throws { try DiagnosticFiles.clearView(paths: paths, boundary: boundary) }

struct DobbyUploadPreparation: Decodable, Sendable {
    let reference: String
    let destination: URL
    let authorization: String
    let provider: String
    let maxBytes: Int64

    private enum CodingKeys: String, CodingKey {
        case reference, destination, authorization, provider
        case maxBytes = "max_bytes"
    }
}

struct DobbyUploadReference: Codable, Sendable {
    let reference: String
    let completed: Date
    let provider: String
    var shareText: String { "DobbyVPN logs: \(reference)" }
}

/// The "latest upload" result shared with dobby-cli: `<ui log>.upload`, JSON `{"reference","time","provider"}`
/// (the file and format written by `dobby-cli logs upload`). Used on macOS; iOS has no CLI and keeps UserDefaults.
enum DobbyLatestUploadFile {
    private struct Record: Codable { let reference: String; let time: String; let provider: String }

    static func url(forLog uiLog: URL) -> URL { URL(fileURLWithPath: uiLog.path + ".upload") }

    /// False only if the file is absent (ENOENT); any other lstat error counts as present.
    static func exists(forLog uiLog: URL) -> Bool {
        var info = stat()
        return lstat(url(forLog: uiLog).path, &info) == 0 || errno != ENOENT
    }

    static func read(forLog uiLog: URL) -> DobbyUploadReference? {
        guard let data = try? Data(contentsOf: url(forLog: uiLog)),
              let record = try? JSONDecoder().decode(Record.self, from: data), !record.reference.isEmpty,
              let time = parse(record.time) else { return nil }
        return DobbyUploadReference(reference: record.reference, completed: time, provider: record.provider)
    }

    /// macOS: the shared file; the old UserDefaults value only when the file is absent (a damaged file must not bring it back).
    /// iOS: UserDefaults.
    static func load(forLog uiLog: URL, defaultsKey: String) -> DobbyUploadReference? {
        #if os(macOS)
        if let upload = read(forLog: uiLog) { return upload }
        if exists(forLog: uiLog) { return nil }
        #endif
        return UserDefaults.standard.data(forKey: defaultsKey).flatMap { try? JSONDecoder().decode(DobbyUploadReference.self, from: $0) }
    }

    static func save(_ upload: DobbyUploadReference, forLog uiLog: URL, defaultsKey: String) throws {
        #if os(macOS)
        try write(upload, forLog: uiLog)
        #else
        UserDefaults.standard.set(try JSONEncoder().encode(upload), forKey: defaultsKey)
        #endif
    }

    static func write(_ upload: DobbyUploadReference, forLog uiLog: URL) throws {
        let destination = url(forLog: uiLog)
        let directory = destination.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let data = try JSONEncoder().encode(Record(reference: upload.reference, time: formatter.string(from: upload.completed), provider: upload.provider))
        let temporary = directory.appendingPathComponent(".upload-preference-" + UUID().uuidString)
        guard FileManager.default.createFile(atPath: temporary.path, contents: data, attributes: [.posixPermissions: 0o600]) else {
            throw CocoaError(.fileWriteUnknown)
        }
        if Darwin.rename(temporary.path, destination.path) != 0 {
            let code = errno
            try? FileManager.default.removeItem(at: temporary)
            throw POSIXError(POSIXErrorCode(rawValue: code) ?? .EIO)
        }
    }

    // Go writes RFC 3339 with a variable number of fractional digits.
    private static func parse(_ text: String) -> Date? {
        let plain = ISO8601DateFormatter()
        if let date = plain.date(from: text) { return date }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: text)
    }
}

private final class DobbyNoUploadRedirect: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

/// Session configuration for uploads started by the view model; tests replace it to inject a URLProtocol.
var uploadSessionConfiguration: () -> URLSessionConfiguration = { .ephemeral }

/// A file-backed, single-attempt upload. Tests can inject URLProtocol through configuration.
func uploadDiagnostics(_ preparation: DobbyUploadPreparation, file: URL,
                       configuration: URLSessionConfiguration = uploadSessionConfiguration(), timeout: TimeInterval = 900) async throws {
    guard preparation.destination.scheme?.lowercased() == "https" else {
        throw DobbyClientError.diagnostics("The upload destination must use HTTPS")
    }
    let size = try file.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
    guard size > 0, Int64(size) <= preparation.maxBytes else {
        throw DobbyClientError.diagnostics("The compressed logs exceed the upload size limit")
    }
    configuration.timeoutIntervalForRequest = timeout
    configuration.timeoutIntervalForResource = timeout
    let session = URLSession(configuration: configuration, delegate: DobbyNoUploadRedirect(), delegateQueue: nil)
    defer { session.invalidateAndCancel() }
    var request = URLRequest(url: preparation.destination)
    request.httpMethod = "PUT"
    request.setValue(String(size), forHTTPHeaderField: "Content-Length")
    request.setValue("application/gzip", forHTTPHeaderField: "Content-Type")
    request.setValue("*", forHTTPHeaderField: "If-None-Match")
    if !preparation.authorization.isEmpty { request.setValue(preparation.authorization, forHTTPHeaderField: "Authorization") }
    let (_, response) = try await session.upload(for: request, fromFile: file)
    guard let http = response as? HTTPURLResponse, http.statusCode == 201 else {
        let status = (response as? HTTPURLResponse).map { String($0.statusCode) } ?? "unknown"
        throw DobbyClientError.diagnostics("Log upload was not confirmed (HTTP \(status))")
    }
}

/// A process-shared lease keeps a live attachment out of startup cleanup.
final class DobbyUploadDirectory {
    let url: URL
    private let descriptor: Int32
    private static let gatePath = FileManager.default.temporaryDirectory.appendingPathComponent("DobbyVPN-upload.lock").path

    private static func withGate<T>(_ body: () throws -> T) throws -> T {
        let fd = Darwin.open(gatePath, O_CREAT | O_RDWR | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        defer { Darwin.close(fd) }
        guard flock(fd, LOCK_EX) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        defer { flock(fd, LOCK_UN) }
        return try body()
    }

    static func removeAbandoned() throws {
        try withGate {
            let files = try FileManager.default.contentsOfDirectory(at: FileManager.default.temporaryDirectory, includingPropertiesForKeys: nil)
            var errors: [String] = []
            for directory in files where directory.lastPathComponent.hasPrefix("DobbyVPN-upload-") {
                let path = directory.appendingPathComponent(".lease").path
                let fd = Darwin.open(path, O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0o600)
                if fd < 0 { errors.append("Open upload lease \(path): errno=\(errno)"); continue }
                if flock(fd, LOCK_EX | LOCK_NB) == 0 {
                    do { try FileManager.default.removeItem(at: directory) }
                    catch { errors.append("Remove abandoned upload \(directory.path): \(String(reflecting: error))") }
                    flock(fd, LOCK_UN)
                } else if errno != EWOULDBLOCK {
                    errors.append("Lock upload lease \(path): errno=\(errno)")
                }
                Darwin.close(fd)
            }
            if !errors.isEmpty { throw DobbyClientError.diagnostics(errors.joined(separator: "\n")) }
        }
    }

    init() throws {
        let result: (URL, Int32) = try Self.withGate {
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent("DobbyVPN-upload-" + UUID().uuidString, isDirectory: true)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
            do {
                let fd = Darwin.open(directory.appendingPathComponent(".lease").path, O_CREAT | O_EXCL | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0o600)
                guard fd >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
                guard flock(fd, LOCK_EX) == 0 else {
                    let cause = errno
                    Darwin.close(fd)
                    throw POSIXError(.init(rawValue: cause) ?? .EIO)
                }
                return (directory, fd)
            } catch {
                let original = error
                do { try FileManager.default.removeItem(at: directory) }
                catch { throw DobbyClientError.diagnostics("\(String(reflecting: original))\nRemove upload directory: \(String(reflecting: error))") }
                throw original
            }
        }
        url = result.0
        descriptor = result.1
    }

    func remove() throws {
        defer { flock(descriptor, LOCK_UN); Darwin.close(descriptor) }
        try Self.withGate { try FileManager.default.removeItem(at: url) }
    }
}
