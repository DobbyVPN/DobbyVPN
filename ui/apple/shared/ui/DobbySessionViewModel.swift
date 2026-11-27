import Combine
import Foundation

private enum DobbySessionErrorOrigin: Equatable {
    case sourceSnapshot
    case other
}

private struct DobbySourceErrorIdentity: Equatable {
    let sessionID: String
    let message: String
}

@MainActor
protocol DobbySessionScheduledWork: AnyObject {
    func cancel()
}

@MainActor
protocol DobbySessionDebounceScheduling {
    func schedule(afterNanoseconds delay: UInt64, action: @escaping @MainActor () -> Void) -> any DobbySessionScheduledWork
}

@MainActor
private final class TaskScheduledWork: DobbySessionScheduledWork {
    var task: Task<Void, Never>?

    func cancel() { task?.cancel() }
}

@MainActor
private struct SystemSessionDebounceScheduler: DobbySessionDebounceScheduling {
    func schedule(afterNanoseconds delay: UInt64, action: @escaping @MainActor () -> Void) -> any DobbySessionScheduledWork {
        let work = TaskScheduledWork()
        work.task = Task {
            do { try await Task.sleep(nanoseconds: delay) } catch { return }
            guard !Task.isCancelled else { return }
            action()
        }
        return work
    }
}

public enum DobbyProfileStatus: String {
    case connecting, connected, reconnecting, stopping, failed
}

@MainActor
public final class DobbySessionViewModel: ObservableObject {
    @Published public private(set) var snapshot = DobbySessionSnapshot.empty { didSet { reconnect.observe(snapshot, busy: busy) } }
    var reconnect = ReconnectStatus() // read by reconnectText(now:) in DobbySessionClient.swift
    @Published public private(set) var busy = false
    @Published public private(set) var error = ""
    @Published public private(set) var importError = ""
    /// An import link waits here until the user confirms that it may replace the saved subscription.
    @Published public private(set) var pendingImport: String?
    @Published var logEntries: [DobbyLogEntry] = []
    #if os(macOS)
    private var lastMacLogCaptureDiagnosticUptime: TimeInterval?
    var macLogCaptureDiagnosticNow: () -> TimeInterval = { ProcessInfo.processInfo.systemUptime }
    #endif
    @Published var clearRevision = 0
    @Published public private(set) var logsError = ""
    @Published public private(set) var exportingLogs = false
    @Published var uploadStatus = ""
    @Published var uploadError = ""
    @Published var latestUpload: DobbyUploadReference?
    private var uploadTask: Task<Void, Never>?
    private var uploadCancellation: DobbyUploadCancellation?
    private let uploadReferenceKey = "DobbyVPN.latestLogUpload"
    private static var cleanedUploadDirectories = false

    @Published public var sourceText = "" {
        didSet {
            sourceIsDirty = sourceText != acceptedSource
        }
    }

    @Published public private(set) var loading = false
    @Published public private(set) var loadError = ""
    @Published private var dismissedFailures = Set<String>()
    private let loadWorker = DispatchQueue(label: "com.dobbyvpn.native-ui.configuration")
    private var loadInFlight = false
    private var loadRevision = 0
    private var pendingLoad: (source: String, automaticOwner: String?)?
    private let debounceScheduler: any DobbySessionDebounceScheduling
    private var debounce: (any DobbySessionScheduledWork)?
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
    private let logViewCache = LogViewCache()
    @Published private var disconnectFailure = ""
    private var lastFailure = ""
    private var errorOrigin: DobbySessionErrorOrigin?
    private var lastSourceErrorIdentity: DobbySourceErrorIdentity?
    private var snapshotFailureMessage: String?
    @Published private var vpnSetup = DobbyVPNSetupFlow()
    private var diagnosticWriteError = ""
    private let logWorker = DispatchQueue(label: "com.dobbyvpn.native-ui.diagnostics")

    public convenience init(client: DobbySessionClient) {
        self.init(client: client, debounceScheduler: SystemSessionDebounceScheduler(), automaticallyPoll: true)
    }

    convenience init(client: DobbySessionClient, automaticallyPoll: Bool) {
        self.init(client: client, debounceScheduler: SystemSessionDebounceScheduler(), automaticallyPoll: automaticallyPoll)
    }

