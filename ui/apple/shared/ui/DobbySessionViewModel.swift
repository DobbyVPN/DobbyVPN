import Combine
import Foundation

@MainActor
public final class DobbySessionViewModel: ObservableObject {
    @Published public private(set) var snapshot = DobbySessionSnapshot.empty
    @Published public private(set) var busy = false
    @Published public private(set) var error = "" {
        didSet { if !error.isEmpty && error != oldValue { recordError(error) } }
    }
    @Published public private(set) var importError = ""
    @Published var logEntries: [DobbyLogEntry] = []
    @Published var clearRevision = 0
    @Published public private(set) var logsError = ""
    @Published public private(set) var exportingLogs = false
    @Published public var sourceText = "" {
        didSet {
            sourceIsDirty = sourceText != acceptedSource
        }
    }

    @Published public private(set) var loading = false
    @Published public private(set) var loadError = ""
    private let loadWorker = DispatchQueue(label: "com.dobbyvpn.native-ui.configuration")
    private var loadInFlight = false
    private var loadRevision = 0
    private var pendingLoad: String?
    private var debounce: Task<Void, Never>?
    private var restoredLoad = ""
    private var acceptedSequence: Int64 = 0

    public let client: DobbySessionClient
    private let worker = DispatchQueue(label: "com.dobbyvpn.native-ui.session")
    private var timer: Timer?
    private var snapshotInFlight = false
    private var sourceIsDirty = false
    private var acceptedSource = ""
    private var logsVisible = false
    private var logsInFlight = false
    private var logsRefreshPending = false
    private var logsClearInFlight = false
    private var logsRevision = 0
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

    public var inventoryReady: Bool {
        snapshot.configured && snapshot.sequence >= acceptedSequence && !sourceIsDirty && !loading && loadError.isEmpty
    }

    public func isStopTarget(_ index: Int?) -> Bool {
        if let pending = snapshot.pendingTarget {
            return pending.digest == snapshot.digest && (index == nil ? pending.mode == "AUTO_SELECT" : pending.mode == "PROFILE_INDEX" && pending.index == index)
        }
        guard snapshot.primaryAction == "STOP" else { return false }
        if index == nil { return snapshot.activeMode == "AUTO_SELECT" && snapshot.activeDigest == snapshot.digest }
        guard snapshot.activeDigest == snapshot.digest else { return false }
        return snapshot.state == "CONNECTED" ? snapshot.activeProfile?.index == index : snapshot.activeMode == "PROFILE_INDEX" && snapshot.activeIndex == index
    }

    public func actionTitle(_ index: Int? = nil) -> String {
        if isStopTarget(index) { return snapshot.state == "CONNECTED" && snapshot.pendingTarget == nil ? "Disconnect" : "Stop" }
        return index == nil ? "Auto connect" : "Connect"
    }

    public func canAct(_ index: Int? = nil) -> Bool {
        !busy && !snapshot.sessionID.isEmpty && (isStopTarget(index) || inventoryReady && (snapshot.primaryAction == "START" || snapshot.canSwitch))
    }

    public func sourceChanged(_ value: String, immediate: Bool = false) {
        if !immediate && value == sourceText { return }
        sourceText = value
        sourceIsDirty = true
        importError = ""
        error = ""
        loadError = ""
        loadRevision += 1
        pendingLoad = nil
        debounce?.cancel()
        let source = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard validSubscription(source) else { return }
        debounce = Task { [weak self] in
            if !immediate { try? await Task.sleep(nanoseconds: 400_000_000) }
            guard !Task.isCancelled, let self else { return }
            self.pendingLoad = source
            self.loadNext()
        }
    }

    public func paste(_ value: String) {
        let source = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard validSubscription(source) else { error = "Paste an HTTPS subscription URL with a host"; return }
        if source == sourceText && (loading || snapshot.configured && !sourceIsDirty) { return }
        sourceChanged(source, immediate: true)
    }

    public func importLink(_ url: URL) {
        do {
            let source = try subscriptionFromLink(url.absoluteString)
            guard let source else { return }
            if !importError.isEmpty {
                if error == importError { error = "" }
                importError = ""
            }
            paste(source)
        } catch {
            importError = error.localizedDescription
            self.error = error.localizedDescription
        }
    }

    public func retryLoad() { sourceChanged(sourceText, immediate: true) }

