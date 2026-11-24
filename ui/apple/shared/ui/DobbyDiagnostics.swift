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

func exportLocalDiagnostics(client: DobbySessionClient, to url: URL, writeError: String) throws -> String {
    var header = "DobbyVPN \(client.version)\nSource commit: \(client.sourceCommit)\n" +
        "Platform: \(ProcessInfo.processInfo.operatingSystemVersionString)\nCaptured: \(ISO8601DateFormatter().string(from: Date()))\n\n" +
        writeError
    do {
        try client.flushDiagnostics()
    } catch {
        header += "Backend diagnostic flush failed: \(String(reflecting: error))\n"
    }
    return try exportDiagnostics(paths: client.diagnosticPaths, to: url, header: header)
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

    /// The logs menu item: "Copy last reference (0192a3b4… · 14:05)", with the date instead of the time when not today.
    func menuTitle(now: Date = Date(), calendar: Calendar = .current) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = calendar.timeZone
        formatter.dateFormat = calendar.isDate(completed, inSameDayAs: now) ? "HH:mm" : "d MMM"
        return "Copy last reference (\(reference.prefix(8))… · \(formatter.string(from: completed)))"
    }
}

extension DobbySessionViewModel {
    /// A confirmed upload: kept for the logs menu, shown once in the result dialog
    /// (never again after a restart), and logged with its upload address.
    func finishUpload(_ result: DobbyUploadReference) {
        latestUpload = result
        uploadResult = result
        recordDiagnostic("reference=\(result.reference) address=\(result.provider) " +
                         "completed=\(ISO8601DateFormatter().string(from: result.completed))",
                         event: "ui.logs_upload", level: "INFO")
    }

    func closeUploadResult() { uploadResult = nil }
    func dismissUploadError() { uploadError = "" }

    /// The result waits behind the error dialog, the import confirmation and the iOS VPN
    /// setup explanation, and while the app is in the background or its window is not focused.
    func uploadResultVisible(foreground: Bool) -> Bool {
        uploadResult != nil && foreground && visibleFailure.isEmpty && pendingImport == nil && !vpnSetupExplanationVisible
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
