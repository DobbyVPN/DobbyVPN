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

public struct DobbySessionSnapshot: Decodable, Sendable {
    public let sessionID: String
    public let sequence: Int64
    public let generation: Int64
    public let state: String
    public let primaryAction: String
    public let configured: Bool
    public let sourceURL: String
    public let sourceError: String
    public let activeProfile: DobbyProfile?
    public let lastFailure: DobbyFailure?
    public let recovering: Bool
    public let digest: String
    public let profiles: [DobbyProfile]
    public let activeDigest: String
    public let activeMode: String
    public let activeIndex: Int
    public let pendingTarget: DobbySelection?
    public let canSwitch: Bool

    public static let empty = Self(
        sessionID: "", sequence: 0, generation: 0, state: "IDLE", primaryAction: "NONE",
        configured: false, sourceURL: "", sourceError: "", activeProfile: nil,
        lastFailure: nil, recovering: false
    )

    private enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case sequence, generation, state, configured
        case primaryAction = "primary_action"
        case sourceURL = "source_url"
        case sourceError = "source_error"
        case activeProfile = "active_profile"
        case lastFailure = "last_failure"
        case recovering, digest, profiles
        case activeDigest = "active_digest"
        case activeMode = "active_mode"
        case activeIndex = "active_index"
        case pendingTarget = "pending_target"
        case canSwitch = "can_switch"
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try values.decodeIfPresent(String.self, forKey: .sessionID) ?? ""
        sequence = try values.decodeIfPresent(Int64.self, forKey: .sequence) ?? 0
        generation = try values.decodeIfPresent(Int64.self, forKey: .generation) ?? 0
        state = try values.decodeIfPresent(String.self, forKey: .state) ?? "IDLE"
        primaryAction = try values.decodeIfPresent(String.self, forKey: .primaryAction) ?? "NONE"
        configured = try values.decodeIfPresent(Bool.self, forKey: .configured) ?? false
        sourceURL = try values.decodeIfPresent(String.self, forKey: .sourceURL) ?? ""
        sourceError = try values.decodeIfPresent(String.self, forKey: .sourceError) ?? ""
        activeProfile = try values.decodeIfPresent(DobbyProfile.self, forKey: .activeProfile)
        lastFailure = try values.decodeIfPresent(DobbyFailure.self, forKey: .lastFailure)
        recovering = try values.decodeIfPresent(Bool.self, forKey: .recovering) ?? false
        digest = try values.decodeIfPresent(String.self, forKey: .digest) ?? ""
        profiles = try values.decodeIfPresent([DobbyProfile].self, forKey: .profiles) ?? []
        activeDigest = try values.decodeIfPresent(String.self, forKey: .activeDigest) ?? ""
        activeMode = try values.decodeIfPresent(String.self, forKey: .activeMode) ?? ""
        activeIndex = try values.decodeIfPresent(Int.self, forKey: .activeIndex) ?? 0
        pendingTarget = try values.decodeIfPresent(DobbySelection.self, forKey: .pendingTarget)
        canSwitch = try values.decodeIfPresent(Bool.self, forKey: .canSwitch) ?? false
    }

    public init(
        sessionID: String, sequence: Int64, generation: Int64, state: String, primaryAction: String,
        configured: Bool, sourceURL: String, sourceError: String,
        activeProfile: DobbyProfile?, lastFailure: DobbyFailure?, recovering: Bool
    ) {
        self.sessionID = sessionID
        self.sequence = sequence
        self.generation = generation
        self.state = state
        self.primaryAction = primaryAction
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
        index = try values.decodeIfPresent(Int.self, forKey: .index) ?? 0
        protocolName = try values.decodeIfPresent(String.self, forKey: .protocolName) ?? ""
        description = try values.decodeIfPresent(String.self, forKey: .description) ?? ""
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
        }
    }
}