    init(
        client: DobbySessionClient,
        debounceScheduler: any DobbySessionDebounceScheduling,
        automaticallyPoll: Bool = true
    ) {
        self.client = client
        self.debounceScheduler = debounceScheduler
        latestUpload = DobbyLatestUploadFile.load(forLog: client.uiDiagnosticPath, defaultsKey: uploadReferenceKey)
        if !Self.cleanedUploadDirectories {
            Self.cleanedUploadDirectories = true
            do { try DobbyUploadDirectory.removeAbandoned() }
            catch { uploadError = "Could not clean abandoned uploads: \(String(reflecting: error))" }
        }
        recordAppStarted()
        if !uploadError.isEmpty { recordError(uploadError) }
        refreshSnapshot()
        if automaticallyPoll {
            timer = Timer.scheduledTimer(withTimeInterval: 0.75, repeats: true) { [weak self] _ in
                Task { @MainActor in self?.refreshForTimerTick() }
            }
        }
    }

    @MainActor
    func refreshForTimerTick() {
        refreshSnapshot()
        if logsVisible { refreshLogs() }
    }

    deinit {
        timer?.invalidate()
    }

    public var status: String {
        if !error.isEmpty { return "Error" }
        if snapshot.state == "FAILED" { return "Failed" }
        if snapshot.recovering { return "Reconnecting" }
        switch snapshot.state {
        case "CONNECTED": return "Connected"
        case "PROBING", "PREPARING": return "Connecting"
        case "STOPPING": return "Stopping"
        default: return "Disconnected"
        }
    }

    public var inventoryReady: Bool {
        snapshot.configured && snapshot.sequence >= acceptedSequence && !sourceIsDirty && !loading && (loadError.isEmpty || snapshot.sourceURL == sourceText)
    }

    var protectedRetryState: Bool {
        snapshot.state == "FAILED" && snapshot.vpnActive && snapshot.canRetry && snapshot.primaryAction == "RETRY"
    }

    public var canRetryActiveSelection: Bool {
        !busy && !snapshot.sessionID.isEmpty && protectedRetryState
    }

    private var networkCanBeStopped: Bool {
        snapshot.vpnActive || snapshot.primaryAction == "STOP"
    }

    public var canStop: Bool {
        !busy && !snapshot.sessionID.isEmpty && networkCanBeStopped
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
        if isStopTarget(index) { return snapshot.state == "CONNECTED" && snapshot.pendingTarget == nil ? "Connected" : "Stop" }
        return index == nil ? "Auto connect" : snapshot.state == "CONNECTED" && snapshot.pendingTarget == nil ? "Switch" : "Connect"
    }

    public func canAct(_ index: Int? = nil) -> Bool {
        guard !busy, !snapshot.sessionID.isEmpty else { return false }
        if isStopTarget(index) { return true }
        if protectedRetryState { return index == nil && canRetryActiveSelection }
        return inventoryReady && (snapshot.primaryAction == "START" || snapshot.canSwitch)
    }

    public func dismissError() {
        if importError.isEmpty {
            error = ""
            return
        }
        if error == importError { error = "" }
        importError = ""
    }

    public func sourceChanged(_ value: String, immediate: Bool = false) {
        scheduleSourceLoad(value, immediate: immediate, automaticOwner: nil)
    }

    public func paste(_ value: String) {
        resetClosedFailures()
        let source = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard validSubscription(source) else {
            let message = "Paste an HTTPS subscription URL with a host"
            // Closing the popup keeps the error, so a repeated rejection is logged here.
            if error == message { recordError(message) }
            setError(message, origin: .other)
            return
        }
        if source == sourceText && (loading || snapshot.configured && !sourceIsDirty) { return }
        sourceChanged(source, immediate: true)
    }

    func pasteCallback(_ values: [String]) {
        let outcome = values.isEmpty ? "empty" : "dispatch_first"
        recordDiagnostic(
            "Paste callback received; item_count=\(values.count); outcome=\(outcome)",
            event: "ui.paste_callback",
            level: "INFO"
        )
        guard let first = values.first else { return }
        paste(first)
    }

    private var importRevision: UInt64 = 0
    private(set) var parsingImport = false

