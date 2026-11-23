import Foundation

/// Small platform boundary shared by the iOS and macOS SwiftUI frontends.
/// The Go session manager owns all connection state and configuration rules.
public protocol DobbySessionClient: AnyObject, Sendable {
    func call(_ method: String, parameters: [String: Any]) -> String
    var diagnosticPaths: [URL] { get }
    var uiDiagnosticPath: URL { get }
    var version: String { get }
    var sourceCommit: String { get }
}

public protocol DobbyVPNSetupRetrying: DobbySessionClient {
    var vpnSetupFailure: String? { get }
    func retryVPNSetupSnapshot() -> String
}

/// Native failure presentation for platforms whose transport may outlive the backend.
public protocol DobbyDisconnectObserving: DobbySessionClient {
    var disconnectFailure: String { get }
    func beginSnapshotObservation() -> UInt64
    func observedSnapshot(_ snapshot: DobbySessionSnapshot, ticket: UInt64)
    func acknowledgeDisconnect(_ message: String)
}

public struct DobbySessionSnapshot: Decodable, Sendable {
    public let sessionID: String
    public let sequence: Int64
    public let generation: Int64
    public let networkGeneration: UInt64
    public let state: String
    public let primaryAction: String
    public let vpnActive: Bool
    public let canRetry: Bool
    public let serviceRestorePending: Bool
    public internal(set) var configured: Bool
    public internal(set) var sourceURL: String
    public var offlineAcceptedAt = ""
    /// Why the connected network runs without leak protection (macOS pf kill switch); empty when it is on.
    public var leakProtectionOff = ""
    public let sourceError: String
    public let activeProfile: DobbyProfile?
    public let lastFailure: DobbyFailure?
    public let recovering: Bool
    public internal(set) var digest: String
    public internal(set) var profiles: [DobbyProfile]
    public let activeDigest: String
    public let activeMode: String
    public let activeIndex: Int
    public let pendingTarget: DobbySelection?
    public let canSwitch: Bool
    public let logsUploadAvailable: Bool

    /// Snapshot schema version this app understands; the Go service sends its own.
    public static let expectedProtocolVersion = 1

    public static let empty = Self(
        sessionID: "", sequence: 0, generation: 0, state: "IDLE", primaryAction: "NONE",
        configured: false, sourceURL: "", sourceError: "", activeProfile: nil,
        lastFailure: nil, recovering: false
    )

    private enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol_version"
        case sessionID = "session_id"
        case serviceVersion = "service_version"
        case sourceKind = "source_kind"
        case cleanupComplete = "cleanup_complete"
        case serviceRestorePending = "service_restore_pending"
        case sequence, generation, state, configured
        case networkGeneration = "network_generation"
        case vpnActive = "vpn_active"
        case canRetry = "can_retry"
        case primaryAction = "primary_action"
        case sourceURL = "source_url"
        case sourceError = "source_error"
        case offlineAcceptedAt = "offline_accepted_at"
        case leakProtectionOff = "leak_protection_off"
        case activeProfile = "active_profile"
        case lastFailure = "last_failure"
        case recovering, digest, profiles
        case activeDigest = "active_digest"
        case activeMode = "active_mode"
        case activeIndex = "active_index"
        case pendingTarget = "pending_target"
        case canSwitch = "can_switch"
        case logsUploadAvailable = "logs_upload_available"
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        // A service that predates the field has no value, which is also a mismatch.
        guard try values.decodeIfPresent(Int.self, forKey: .protocolVersion) == Self.expectedProtocolVersion else {
            throw DobbyClientError.protocolMismatch
        }
        _ = try values.decode(String.self, forKey: .serviceVersion)
        _ = try values.decode(String.self, forKey: .sourceKind)
        _ = try values.decode(Bool.self, forKey: .cleanupComplete)
        serviceRestorePending = values.contains(.serviceRestorePending)
            ? try values.decode(Bool.self, forKey: .serviceRestorePending) : false
        sessionID = try values.decode(String.self, forKey: .sessionID)
        sequence = try values.decode(Int64.self, forKey: .sequence)
        generation = try values.decode(Int64.self, forKey: .generation)
        networkGeneration = try values.decode(UInt64.self, forKey: .networkGeneration)
        guard sequence >= 0, generation >= 0 else { throw DobbyClientError.invalidResponse }
        state = try values.decode(String.self, forKey: .state)
        primaryAction = try values.decode(String.self, forKey: .primaryAction)
        vpnActive = try values.decode(Bool.self, forKey: .vpnActive)
        canRetry = try values.decode(Bool.self, forKey: .canRetry)
        configured = try values.decode(Bool.self, forKey: .configured)
        sourceURL = values.contains(.sourceURL) ? try values.decode(String.self, forKey: .sourceURL) : ""
        offlineAcceptedAt = values.contains(.offlineAcceptedAt) ? try values.decode(String.self, forKey: .offlineAcceptedAt) : ""
        leakProtectionOff = values.contains(.leakProtectionOff) ? try values.decode(String.self, forKey: .leakProtectionOff) : ""
        sourceError = values.contains(.sourceError) ? try values.decode(String.self, forKey: .sourceError) : ""
        activeProfile = try values.decodeIfPresent(DobbyProfile.self, forKey: .activeProfile)
        lastFailure = try values.decodeIfPresent(DobbyFailure.self, forKey: .lastFailure)
        recovering = try values.decode(Bool.self, forKey: .recovering)
        digest = try values.decode(String.self, forKey: .digest)
        profiles = try values.decode([DobbyProfile].self, forKey: .profiles)
        activeDigest = try values.decode(String.self, forKey: .activeDigest)
        activeMode = try values.decode(String.self, forKey: .activeMode)
        activeIndex = try values.decode(Int.self, forKey: .activeIndex)
        pendingTarget = try values.decodeIfPresent(DobbySelection.self, forKey: .pendingTarget)
        canSwitch = try values.decode(Bool.self, forKey: .canSwitch)
        logsUploadAvailable = try values.decode(Bool.self, forKey: .logsUploadAvailable)
    }

    public init(
        sessionID: String, sequence: Int64, generation: Int64, state: String, primaryAction: String,
        configured: Bool, sourceURL: String, sourceError: String,
        activeProfile: DobbyProfile?, lastFailure: DobbyFailure?, recovering: Bool,
        networkGeneration: UInt64 = 0, vpnActive: Bool = false, canRetry: Bool = false
    ) {
        self.sessionID = sessionID
        self.sequence = sequence
        self.generation = generation
        self.networkGeneration = networkGeneration
        self.state = state
        self.primaryAction = primaryAction
        self.vpnActive = vpnActive
        self.canRetry = canRetry
        serviceRestorePending = false
        self.configured = configured
        self.sourceURL = sourceURL
        self.sourceError = sourceError
        self.activeProfile = activeProfile
        self.lastFailure = lastFailure
        self.recovering = recovering
        digest = ""
        profiles = []
        activeDigest = ""
        activeMode = ""
        activeIndex = 0
        pendingTarget = nil
        canSwitch = false
        logsUploadAvailable = false
    }
}

