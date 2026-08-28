import Foundation
import CryptoKit

public extension Notification.Name {
    /// Content-free wake signal; callers fetch ordered payloads with Go Observe.
    static let iosSessionEventAvailable = Notification.Name("vpn.dobby.sessionapi.event-available")
}

/// Darwin notifications are the cross-process, content-free wake channel used
/// between the NetworkExtension process and the containing app. The payload is
/// never carried here; the app follows a wake with authenticated Go Observe.
public enum IOSDarwinEventSink {
    public static let notificationName = "vpn.dobby.sessionapi.event-available"
}

/// The small, authenticated message protocol used between the containing app
/// and its NetworkExtension.  It is deliberately an opaque control channel:
/// configuration bytes are never part of a message and responses are returned
/// from Go without being re-shaped by Swift.
public enum IOSProviderOperation: String, CaseIterable, Equatable {
    case create
    case recover
    case configure
    case start
    case snapshot
    case observe
    case stop
    case destroy
}

public enum IOSProviderMessageError: String, Error, Equatable {
    case malformed = "SESSIONAPI_MALFORMED"
    case tooLarge = "SESSIONAPI_MESSAGE_TOO_LARGE"
    case unauthenticated = "SESSIONAPI_UNAUTHENTICATED"
    case unsupportedOperation = "SESSIONAPI_UNSUPPORTED"
}

public enum IOSProviderResponseKind: String, Equatable {
    case go
    case transport
}

/// Shared timeout contract for the two halves of the provider message path.
/// The provider must complete its command callback before the containing app
/// gives up waiting, so an authenticated bounded timeout can cross the
/// process boundary without an overlapping transport retry.
public enum IOSProviderTiming {
    public static let providerCommandCompletionTimeout: TimeInterval = 25
    public static let appMessageTimeout: TimeInterval = 30
    public static let appTransportRetries = 6
    public static let appRetryDelay: TimeInterval = 0.5
}

/// Monotonic budget for the whole app/provider exchange, including retries.
/// A retry is permitted only while the same deadline remains; callers must not
/// allocate a fresh timeout per attempt.
public struct IOSProviderRetryBudget {
    public let deadline: TimeInterval
    public let maximumRetries: Int
    public let retryDelay: TimeInterval
    private var attempts = 0

    public init(
        start: TimeInterval,
        budget: TimeInterval = IOSProviderTiming.appMessageTimeout,
        maximumRetries: Int = IOSProviderTiming.appTransportRetries,
        retryDelay: TimeInterval = IOSProviderTiming.appRetryDelay
    ) {
        self.deadline = start + max(0, budget)
        self.maximumRetries = max(0, maximumRetries)
        self.retryDelay = max(0, retryDelay)
    }

    /// Returns the remaining time for the next provider attempt, or nil after
    /// the aggregate budget has expired or all retries have been used.
    public mutating func nextAttemptTimeout(now: TimeInterval) -> TimeInterval? {
        guard attempts <= maximumRetries else { return nil }
        let remaining = deadline - now
        guard remaining > 0 else { return nil }
        attempts += 1
        return remaining
    }

    /// Returns a bounded backoff that cannot extend the aggregate deadline.
    public func nextRetryDelay(now: TimeInterval) -> TimeInterval? {
        guard attempts < maximumRetries else { return nil }
        let remaining = deadline - now
        guard remaining > 0 else { return nil }
        return min(retryDelay, remaining)
    }
}

/// State fence for an asynchronous NetworkExtension settings operation. Once
/// poisoned, no later operation may commit settings until a fresh provider
/// process creates a new fence. This is also the test seam for late completion
/// handling: a completion from an old epoch is never accepted.
public final class IOSSettingsOperationFence {
    private var poisoned = false
    private var epoch: UInt64 = 0
    private let lock = NSLock()

    public init() {}

    public func begin() -> UInt64? {
        lock.lock()
        defer { lock.unlock() }
        guard !poisoned else { return nil }
        epoch &+= 1
        return epoch
    }

    public func canCommit(_ candidate: UInt64) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return !poisoned && candidate == epoch
    }

    public func poison() {
        lock.lock()
        poisoned = true
        lock.unlock()
    }

    public var isPoisoned: Bool {
        lock.lock()
        defer { lock.unlock() }
        return poisoned
    }
}