    private func loadNext() {
        guard !loadInFlight, let source = pendingLoad, !snapshot.sessionID.isEmpty else { return }
        pendingLoad = nil
        loadInFlight = true
        loading = true
        let revision = loadRevision
        let client = client
        loadWorker.async { [weak self, client] in
            let result = Result {
                let current = try DobbyResponse.result(from: client.call("Snapshot", parameters: ["session_id": ""]), as: DobbySessionSnapshot.self)
                let response = client.call("Configure", parameters: [
                    "session_id": current.sessionID, "expected_sequence": current.sequence, "source": source,
                ])
                return try DobbyResponse.result(from: response, as: ConfigurationReceipt.self).sequence
            }
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.loadInFlight = false
                self.loading = false
                if revision == self.loadRevision {
                    switch result {
                    case let .success(sequence): self.acceptedSequence = sequence; self.markSourceAccepted(source)
                    case let .failure(failure): self.loadError = failure.localizedDescription
                    }
                }
                self.refreshSnapshot()
                self.loadNext()
            }
        }
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
                    if value.sessionID == self.snapshot.sessionID && value.sequence < max(self.snapshot.sequence, self.acceptedSequence) { return }
                    let sameSession = value.sessionID == self.snapshot.sessionID
                    if !sameSession { self.acceptedSequence = 0 }
                    var displayedValue = value
                    if sameSession && self.sourceIsDirty {
                        displayedValue.configured = self.snapshot.configured
                        displayedValue.sourceURL = self.snapshot.sourceURL
                        displayedValue.digest = self.snapshot.digest
                        displayedValue.profiles = self.snapshot.profiles
                    }
                    self.snapshot = displayedValue
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
                    let restoreKey = value.sessionID + "|" + self.sourceText
                    if !value.configured && !self.sourceIsDirty && !self.sourceText.isEmpty && self.restoredLoad != restoreKey {
                        self.restoredLoad = restoreKey
                        self.sourceChanged(self.sourceText, immediate: true)
                    }
                    self.loadNext()
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

    public func performPrimaryAction(_ index: Int? = nil) {
        guard canAct(index) else { return }
        if isStopTarget(index) { stop(); return }
        performCommand(index: index, stopping: false)
    }

    public func stop() {
        guard !busy, snapshot.primaryAction == "STOP" else { return }
        performCommand(index: nil, stopping: true)
    }

    private func performCommand(index: Int?, stopping: Bool) {
        busy = true
        error = ""
        let current = snapshot
        let client = client
        worker.async { [weak self, client] in
            let outcome = Result {
                if stopping {
                    try DobbyResponse.check(client.call("Stop", parameters: ["session_id": current.sessionID, "generation": current.generation]))
                } else {
                    try DobbyResponse.check(client.call("Start", parameters: [
                        "session_id": current.sessionID, "expected_sequence": current.sequence,
                        "mode": index == nil ? "AUTO_SELECT" : "PROFILE_INDEX", "index": index ?? 0,
                        "digest": current.digest, "replace_current": true,
                    ]))
                }
            }
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.busy = false
                if case let .failure(failure) = outcome { self.error = failure.localizedDescription }
                self.refreshSnapshot()
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
            do {
                try appendUIDiagnostic(message, to: path)
                Task { @MainActor [weak self] in
                    guard let self, self.logsVisible else { return }
                    self.refreshLogs()
                }
            } catch {
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
        guard !logsInFlight else { logsRefreshPending = true; return }
        logsInFlight = true
        logsRefreshPending = false
        let revision = logsRevision
        let paths = client.diagnosticPaths
        let boundary = client.uiDiagnosticPath.appendingPathExtension("view")
        logWorker.async { [weak self] in
            let result = structuredPreview(paths: paths, boundary: boundary)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.logsInFlight = false
                if revision == self.logsRevision && !self.logsClearInFlight {
                    self.logEntries = result.entries
                    self.logsError = [result.error, self.diagnosticWriteError].filter { !$0.isEmpty }.joined(separator: "\n")
                } else {
                    self.logsRefreshPending = true
                }
                if self.logsRefreshPending && !self.logsClearInFlight {
                    self.refreshLogs()
                }
            }
        }
    }

    public func clearLogs() {
        logsRevision += 1
        let revision = logsRevision
        logsClearInFlight = true
        logsRefreshPending = true
        let paths = client.diagnosticPaths
        let boundary = client.uiDiagnosticPath.appendingPathExtension("view")
        logWorker.async { [weak self] in
            let result = Result { try clearDiagnosticView(paths: paths, boundary: boundary) }
            Task { @MainActor [weak self] in
                guard let self, revision == self.logsRevision else { return }
                self.logsClearInFlight = false
                switch result {
                case .success:
                    self.logEntries = []
                    self.clearRevision += 1
                case let .failure(error): self.reportLogsError(error.localizedDescription)
                }
                self.refreshLogs()
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

func validSubscription(_ value: String) -> Bool {
    guard let url = URLComponents(string: value) else { return false }
    return url.scheme?.lowercased() == "https" && !(url.host ?? "").isEmpty
}

func subscriptionFromLink(_ value: String) throws -> String? {
    func invalid() -> DobbyClientError { .command(code: "INVALID_ARGUMENT", message: "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL") }
    guard value.range(of: "%(?![0-9a-fA-F]{2})", options: .regularExpression) == nil,
          let link = URLComponents(string: value), link.scheme?.lowercased() == "dobbyvpn",
          link.user == nil, link.password == nil, link.port == nil, link.fragment == nil else { throw invalid() }
    if (link.host ?? "").isEmpty && link.path.isEmpty && link.query == nil { return nil }
    guard link.host == "import", link.path.isEmpty,
          let parameters = link.queryItems, parameters.count == 1,
          parameters[0].name == "url", let source = parameters[0].value,
          validSubscription(source) else { throw invalid() }
    return source
}

private struct ConfigurationReceipt: Decodable { let sequence: Int64 }
