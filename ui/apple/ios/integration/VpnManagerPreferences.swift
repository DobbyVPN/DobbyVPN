import Foundation
import NetworkExtension

extension VpnManagerImpl {
    // A load/save/reload is one preference operation. Serializing only the
    // individual callbacks could let readiness save an old On Demand value
    // after an explicit Stop disabled it.
    private func withPreferences(_ operation: @escaping (@escaping () -> Void) -> Void) {
        preferenceQueue.async {
            self.preferenceOperations.append(operation)
            if self.preferenceOperations.count == 1 { self.runPreferenceOperation() }
        }
    }

    private func runPreferenceOperation() {
        guard let operation = preferenceOperations.first else { return }
        operation {
            self.preferenceQueue.async {
                self.preferenceOperations.removeFirst()
                self.runPreferenceOperation()
            }
        }
    }

    func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        withPreferences { finished in
            self.loadOrCreateManager { manager, error in
                completion(manager, error)
                finished()
            }
        }
    }

    private func loadOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, error in
            guard let self else { completion(nil, error); return }
            if let error {
                self.logs.writeLog(level: "ERROR", log: "[provider] preference load failed:\n\(diagnosticErrorDescription(error))")
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
            self.logs.writeLog(level: "ERROR", log: "[provider] preference save failed:\n\(diagnosticErrorDescription(saveError))")
            completion(nil, saveError)
        }
    }

    private func reloadSavedManager(
        completion: @escaping (NETunnelProviderManager?, Error?) -> Void
    ) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, loadError in
            guard let self else { completion(nil, loadError); return }
            if let loadError {
                self.logs.writeLog(level: "ERROR", log: "[provider] preference reload failed:\n\(diagnosticErrorDescription(loadError))")
                completion(nil, loadError)
                return
            }
            guard let reloaded = managers?.first(where: { $0.localizedDescription == Self.dobbyName }) else {
                let missing = NSError(
                    domain: "PacketTunnelProvider.preferences",
                    code: -8,
                    userInfo: [NSLocalizedDescriptionKey: "saved DobbyVPN NetworkExtension manager was not returned by reload"]
                )
                self.logs.writeLog(level: "ERROR", log: "[provider] preference reload did not return the saved DobbyVPN manager")
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

    func statusName(_ status: NEVPNStatus) -> String {
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

extension VpnManagerImpl {
    /// On Demand is an OS preference. Go still owns the saved selection and
    /// every connection attempt when NetworkExtension launches the provider.
    public func onDemandEnabled() async throws -> Bool {
        try await withCheckedThrowingContinuation { continuation in
            withPreferences { finished in
                NETunnelProviderManager.loadAllFromPreferences { managers, error in
                    defer { finished() }
                    if let error { continuation.resume(throwing: error); return }
                    continuation.resume(returning: managers?.first(where: { $0.localizedDescription == Self.dobbyName })?.isOnDemandEnabled ?? false)
                }
            }
        }
    }

    public func setOnDemandEnabled(_ enabled: Bool) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            changeOnDemand(enabled) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
    }

    private func changeOnDemand(_ enabled: Bool, completion: @escaping (Error?) -> Void) {
        withPreferences { finished in
            self.performOnDemandChange(enabled) { error in
                completion(error)
                finished()
            }
        }
    }

    private func performOnDemandChange(_ enabled: Bool, completion: @escaping (Error?) -> Void) {
        if enabled && SharedKeychainSecretStore.shared.string(for: SharedKeychainSecretStore.activationKey) == nil {
            completion(NSError(domain: "DobbyVPN.OnDemand", code: 1,
                userInfo: [NSLocalizedDescriptionKey: "Connect once before enabling On Demand."]))
            return
        }
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, error in
            guard let self else { completion(error); return }
            if let error { completion(error); return }
            guard let manager = managers?.first(where: { $0.localizedDescription == Self.dobbyName }) else {
                if !enabled { completion(nil); return }
                completion(NSError(domain: "DobbyVPN.OnDemand", code: 2,
                    userInfo: [NSLocalizedDescriptionKey: "Connect once to create the VPN configuration."]))
                return
            }
            if enabled {
                let connect = NEOnDemandRuleConnect()
                connect.interfaceTypeMatch = .any
                manager.onDemandRules = [connect]
            }
            manager.isOnDemandEnabled = enabled
            manager.saveToPreferences { [weak self] error in
                guard let self else { completion(error); return }
                if let error {
                    self.logs.writeLog(level: "ERROR", log: "[provider] On Demand preference save failed:\n\(diagnosticErrorDescription(error))")
                    completion(error)
                    return
                }
                self.reloadSavedManager { saved, error in
                    if let error { completion(error); return }
                    guard saved?.isOnDemandEnabled == enabled else {
                        completion(NSError(domain: "DobbyVPN.OnDemand", code: 3,
                            userInfo: [NSLocalizedDescriptionKey: "Saved On Demand preference did not match the requested value."]))
                        return
                    }
                    self.logs.writeLog(log: "[provider] On Demand enabled=\(enabled)")
                    completion(nil)
                }
            }
        }
    }

    // Start the preference update alongside Go Stop. A failed or slow OS
    // preference write must not prevent release of app-owned protection.
    // Both operations consume the existing command deadline.
    func beginDisableOnDemandForStop(until deadline: TimeInterval) -> () -> String? {
        let completed = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var failure: Error?
        changeOnDemand(false) { error in
            lock.lock(); failure = error; lock.unlock()
            if let error {
                self.logs.writeLog(level: "ERROR", log: "[provider] Stop could not disable On Demand:\n\(diagnosticErrorDescription(error))")
            }
            completed.signal()
        }
        return {
            let remaining = max(0, deadline - ProcessInfo.processInfo.systemUptime)
            guard completed.wait(timeout: .now() + remaining) == .success else {
                return "On Demand preference update did not complete before the Stop deadline"
            }
            lock.lock(); defer { lock.unlock() }
            return failure.map { diagnosticErrorDescription($0) }
        }
    }
}
