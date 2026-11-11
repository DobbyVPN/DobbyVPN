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
            let matching = Self.matchingManagers(managers ?? [])
            let selected: NETunnelProviderManager?
            do { selected = try Self.selectManager(matching) }
            catch { completion(nil, error); return }
            if let existing = selected {
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

    /// Save completion acknowledges persistence. Reload that same native
    /// preference object before binding it for a subsequent start.
    private func saveAndReload(
        _ manager: NETunnelProviderManager,
        bind: Bool = true,
        completion: @escaping (NETunnelProviderManager?, Error?) -> Void
    ) {
        VPNPreferenceOperations.saveAndReload(
            manager,
            save: { manager, done in manager.saveToPreferences(completionHandler: done) },
            reload: { manager, done in manager.loadFromPreferences(completionHandler: done) },
            bind: bind ? { manager in
                self.condition.lock()
                self.vpnManager = manager
                self.providerStatus = manager.connection.status
                self.condition.broadcast()
                self.condition.unlock()
            } : nil,
            completion: { saved, phase, error in
                if let error {
                    let operation = phase == .save ? "save" : "reload"
                    self.logs.writeLog(level: "ERROR", log: "[provider] preference \(operation) failed:\n\(diagnosticErrorDescription(error))")
                }
                completion(saved, error)
            }
        )
    }

    private static func matchingManagers(_ managers: [NETunnelProviderManager]) -> [NETunnelProviderManager] {
        VPNPreferenceOperations.matching(managers, provider: dobbyBundleIdentifier) {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier
        }
    }

    private static func selectManager(_ matching: [NETunnelProviderManager]) throws -> NETunnelProviderManager? {
        let index = try VPNManagerIdentity.select(matchingActiveStates: matching.map {
            switch $0.connection.status {
            case .connecting, .connected, .reasserting, .disconnecting: return true
            default: return false
            }
        })
        return index.map { matching[$0] }
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
                    continuation.resume(returning: Self.matchingManagers(managers ?? []).contains(where: { $0.isOnDemandEnabled }))
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
            let matching = Self.matchingManagers(managers ?? [])
            if !enabled {
                self.disableOnDemand(matching, completion: completion)
                return
            }
            let manager: NETunnelProviderManager
            do {
                guard let selected = try Self.selectManager(matching) else {
                    completion(NSError(domain: "DobbyVPN.OnDemand", code: 2,
                        userInfo: [NSLocalizedDescriptionKey: "Connect once to create the VPN configuration."]))
                    return
                }
                manager = selected
            } catch { completion(error); return }
            let connect = NEOnDemandRuleConnect()
            connect.interfaceTypeMatch = .any
            manager.onDemandRules = [connect]
            manager.isOnDemandEnabled = true
            self.saveOnDemand(manager, enabled: true, completion: completion)
        }
    }

    private func saveOnDemand(_ manager: NETunnelProviderManager, enabled: Bool,
                              completion: @escaping (Error?) -> Void) {
        saveAndReload(manager, bind: enabled) { saved, error in
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

    // Try every matching configuration, retaining every original failure. A
    // failed preference must never prevent disabling the remaining managers.
    private func disableOnDemand(_ managers: [NETunnelProviderManager],
                                 completion: @escaping (Error?) -> Void) {
        VPNPreferenceOperations.performSequentially(managers, perform: { index, manager, done in
            manager.isOnDemandEnabled = false
            self.saveOnDemand(manager, enabled: false) { error in
                if let error {
                    self.logs.writeLog(level: "ERROR", log: "[provider] On Demand disable failed manager=\(index):\n\(diagnosticErrorDescription(error))")
                }
                done(error)
            }
        }, completion: { failures in
            if failures.isEmpty { completion(nil) }
            else {
                completion(NSError(domain: "DobbyVPN.OnDemand", code: 5,
                    userInfo: [NSLocalizedDescriptionKey: "Could not disable On Demand on every matching VPN configuration.",
                               "underlyingErrors": failures,
                               NSUnderlyingErrorKey: failures[0]]))
            }
        })
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