/// Mailbox deletion policy shared by the app and provider. Any valid Go
/// result (success or typed failure) consumes the one-shot configuration;
/// transport timeout, authentication, and malformed responses retain it.
public enum IOSMailboxLifecycle {
    /// A valid Go configure result, including a typed Go rejection, means the
    /// provider has consumed the mailbox. Transport, authentication, and
    /// malformed provider responses are not Go results and retain it.
    public static func mayConsumeConfigureResponse(_ response: Data) -> Bool {
        guard let root = try? JSONSerialization.jsonObject(with: response) as? [String: Any],
              let ok = root["ok"] as? Bool else { return false }
        if ok { return root["result"] is [String: Any] }
        guard let error = root["error"] as? [String: Any],
              let code = error["code"] as? String else { return false }
        return !code.isEmpty
    }

    public static func isSuccessfulGoResponse(_ response: Data) -> Bool {
        guard let root = try? JSONSerialization.jsonObject(with: response) as? [String: Any],
              root["ok"] as? Bool == true,
              root["result"] is [String: Any] else { return false }
        return true
    }
}

/// A canonical command envelope.  Optional fields are operation-specific and
/// cannot be smuggled into another operation.  The HMAC is over the canonical
/// envelope without `mac`; the transmitted envelope is canonical JSON with
/// sorted keys and no insignificant whitespace.
public struct IOSProviderCommand: Equatable {
    public static let version = 1
    public static let maximumBytes = 64 * 1024
    /// SessionV2's accepted raw configuration ceiling. Configuration is
    /// mailbox data, not a provider-message payload, so it has its own bound.
    public static let maximumConfigurationBytes = 1 * 1024 * 1024
    public static let maximumRequestIDBytes = 128
    public static let maximumSessionIDBytes = 128

    public let operation: IOSProviderOperation
    public let requestID: String
    public let sessionID: String?
    public let generation: Int64?
    public let mode: String?
    public let index: Int32?
    public let afterSequence: Int64?

    public init(
        operation: IOSProviderOperation,
        requestID: String,
        sessionID: String? = nil,
        generation: Int64? = nil,
        mode: String? = nil,
        index: Int32? = nil,
        afterSequence: Int64? = nil
    ) throws {
        self.operation = operation
        self.requestID = requestID
        self.sessionID = sessionID
        self.generation = generation
        self.mode = mode
        self.index = index
        self.afterSequence = afterSequence
        try validateFields()
    }

    /// Encode one command.  Callers must retain the returned bytes and retry
    /// those exact bytes when the NetworkExtension transport times out.
    public func encoded(using secret: Data) throws -> Data {
        guard !secret.isEmpty else { throw IOSProviderMessageError.unauthenticated }
        let unsigned = try canonicalObject(includeMAC: false)
        let digest = HMAC<SHA256>.authenticationCode(for: unsigned, using: SymmetricKey(data: secret))
        let mac = Data(digest).map { String(format: "%02x", $0) }.joined()
        let signed = try canonicalObject(includeMAC: true, mac: mac)
        guard signed.count <= Self.maximumBytes else { throw IOSProviderMessageError.tooLarge }
        return signed
    }

    /// Parse and authenticate a command.  The byte-for-byte canonical check
    /// rejects alternate key order, whitespace, duplicate operation fields,
    /// and extra payload keys before any operation is dispatched.
    public static func decode(_ data: Data, using secret: Data) throws -> IOSProviderCommand {
        guard data.count <= maximumBytes else { throw IOSProviderMessageError.tooLarge }
        guard !secret.isEmpty else { throw IOSProviderMessageError.unauthenticated }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = object["version"] as? NSNumber,
              version.intValue == Self.version,
              let operationRaw = object["operation"] as? String,
              let operation = IOSProviderOperation(rawValue: operationRaw),
              let requestID = object["request_id"] as? String,
              let mac = object["mac"] as? String else {
            throw IOSProviderMessageError.malformed
        }
        guard object["version"] is NSNumber, !isBoolean(version),
              isIdentifier(requestID, maximumBytes: maximumRequestIDBytes),
              mac.count == 64, mac.allSatisfy(isLowerHex) else {
            throw IOSProviderMessageError.malformed
        }

        let command = try IOSProviderCommand(
            operation: operation,
            requestID: requestID,
            sessionID: object["session_id"] as? String,
            generation: int64(object["generation"]),
            mode: object["mode"] as? String,
            index: int32(object["index"]),
            afterSequence: int64(object["after_sequence"])
        )
        let unsigned = try command.canonicalObject(includeMAC: false)
        let expected = HMAC<SHA256>.authenticationCode(for: unsigned, using: SymmetricKey(data: secret))
        let expectedHex = Data(expected).map { String(format: "%02x", $0) }.joined()
        guard constantTimeEqual(mac.utf8, expectedHex.utf8) else {
            throw IOSProviderMessageError.unauthenticated
        }
        let canonical = try command.canonicalObject(includeMAC: true, mac: mac)
        guard canonical == data else { throw IOSProviderMessageError.malformed }
        return command
    }

