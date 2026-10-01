import Foundation

private func isJSONBoolean(_ number: NSNumber) -> Bool {
    String(cString: number.objCType) == "c"
}

public enum IOSProviderMessageError: String, Error, Equatable {
    case malformed = "SESSIONAPI_MALFORMED"
}

public enum IOSProviderResponseKind: String, Equatable {
    case go
    case transport
}

/// Timeout for one containing-app/provider exchange.
public enum IOSProviderTiming {
    public static let appMessageTimeout: TimeInterval = 30
}

/// Configuration stays in the shared Keychain item instead of the
/// NetworkExtension message. The request ID binds those bytes to one command.
public struct IOSConfigurationMailbox: Equatable {
    private static let header = Data("DobbyVPN configuration mailbox v1\n".utf8)
    private static let separator: UInt8 = 0x0A

    public let requestID: String
    public let configuration: Data

    public init(requestID: String, configuration: Data) throws {
        guard !requestID.isEmpty, !requestID.utf8.contains(Self.separator) else {
            throw IOSProviderMessageError.malformed
        }
        self.requestID = requestID
        self.configuration = configuration
    }

    public func encoded() -> Data {
        var bytes = Self.header
        bytes.append(contentsOf: requestID.utf8)
        bytes.append(Self.separator)
        bytes.append(configuration)
        return bytes
    }

    public static func decode(_ data: Data) throws -> Self {
        guard data.starts(with: Self.header) else {
            throw IOSProviderMessageError.malformed
        }
        let content = data.dropFirst(Self.header.count)
        guard let separatorIndex = content.firstIndex(of: Self.separator) else {
            throw IOSProviderMessageError.malformed
        }
        let requestIDBytes = content[..<separatorIndex]
        guard let requestID = String(data: requestIDBytes, encoding: .utf8), !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }
        let configurationStart = content.index(after: separatorIndex)
        return try Self(
            requestID: requestID,
            configuration: Data(content[configurationStart...])
        )
    }
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

/// Provider command envelope. `method` and `params` keep the same shape as the
/// desktop Go dispatcher; the remaining fields correlate the NetworkExtension
/// transport exchange.
public struct IOSProviderCommand: Equatable {
    public static let version = 2

    public let method: String
    public let requestID: String
    public let params: Data

    public init(
        method: String,
        requestID: String,
        params: Data
    ) throws {
        guard !method.isEmpty, !requestID.isEmpty else {
            throw IOSProviderMessageError.malformed
        }
        guard (try? JSONSerialization.jsonObject(with: params, options: [.fragmentsAllowed])) != nil else {
            throw IOSProviderMessageError.malformed
        }
        self.method = method
        self.requestID = requestID
        self.params = params
    }

    public func encoded() throws -> Data {
        let parameters = try JSONSerialization.jsonObject(with: params, options: [.fragmentsAllowed])
        return try JSONSerialization.data(
            withJSONObject: [
                "method": method,
                "params": parameters,
                "request_id": requestID,
                "version": Self.version,
            ],
            options: [.sortedKeys, .withoutEscapingSlashes]
        )
    }

    public static func decode(_ data: Data) throws -> Self {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = try int64(object["version"]),
              version == Int64(Self.version),
              let method = object["method"] as? String,
              let requestID = object["request_id"] as? String,
              let parameters = object["params"] else {
            throw IOSProviderMessageError.malformed
        }
        let params = try JSONSerialization.data(
            withJSONObject: parameters,
            options: [.sortedKeys, .withoutEscapingSlashes, .fragmentsAllowed]
        )
        return try Self(method: method, requestID: requestID, params: params)
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
    ) throws -> Self {
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
        return try Self(requestID: requestID, kind: kind, payload: payload)
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
