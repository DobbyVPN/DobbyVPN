import NetworkExtension
import Foundation

/// The NetworkExtension calls VpnManagerImpl makes. Production uses the
/// system; tests replace only these calls, keeping the readiness logic real.
struct IOSNativeVPN {
    var load: VPNPreferenceCoordinator.Inventory = { NETunnelProviderManager.loadAllFromPreferences(completionHandler: $0) }
    var save: VPNPreferenceCoordinator.Operation = { $0.saveToPreferences(completionHandler: $1) }
    var reload: VPNPreferenceCoordinator.Operation = { $0.loadFromPreferences(completionHandler: $1) }
    var status: (NETunnelProviderManager) -> NEVPNStatus = { $0.connection.status }
    var start: (NETunnelProviderManager, [String: NSObject]) throws -> Void = { try $0.connection.startVPNTunnel(options: $1) }
    var send: (NETunnelProviderManager, Data, @escaping (Data?) -> Void) throws -> Void = { manager, data, response in
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw NSError(domain: "VpnManagerImpl.sessionapi", code: -1,
                          userInfo: [NSLocalizedDescriptionKey: "provider session is unavailable"])
        }
        try session.sendProviderMessage(data, responseHandler: response)
    }
    var messageTimeout = IOSProviderTiming.appMessageTimeout
}

/// Response codes for a containing app whose VPN profile is missing or was refused.
public enum IOSVPNSetupCode {
    /// No DobbyVPN profile exists yet; the window shows its ordinary first screen.
    public static let notSetUp = "VPN_NOT_SET_UP"
    /// The last setup the user started failed (denied, timed out or another
    /// preference error); only the user's next setup action saves again.
    public static let setupFailed = "VPN_SETUP_FAILED"
}

private enum ProviderReadiness {
    case ready
    case notSetUp(String)
    case setupFailed(String)
    case failed(String)
}

/// A NetworkExtension readiness and message-transport shell.
///
/// This class deliberately has no VPN product state, generation, configured
/// flag, or event sequence. Those values belong to the Go session manager in the
/// packet-tunnel process. The only state retained here is the private
/// readiness state needed to deliver a provider message.
public final class VpnManagerImpl: NSObject {
    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"
    static let defaultServerAddress = "Dobby VPN"

    let logs = IOSAppCompositionRoot.logsRepository
    let native: IOSNativeVPN
    let condition = NSCondition()
    var vpnManager: NETunnelProviderManager?
    var providerStatus: NEVPNStatus = .invalid
    private var preferenceCoordinator: VPNPreferenceCoordinator?
    var preferences: VPNPreferenceCoordinator {
        condition.lock()
        defer { condition.unlock() }
        if let preferenceCoordinator { return preferenceCoordinator }
        let coordinator = VPNPreferenceCoordinator(
            providerIdentifier: Self.dobbyBundleIdentifier, name: Self.dobbyName,
            serverAddress: Self.defaultServerAddress,
            load: native.load, save: native.save, reload: native.reload, status: native.status,
            bind: { [weak self] manager in
                guard let self else { return }
                self.condition.lock()
                self.vpnManager = manager
                self.providerStatus = self.native.status(manager)
                self.condition.broadcast()
                self.condition.unlock()
            },
            log: { [weak self] level, message in self?.logs.writeLog(level: level, log: message) },
            describeError: diagnosticErrorDescription
        )
        preferenceCoordinator = coordinator
        return coordinator
    }
    private var snapshotReadinessFailureCooldown = IOSReadinessFailureCooldown()
    private var setupFailure: String?
    private var reportedNotSetUp = false
    private var cachedFailureReads = 0

    public var vpnSetupFailure: String? {
        condition.lock()
        defer { condition.unlock() }
        return setupFailure
    }

    public let disconnectHistory = IOSDisconnectHistory()
    private let diagnosticSampler = IOSCommandLogSampler()
    private var observer: NSObjectProtocol?

    override public convenience init() {
        self.init(native: IOSNativeVPN())
    }

