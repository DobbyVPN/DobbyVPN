import Foundation

/// Small platform boundary shared by the iOS and macOS SwiftUI frontends.
/// The Go session manager owns all connection state and configuration rules.
public protocol DobbySessionClient: AnyObject, Sendable {
    func call(_ method: String, parameters: [String: Any]) -> String
    func flushDiagnostics() throws
    var diagnosticPaths: [URL] { get }
    var uiDiagnosticPath: URL { get }
    var version: String { get }
    var sourceCommit: String { get }
}

public extension DobbySessionClient {
    func flushDiagnostics() throws {}
}

/// A platform whose VPN profile is created by the app after the user's consent (iOS).
/// Snapshot fails with `VPN_NOT_SET_UP` until then, or with `VPN_SETUP_FAILED`
/// after a refused or timed-out setup. Only `setUpVPNSnapshot` may create the
/// profile, so the system permission prompt never appears without a user action.
public protocol DobbyVPNSetupProviding: DobbySessionClient {
    func setUpVPNSnapshot() -> String
}

/// iOS VPN setup as the window sees it. Only a `DobbyVPNSetupProviding`
/// client reports it, through Snapshot codes; other platforms never do.
struct DobbyVPNSetupFlow: Equatable {
    enum Report: Equatable { case notSetUp, failed(String) }

    private(set) var notSetUp = false
    /// Detail of the last setup the user started, until a setup succeeds.
    private(set) var failure: String?
    private(set) var explanationVisible = false
    var retryPending = false
    var retryRunning = false

    var canRetry: Bool { failure != nil && !retryPending && !retryRunning }

    /// A Snapshot returned; reports whether a setup tap waited for it.
    mutating func finishSnapshot(retrying: Bool) -> Bool {
        defer { retryPending = false }
        if retrying { retryRunning = false }
        return retryPending
    }

    static func report(_ error: Error, client: DobbySessionClient) -> Report? {
        guard client is DobbyVPNSetupProviding,
              case let DobbyClientError.command(code, message) = error else { return nil }
        switch code {
        case "VPN_NOT_SET_UP": return .notSetUp
        case "VPN_SETUP_FAILED": return .failed(message)
        default: return nil
        }
    }

    /// A Snapshot succeeded, or failed for another reason: no setup state remains.
    mutating func ready() {
        notSetUp = false
        failure = nil
        explanationVisible = false
    }

    /// Not set up is the ordinary first screen, not an error. Returns a new failure once, for the log.
    mutating func observe(_ report: Report) -> String? {
        notSetUp = true
        var next: String?
        if case let .failed(message) = report { next = message }
        defer { failure = next }
        if next != nil { explanationVisible = false }
        return next != failure ? next : nil
    }

    /// A user action needs the profile: explain the iOS prompt once before saving.
    mutating func request() -> Bool {
        guard notSetUp, failure == nil, !explanationVisible, !retryPending, !retryRunning else { return false }
        explanationVisible = true
        return true
    }

    mutating func dismissExplanation() -> Bool {
        guard explanationVisible else { return false }
        explanationVisible = false
        return true
    }
}

/// Reads one Snapshot; a user setup action asks the iOS shell to set up the VPN first.
func readSnapshot(
    client: DobbySessionClient,
    sessionID: String,
    retryingVPNSetup: Bool = false
) -> Result<(DobbySessionSnapshot, reattached: Bool), Error> {
    let response: String
    if retryingVPNSetup, let setup = client as? DobbyVPNSetupProviding {
        response = setup.setUpVPNSnapshot()
    } else {
        response = client.call("Snapshot", parameters: ["session_id": sessionID])
    }
    do {
        return .success((try DobbyResponse.result(from: response, as: DobbySessionSnapshot.self), false))
    } catch DobbyClientError.command(let code, _) where code == "NOT_FOUND" && !sessionID.isEmpty {
        let replacement = client.call("Snapshot", parameters: ["session_id": ""])
        return Result {
            (try DobbyResponse.result(from: replacement, as: DobbySessionSnapshot.self), true)
        }
    } catch {
        return .failure(error)
    }
}

/// macOS: a system extension request waits for the user's approval in System Settings.
/// The window shows the step and its main button opens the pane (#586).
public protocol DobbySystemApprovalProviding: DobbySessionClient {
    var systemApprovalPending: Bool { get }
    func openSystemApprovalSettings()
}

/// Polls the client for a pending System Settings approval, independently of
/// the Go snapshot: the approval belongs to macOS, not to the session.
@MainActor
final class DobbySystemApprovalMonitor: ObservableObject {
    @Published private(set) var pending = false
    private let provider: DobbySystemApprovalProviding?
    private var timer: Timer?

    init(client: DobbySessionClient, interval: TimeInterval = 0.75) {
        provider = client as? DobbySystemApprovalProviding
        guard provider != nil else { return }
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: interval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
    }

