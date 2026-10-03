import Combine
import Foundation

@MainActor
public final class DobbySessionViewModel: ObservableObject {
    @Published public private(set) var snapshot = DobbySessionSnapshot.empty
    @Published public private(set) var busy = false
    @Published public private(set) var error = "" {
        didSet { if !error.isEmpty && diagnosticErrors.last != error { diagnosticErrors.append(error) } }
    }
    @Published public private(set) var logs = ""
    @Published public private(set) var logsError = "" {
        didSet { if !logsError.isEmpty && diagnosticErrors.last != logsError { diagnosticErrors.append(logsError) } }
    }
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
    private var diagnosticErrors: [String] = []
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
                        if self.diagnosticErrors.last != details { self.diagnosticErrors.append(details) }
                    }
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

    public func refreshLogs() {
        guard !logsInFlight else { return }
        logsInFlight = true
        let client = client
        let errors = diagnosticErrors
        logWorker.async { [weak self, client] in
            let result = readDiagnostics(client: client, errors: errors, preview: true)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.logsInFlight = false
                if self.logs != result.text { self.logs = result.text }
                self.logsError = result.error
            }
        }
    }

    public func prepareLogsExport(completion: @escaping @MainActor (URL) -> Void) {
        let client = client
        let errors = diagnosticErrors
        logWorker.async { [weak self, client] in
            let diagnostics = readDiagnostics(client: client, errors: errors)
            let url = FileManager.default.temporaryDirectory
                .appendingPathComponent("DobbyVPN-logs-\(UUID().uuidString).txt")
            let header = "DobbyVPN \(client.version)\nSource commit: \(client.sourceCommit)\n" +
                "Platform: \(ProcessInfo.processInfo.operatingSystemVersionString)\nCaptured: \(Date())\n\n"
            do {
                try Data((header + diagnostics.text + diagnostics.error).utf8).write(to: url, options: .atomic)
                Task { @MainActor [weak self] in
                    self?.logsError = diagnostics.error
                    completion(url)
                }
            } catch {
                let message = error.localizedDescription
                Task { @MainActor [weak self] in self?.logsError = message }
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

private func readDiagnostics(client: DobbySessionClient, errors: [String], preview: Bool = false) -> (text: String, error: String) {
    var output: [String] = []
    var issues: [String] = []
    for url in client.diagnosticPaths {
        do {
            let text: String
            if preview {
                let file = try FileHandle(forReadingFrom: url)
                do {
                    let size = try file.seekToEnd()
                    try file.seek(toOffset: size > 262_144 ? size - 262_144 : 0)
                    let data = try file.readToEnd() ?? Data()
                    text = String(decoding: data, as: UTF8.self)
                    try file.close()
                } catch {
                    do { try file.close() } catch { issues.append("\(url.path): \(error.localizedDescription)") }
                    throw error
                }
            } else { text = try String(contentsOf: url, encoding: .utf8) }
            output.append("--- \(url.lastPathComponent) ---\n" + text)
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            continue
        } catch {
            issues.append("\(url.path): \(error.localizedDescription)")
        }
    }
    if !errors.isEmpty {
        let details = errors.joined(separator: "\n")
        output.append("UI diagnostics\n" + (preview ? String(details.suffix(262_144)) : details))
    }
    return (output.joined(separator: "\n"), issues.joined(separator: "\n"))
}
