import Foundation

private func isJSONBoolean(_ number: NSNumber) -> Bool {
    String(cString: number.objCType) == "c"
}

/// The small message protocol used between the containing app
/// and its NetworkExtension.  It is deliberately an opaque control channel:
/// configuration bytes are never part of a message and responses are returned
/// from Go without being re-shaped by Swift.
public enum IOSProviderOperation: String, Equatable {
    case configure
    case start
    case snapshot
    case stop
    case reset
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
    /// A valid Go result, including a typed Go rejection, means the provider
    /// has consumed the mailbox. Transport and malformed responses retain it.
    public static func mayConsumeConfigurationResponse(_ response: Data) -> Bool {
        guard let root = try? JSONSerialization.jsonObject(with: response) as? [String: Any],
              let ok = root["ok"] as? Bool else { return false }
        if ok { return root["result"] is [String: Any] }
        guard let error = root["error"] as? [String: Any],
              let code = error["code"] as? String else { return false }
        return !code.isEmpty
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
    public let expectedSequence: Int64?

    public init(
        operation: IOSProviderOperation,
        requestID: String,
        sessionID: String? = nil,
        generation: Int64? = nil,
        mode: String? = nil,
        index: Int32? = nil,
        expectedSequence: Int64? = nil
    ) throws {
        self.operation = operation
        self.requestID = requestID
        self.sessionID = sessionID
        self.generation = generation
        self.mode = mode
        self.index = index
        self.expectedSequence = expectedSequence
        try validateFields()
    }

    public func encoded() throws -> Data {
        try jsonData()
    }

    public static func decode(_ data: Data) throws -> IOSProviderCommand {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = try int64(object["version"]),
              version == Int64(Self.version),
              let operationRaw = object["operation"] as? String,
              let operation = IOSProviderOperation(rawValue: operationRaw),
              let requestID = object["request_id"] as? String else {
            throw IOSProviderMessageError.malformed
        }
        guard !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }

        return try IOSProviderCommand(
            operation: operation,
            requestID: requestID,
            sessionID: object["session_id"] as? String,
            generation: try int64(object["generation"]),
            mode: object["mode"] as? String,
            index: try int32(object["index"]),
            expectedSequence: try int64(object["expected_sequence"])
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
        if let expectedSequence, expectedSequence < 0 { throw IOSProviderMessageError.malformed }
        let present: Set<String> = Set([
            sessionID == nil ? nil : "session_id",
            generation == nil ? nil : "generation",
            mode == nil ? nil : "mode",
            index == nil ? nil : "index",
            expectedSequence == nil ? nil : "expected_sequence",
        ].compactMap { $0 })
        let required: Set<String>
        let allowed: Set<String>
        switch operation {
        case .snapshot:
            required = []
            allowed = ["session_id"]
        case .configure, .reset:
            // The mobile manager is process-local and allocates its opaque
            // owner on the first snapshot/configure call.  An omitted owner
            // therefore means "the current process owner"; a non-empty
            // owner is still checked by Go for stale-session fencing.
            required = ["expected_sequence"]
            allowed = ["session_id", "expected_sequence"]
        case .start:
            required = ["expected_sequence", "mode", "index"]
            allowed = ["session_id", "expected_sequence", "mode", "index"]
        case .stop:
            required = ["generation"]
            allowed = ["session_id", "generation"]
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
        if let expectedSequence { value["expected_sequence"] = expectedSequence }
        return try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    }

    fileprivate static func int64(_ value: Any?) throws -> Int64? {
        guard let value else { return nil }
        guard let number = value as? NSNumber,
              !isJSONBoolean(number),
              number.doubleValue.isFinite else {
            throw IOSProviderMessageError.malformed
        }
        let integer = number.int64Value
        guard number.compare(NSNumber(value: integer)) == .orderedSame else {
            throw IOSProviderMessageError.malformed
        }
        return integer
    }

    private static func int32(_ value: Any?) throws -> Int32? {
        guard let integer = try int64(value) else { return nil }
        guard integer >= Int64(Int32.min), integer <= Int64(Int32.max) else {
            throw IOSProviderMessageError.malformed
        }
        return Int32(integer)
    }

}

/// Provider response envelope. The payload is the exact UTF-8
/// byte sequence returned by Go, carried as base64 so Swift never reserializes
/// or changes the inner JSON. The containing app validates this envelope and
/// then returns only the untouched inner Go bytes to the SwiftUI front-end.
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
              let version = try IOSProviderCommand.int64(object["version"]),
              version == Int64(Self.version),
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
