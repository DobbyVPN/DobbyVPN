import Combine
import Foundation

@MainActor
public final class DobbySessionViewModel: ObservableObject {
    @Published public private(set) var snapshot = DobbySessionSnapshot.empty
    @Published public private(set) var busy = false
    @Published public private(set) var error = "" {
        didSet { if !error.isEmpty && error != oldValue { recordError(error) } }
    }
    @Published public private(set) var logs = ""
    @Published public private(set) var logsError = ""
    @Published public private(set) var exportingLogs = false
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
    private var logsVisible = false
    private var logsInFlight = false
    private var lastFailure = ""
    private var diagnosticWriteError = ""
    private let logWorker = DispatchQueue(label: "com.dobbyvpn.native-ui.diagnostics")

    public init(client: DobbySessionClient) {
        self.client = client
        refreshSnapshot()
        timer = Timer.scheduledTimer(withTimeInterval: 0.75, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.refreshSnapshot()
                if self?.logsVisible == true { self?.refreshLogs() }
            }
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
        case "STOP": return snapshot.state == "CONNECTED" ? "Disconnect" : "Cancel"
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
        if !message.isEmpty { recordError(message) }
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
                    if let failure = value.lastFailure {
                        let details = "\(failure.message) (\(failure.code))"
                        if self.lastFailure != details { self.recordError(details) }
                        self.lastFailure = details
                    }
                    if value.lastFailure == nil { self.lastFailure = "" }
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
            let result = runPrimaryAction(
                client: client,
                current: current,
                source: source,
                submitSource: submitSource,
                action: action
            )
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.busy = false
                switch result.outcome {
                case .success:
                    if submitSource && action == "START" { self.markSourceAccepted(source) }
                    self.refreshSnapshot()
                case let .failure(failure):
                    if let refreshed = result.snapshotAfterFailure {
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

    public func setLogsVisible(_ visible: Bool) {
        logsVisible = visible
        if visible { refreshLogs() }
    }

    private func recordError(_ message: String) {
        let path = client.uiDiagnosticPath
        logWorker.async { [weak self] in
            do { try appendUIDiagnostic(message, to: path) } catch {
                var failure = "UI diagnostic write failed: \(String(reflecting: error))\nOriginal diagnostic: \(message)"
                do { try FileHandle.standardError.write(contentsOf: Data((failure + "\n").utf8)) } catch {
                    failure += "\nStderr write failed: \(String(reflecting: error))"
                }
                Task { @MainActor [weak self] in
                    self?.diagnosticWriteError = failure
                    self?.logsError = failure
                }
            }
        }
    }

    public func refreshLogs() {
        guard !logsInFlight else { return }
        logsInFlight = true
        let paths = client.diagnosticPaths
        logWorker.async { [weak self] in
            let result = diagnosticPreview(paths: paths)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.logsInFlight = false
                if self.logs != result.text { self.logs = result.text }
                self.logsError = [result.error, self.diagnosticWriteError].filter { !$0.isEmpty }.joined(separator: "\n")
            }
        }
    }

    public func prepareLogsExport(completion: @escaping @MainActor (URL) -> Void) {
        guard !exportingLogs else { return }
        exportingLogs = true
        let client = client
        let writeError = diagnosticWriteError
        logWorker.async { [weak self, client] in
            let url = FileManager.default.temporaryDirectory
                .appendingPathComponent("DobbyVPN-logs-\(UUID().uuidString).gz")
            let header = "DobbyVPN \(client.version)\nSource commit: \(client.sourceCommit)\n" +
                "Platform: \(ProcessInfo.processInfo.operatingSystemVersionString)\nCaptured: \(ISO8601DateFormatter().string(from: Date()))\n\n"
            do {
                let issues = try exportDiagnostics(paths: client.diagnosticPaths, to: url, header: header + writeError)
                Task { @MainActor [weak self] in
                    self?.exportingLogs = false
                    self?.logsError = issues
                    completion(url)
                }
            } catch {
                let message = String(reflecting: error)
                Task { @MainActor [weak self] in
                    self?.exportingLogs = false
                    self?.reportLogsError(message)
                }
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

private func runPrimaryAction(
    client: DobbySessionClient,
    current: DobbySessionSnapshot,
    source: String,
    submitSource: Bool,
    action: String
) -> (outcome: Result<Void, Error>, snapshotAfterFailure: DobbySessionSnapshot?) {
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
    if case .failure = outcome,
       case let .success((value, _)) = readSnapshot(client: client, sessionID: current.sessionID) {
        snapshotAfterFailure = value
    } else {
        snapshotAfterFailure = nil
    }
    return (outcome, snapshotAfterFailure)
}
