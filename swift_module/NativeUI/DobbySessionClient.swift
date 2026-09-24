import Foundation

/// Small platform boundary shared by the iOS and macOS SwiftUI frontends.
/// The Go session manager owns all connection state and configuration rules.
public protocol DobbySessionClient: AnyObject, Sendable {
    func call(_ method: String, parameters: [String: Any]) -> String
    var diagnosticPaths: [URL] { get }
    var version: String { get }
    var sourceCommit: String { get }
}

public struct DobbySessionSnapshot: Decodable, Sendable {
    public let sessionID: String
    public let sequence: Int64
    public let generation: Int64
    public let state: String
    public let configured: Bool
    public let sourceURL: String
    public let sourceError: String
    public let activeProfile: DobbyProfile?
    public let warnings: [DobbyWarning]
    public let lastFailure: DobbyFailure?
    public let recovering: Bool

    public static let empty = DobbySessionSnapshot(
        sessionID: "", sequence: 0, generation: 0, state: "IDLE",
        configured: false, sourceURL: "", sourceError: "", activeProfile: nil,
        warnings: [], lastFailure: nil, recovering: false
    )

    private enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case sequence, generation, state, configured
        case sourceURL = "source_url"
        case sourceError = "source_error"
        case activeProfile = "active_profile"
        case warnings
        case lastFailure = "last_failure"
        case recovering
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try values.decodeIfPresent(String.self, forKey: .sessionID) ?? ""
        sequence = try values.decodeIfPresent(Int64.self, forKey: .sequence) ?? 0
        generation = try values.decodeIfPresent(Int64.self, forKey: .generation) ?? 0
        state = try values.decodeIfPresent(String.self, forKey: .state) ?? "IDLE"
        configured = try values.decodeIfPresent(Bool.self, forKey: .configured) ?? false
        sourceURL = try values.decodeIfPresent(String.self, forKey: .sourceURL) ?? ""
        sourceError = try values.decodeIfPresent(String.self, forKey: .sourceError) ?? ""
        activeProfile = try values.decodeIfPresent(DobbyProfile.self, forKey: .activeProfile)
        warnings = try values.decodeIfPresent([DobbyWarning].self, forKey: .warnings) ?? []
        lastFailure = try values.decodeIfPresent(DobbyFailure.self, forKey: .lastFailure)
        recovering = try values.decodeIfPresent(Bool.self, forKey: .recovering) ?? false
    }

    public init(
        sessionID: String, sequence: Int64, generation: Int64, state: String,
        configured: Bool, sourceURL: String, sourceError: String,
        activeProfile: DobbyProfile?, warnings: [DobbyWarning],
        lastFailure: DobbyFailure?, recovering: Bool
    ) {
        self.sessionID = sessionID
        self.sequence = sequence
        self.generation = generation
        self.state = state
        self.configured = configured
        self.sourceURL = sourceURL
        self.sourceError = sourceError
        self.activeProfile = activeProfile
        self.warnings = warnings
        self.lastFailure = lastFailure
        self.recovering = recovering
    }
}

public struct DobbyProfile: Decodable, Sendable {
    public let protocolName: String
    public let description: String

    private enum CodingKeys: String, CodingKey {
        case protocolName = "protocol"
        case description
    }

    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        protocolName = try values.decodeIfPresent(String.self, forKey: .protocolName) ?? ""
        description = try values.decodeIfPresent(String.self, forKey: .description) ?? ""
    }
}

public struct DobbyWarning: Decodable, Sendable {
    public let code: String
    public let message: String
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
            return "Enter an HTTPS connection URL or inline configuration"
        case let .diagnostics(message):
            return message
        }
    }
}