    public func importLink(_ url: URL) {
        importRevision &+= 1
        let revision = importRevision
        parsingImport = true
        let link = url.absoluteString
        worker.async { [weak self, client] in
            let result = Result {
                try DobbyResponse.result(from: client.call("ParseImportLink", parameters: ["link": link]),
                                         as: DobbyImportLink.self)
            }
            Task { @MainActor [weak self] in
                guard let self, self.importRevision == revision else { return }
                self.parsingImport = false
                switch result {
                case let .success(parsed):
                    guard parsed.kind == "import", let source = parsed.source else { return }
                    if !self.importError.isEmpty {
                        if self.error == self.importError { self.setError("", origin: .other) }
                        self.importError = ""
                    }
                    self.pendingImport = source
                case let .failure(failure):
                    self.importError = failure.localizedDescription
                    self.setError(failure.localizedDescription, origin: .other)
                }
            }
        }
    }

    public func retryLoad() {
        resetClosedFailures()
        sourceChanged(sourceText, immediate: true)
    }

    private func loadNext() {
        guard !loadInFlight, let (source, automaticOwner) = pendingLoad, !snapshot.sessionID.isEmpty else { return }
        pendingLoad = nil
        loadInFlight = true
        loading = true
        let revision = loadRevision
        let client = client
        loadWorker.async { [weak self, client] in
            let result = Result { () -> Int64? in
                let current = try DobbyResponse.result(from: client.call("Snapshot", parameters: ["session_id": ""]), as: DobbySessionSnapshot.self)
                if let automaticOwner, current.sessionID != automaticOwner || current.serviceRestorePending || current.configured {
                    // Recheck the live owner before loading its saved source. Configure supersedes its restore.
                    return nil
                }
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
                    case let .success(sequence?):
                        self.acceptedSequence = sequence
                        self.markSourceAccepted(source)
                    case .success(nil):
                        self.sourceIsDirty = false
                    case let .failure(failure) where DobbyVPNSetupFlow.report(failure, client: client) != nil:
                        // The VPN profile is gone; keep the subscription until setup succeeds.
                        if self.pendingLoad == nil { self.pendingLoad = (source, automaticOwner) }
                    case let .failure(failure):
                        let accepted = self.snapshot.sourceURL
                        let sameSource = self.snapshot.configured && !accepted.isEmpty && accepted == source
                        if sameSource { self.sourceIsDirty = false }
                        let owner = self.snapshot.configured && !sameSource && !accepted.isEmpty
                            ? " Showing profiles from \(accepted)." : ""
                        self.loadError = "Could not load \(source): \(failure.localizedDescription).\(owner)"
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
        refreshSnapshot(retryingVPNSetup: false)
    }

    private func refreshSnapshot(retryingVPNSetup: Bool) {
        guard !snapshotInFlight else { return }
        snapshotInFlight = true
        if retryingVPNSetup { updateVPNSetup { $0.retryRunning = true } }
        let currentID = snapshot.sessionID
        let recoveringFromSnapshotFailure = currentID.isEmpty && snapshotFailureMessage != nil
        let client = client
        worker.async { [weak self, client] in
            let observer = client as? DobbyDisconnectObserving
            let ticket = observer?.beginSnapshotObservation()
            let decoded = readSnapshot(client: client, sessionID: currentID, retryingVPNSetup: retryingVPNSetup)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.snapshotInFlight = false
                let retryPending = self.updateVPNSetup { $0.finishSnapshot(retrying: retryingVPNSetup) }
                self.disconnectFailure = observer?.disconnectFailure ?? ""
                switch decoded {
                case let .success((value, reattached)):
                    if value.sessionID == self.snapshot.sessionID && value.sequence < max(self.snapshot.sequence, self.acceptedSequence) { return }
                    self.updateVPNSetup { $0.ready() }
                    if let observer, let ticket {
                        observer.observedSnapshot(value, ticket: ticket)
                        self.disconnectFailure = observer.disconnectFailure
                    }
                    let sameSession = value.sessionID == self.snapshot.sessionID
                    if value.serviceRestorePending && (!sameSession || !self.snapshot.serviceRestorePending) {
                        self.recordDiagnostic(
                            "Waiting for the service to restore its saved VPN session. session_id=\(value.sessionID)",
                            event: "subscription.restore-pending", level: "INFO"
                        )
                    }
                    if !sameSession {
                        self.acceptedSequence = 0
                        self.clearError(from: .sourceSnapshot)
                    }
                    var displayedValue = value
                    if sameSession && self.sourceIsDirty {
                        displayedValue.configured = self.snapshot.configured
                        displayedValue.sourceURL = self.snapshot.sourceURL
                        displayedValue.offlineAcceptedAt = self.snapshot.offlineAcceptedAt
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
                    if !value.configured && !value.serviceRestorePending && !self.sourceIsDirty &&
                        !self.sourceText.isEmpty && self.restoredLoad != restoreKey {
                        self.restoredLoad = restoreKey
                        self.scheduleSourceLoad(self.sourceText, immediate: true, automaticOwner: value.sessionID)
                    }
                    self.loadNext()
                    if reattached || recoveringFromSnapshotFailure { self.clearSnapshotFailure() }
                    self.observeSourceError(value.sourceError, sessionID: value.sessionID)
                case let .failure(failure):
                    self.observeSnapshotFailure(failure)
                }
                self.pruneClosedFailures()
                if retryPending, case .failure = decoded {
                    self.refreshSnapshot(retryingVPNSetup: true)
                }
            }
        }
    }

    public func performPrimaryAction(_ index: Int? = nil) {
        guard canAct(index) else { return }
        if protectedRetryState, index == nil { retryActiveSelection(); return }
        if isStopTarget(index) { stop(); return }
        performCommand(index: index, stopping: false)
    }

    public func retryActiveSelection() {
        guard canRetryActiveSelection else { return }
        performCommand(index: nil, stopping: false, retrying: true)
    }

    public func stop() {
        guard canStop else { return }
        performCommand(index: nil, stopping: true)
    }

    private func performCommand(index: Int?, stopping: Bool, retrying: Bool = false) {
        reconnect.userActed()
        if stopping { acknowledgeDisplayedTransportFailure() } else { resetClosedFailures() }
        busy = true
        setError("", origin: .other)
        let current = snapshot
        let client = client
        worker.async { [weak self, client] in
            let outcome = Result {
                if stopping {
                    try DobbyResponse.check(client.call("Stop", parameters: ["session_id": current.sessionID, "generation": current.generation]))
                } else if retrying {
                    let latest = try DobbyResponse.result(from: client.call("Snapshot", parameters: ["session_id": current.sessionID]), as: DobbySessionSnapshot.self)
                    guard latest.canRetry, latest.vpnActive, latest.state == "FAILED",
                          latest.generation == current.generation, latest.activeDigest == current.activeDigest,
                          latest.activeMode == current.activeMode, latest.activeIndex == current.activeIndex else {
                        throw NSError(domain: "DobbyVPN.Session", code: 1, userInfo: [NSLocalizedDescriptionKey: "Connection changed before Retry. Refresh and try again."])
                    }
                    try DobbyResponse.check(client.call("Start", parameters: [
                        "session_id": current.sessionID, "expected_sequence": latest.sequence,
                        "mode": current.activeMode, "index": current.activeIndex,
                        "digest": current.activeDigest,
                    ]))
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
                if case let .failure(failure) = outcome, DobbyVPNSetupFlow.report(failure, client: client) == nil {
                    let message = failure.localizedDescription
                    self.dismissedFailures.remove(message)
                    self.setError(message, origin: .other)
                }
                self.refreshSnapshot()
            }
        }
    }

    private func setError(
        _ message: String,
        origin: DobbySessionErrorOrigin,
        record: Bool = true,
        preservingSnapshotFailure: Bool = false
    ) {
        if !preservingSnapshotFailure { snapshotFailureMessage = nil }
        if error == message {
            if message.isEmpty {
                errorOrigin = nil
            } else if origin == .other || errorOrigin == nil {
                // Source observations must not take ownership of an identical unrelated banner.
                errorOrigin = origin
            }
            return
        }
        errorOrigin = message.isEmpty ? nil : origin
        error = message
        if record && !message.isEmpty { recordError(message) }
    }

    private func clearError(from origin: DobbySessionErrorOrigin) {
        guard errorOrigin == origin else { return }
        setError("", origin: origin)
    }

    private func clearSnapshotFailure() {
        guard let failure = snapshotFailureMessage else { return }
        snapshotFailureMessage = nil
        guard errorOrigin == .other, error == failure else { return }
        setError("", origin: .other)
    }

    private func observeSourceError(_ message: String, sessionID: String) {
        guard !message.isEmpty else {
            lastSourceErrorIdentity = nil
            if !sourceIsDirty { clearError(from: .sourceSnapshot) }
            return
        }

        let identity = DobbySourceErrorIdentity(sessionID: sessionID, message: message)
        if lastSourceErrorIdentity != identity {
            lastSourceErrorIdentity = identity
            recordError("Source snapshot error for session \(sessionID): \(message)")
        }
        guard !sourceIsDirty else { return }
        setError(message, origin: .sourceSnapshot, record: false)
    }

    public func setLogsVisible(_ visible: Bool) {
        logsVisible = visible
        if visible { refreshLogs() }
    }

    public func refreshLogs() {
        guard !logsInFlight else { logsRefreshPending = true; return }
        logsInFlight = true
        logsRefreshPending = false
        let revision = logsRevision
        let paths = client.diagnosticPaths
        let boundary = client.uiDiagnosticPath.appendingPathExtension("view")
        let cache = logViewCache
        logWorker.async { [weak self, cache] in
            let result = cache.preview(paths: paths, boundary: boundary)
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.logsInFlight = false
                if revision == self.logsRevision && !self.logsClearInFlight {
                    // Publish only real changes so an idle log view is not redrawn.
                    if self.logEntries != result.entries { self.logEntries = result.entries }
                    let logsError = [result.error, self.diagnosticWriteError].filter { !$0.isEmpty }.joined(separator: "\n")
                    if self.logsError != logsError { self.logsError = logsError }
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
                    self.recordAppStarted()
                case let .failure(error): self.reportLogsError(error.localizedDescription)
                }
                self.refreshLogs()
            }
        }
    }
}

private extension DobbySessionViewModel {
    func scheduleSourceLoad(_ value: String, immediate: Bool, automaticOwner: String?) {
        if !immediate && value == sourceText { return }
        sourceText = value
        sourceIsDirty = true
        importError = ""
        setError("", origin: .other)
        loadError = ""
        // Typing only forgets the closed failures it cleared; a persistent one must not reopen on every keystroke.
        pruneClosedFailures()
        loadRevision += 1
        pendingLoad = nil
        debounce?.cancel()
        debounce = nil
        let source = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard validSubscription(source) else { return }
        let enqueueLoad: @MainActor () -> Void = { [weak self] in
            guard let self else { return }
            self.debounce = nil
            self.pendingLoad = (source, automaticOwner)
            // A subscription entered before setup is the user action that starts it.
            self.requestVPNSetup()
            self.loadNext()
        }
        if immediate {
            enqueueLoad()
        } else {
            debounce = debounceScheduler.schedule(afterNanoseconds: 400_000_000, action: enqueueLoad)
        }
    }
}

extension DobbySessionViewModel {
    private func recordAppStarted() {
        let commit = client.sourceCommit == "N/A" ? "" : String(client.sourceCommit.prefix(8))
        let line = ["app started", "doBBYVPN \(client.version)", commit].filter { !$0.isEmpty }.joined(separator: " · ")
        recordDiagnostic(line, event: "app.started", level: "INFO")
    }

    private func recordError(_ message: String) {
        recordDiagnostic(message)
    }

    private func recordDiagnostic(
        _ message: String,
        event: String = "ui.failure",
        level: String = "ERROR"
    ) {
        let path = client.uiDiagnosticPath
        let excludedPaths = client.diagnosticPaths
        logWorker.async { [weak self] in
            do {
                try appendUIDiagnostic(message, to: path, event: event, level: level, excludedPaths: excludedPaths)
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

    func recordLogsShare(kind: String, phase: String, context: String = "") {
        recordDiagnostic("kind=\(kind) phase=\(phase)\(context.isEmpty ? "" : " " + context)",
                         event: "ui.logs_share", level: "INFO")
    }
}

extension DobbySessionViewModel {
    func cancelLogsUpload() { uploadCancellation?.cancel(); uploadTask?.cancel() }

    func startLogsUpload() {
        guard uploadTask == nil, snapshot.logsUploadAvailable else { return }
        let client = client
        let sessionID = snapshot.sessionID
        let digest = snapshot.digest
        let writeError = diagnosticWriteError
        uploadError = ""
        uploadStatus = "Preparing logs…"
        let cancellation = DobbyUploadCancellation()
        uploadCancellation = cancellation
        uploadTask = Task { [weak self] in
            guard let self else { return }
            var directory: DobbyUploadDirectory?
            defer {
                if let directory {
                    do { try directory.remove() }
                    catch {
                        let cleanup = "Could not remove upload temporary: \(String(reflecting: error))"
                        self.uploadError = [self.uploadError, cleanup].filter { !$0.isEmpty }.joined(separator: "\n")
                        self.recordError(cleanup)
                    }
                }
                self.uploadTask = nil
                self.uploadCancellation = nil
                self.uploadStatus = ""
            }
            do {
                let preparation = try await Task.detached {
                    try DobbyResponse.result(from: client.call("PrepareLogUpload", parameters: ["session_id": sessionID, "digest": digest]), as: DobbyUploadPreparation.self)
                }.value
                try Task.checkCancellation()
                let dir = try DobbyUploadDirectory()
                directory = dir
                let file = dir.url.appendingPathComponent(preparation.reference + ".gz")
                let header = "DobbyVPN \(client.version)\nSource commit: \(client.sourceCommit)\nCaptured: \(ISO8601DateFormatter().string(from: Date()))\n\n" + writeError
                let issues = try await withTaskCancellationHandler {
                    try await Task.detached {
                        try exportDiagnostics(paths: client.diagnosticPaths, to: file, header: header, checkCancellation: cancellation.check)
                    }.value
                } onCancel: {
                    cancellation.cancel()
                }
                guard issues.isEmpty else { throw DobbyClientError.diagnostics(issues) }
                try Task.checkCancellation()
                self.uploadStatus = "Uploading logs…"
                try await uploadDiagnostics(preparation, file: file)
                let result = DobbyUploadReference(reference: preparation.reference, completed: Date(), provider: preparation.provider)
                self.latestUpload = result
                // The upload already succeeded; failing to save the reference must not be reported as a failed upload.
                do { try DobbyLatestUploadFile.save(result, forLog: client.uiDiagnosticPath, defaultsKey: self.uploadReferenceKey) }
                catch { self.recordError("Could not save latest upload reference: \(String(reflecting: error))") }
            } catch is CancellationError {
                self.uploadError = "Log upload cancelled"
            } catch {
                if Task.isCancelled {
                    self.uploadError = "Log upload cancelled"
                } else {
                    self.uploadError = "Log upload failed: \(error.localizedDescription)"
                    self.recordError(self.uploadError)
                }
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
            do {
                let issues = try exportLocalDiagnostics(client: client, to: url, writeError: writeError)
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

extension DobbySessionViewModel {
    private func markSourceAccepted(_ source: String) {
        acceptedSource = source
        sourceIsDirty = false
        sourceText = source
    }

    private var connected: Bool { snapshot.primaryAction == "STOP" }

    public var mainActionTitle: String {
        if vpnPermissionNeeded { return "Allow VPN" }
        if protectedRetryState { return busy ? "Retrying…" : "Retry" }
        if !networkCanBeStopped { return busy ? "Connecting…" : "Auto connect" }
        if snapshot.state == "CONNECTED" && snapshot.pendingTarget == nil { return "Disconnect" }
        if snapshot.state == "STOPPING" { return "Stopping…" }
        return busy ? "Stopping…" : "Stop"
    }

    public var mainActionEnabled: Bool { canRetryVPNSetup || canRetryActiveSelection || canStop || canAct() }

    public func mainAction() {
        if vpnPermissionNeeded { retryVPNSetup() }
        else if protectedRetryState { retryActiveSelection() }
        else if networkCanBeStopped { stop() }
        else { performPrimaryAction() }
    }

    public func profileStatus(_ index: Int) -> DobbyProfileStatus? {
        guard snapshot.activeDigest == snapshot.digest else { return nil }
        let active = snapshot.activeProfile.map { $0.index == index }
            ?? (snapshot.activeMode == "PROFILE_INDEX" && snapshot.activeIndex == index)
        return active ? sessionStatus : nil
    }

    public func profileActionTitle(_ index: Int) -> String {
        let title = actionTitle(index)
        return profileStatus(index) == .connecting || title == "Stop" ? "Connecting…" : title
    }

    /// The connection still running from a subscription that has since been replaced.
    public var orphanProfile: DobbyProfile? {
        connected && snapshot.activeDigest != snapshot.digest ? snapshot.activeProfile : nil
    }

    public var sessionStatus: DobbyProfileStatus? {
        if snapshot.recovering { return .reconnecting }
        switch snapshot.state {
        case "CONNECTED": return .connected
        case "PROBING", "PREPARING": return .connecting
        case "STOPPING": return .stopping
        default: return snapshot.lastFailure == nil ? nil : .failed
        }
    }

    private var failures: [String] {
        var seen = Set<String>()
        return [importError, loadError, disconnectFailure, error, snapshot.lastFailure.map { "\($0.message) (\($0.code))" } ?? "", logsError]
            .filter { !$0.isEmpty && seen.insert($0).inserted }
    }

    public var failureDetail: String { failures.first ?? "" }

    public var visibleFailure: String {
        failures.first { !dismissedFailures.contains($0) } ?? ""
    }

    /// Import guidance is cleared; other failures stay set while their cause persists, so polling cannot reopen them.
    public func closeFailure() {
        let shown = visibleFailure
        let branch = shown.isEmpty ? "none" : shown == importError ? "import" : shown == loadError ? "load" : "retained"
        let scalarCount = shown.unicodeScalars.count
        recordDiagnostic("phase=requested branch=\(branch) scalar_count=\(scalarCount) visible_before=\(!shown.isEmpty)",
                         event: "ui.error_close", level: "DEBUG")
        if !shown.isEmpty {
            if shown == disconnectFailure, let observer = client as? DobbyDisconnectObserving {
                observer.acknowledgeDisconnect(shown)
                disconnectFailure = observer.disconnectFailure
            } else if shown == importError {
                dismissError()
            } else {
                dismissedFailures.insert(shown)
            }
        }
        recordDiagnostic("phase=completed branch=\(branch) scalar_count=\(scalarCount) " +
                         "same_failure_retained=\(!shown.isEmpty && failures.contains(shown)) " +
                         "same_failure_visible=\(!shown.isEmpty && visibleFailure == shown) visible_after=\(!visibleFailure.isEmpty)",
                         event: "ui.error_close", level: "DEBUG")
    }

    private func acknowledgeDisplayedTransportFailure() {
        guard let failure = snapshot.lastFailure else { return }
        let message = "\(failure.message) (\(failure.code))"
        if visibleFailure == message { dismissedFailures.insert(message) }
    }

    /// A new attempt shows its failure again, even when it repeats one the user closed.
    private func resetClosedFailures() {
        if !dismissedFailures.isEmpty { dismissedFailures = [] }
    }

    private func pruneClosedFailures() {
        let current = Set(failures)
        if !dismissedFailures.isSubset(of: current) { dismissedFailures.formIntersection(current) }
    }

    /// Closes the error the popup shows first: import guidance, then the last command error.
}

extension DobbySessionViewModel {
    public var vpnNotSetUp: Bool { vpnSetup.notSetUp }
    public var vpnSetupExplanationVisible: Bool { vpnSetup.explanationVisible }
    /// The denied (or timed-out) setup shown inline; its detail is only behind Copy.
    public var vpnPermissionNeeded: Bool { snapshot.sessionID.isEmpty && vpnSetup.failure != nil }
    public var vpnSetupFailureDetail: String { vpnSetup.failure ?? "" }
    public var canRetryVPNSetup: Bool { snapshot.sessionID.isEmpty && vpnSetup.canRetry }

    /// "Allow VPN": after a refusal only this tap saves again.
    public func retryVPNSetup() {
        guard canRetryVPNSetup else { return }
        recordDiagnostic("VPN setup retried by the user", event: "vpn.setup", level: "INFO")
        startVPNSetup()
    }

    /// "Continue" on the explanation: the save that shows the iOS permission prompt.
    public func continueVPNSetup() {
        guard updateVPNSetup({ $0.dismissExplanation() }) else { return }
        recordDiagnostic("VPN setup continued after the explanation", event: "vpn.setup", level: "INFO")
        startVPNSetup()
    }

    func requestVPNSetup() {
        if updateVPNSetup({ $0.request() }) { recordDiagnostic("VPN setup explanation shown", event: "vpn.setup", level: "INFO") }
    }

    private func startVPNSetup() {
        resetClosedFailures()
        if snapshotInFlight {
            updateVPNSetup { $0.retryPending = true }
        } else {
            refreshSnapshot(retryingVPNSetup: true)
        }
    }

    /// Publishes only real changes, so polling does not redraw the window.
    @discardableResult
    private func updateVPNSetup<T>(_ change: (inout DobbyVPNSetupFlow) -> T) -> T {
        var value = vpnSetup
        let result = change(&value)
        if value != vpnSetup { vpnSetup = value }
        return result
    }

    private func observeSnapshotFailure(_ failure: Error) {
        snapshot = .empty
        guard let report = DobbyVPNSetupFlow.report(failure, client: client) else {
            updateVPNSetup { $0.ready() }
            snapshotFailureMessage = failure.localizedDescription
            setError(failure.localizedDescription, origin: .other, preservingSnapshotFailure: true)
            return
        }
        if let newFailure = updateVPNSetup({ $0.observe(report) }) { recordError("VPN setup failed: \(newFailure)") }
        // An earlier transport error no longer describes this state.
        clearSnapshotFailure()
        if report == .notSetUp && pendingLoad != nil { requestVPNSetup() }
    }
}

func validSubscription(_ value: String) -> Bool {
    guard let url = URLComponents(string: value) else { return false }
    return url.scheme?.lowercased() == "https" && !(url.host ?? "").isEmpty
}

private struct DobbyImportLink: Decodable {
    let kind: String
    let source: String?
}

extension DobbySessionViewModel {
    public func confirmImport() {
        importRevision &+= 1
        parsingImport = false
        guard let source = pendingImport else { return }
        pendingImport = nil
        paste(source)
    }

    public func cancelImport() { importRevision &+= 1; parsingImport = false; pendingImport = nil }
}

extension DobbySessionViewModel {
    func recordErrorCopy(_ observation: ErrorCopyObservation) {
        let accepted = observation.accepted.map { String($0) } ?? "unavailable"
        recordDiagnostic(
            "phase=\(observation.phase) unicode_scalars=\(observation.scalarCount) " +
            "utf8_bytes=\(observation.utf8Count) pasteboard_change_count=\(observation.changeCount) accepted=\(accepted)",
            event: "ui.error_copy", level: "DEBUG"
        )
    }
}

private struct ConfigurationReceipt: Decodable { let sequence: Int64 }

#if os(macOS)
private let macLogCaptureDiagnosticInterval: TimeInterval = 1

extension DobbySessionViewModel {
    func recordMacLogAccessibility(_ observation: MacLogAccessibilityObservation) {
        recordDiagnostic(
            "parent_role=\(observation.parentRole) visible_count=\(observation.visibleCount) " +
            "first_id=\(observation.firstID) first_role=\(observation.firstRole) " +
            "first_frame=\(NSStringFromRect(observation.firstFrame))",
            event: "logs.accessibility-children", level: "DEBUG"
        )
    }

    func recordMacLogCapture(_ anchor: MacLogReadingAnchor) {
        let now = macLogCaptureDiagnosticNow()
        if let last = lastMacLogCaptureDiagnosticUptime, now - last < macLogCaptureDiagnosticInterval { return }
        lastMacLogCaptureDiagnosticUptime = now
        recordDiagnostic(
            "record_id=\(anchor.recordID) character_offset=\(anchor.characterOffset) " +
            "anchor_viewport_y=\(anchor.viewportY)",
            event: "logs.frozen-position-captured", level: "DEBUG"
        )
    }

    func recordMacLogRestore(_ observation: MacLogRestoreObservation) {
        recordDiagnostic(
            "record_id=\(observation.anchor.recordID) character_offset=\(observation.anchor.characterOffset) " +
            "anchor_viewport_y=\(observation.anchor.viewportY) " +
            "previous_origin=\(observation.previousOrigin) restored_origin=\(observation.restoredOrigin) " +
            "viewport_size=\(observation.viewportSize) content_size=\(observation.contentSize)",
            event: "logs.frozen-position-restored", level: "DEBUG"
        )
    }
}
#endif

#if os(iOS)
extension DobbySessionViewModel {
    func recordLogMenuGesture(_ observation: String) {
        recordDiagnostic(observation, event: "ui.logs_menu_gesture", level: "DEBUG")
    }
}
#endif