    deinit { timer?.invalidate() }

    func refresh() {
        let value = provider?.systemApprovalPending ?? false
        if value != pending { pending = value }
    }

    func openSettings() { provider?.openSystemApprovalSettings() }

    /// While approval is pending the main button opens System Settings instead.
    func title(replacing title: String) -> String { pending ? "Open System Settings" : title }
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
    /// When the service retries a FAILED connection by itself; nil when no attempt is scheduled.
    public let nextRetryAt: Date?

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
        case activeProfile = "active_profile"
        case lastFailure = "last_failure"
        case recovering, digest, profiles
        case activeDigest = "active_digest"
        case activeMode = "active_mode"
        case activeIndex = "active_index"
        case pendingTarget = "pending_target"
        case canSwitch = "can_switch"
        case logsUploadAvailable = "logs_upload_available"
        case nextRetryAtUnixMs = "next_retry_at_unix_ms"
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
        // Older services do not send it; a missing or non-positive value means no scheduled attempt.
        let nextRetryMs = try values.decodeIfPresent(Int64.self, forKey: .nextRetryAtUnixMs) ?? 0
        nextRetryAt = nextRetryMs > 0 ? Date(timeIntervalSince1970: TimeInterval(nextRetryMs) / 1000) : nil
    }

    public init(
        sessionID: String, sequence: Int64, generation: Int64, state: String, primaryAction: String,
        configured: Bool, sourceURL: String, sourceError: String,
        activeProfile: DobbyProfile?, lastFailure: DobbyFailure?, recovering: Bool,
        networkGeneration: UInt64 = 0, vpnActive: Bool = false, canRetry: Bool = false,
        nextRetryAt: Date? = nil
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
        self.nextRetryAt = nextRetryAt
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
    private struct Envelope<Value: Decodable>: Decodable {
        let result: Value?

        private enum Keys: String, CodingKey { case ok, result, error }
        private enum FailureKeys: String, CodingKey { case code, message }

        init(from decoder: Decoder) throws {
            guard let fields = try? decoder.container(keyedBy: Keys.self) else {
                throw DobbyClientError.invalidResponse
            }
            guard (try? fields.decode(Bool.self, forKey: .ok)) == true else {
                let error = try? fields.nestedContainer(keyedBy: FailureKeys.self, forKey: .error)
                throw DobbyClientError.command(
                    code: (try? error?.decode(String.self, forKey: .code)) ?? "INTERNAL",
                    message: (try? error?.decode(String.self, forKey: .message)) ?? "Go backend command failed"
                )
            }
            result = try fields.decodeIfPresent(Value.self, forKey: .result)
        }
    }

    public static func result<T: Decodable>(from text: String, as type: T.Type) throws -> T {
        guard let value = try JSONDecoder().decode(Envelope<T>.self, from: Data(text.utf8)).result else {
            throw DobbyClientError.invalidResponse
        }
        return value
    }

    // A command acknowledgement accepts any result, including an absent one.
    private struct IgnoredResult: Decodable {
        init(from decoder: Decoder) {}
    }

    public static func check(_ text: String) throws {
        _ = try JSONDecoder().decode(Envelope<IgnoredResult>.self, from: Data(text.utf8))
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

/// Remembers whether the current failure came with an automatic attempt the
/// service scheduled. A Retry the user presses looks the same in the snapshot,
/// so only this marks an attempt as automatic.
struct ReconnectStatus {
    private(set) var automatic = false

    mutating func observe(_ snapshot: DobbySessionSnapshot, busy userCommandRunning: Bool) {
        if snapshot.state == "FAILED" {
            // A poll that still shows the failure while the user's command runs does not restart the chain.
            automatic = !userCommandRunning && snapshot.vpnActive && snapshot.nextRetryAt != nil
        } else if snapshot.state == "CONNECTED" || !snapshot.vpnActive || snapshot.lastFailure == nil {
            automatic = false
        }
    }

    /// The user's own Retry, Start or Stop is not an automatic attempt.
    mutating func userActed() { automatic = false }
}

extension DobbySessionViewModel {
    /// The line next to Retry and Stop while the service reconnects a FAILED
    /// connection: a countdown to its next automatic attempt, then
    /// "Reconnecting…" while that automatic attempt runs. A Retry or Start the
    /// user pressed shows no line.
    public func reconnectText(now: Date) -> String? {
        if protectedRetryState, !busy, let next = snapshot.nextRetryAt {
            let seconds = Int(next.timeIntervalSince(now).rounded(.up))
            return seconds > 0 ? "Reconnecting in \(seconds) s…" : "Reconnecting…"
        }
        if reconnect.automatic, snapshot.vpnActive, snapshot.state == "PROBING" || snapshot.state == "PREPARING" {
            return "Reconnecting…"
        }
        return nil
    }
}