    init(native: IOSNativeVPN) {
        self.native = native
        super.init()
        observer = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: nil,
            queue: nil
        ) { [weak self] notification in
            guard let self, let connection = notification.object as? NEVPNConnection else { return }
            self.condition.lock()
            guard let own = self.vpnManager?.connection, own === connection else {
                self.condition.unlock()
                return
            }
            self.providerStatus = connection.status
            if connection.status == .connected {
                self.snapshotReadinessFailureCooldown.clear()
            }
            self.condition.broadcast()
            self.condition.unlock()
            self.observeDisconnect(connection, status: connection.status)
            self.logs.writeLog(
                log: "[NEVPNStatusDidChange] provider status=\(self.statusName(connection.status)) " +
                    "raw=\(connection.status.rawValue)"
            )
        }
    }

    private func observeDisconnect(_ connection: NEVPNConnection, status: NEVPNStatus) {
        guard status == .disconnected || status == .invalid,
              let ticket = disconnectHistory.disconnected() else { return }
        logs.writeLog(level: "ERROR", log: "[provider] \(disconnectHistory.message)")
        if #available(iOS 16.0, macOS 13.0, *) {
            connection.fetchLastDisconnectError { [weak self] error in
                guard let self, let error else { return }
                let native = error as NSError
                self.disconnectHistory.receivedReason("\(native.localizedDescription) (\(native.domain) \(native.code))", ticket: ticket)
                self.logs.writeLog(level: "ERROR", log: "[provider] last disconnect reason:\n\(diagnosticErrorDescription(error))")
            }
        }
    }

    deinit {
        if let observer { NotificationCenter.default.removeObserver(observer) }
    }

    /// Sends one command to the provider. The inner Go payload is never rewritten.
    /// `setUpVPN` marks a Snapshot the user started (Continue or Allow VPN):
    /// only it may create the VPN profile and show the iOS permission prompt.
    public func sendProviderMessage(_ messageData: Data, setUpVPN: Bool = false) -> Data {
        guard !messageData.isEmpty else {
            logs.writeLog(level: "ERROR", log: "[provider-message] rejected empty command")
            return Self.transportFailure("INTERNAL", message: "provider command is empty")
        }
        let command = try? IOSProviderCommand.decode(messageData)
        let commandMethod = command?.method
        let identity = command?.diagnosticIdentity ?? "request_id=unavailable method=unavailable"
        let started = monotonicNow()
        let trace = diagnosticSampler.shouldTrace(method: commandMethod ?? "")
        var stage = "begin"
        if trace { logs.writeLog(log: "[provider-message] begin \(identity)") }
        defer { if trace { logs.writeLog(log: "[provider-message] end \(identity) stage=\(stage) elapsed=\(monotonicNow() - started)") } }
        let userSetup = setUpVPN && commandMethod == "Snapshot"
        let useSnapshotReadinessCooldown = commandMethod == "Snapshot" && !Thread.isMainThread && !userSetup
        if useSnapshotReadinessCooldown,
           let readinessFailure = cachedSnapshotReadinessFailure() {
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: readinessFailure
            )
        }
        let deadline = monotonicNow() + native.messageTimeout
        var stopPreferenceStarted = false
        defer {
            // Even an unavailable provider must not leave an explicit Stop's
            // restart preference untouched. This work never gates Go cleanup.
            if commandMethod == "Stop" && !stopPreferenceStarted {
                preferences.changeOnDemand(false) { [weak self] error in
                    if let error {
                        self?.logs.writeLog(level: "ERROR", log: "[provider] unsent Stop could not disable On Demand:\n\(diagnosticErrorDescription(error))")
                    }
                }
            }
        }
        stage = "readiness"
        if trace { logs.writeLog(log: "[provider-message] readiness begin \(identity)") }
        condition.lock()
        let repeats = cachedFailureReads
        cachedFailureReads = 0
        condition.unlock()
        if repeats > 0 {
            logs.writeLog(log: "[provider-message] cached readiness failure repeats=\(repeats)")
        }
        let access: VPNPreferenceAccess = userSetup ? .setUp : passiveAccess()
        let trigger = userSetup ? "user_setup" : commandMethod ?? "unknown"
        let readinessFailure: String
        switch ensureProviderReady(until: deadline, identity: identity, trace: trace,
                                   trigger: trigger, access: access, stage: &stage) {
        case .ready:
            condition.lock()
            setupFailure = nil
            reportedNotSetUp = false
            condition.unlock()
            readinessFailure = ""
        case let .notSetUp(reason):
            condition.lock()
            let failure = setupFailure
            let firstReport = !reportedNotSetUp
            reportedNotSetUp = true
            condition.unlock()
            if firstReport {
                logs.writeLog(log: "[provider-message] VPN not set up \(identity) access=\(access) reason=\(reason) " +
                              "setup_failure_pending=\(failure != nil)")
            }
            if let failure {
                return transportFailureResponse(for: messageData, code: IOSVPNSetupCode.setupFailed, message: failure)
            }
            return transportFailureResponse(for: messageData, code: IOSVPNSetupCode.notSetUp,
                                            message: "DobbyVPN is not set up on this device yet.")
        case let .setupFailed(detail):
            condition.lock()
            setupFailure = detail
            condition.unlock()
            return transportFailureResponse(for: messageData, code: IOSVPNSetupCode.setupFailed, message: detail)
        case let .failed(message):
            readinessFailure = message
        }
        if !readinessFailure.isEmpty {
            logs.writeLog(level: "ERROR", log: "[provider-message] readiness failed \(identity) stage=\(stage) elapsed=\(monotonicNow() - started): \(readinessFailure)")
            if commandMethod != nil && !Thread.isMainThread {
                condition.lock()
                snapshotReadinessFailureCooldown.recordFailure(readinessFailure, at: monotonicNow())
                condition.unlock()
            }
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: readinessFailure
            )
        }
        if trace { logs.writeLog(log: "[provider-message] readiness end \(identity) elapsed=\(monotonicNow() - started)") }
        guard let timeout = remaining(until: deadline) else {
            logs.writeLog(level: "ERROR", log: "[provider-message] deadline expired \(identity) stage=\(stage) elapsed=\(monotonicNow() - started)")
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: "provider message deadline expired"
            )
        }
        do {
            return try performProviderMessage(commandData: messageData, send: {
                stage = "send"
                if trace { logs.writeLog(log: "[provider-message] send begin \(identity)") }
                let response: Data
                let onDemandFailure: String?
                if commandMethod == "Stop" {
                    stopPreferenceStarted = true
                    (response, onDemandFailure) = try preferences.performStop(until: deadline) {
                        try sendOnce(messageData, timeout: remaining(until: deadline) ?? 0.1)
                    }
                } else {
                    // Connect re-arms the On Demand that an earlier Stop turned off.
                    // It never gates the Go command; a failure is only logged.
                    if commandMethod == "Start" {
                        preferences.changeOnDemand(true) { [weak self] error in
                            if let error {
                                self?.logs.writeLog(level: "ERROR", log: "[provider] Start could not enable On Demand:\n\(diagnosticErrorDescription(error))")
                            }
                        }
                    }
                    response = try sendOnce(messageData, timeout: timeout)
                    onDemandFailure = nil
                }
                if trace { logs.writeLog(log: "[provider-message] send end \(identity) elapsed=\(monotonicNow() - started)") }
                if let onDemandFailure {
                    logs.writeLog(level: "ERROR", log: "[provider] Go Stop response after On Demand failure \(identity): \(String(decoding: response, as: UTF8.self))")
                    return transportFailureResponse(for: messageData, code: "PLATFORM_FAILED", message: onDemandFailure)
                }
                return response
            }, onFailure: { error in
                logs.writeLog(level: "ERROR", log: "[provider] sendProviderMessage failed \(identity) stage=\(stage) elapsed=\(monotonicNow() - started):\n\(diagnosticErrorDescription(error))")
            })
        } catch {
            logs.writeLog(level: "ERROR", log: "[provider] transport failure response encoding failed \(identity) stage=\(stage) elapsed=\(monotonicNow() - started):\n\(diagnosticErrorDescription(error))")
            return Self.transportFailure("INTERNAL", message: "provider request failed")
        }
    }

    public static func transportFailure(_ code: String, message: String) -> Data {
        let value: [String: Any] = [
            "ok": false,
            "error": ["code": code, "message": message],
        ]
        do {
            return try JSONSerialization.data(withJSONObject: value)
        } catch {
            IOSAppCompositionRoot.logsRepository.writeLog(level: "ERROR",
                log: "[provider] transport failure encoding failed:\n\(diagnosticErrorDescription(error))"
            )
            // No valid response can be encoded. Return no decodable bytes so
            // the caller observes a transport failure rather than success.
            return Data()
        }
    }

    private func transportFailureResponse(for messageData: Data, code: String, message: String) -> Data {
        do {
            let command = try IOSProviderCommand.decode(messageData)
            let envelope = try IOSProviderResponse(
                requestID: command.requestID,
                kind: .transport,
                payload: Self.transportFailure(code, message: message)
            )
            return try envelope.encoded()
        } catch {
            logs.writeLog(level: "ERROR", log: "[provider] transport failure response encoding failed:\n\(diagnosticErrorDescription(error))")
            return Self.transportFailure("INTERNAL", message: "provider request failed")
        }
    }

    private func sendOnce(_ messageData: Data, timeout: TimeInterval) throws -> Data {
        condition.lock()
        let manager = vpnManager
        condition.unlock()
        guard let manager else {
            throw NSError(
                domain: "VpnManagerImpl.sessionapi",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: "provider session is unavailable"]
            )
        }
        let semaphore = DispatchSemaphore(value: 0)
        let responseLock = NSLock()
        var response: Data?
        try native.send(manager, messageData) { value in
            responseLock.lock()
            response = value
            responseLock.unlock()
            semaphore.signal()
        }
        guard semaphore.wait(timeout: .now() + timeout) == .success else {
            throw NSError(
                domain: "VpnManagerImpl.sessionapi",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "provider message timed out"]
            )
        }
        responseLock.lock()
        defer { responseLock.unlock() }
        guard let response else {
            throw NSError(
                domain: "VpnManagerImpl.sessionapi",
                code: -3,
                userInfo: [NSLocalizedDescriptionKey: "provider message completed without a response"]
            )
        }
        return response
    }

    private func monotonicNow() -> TimeInterval { ProcessInfo.processInfo.systemUptime }

    /// After a failed setup, polls only load: the next save waits for the user's tap.
    private func passiveAccess() -> VPNPreferenceAccess {
        condition.lock()
        defer { condition.unlock() }
        return setupFailure == nil ? .passive : .loadOnly
    }

    private func cachedSnapshotReadinessFailure() -> String? {
        condition.lock()
        if let current = vpnManager {
            providerStatus = native.status(current)
        }
        if providerStatus == .connected {
            snapshotReadinessFailureCooldown.clear()
            condition.unlock()
            return nil
        }
        let failure = snapshotReadinessFailureCooldown.cachedFailure(at: monotonicNow())
        if failure != nil { cachedFailureReads += 1 }
        condition.unlock()
        return failure
    }

    private func ensureProviderReady(until deadline: TimeInterval, identity: String, trace: Bool, trigger: String,
                                     access: VPNPreferenceAccess, stage: inout String) -> ProviderReadiness {
        if Thread.isMainThread {
            // Provider readiness waits must never freeze the UI thread. Refuse
            // a main-thread call if a caller violates that boundary.
            logs.writeLog(level: "ERROR", log: "[provider] readiness check rejected on the main thread")
            return .failed("readiness check rejected on the main thread")
        }
        // Once the loaded manager is connected, use that exact bound object
        // for subsequent commands. Re-reading preferences for every snapshot
        // would add needless transition risk and consume the command budget.
        condition.lock()
        if let current = vpnManager {
            providerStatus = native.status(current)
            if providerStatus == .connected {
                snapshotReadinessFailureCooldown.clear()
                condition.unlock()
                return .ready
            }
        }
        condition.unlock()
        stage = "preferences"
        let loaded = DispatchSemaphore(value: 0)
        let resultLock = NSLock()
        var loadedManager: NETunnelProviderManager?
        var loadError: Error?
        if trace { logs.writeLog(log: "[provider-message] preferences begin \(identity)") }
        let preferencesStarted = monotonicNow()
        preferences.prepareManager(access: access == .setUp ? .setUp : self.passiveAccess(), trigger: trigger) { value, error in
            if let setup = error as? VPNSetupFailure {
                // Record it before the next queued operation reads the access kind.
                self.condition.lock()
                self.setupFailure = setup.localizedDescription
                self.condition.unlock()
            }
            resultLock.lock()
            loadedManager = value
            loadError = error
            resultLock.unlock()
            loaded.signal()
        }
        guard let loadRemaining = remaining(until: deadline),
              loaded.wait(timeout: .now() + loadRemaining) == .success else {
            let elapsed = monotonicNow() - preferencesStarted
            switch access == .setUp ? .setUp : passiveAccess() {
            case .setUp:
                // Usually the iOS permission prompt is still open. This is the same
                // setup failure as a refusal: the user can tap Allow VPN again.
                logs.writeLog(level: "ERROR",
                              log: "[provider] VPN setup trigger=\(trigger) operation=save timed_out=true elapsed=\(elapsed)")
                return .setupFailed("Timed out after \(Int(elapsed.rounded())) s waiting for the VPN configuration to be saved.")
            case .loadOnly:
                // An earlier setup save still holds the preference queue.
                return .notSetUp("preferences busy after a failed setup, elapsed=\(elapsed)")
            case .passive:
                logs.writeLog(level: "ERROR", log: "[provider] timed out loading NetworkExtension preferences")
                return .failed("timed out loading NetworkExtension preferences")
            }
        }
        resultLock.lock()
        let current = loadedManager
        let error = loadError
        resultLock.unlock()
        if trace && error == nil { logs.writeLog(log: "[provider-message] preferences end \(identity)") }
        if let notSetUp = error as? VPNNotSetUp {
            condition.lock()
            // The profile may have been removed in Settings; never keep using it.
            vpnManager = nil
            providerStatus = .invalid
            condition.unlock()
            return .notSetUp(notSetUp.reason)
        }
        if let setup = error as? VPNSetupFailure {
            // The preference operation logged its precise phase and original OS error.
            return .setupFailed(setup.localizedDescription)
        }
        if let error { return .failed(String(reflecting: error)) }
        guard let current else {
            logs.writeLog(level: "ERROR", log: "[provider] NetworkExtension manager is unavailable without an error")
            return .failed("NetworkExtension manager is unavailable without an error")
        }
        condition.lock()
        // Bind the loaded (or saved and reloaded) object. Never prefer an
        // older in-memory manager after a provider-process restart.
        vpnManager = current
        providerStatus = native.status(current)
        if providerStatus == .connected {
            snapshotReadinessFailureCooldown.clear()
        }
        var observedStatus = providerStatus
        condition.unlock()
        observeDisconnect(current.connection, status: observedStatus)
        while remaining(until: deadline) != nil {
            if observedStatus == .connected {
                condition.lock()
                snapshotReadinessFailureCooldown.clear()
                condition.unlock()
                return .ready
            }
            if observedStatus == .disconnecting {
                stage = "disconnect-wait"
                guard let settled = waitForDisconnectToSettle(until: deadline) else {
                    logs.writeLog(level: "ERROR", log: "[provider] disconnect did not settle before the readiness deadline")
                    return .failed("disconnect did not settle before the readiness deadline")
                }
                observedStatus = settled
                continue
            }
            if observedStatus == .connecting || observedStatus == .reasserting {
                stage = "provider-ready-wait"
                if waitForReady(until: deadline) { return .ready }
                observedStatus = native.status(current)
                continue
            }
            do {
                stage = "provider-start"
                observeDisconnect(current.connection, status: observedStatus)
                try native.start(current, ["dobby_control_only": NSNumber(value: true)])
            } catch {
                logs.writeLog(level: "ERROR", log: "[provider] control-mode start failed:\n\(diagnosticErrorDescription(error))")
                return .failed(String(reflecting: error))
            }
            stage = "provider-ready-wait"
            if waitForReady(until: deadline) { return .ready }
            observedStatus = native.status(current)
        }
        logs.writeLog(level: "ERROR", log: "[provider] control-mode provider did not reach connected state")
        return .failed("control-mode provider did not reach connected state before the readiness deadline")
    }

    private func waitForReady(until monotonicDeadline: TimeInterval) -> Bool {
        condition.lock()
        defer { condition.unlock() }
        while providerStatus != .connected &&
            providerStatus != .disconnected &&
            providerStatus != .invalid &&
            providerStatus != .disconnecting &&
            monotonicNow() < monotonicDeadline {
            let deadline = Date().addingTimeInterval(min(0.1, remaining(until: monotonicDeadline) ?? 0))
            condition.wait(until: deadline)
            if let manager = vpnManager {
                let status = native.status(manager)
                providerStatus = status
                if status == .connected { snapshotReadinessFailureCooldown.clear() }
            }
        }
        if providerStatus == .connected { snapshotReadinessFailureCooldown.clear() }
        return providerStatus == .connected
    }

    private func waitForDisconnectToSettle(until monotonicDeadline: TimeInterval) -> NEVPNStatus? {
        condition.lock()
        defer { condition.unlock() }
        while true {
            if let manager = vpnManager {
                let status = native.status(manager)
                providerStatus = status
                if status == .connected { snapshotReadinessFailureCooldown.clear() }
            }
            if providerStatus != .disconnecting {
                if providerStatus == .connected { snapshotReadinessFailureCooldown.clear() }
                return providerStatus
            }
            guard let remaining = remaining(until: monotonicDeadline), remaining > 0 else { return nil }
            condition.wait(until: Date().addingTimeInterval(min(0.1, remaining)))
        }
    }

    private func remaining(until deadline: TimeInterval) -> TimeInterval? {
        let value = deadline - monotonicNow()
        return value > 0 ? value : nil
    }
}
