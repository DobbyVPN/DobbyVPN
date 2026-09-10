import app
import NetworkExtension
import Foundation

/// A NetworkExtension readiness and message-transport shell.
///
/// This class deliberately has no VPN product state, generation, configured
/// flag, or event sequence. Those values belong to Go SessionV2 in the
/// packet-tunnel process. The only state retained here is the private
/// readiness state needed to deliver a provider message.
public final class VpnManagerImpl: NSObject {
    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"
    private static let defaultServerAddress = "Dobby VPN"

    private let logs = NativeModuleHolder.logsRepository
    private let condition = NSCondition()
    private var vpnManager: NETunnelProviderManager?
    private var providerStatus: NEVPNStatus = .invalid
    private var observer: NSObjectProtocol?

    public override init() {
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
            self.logs.writeLog(log: "[NEVPNStatusDidChange] provider status=\(self.statusName(connection.status)) raw=\(connection.status.rawValue)")
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
            logs.writeLog(log: "[provider] sendProviderMessage failed: \(String(reflecting: error))")
            return transportFailureResponse(
                for: messageData,
                code: "PLATFORM_FAILED",
                message: String(reflecting: error)
            )
        }
    }

    /// Stops only the control-mode provider. Callers invoke this after a
    /// successful Go Destroy response; a failed or timed-out destroy retains
    /// the provider and its mailbox for recovery.
    public func stopControlProvider() {
        condition.lock()
        let manager = vpnManager
        condition.unlock()
        manager?.connection.stopVPNTunnel()
    }

    public static func transportFailure(_ code: String, message: String) -> Data {
        let value: [String: Any] = [
            "ok": false,
            "error": ["code": code, "message": message],
        ]
        do {
            return try JSONSerialization.data(withJSONObject: value)
        } catch {
            NativeModuleHolder.logsRepository.writeLog(
                log: "[provider] transport failure encoding failed: \(String(reflecting: error))"
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
            logs.writeLog(log: "[provider] transport failure response encoding failed: \(String(reflecting: error))")
            return Self.transportFailure("INTERNAL", message: String(reflecting: error))
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
            // KMP invokes this bridge on Dispatchers.Default. Refuse a main
            // thread wait rather than freezing the UI if a caller violates the
            // boundary.
            logs.writeLog(log: "[provider] readiness check rejected on the main thread")
            return "readiness check rejected on the main thread"
        }
        // Once the saved-and-reloaded manager is connected, use that exact
        // bound object for subsequent commands. Re-saving preferences for
        // every Observe/Snapshot would add needless transition risk and
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
            logs.writeLog(log: "[provider] NetworkExtension preference save/load failed: \(String(reflecting: loadError))")
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
                logs.writeLog(log: "[provider] control-mode start failed: \(String(reflecting: error))")
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

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, error in
            guard let self else { completion(nil, error); return }
            if let error {
                self.logs.writeLog(log: "[provider] preference load failed: \(String(reflecting: error))")
                completion(nil, error)
                return
            }
            if let existing = managers?.first(where: { $0.localizedDescription == Self.dobbyName }) {
                self.applyProtocolDefaults(manager: existing)
                // Persist control-mode routing defaults before any start. An
                // in-memory fix is not sufficient because NetworkExtension
                // may reload the saved manager for the provider process.
                self.saveAndReload(existing, completion: completion)
                return
            }
            let created = self.makeManager()
            self.saveAndReload(created, completion: completion)
        }
    }

    /// NetworkExtension's save completion acknowledges persistence, but the
    /// manager object passed to it is not the authoritative object used by a
    /// subsequent start. Reload the saved preference and bind all state to
    /// that object before returning from the readiness fence.
    private func saveAndReload(
        _ manager: NETunnelProviderManager,
        completion: @escaping (NETunnelProviderManager?, Error?) -> Void
    ) {
        manager.saveToPreferences { [weak self] saveError in
            guard let self else { completion(nil, saveError); return }
            guard let saveError else {
                self.reloadSavedManager(completion: completion)
                return
            }
            self.logs.writeLog(log: "[provider] preference save failed: \(String(reflecting: saveError))")
            completion(nil, saveError)
        }
    }

    private func reloadSavedManager(
        completion: @escaping (NETunnelProviderManager?, Error?) -> Void
    ) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, loadError in
            guard let self else { completion(nil, loadError); return }
            if let loadError {
                self.logs.writeLog(log: "[provider] preference reload failed: \(String(reflecting: loadError))")
                completion(nil, loadError)
                return
            }
            guard let reloaded = managers?.first(where: { $0.localizedDescription == Self.dobbyName }) else {
                let missing = NSError(
                    domain: "PacketTunnelProvider.preferences",
                    code: -8,
                    userInfo: [NSLocalizedDescriptionKey: "saved DobbyVPN NetworkExtension manager was not returned by reload"]
                )
                self.logs.writeLog(log: "[provider] preference reload did not return the saved DobbyVPN manager")
                completion(nil, missing)
                return
            }
            self.condition.lock()
            self.vpnManager = reloaded
            self.providerStatus = reloaded.connection.status
            self.condition.broadcast()
            self.condition.unlock()
            completion(reloaded, nil)
        }
    }

    private func makeManager() -> NETunnelProviderManager {
        let manager = NETunnelProviderManager()
        manager.localizedDescription = Self.dobbyName
        manager.protocolConfiguration = makeDefaultProtocol()
        manager.isEnabled = true
        return manager
    }

    private func applyProtocolDefaults(manager: NETunnelProviderManager) {
        let proto = (manager.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        applyProtocolDefaults(proto)
        manager.protocolConfiguration = proto
        manager.isEnabled = true
    }

    private func makeDefaultProtocol() -> NETunnelProviderProtocol {
        let proto = NETunnelProviderProtocol()
        proto.providerConfiguration = ["mode": "control"]
        applyProtocolDefaults(proto)
        return proto
    }

    private func applyProtocolDefaults(_ proto: NETunnelProviderProtocol) {
        proto.providerBundleIdentifier = Self.dobbyBundleIdentifier
        proto.serverAddress = Self.defaultServerAddress
        proto.providerConfiguration = ["mode": "control"]
        // Control mode must not claim all traffic before Go owns a generation
        // and the provider applies settings at its AcquireTunnel callback.
        proto.includeAllNetworks = false
        proto.excludeLocalNetworks = false
        proto.enforceRoutes = false
        if #available(iOS 16.4, *) {
            proto.excludeCellularServices = false
            proto.excludeAPNs = false
        }
        if #available(iOS 17.4, *) { proto.excludeDeviceCommunication = false }
    }

    private func statusName(_ status: NEVPNStatus) -> String {
        switch status {
        case .invalid: return "invalid"
        case .disconnected: return "disconnected"
        case .connecting: return "connecting"
        case .connected: return "connected"
        case .reasserting: return "reasserting"
        case .disconnecting: return "disconnecting"
        @unknown default: return "unknown"
        }
    }
}