    private func validateFields() throws {
        guard Self.isIdentifier(requestID, maximumBytes: Self.maximumRequestIDBytes) else {
            throw IOSProviderMessageError.malformed
        }
        if let sessionID {
            guard Self.isIdentifier(sessionID, maximumBytes: Self.maximumSessionIDBytes) else {
                throw IOSProviderMessageError.malformed
            }
        }
        if let generation, generation < 0 { throw IOSProviderMessageError.malformed }
        if let index, index < 0 { throw IOSProviderMessageError.malformed }
        if let afterSequence, afterSequence < 0 { throw IOSProviderMessageError.malformed }
        let present: Set<String> = Set([
            sessionID == nil ? nil : "session_id",
            generation == nil ? nil : "generation",
            mode == nil ? nil : "mode",
            index == nil ? nil : "index",
            afterSequence == nil ? nil : "after_sequence",
        ].compactMap { $0 })
        let required: Set<String>
        let allowed: Set<String>
        switch operation {
        case .create, .recover:
            required = []
            allowed = []
        case .configure, .snapshot, .destroy:
            required = ["session_id"]
            allowed = ["session_id"]
        case .start:
            required = ["session_id", "mode", "index"]
            allowed = required
        case .observe:
            required = ["session_id", "after_sequence"]
            allowed = required
        case .stop:
            required = ["session_id", "generation"]
            allowed = required
        }
        guard present.isSubset(of: allowed), required.isSubset(of: present) else {
            throw IOSProviderMessageError.malformed
        }
        if operation == .start {
            guard mode == "AUTO_SELECT" || mode == "PROFILE_INDEX" else {
                throw IOSProviderMessageError.unsupportedOperation
            }
        }
    }

    private func canonicalObject(includeMAC: Bool, mac: String? = nil) throws -> Data {
        var value: [String: Any] = [
            "operation": operation.rawValue,
            "request_id": requestID,
            "version": Self.version,
        ]
        if let sessionID { value["session_id"] = sessionID }
        if let generation { value["generation"] = generation }
        if let mode { value["mode"] = mode }
        if let index { value["index"] = index }
        if let afterSequence { value["after_sequence"] = afterSequence }
        if includeMAC { value["mac"] = mac ?? "" }
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    }

    private static func isIdentifier(_ value: String, maximumBytes: Int) -> Bool {
        let bytes = Array(value.utf8)
        guard !bytes.isEmpty, bytes.count <= maximumBytes else { return false }
        return bytes.allSatisfy { byte in
            (byte >= 48 && byte <= 57) ||
                (byte >= 65 && byte <= 90) ||
                (byte >= 97 && byte <= 122) ||
                byte == 45 || byte == 46 || byte == 95
        }
    }

    private static func isLowerHex(_ byte: Character) -> Bool {
        (byte >= "0" && byte <= "9") || (byte >= "a" && byte <= "f")
    }

    private static func isBoolean(_ number: NSNumber) -> Bool {
        String(cString: number.objCType) == "c"
    }

    private static func int64(_ value: Any?) -> Int64? {
        guard let number = value as? NSNumber, !isBoolean(number) else { return nil }
        return number.int64Value
    }

    private static func int32(_ value: Any?) -> Int32? {
        guard let number = value as? NSNumber, !isBoolean(number), number.int64Value >= Int64(Int32.min), number.int64Value <= Int64(Int32.max) else { return nil }
        return number.int32Value
    }

    private static func constantTimeEqual<S: Sequence, T: Sequence>(_ left: S, _ right: T) -> Bool where S.Element == UInt8, T.Element == UInt8 {
        let a = Array(left)
        let b = Array(right)
        guard a.count == b.count else { return false }
        var result: UInt8 = 0
        for index in a.indices { result |= a[index] ^ b[index] }
        return result == 0
    }
}

/// Authenticated provider response envelope. The payload is the exact UTF-8
/// byte sequence returned by Go, carried as base64 so Swift never reserializes
/// or changes the inner JSON. The containing app validates this envelope and
/// then returns only the untouched inner Go bytes to KMP.
public struct IOSProviderResponse: Equatable {
    public static let version = 1
    public static let maximumBytes = 256 * 1024
    public static let maximumRequestIDBytes = IOSProviderCommand.maximumRequestIDBytes

