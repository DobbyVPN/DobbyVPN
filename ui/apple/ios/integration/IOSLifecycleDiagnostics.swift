import Foundation

/// Errors raised when a provider envelope contains bytes that cannot be
/// represented as the UTF-8 text consumed by the Go bridge.
internal enum IOSProviderPayloadError: Error, Equatable {
    case invalidUTF8(hex: String)
}

/// Pure provider-payload decoding shared by the production iOS shell and the
/// platform-neutral lifecycle tests.
internal enum IOSProviderPayload {
    static func decode(_ payload: Data) throws -> String {
        guard let response = String(data: payload, encoding: .utf8) else {
            let hex = payload.map { String(format: "%02x", $0) }.joined()
            throw IOSProviderPayloadError.invalidUTF8(hex: hex)
        }
        return response
    }
}

/// Preserve invalid diagnostic bytes without silently replacing them with
/// U+FFFD before the native export path receives them.
public func reversibleDiagnosticText(_ data: Data) -> String {
    if let text = String(data: data, encoding: .utf8) {
        return text
    }
    let hex = data.map { String(format: "%02x", $0) }.joined()
    return "[invalid-utf8-hex:\(hex)]"
}

/// Render the complete NSError details and its underlying cause chain for the
/// exported native diagnostic log. `localizedDescription` alone often drops
/// the domain, code, and NetworkExtension's underlying failure information.
public func diagnosticErrorDescription(_ error: Error) -> String {
    var visited = Set<ObjectIdentifier>()

    func describe(_ error: Error, indentation: String) -> String {
        let nsError = error as NSError
        let identity = ObjectIdentifier(nsError)
        guard visited.insert(identity).inserted else {
            return "\(indentation)[underlying error cycle: \(nsError.domain)(\(nsError.code))]"
        }

        var lines = [
            "\(indentation)error=\(String(reflecting: error))",
            "\(indentation)domain=\(nsError.domain)",
            "\(indentation)code=\(nsError.code)",
            "\(indentation)localizedDescription=\(nsError.localizedDescription)",
        ]
        for key in nsError.userInfo.keys.sorted() where key != NSUnderlyingErrorKey {
            if let value = nsError.userInfo[key] {
                lines.append("\(indentation)userInfo[\(key)]=\(String(reflecting: value))")
            }
        }
        if let cause = nsError.userInfo[NSUnderlyingErrorKey] as? Error {
            lines.append("\(indentation)underlying:")
            lines.append(describe(cause, indentation: indentation + "  "))
        }
        return lines.joined(separator: "\n")
    }

    return describe(error, indentation: "")
}

/// Execute the provider exchange and encode its original error in the normal response.
public func performProviderMessage(commandData: Data, send: () throws -> Data, onFailure: (Error) -> Void) throws -> Data {
    do { return try send() }
    catch {
        onFailure(error)
        return try providerFailureResponse(commandData: commandData, error: error)
    }
}

private func providerFailureResponse(commandData: Data, error: Error) throws -> Data {
    let command = try IOSProviderCommand.decode(commandData)
    let payload = try JSONSerialization.data(withJSONObject: [
        "ok": false,
        "error": ["code": "PLATFORM_FAILED", "message": "provider request failed:\n\(diagnosticErrorDescription(error))"],
    ])
    return try IOSProviderResponse(requestID: command.requestID, kind: .transport, payload: payload).encoded()
}

/// Snapshot polling is frequent; keep one correlated trace per minute while
/// errors are always emitted by the caller with their last entered stage.
public final class IOSCommandLogSampler {
    private let lock = NSLock()
    private let now: () -> TimeInterval
    private var lastSnapshot: TimeInterval?

    public init(now: @escaping () -> TimeInterval = { ProcessInfo.processInfo.systemUptime }) {
        self.now = now
    }

    public func shouldTrace(method: String) -> Bool {
        guard method == "Snapshot" else { return true }
        lock.lock(); defer { lock.unlock() }
        let current = now()
        if let lastSnapshot, current - lastSnapshot < 60 { return false }
        lastSnapshot = current
        return true
    }
}

/// Retains the OS failure separately from Go's snapshot. A control-only
/// provider can be connected even when no VPN session has ever been active.
public final class IOSDisconnectHistory: @unchecked Sendable {
    private let lock = NSLock()
    private var revision: UInt64 = 0
    private var armed = false
    private var stopping = false
    private var failure = ""

    private var reportedReplacement: String?
    private var replacementFailure = false

    public func observedReplacement(of previousLaunch: String) {
        lock.lock()
        defer { lock.unlock() }
        guard reportedReplacement != previousLaunch, !stopping else { return }
        reportedReplacement = previousLaunch
        replacementFailure = true
        failure = "The VPN extension restarted unexpectedly."
    }

    public func begin(_ method: String) -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        if method == "Stop" {
            revision &+= 1
            armed = false
            stopping = true
            replacementFailure = false
            failure = ""
        } else if method == "Start" {
            revision &+= 1
            stopping = false
        }
        return revision
    }

    public func acceptedStart(_ ticket: UInt64) {
        lock.lock()
        defer { lock.unlock() }
        if ticket == revision { armed = true }
    }

    public func observedSnapshot(_ ticket: UInt64, state: String, recovering: Bool) {
        lock.lock()
        defer { lock.unlock() }
        guard ticket == revision else { return }
        if state == "IDLE" || state == "CONFIGURED" {
            stopping = false
        }
        guard !stopping else { return }
        armed = recovering || ["PROBING", "PREPARING", "CONNECTED"].contains(state)
        if state == "CONNECTED" && !recovering {
            revision &+= 1
            if !replacementFailure { failure = "" }
        }
    }

    public func disconnected() -> UInt64? {
        lock.lock()
        defer { lock.unlock() }
        guard armed else { return nil }
        armed = false
        revision &+= 1
        failure = "The VPN extension disconnected unexpectedly."
        return revision
    }

    public func receivedReason(_ reason: String, ticket: UInt64) {
        lock.lock()
        defer { lock.unlock() }
        if ticket == revision && !failure.isEmpty { failure += " " + reason }
    }

    public var message: String {
        lock.lock()
        defer { lock.unlock() }
        return failure
    }

    public func acknowledge(_ message: String) {
        lock.lock()
        defer { lock.unlock() }
        guard !message.isEmpty, failure == message else { return }
        replacementFailure = false
        revision &+= 1
        failure = ""
    }
}

public struct IOSProviderRunRecord: Codable {
    public let launchID: String
    public var protected = false
    public var peakBytes: UInt64 = 0
    public var peakResources = ""
    public var unexpectedPreviousLaunch: String?

    public init(launchID: String, replacing previous: Self?) {
        self.launchID = launchID
        if let previous, previous.launchID != launchID, previous.protected {
            unexpectedPreviousLaunch = previous.launchID
        }
    }

    public static func load(from path: URL) throws -> Self? {
        do {
            return try JSONDecoder().decode(Self.self, from: Data(contentsOf: path))
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            return nil
        }
    }

    public func save(to path: URL) throws {
        try JSONEncoder().encode(self).write(to: path, options: .atomic)
    }
}
