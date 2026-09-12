import Foundation

private func isJSONBoolean(_ number: NSNumber) -> Bool {
    String(cString: number.objCType) == "c"
}

public extension Notification.Name {
    /// Content-free wake signal; callers fetch ordered payloads with Go Observe.
    static let iosSessionEventAvailable = Notification.Name("vpn.dobby.sessionapi.event-available")
}

/// Darwin notifications are the cross-process, content-free wake channel used
/// between the NetworkExtension process and the containing app. The payload is
/// never carried here; the app follows a wake with Go Observe.
public enum IOSDarwinEventSink {
    public static let notificationName = "vpn.dobby.sessionapi.event-available"
}

/// The small message protocol used between the containing app
/// and its NetworkExtension.  It is deliberately an opaque control channel:
/// configuration bytes are never part of a message and responses are returned
/// from Go without being re-shaped by Swift.
public enum IOSProviderOperation: String, Equatable {
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
    case unsupportedOperation = "SESSIONAPI_UNSUPPORTED"
}

public enum IOSProviderResponseKind: String, Equatable {
    case go
    case transport
}

/// Timeout for one containing-app/provider exchange.
public enum IOSProviderTiming {
    public static let appMessageTimeout: TimeInterval = 30
}

/// Mailbox deletion policy shared by the app and provider. Any valid Go
/// result (success or typed failure) consumes the one-shot configuration;
/// transport timeout and malformed responses retain it.
public enum IOSMailboxLifecycle {
    /// A valid Go configure result, including a typed Go rejection, means the
    /// provider has consumed the mailbox. Transport and malformed provider
    /// responses are not Go results and retain it.
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

/// Provider command envelope. Optional fields are operation-specific.
public struct IOSProviderCommand: Equatable {
    public static let version = 1

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

    public func encoded() throws -> Data {
        try jsonData()
    }

    public static func decode(_ data: Data) throws -> IOSProviderCommand {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = object["version"] as? NSNumber,
              version.intValue == Self.version,
              let operationRaw = object["operation"] as? String,
              let operation = IOSProviderOperation(rawValue: operationRaw),
              let requestID = object["request_id"] as? String else {
            throw IOSProviderMessageError.malformed
        }
        guard object["version"] is NSNumber, !isJSONBoolean(version), !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }

        return try IOSProviderCommand(
            operation: operation,
            requestID: requestID,
            sessionID: object["session_id"] as? String,
            generation: int64(object["generation"]),
            mode: object["mode"] as? String,
            index: int32(object["index"]),
            afterSequence: int64(object["after_sequence"])
        )
    }

    private func validateFields() throws {
        guard !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }
        if let sessionID {
            guard !sessionID.isEmpty else {
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

    private func jsonData() throws -> Data {
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
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    }

    private static func int64(_ value: Any?) -> Int64? {
        guard let number = value as? NSNumber, !isJSONBoolean(number) else { return nil }
        return number.int64Value
    }

    private static func int32(_ value: Any?) -> Int32? {
        guard let number = value as? NSNumber, !isJSONBoolean(number), number.int64Value >= Int64(Int32.min), number.int64Value <= Int64(Int32.max) else { return nil }
        return number.int32Value
    }

}

/// Provider response envelope. The payload is the exact UTF-8
/// byte sequence returned by Go, carried as base64 so Swift never reserializes
/// or changes the inner JSON. The containing app validates this envelope and
/// then returns only the untouched inner Go bytes to KMP.
public struct IOSProviderResponse: Equatable {
    public static let version = 1

    public let requestID: String
    public let kind: IOSProviderResponseKind
    public let payload: Data

    public init(requestID: String, kind: IOSProviderResponseKind = .go, payload: Data) throws {
        guard !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }
        self.requestID = requestID
        self.kind = kind
        self.payload = payload
    }

    public func encoded() throws -> Data {
        try jsonData()
    }

    public static func decode(
        _ data: Data,
        expectedRequestID: String
    ) throws -> IOSProviderResponse {
        guard !expectedRequestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = object["version"] as? NSNumber,
              version.intValue == Self.version,
              !isJSONBoolean(version),
              let requestID = object["request_id"] as? String,
              let kindRaw = object["kind"] as? String,
              let kind = IOSProviderResponseKind(rawValue: kindRaw),
              let encodedPayload = object["payload"] as? String,
              !requestID.isEmpty,
              requestID == expectedRequestID,
              let payload = Data(base64Encoded: encodedPayload) else {
            throw IOSProviderMessageError.malformed
        }
        return try IOSProviderResponse(requestID: requestID, kind: kind, payload: payload)
    }

    private func jsonData() throws -> Data {
        let value: [String: Any] = [
            "kind": kind.rawValue,
            "payload": payload.base64EncodedString(),
            "request_id": requestID,
            "version": Self.version,
        ]
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    }

}
