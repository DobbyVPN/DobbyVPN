import NetworkExtension
import Foundation

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
    let condition = NSCondition()
    var vpnManager: NETunnelProviderManager?
    var providerStatus: NEVPNStatus = .invalid
    let preferenceQueue = DispatchQueue(label: "vpn.dobby.preferences")
    var preferenceOperations: [(@escaping () -> Void) -> Void] = []
    private var snapshotReadinessFailureCooldown = IOSReadinessFailureCooldown()
    private let diagnosticSampler = IOSCommandLogSampler()
    private var observer: NSObjectProtocol?

    override public init() {
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
            self.logs.writeLog(
                log: "[NEVPNStatusDidChange] provider status=\(self.statusName(connection.status)) " +
                    "raw=\(connection.status.rawValue)"
            )
        }
    }

    deinit {
        if let observer { NotificationCenter.default.removeObserver(observer) }
    }

    /// Sends one command to the provider. The inner Go payload is never rewritten.
    public func sendProviderMessage(_ messageData: Data) -> Data {
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
        let useSnapshotReadinessCooldown = commandMethod == "Snapshot" && !Thread.isMainThread
        if useSnapshotReadinessCooldown,
           let readinessFailure = cachedSnapshotReadinessFailure() {
            logs.writeLog(level: "ERROR", log: "[provider-message] cached readiness failure \(identity) stage=cached-readiness elapsed=\(monotonicNow() - started): \(readinessFailure)")
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: readinessFailure
            )
        }
        let deadline = monotonicNow() + IOSProviderTiming.appMessageTimeout
        if commandMethod == "Stop" { logs.writeLog(log: "[provider-message] On Demand disable begin \(identity)") }
        let onDemandCompletion: (() -> String?)? = commandMethod == "Stop" ? beginDisableOnDemandForStop(until: deadline) : nil
        stage = "readiness"
        if trace { logs.writeLog(log: "[provider-message] readiness begin \(identity)") }
        if let readinessFailure = ensureProviderReady(until: deadline, identity: identity, trace: trace, stage: &stage) {
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
                let response = try sendOnce(messageData, timeout: timeout)
                if trace { logs.writeLog(log: "[provider-message] send end \(identity) elapsed=\(monotonicNow() - started)") }
                if let onDemandCompletion {
                    logs.writeLog(log: "[provider-message] On Demand wait begin \(identity)")
                    let onDemandFailure = onDemandCompletion()
                    logs.writeLog(level: onDemandFailure == nil ? "INFO" : "ERROR", log: "[provider-message] On Demand wait end \(identity) elapsed=\(monotonicNow() - started) error=\(onDemandFailure ?? "none")")
                    if let onDemandFailure {
                        // Preserve the actual Go stop result even when the OS restart
                        // preference failed. The app must expose that failure.
                        logs.writeLog(level: "ERROR", log: "[provider] Go Stop response after On Demand failure \(identity): \(String(decoding: response, as: UTF8.self))")
                        return transportFailureResponse(for: messageData, code: "PLATFORM_FAILED", message: onDemandFailure)
                    }
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
        let session = vpnManager?.connection as? NETunnelProviderSession
        condition.unlock()
        guard let session else {
            throw NSError(
                domain: "VpnManagerImpl.sessionapi",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: "provider session is unavailable"]
            )
        }
        let semaphore = DispatchSemaphore(value: 0)
        let responseLock = NSLock()
        var response: Data?
        try session.sendProviderMessage(messageData) { value in
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

    private func cachedSnapshotReadinessFailure() -> String? {
        condition.lock()
        if let current = vpnManager {
            providerStatus = current.connection.status
        }
        if providerStatus == .connected {
            snapshotReadinessFailureCooldown.clear()
            condition.unlock()
            return nil
        }
        let failure = snapshotReadinessFailureCooldown.cachedFailure(at: monotonicNow())
        condition.unlock()
        return failure
    }

    private func ensureProviderReady(until deadline: TimeInterval, identity: String, trace: Bool, stage: inout String) -> String? {
        if Thread.isMainThread {
            // Provider readiness waits must never freeze the UI thread. Refuse
            // a main-thread call if a caller violates that boundary.
            logs.writeLog(level: "ERROR", log: "[provider] readiness check rejected on the main thread")
            return "readiness check rejected on the main thread"
        }
        // Once the saved-and-reloaded manager is connected, use that exact
        // bound object for subsequent commands. Re-saving preferences for
        // every snapshot would add needless transition risk and
        // consume the aggregate command budget.
        condition.lock()
        if let current = vpnManager {
            providerStatus = current.connection.status
            if providerStatus == .connected {
                snapshotReadinessFailureCooldown.clear()
                condition.unlock()
                return nil
            }
        }
        condition.unlock()
        stage = "preferences"
        let loaded = DispatchSemaphore(value: 0)
        var loadedManager: NETunnelProviderManager?
        var loadError: Error?
        if trace { logs.writeLog(log: "[provider-message] preferences begin \(identity)") }
        getOrCreateManager { value, error in
            loadedManager = value
            loadError = error
            loaded.signal()
        }
        guard let loadRemaining = remaining(until: deadline),
              loaded.wait(timeout: .now() + loadRemaining) == .success else {
            logs.writeLog(level: "ERROR", log: "[provider] timed out loading NetworkExtension preferences")
            return "timed out loading NetworkExtension preferences"
        }
        if trace || loadError != nil { logs.writeLog(level: loadError == nil ? "INFO" : "ERROR", log: "[provider-message] preferences end \(identity) error=\(loadError.map(diagnosticErrorDescription) ?? "none")") }
        condition.lock()
        // getOrCreateManager returns the post-save reloaded object. Never
        // prefer an older in-memory manager after a provider-process restart.
        let current = loadedManager
        if let current {
            vpnManager = current
            providerStatus = current.connection.status
            if providerStatus == .connected {
                snapshotReadinessFailureCooldown.clear()
            }
        }
        let status = current?.connection.status ?? .invalid
        condition.unlock()
        if let loadError {
            // The preference operation records its precise phase and original
            // OS error. Propagate that error without logging it a second time.
            return String(reflecting: loadError)
        }
        guard let current else {
            logs.writeLog(level: "ERROR", log: "[provider] NetworkExtension manager is unavailable without an error")
            return "NetworkExtension manager is unavailable without an error"
        }
        var observedStatus = status
        while remaining(until: deadline) != nil {
            if observedStatus == .connected {
                condition.lock()
                snapshotReadinessFailureCooldown.clear()
                condition.unlock()
                return nil
            }
            if observedStatus == .disconnecting {
                stage = "disconnect-wait"
                guard let settled = waitForDisconnectToSettle(until: deadline) else {
                    logs.writeLog(level: "ERROR", log: "[provider] disconnect did not settle before the readiness deadline")
                    return "disconnect did not settle before the readiness deadline"
                }
                observedStatus = settled
                continue
            }
            if observedStatus == .connecting || observedStatus == .reasserting {
                stage = "provider-ready-wait"
                if waitForReady(until: deadline) { return nil }
                observedStatus = current.connection.status
                continue
            }
            do {
                stage = "provider-start"
                try current.connection.startVPNTunnel(options: ["dobby_control_only": NSNumber(value: true)])
            } catch {
                logs.writeLog(level: "ERROR", log: "[provider] control-mode start failed:\n\(diagnosticErrorDescription(error))")
                return String(reflecting: error)
            }
            stage = "provider-ready-wait"
            if waitForReady(until: deadline) { return nil }
            observedStatus = current.connection.status
        }
        logs.writeLog(level: "ERROR", log: "[provider] control-mode provider did not reach connected state")
        return "control-mode provider did not reach connected state before the readiness deadline"
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
            if let status = vpnManager?.connection.status {
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
            if let status = vpnManager?.connection.status {
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