    public let requestID: String
    public let kind: IOSProviderResponseKind
    public let payload: Data

    public init(requestID: String, kind: IOSProviderResponseKind = .go, payload: Data) throws {
        guard IOSProviderResponse.isIdentifier(requestID, maximumBytes: Self.maximumRequestIDBytes) else {
            throw IOSProviderMessageError.malformed
        }
        self.requestID = requestID
        self.kind = kind
        self.payload = payload
    }

    public func encoded(using secret: Data) throws -> Data {
        guard !secret.isEmpty else { throw IOSProviderMessageError.unauthenticated }
        let unsigned = try canonicalObject(includeMAC: false)
        let digest = HMAC<SHA256>.authenticationCode(for: unsigned, using: SymmetricKey(data: secret))
        let mac = Data(digest).map { String(format: "%02x", $0) }.joined()
        let signed = try canonicalObject(includeMAC: true, mac: mac)
        guard signed.count <= Self.maximumBytes else { throw IOSProviderMessageError.tooLarge }
        return signed
    }

    public static func decode(
        _ data: Data,
        expectedRequestID: String,
        using secret: Data
    ) throws -> IOSProviderResponse {
        guard data.count <= maximumBytes else { throw IOSProviderMessageError.tooLarge }
        guard !secret.isEmpty,
              isIdentifier(expectedRequestID, maximumBytes: maximumRequestIDBytes) else {
            throw IOSProviderMessageError.unauthenticated
        }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = object["version"] as? NSNumber,
              version.intValue == Self.version,
              !isBoolean(version),
              let requestID = object["request_id"] as? String,
              let kindRaw = object["kind"] as? String,
              let kind = IOSProviderResponseKind(rawValue: kindRaw),
              let encodedPayload = object["payload"] as? String,
              let mac = object["mac"] as? String,
              isIdentifier(requestID, maximumBytes: maximumRequestIDBytes),
              requestID == expectedRequestID,
              mac.count == 64,
              mac.allSatisfy(isLowerHex),
              let payload = Data(base64Encoded: encodedPayload) else {
            throw IOSProviderMessageError.malformed
        }
        let response = try IOSProviderResponse(requestID: requestID, kind: kind, payload: payload)
        let unsigned = try response.canonicalObject(includeMAC: false)
        let expected = HMAC<SHA256>.authenticationCode(for: unsigned, using: SymmetricKey(data: secret))
        let expectedHex = Data(expected).map { String(format: "%02x", $0) }.joined()
        guard constantTimeEqual(mac.utf8, expectedHex.utf8) else {
            throw IOSProviderMessageError.unauthenticated
        }
        let canonical = try response.canonicalObject(includeMAC: true, mac: mac)
        guard canonical == data else { throw IOSProviderMessageError.malformed }
        return response
    }

    private func canonicalObject(includeMAC: Bool, mac: String? = nil) throws -> Data {
        var value: [String: Any] = [
            "kind": kind.rawValue,
            "payload": payload.base64EncodedString(),
            "request_id": requestID,
            "version": Self.version,
        ]
        if includeMAC { value["mac"] = mac ?? "" }
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    }

    private static func isIdentifier(_ value: String, maximumBytes: Int) -> Bool {
        let bytes = Array(value.utf8)
        guard !bytes.isEmpty, bytes.count <= maximumBytes else { return false }
        return bytes.allSatisfy { byte in
            (byte >= 48 && byte <= 57) ||
                (byte >= 65 && byte <= 90) ||
                (byte >= 97 && byte <= 122) ||
                byte == 45 || byte == 46 || byte == 95
        }
    }

    private static func isLowerHex(_ byte: Character) -> Bool {
        (byte >= "0" && byte <= "9") || (byte >= "a" && byte <= "f")
    }

    private static func isBoolean(_ number: NSNumber) -> Bool {
        String(cString: number.objCType) == "c"
    }

    private static func constantTimeEqual<S: Sequence, T: Sequence>(_ left: S, _ right: T) -> Bool where S.Element == UInt8, T.Element == UInt8 {
        let a = Array(left)
        let b = Array(right)
        guard a.count == b.count else { return false }
        var result: UInt8 = 0
        for index in a.indices { result |= a[index] ^ b[index] }
        return result == 0
    }
}
