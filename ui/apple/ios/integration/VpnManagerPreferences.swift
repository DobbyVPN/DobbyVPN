import Foundation
import IOSIntegration
import NetworkExtension

extension VpnManagerImpl {
    func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, error in
            guard let self else { completion(nil, error); return }
            if let error {
                self.logs.writeLog(log: "[provider] preference load failed:\n\(diagnosticErrorDescription(error))")
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
            self.logs.writeLog(log: "[provider] preference save failed:\n\(diagnosticErrorDescription(saveError))")
            completion(nil, saveError)
        }
    }

    private func reloadSavedManager(
        completion: @escaping (NETunnelProviderManager?, Error?) -> Void
    ) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, loadError in
            guard let self else { completion(nil, loadError); return }
            if let loadError {
                self.logs.writeLog(log: "[provider] preference reload failed:\n\(diagnosticErrorDescription(loadError))")
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
