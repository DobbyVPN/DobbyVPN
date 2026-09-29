import Combine
import Foundation

@MainActor
public final class DobbySessionViewModel: ObservableObject {
    @Published public private(set) var snapshot = DobbySessionSnapshot.empty
    @Published public private(set) var busy = false
    @Published public private(set) var error = ""
    @Published public private(set) var logs = ""
    @Published public private(set) var logsError = ""
    @Published public var sourceText = "" {
        didSet {
            sourceIsDirty = sourceText != acceptedSource
        }
    }

    public let client: DobbySessionClient
    private let worker = DispatchQueue(label: "com.dobbyvpn.native-ui.session")
    private var timer: Timer?
    private var snapshotInFlight = false
    private var sourceIsDirty = false
    private var acceptedSource = ""

    public init(client: DobbySessionClient) {
        self.client = client
        refreshSnapshot()
        timer = Timer.scheduledTimer(withTimeInterval: 0.75, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refreshSnapshot() }
        }
    }

    deinit {
        timer?.invalidate()
    }

    public var status: String {
        if !error.isEmpty { return "Error" }
        if snapshot.recovering { return "Reconnecting" }
        switch snapshot.state {
        case "CONNECTED": return "Connected"
        case "PROBING", "PREPARING": return "Connecting"
        case "STOPPING": return "Stopping"
        case "FAILED": return "Failed"
        default: return "Disconnected"
        }
    }

    public var actionTitle: String {
        switch snapshot.primaryAction {
        case "START": return "Connect"
        case "STOP": return snapshot.state == "CONNECTED" ? "Disconnect" : "Stop"
        default: return snapshot.state == "STOPPING" ? "Stopping…" : "Waiting…"
        }
    }

    public var canPerformPrimaryAction: Bool {
        !snapshot.sessionID.isEmpty && ["START", "STOP"].contains(snapshot.primaryAction)
    }

    public func sourceChanged(_ value: String) {
        sourceText = value
        error = ""
    }

    public func reportLogsError(_ message: String) {
        logsError = message
    }

    public func refreshSnapshot() {
        guard !snapshotInFlight else { return }
        snapshotInFlight = true
        let currentID = snapshot.sessionID
        let recoveringFromSnapshotFailure = currentID.isEmpty && !error.isEmpty
        let client = client
        worker.async { [weak self, client] in
            let decoded = readSnapshot(client: client, sessionID: currentID)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.snapshotInFlight = false
                switch decoded {
                case let .success((value, reattached)):
                    self.snapshot = value
                    if !self.sourceIsDirty {
                        if !value.sourceURL.isEmpty {
                            self.acceptedSource = value.sourceURL
                            self.sourceText = value.sourceURL
                        } else if !value.configured {
                            self.acceptedSource = ""
                            self.sourceText = ""
                        }
                    }
                    if !value.sourceError.isEmpty {
                        self.error = value.sourceError
                    } else if reattached || recoveringFromSnapshotFailure {
                        self.error = ""
                    }
                case let .failure(failure):
                    self.snapshot = .empty
                    self.error = failure.localizedDescription
                }
            }
        }
    }

    public func performPrimaryAction() {
        guard !busy else { return }
        guard canPerformPrimaryAction else { return }
        guard !snapshot.sessionID.isEmpty else {
            error = "Go backend session is not ready"
            return
        }
        busy = true
        error = ""
        let current = snapshot
        let action = current.primaryAction
        let source = sourceText.trimmingCharacters(in: .whitespacesAndNewlines)
        let submitSource = action == "START" && (!current.configured || sourceIsDirty)
        let client = client
        worker.async { [weak self, client, current, source, submitSource, action] in
            let outcome = Result {
                if action == "STOP" {
                    let response = client.call("Stop", parameters: [
                        "session_id": current.sessionID,
                        "generation": current.generation,
                    ])
                    try DobbyResponse.check(response)
                } else if action == "START" {
                    guard !submitSource || !source.isEmpty else { throw DobbyClientError.noConfiguration }
                    var parameters: [String: Any] = [
                        "session_id": current.sessionID,
                        "expected_sequence": current.sequence,
                        "mode": "AUTO_SELECT",
                        "index": 0,
                    ]
                    if submitSource { parameters["source"] = source }
                    let response = client.call("Start", parameters: parameters)
                    try DobbyResponse.check(response)
                } else {
                    throw DobbyClientError.invalidResponse
                }
            }
            let snapshotAfterFailure: DobbySessionSnapshot?
            if case .failure = outcome {
                if case let .success((value, _)) = readSnapshot(client: client, sessionID: current.sessionID) {
                    snapshotAfterFailure = value
                } else {
                    snapshotAfterFailure = nil
                }
            } else {
                snapshotAfterFailure = nil
            }
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.busy = false
                switch outcome {
                case .success:
                    if submitSource && action == "START" { self.markSourceAccepted(source) }
                    self.refreshSnapshot()
                case let .failure(failure):
                    if let refreshed = snapshotAfterFailure {
                        self.snapshot = refreshed
                    }
                    self.error = failure.localizedDescription
                    self.refreshSnapshot()
                }
            }
        }
    }

    private func markSourceAccepted(_ source: String) {
        acceptedSource = source
        sourceIsDirty = false
        sourceText = source
    }

    public func refreshLogs() {
        logsError = ""
        let client = client
        worker.async { [weak self, client] in
            var output: [String] = []
            var errors: [String] = []
            for url in client.diagnosticPaths {
                guard FileManager.default.fileExists(atPath: url.path) else { continue }
                do {
                    output.append(try String(contentsOf: url, encoding: .utf8))
                } catch {
                    errors.append("\(url.path): \(error.localizedDescription)")
                }
            }
            let text = output.joined(separator: "\n")
            let issue = errors.joined(separator: "\n")
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.logs = text
                self.logsError = issue
            }
        }
    }
}

private func readSnapshot(
    client: DobbySessionClient,
    sessionID: String
) -> Result<(DobbySessionSnapshot, reattached: Bool), Error> {
    let response = client.call("Snapshot", parameters: ["session_id": sessionID])
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
