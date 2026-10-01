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
            logs.writeLog(log: "[provider-message] rejected empty command")
            return Self.transportFailure("INTERNAL", message: "provider command is empty")
        }
        let deadline = monotonicNow() + IOSProviderTiming.appMessageTimeout
        if let readinessFailure = ensureProviderReady(until: deadline) {
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: readinessFailure
            )
        }
        guard let timeout = remaining(until: deadline) else {
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: "provider message deadline expired"
            )
        }
        do {
            return try sendOnce(messageData, timeout: timeout)
        } catch {
            logs.writeLog(log: "[provider] sendProviderMessage failed:\n\(diagnosticErrorDescription(error))")
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: "provider request failed"
            )
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
            IOSAppCompositionRoot.logsRepository.writeLog(
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
            logs.writeLog(log: "[provider] transport failure response encoding failed:\n\(diagnosticErrorDescription(error))")
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

    private func ensureProviderReady(until deadline: TimeInterval) -> String? {
        if Thread.isMainThread {
            // Provider readiness waits must never freeze the UI thread. Refuse
            // a main-thread call if a caller violates that boundary.
            logs.writeLog(log: "[provider] readiness check rejected on the main thread")
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
                condition.unlock()
                return nil
            }
        }
        condition.unlock()
        let loaded = DispatchSemaphore(value: 0)
        var loadedManager: NETunnelProviderManager?
        var loadError: Error?
        getOrCreateManager { value, error in
            loadedManager = value
            loadError = error
            loaded.signal()
        }
        guard let loadRemaining = remaining(until: deadline),
              loaded.wait(timeout: .now() + loadRemaining) == .success else {
            logs.writeLog(log: "[provider] timed out loading NetworkExtension preferences")
            return "timed out loading NetworkExtension preferences"
        }
        condition.lock()
        // getOrCreateManager returns the post-save reloaded object. Never
        // prefer an older in-memory manager after a provider-process restart.
        let current = loadedManager
        if let current {
            vpnManager = current
            providerStatus = current.connection.status
        }
        let status = current?.connection.status ?? .invalid
        condition.unlock()
        if let loadError {
            logs.writeLog(log: "[provider] NetworkExtension preference save/load failed:\n\(diagnosticErrorDescription(loadError))")
            return String(reflecting: loadError)
        }
        guard let current else {
            logs.writeLog(log: "[provider] NetworkExtension manager is unavailable without an error")
            return "NetworkExtension manager is unavailable without an error"
        }
        var observedStatus = status
        while remaining(until: deadline) != nil {
            if observedStatus == .connected { return nil }
            if observedStatus == .disconnecting {
                guard let settled = waitForDisconnectToSettle(until: deadline) else {
                    logs.writeLog(log: "[provider] disconnect did not settle before the readiness deadline")
                    return "disconnect did not settle before the readiness deadline"
                }
                observedStatus = settled
                continue
            }
            if observedStatus == .connecting || observedStatus == .reasserting {
                if waitForReady(until: deadline) { return nil }
                observedStatus = current.connection.status
                continue
            }
            do {
                try current.connection.startVPNTunnel(options: nil)
            } catch {
                logs.writeLog(log: "[provider] control-mode start failed:\n\(diagnosticErrorDescription(error))")
                return String(reflecting: error)
            }
            if waitForReady(until: deadline) { return nil }
            observedStatus = current.connection.status
        }
        logs.writeLog(log: "[provider] control-mode provider did not reach connected state")
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
            if let status = vpnManager?.connection.status { providerStatus = status }
        }
        return providerStatus == .connected
    }

    private func waitForDisconnectToSettle(until monotonicDeadline: TimeInterval) -> NEVPNStatus? {
        condition.lock()
        defer { condition.unlock() }
        while true {
            if let status = vpnManager?.connection.status { providerStatus = status }
            if providerStatus != .disconnecting { return providerStatus }
            guard let remaining = remaining(until: monotonicDeadline), remaining > 0 else { return nil }
            condition.wait(until: Date().addingTimeInterval(min(0.1, remaining)))
        }
    }

    private func remaining(until deadline: TimeInterval) -> TimeInterval? {
        let value = deadline - monotonicNow()
        return value > 0 ? value : nil
    }
}