public struct DobbySelection: Decodable, Sendable {
    public let digest: String
    public let mode: String
    public let index: Int
}

public struct DobbyProfile: Decodable, Sendable, Identifiable {
    public let index: Int
    public var id: Int { index }
    public var name: String { description.isEmpty ? "Profile \(index + 1)" : description }
    public let protocolName: String
    public let description: String

    private enum CodingKeys: String, CodingKey {
        case protocolName = "protocol"
        case description, index
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        index = try values.decode(Int.self, forKey: .index)
        protocolName = try values.decode(String.self, forKey: .protocolName)
        description = try values.decode(String.self, forKey: .description)
    }
}

public struct DobbyFailure: Decodable, Sendable {
    public let code: String
    public let message: String
}

public enum DobbyResponse {
    public static func result<T: Decodable>(from text: String, as type: T.Type) throws -> T {
        guard let data = text.data(using: .utf8),
              let envelope = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw DobbyClientError.invalidResponse
        }
        guard envelope["ok"] as? Bool == true else {
            let error = envelope["error"] as? [String: Any]
            throw DobbyClientError.command(
                code: error?["code"] as? String ?? "INTERNAL",
                message: error?["message"] as? String ?? "Go backend command failed"
            )
        }
        guard let value = envelope["result"], JSONSerialization.isValidJSONObject(value),
              let encoded = try? JSONSerialization.data(withJSONObject: value) else {
            throw DobbyClientError.invalidResponse
        }
        return try JSONDecoder().decode(type, from: encoded)
    }

    public static func check(_ text: String) throws {
        guard let data = text.data(using: .utf8),
              let envelope = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw DobbyClientError.invalidResponse
        }
        guard envelope["ok"] as? Bool == true else {
            let error = envelope["error"] as? [String: Any]
            throw DobbyClientError.command(
                code: error?["code"] as? String ?? "INTERNAL",
                message: error?["message"] as? String ?? "Go backend command failed"
            )
        }
    }
}

public enum DobbyClientError: LocalizedError {
    case invalidResponse
    case command(code: String, message: String)
    case noConfiguration
    case diagnostics(String)
    case protocolMismatch

    public var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "Go backend returned an invalid response"
        case let .command(code, message):
            return "\(message) (\(code))"
        case .noConfiguration:
            return "Enter an HTTPS subscription URL"
        case let .diagnostics(message):
            return message
        case .protocolMismatch:
            return "The app and the VPN service are different versions. Restart the app."
        }
    }
}
