import Foundation
import NetworkExtension
import app

/// App-process half of the iOS session boundary.
///
/// Go lives in the packet-tunnel process because it owns the TUN and protocol
/// resources.  This object deliberately does not create a Go manager: it
/// stores opaque bytes in the App Group, requests NetworkExtension start/stop,
/// and exposes *NetworkExtension status* to the KMP UI.  Persisted diagnostics
/// can never manufacture a connected UI state here.
final class IOSSessionShell: NSObject, IosSessionBridge {
    private let defaults = UserDefaults(suiteName: appGroupIdentifier) ?? .standard
    private let secrets = SharedKeychainSecretStore.shared
    private let rawConfigurationKey = "sessionapi.v1.rawConfiguration"
    private let manager: VpnManagerImpl
    private var configured = false
    private var sequence: Int64 = 0
    private var lastState = "IDLE"

    init(manager: VpnManagerImpl) {
        self.manager = manager
        super.init()
        secrets.migrate(keys: [rawConfigurationKey], from: defaults)
        configured = secrets.data(for: rawConfigurationKey)?.isEmpty == false
    }

    func configure(rawConfig: KotlinByteArray) -> String {
        let raw = data(from: rawConfig)
        guard !raw.isEmpty else { return failure("MALFORMED_CONFIG") }
        guard secrets.set(raw, for: rawConfigurationKey) else { return failure("SECURE_STORAGE_FAILED") }
        defaults.removeObject(forKey: rawConfigurationKey)
        configured = true
        return success(["digest": "extension-pending", "profiles": [], "warnings": []])
    }

    func start(mode: String, index: Int32) -> String {
        guard configured, secrets.data(for: rawConfigurationKey)?.isEmpty == false else {
            return failure("NOT_CONFIGURED")
        }
        // iOS intentionally has no UI-side profile selection. The extension
        // sends AUTO_SELECT to Go after NE creates its packet tunnel.
        guard mode == "AUTO_SELECT", index == 0 else { return failure("UNSUPPORTED") }
        manager.start(isProtocolProbe: false)
        let generation = Int64(IOSVpnConnectionAuthority.currentGeneration())
        return success(["generation": generation])
    }

    func stop(generation: Int64) -> String {
        guard generation > 0 else { return failure("STALE_GENERATION") }
        guard UInt64(generation) == IOSVpnConnectionAuthority.currentGeneration() else {
            return failure("STALE_GENERATION")
        }
        manager.stop(isUserInitiated: true)
        return success(["generation": generation])
    }

    func snapshot() -> String {
        let generation = Int64(IOSVpnConnectionAuthority.currentGeneration())
        let state = extensionState()
        return success([
            "generation": generation,
            "state": state,
            "configured": configured,
            "cleanup_complete": state == "IDLE" || state == "FAILED",
        ])
    }

    func observe(afterSequence: Int64) -> String {
        let state = extensionState()
        if state != lastState {
            sequence += 1
            lastState = state
        }
        let events: [[String: Any]]
        if sequence > afterSequence {
            events = [[
                "generation": Int64(IOSVpnConnectionAuthority.currentGeneration()),
                "sequence": sequence,
                "state": state,
            ]]
        } else {
            events = []
        }
        return success(["events": events, "next_sequence": sequence])
    }

    func destroy() -> String {
        configured = false
        secrets.remove(rawConfigurationKey)
        defaults.removeObject(forKey: rawConfigurationKey)
        return success([:])
    }

    private func extensionState() -> String {
        // IOSVpnConnectionAuthority is updated solely from NEVPNStatusDidChange.
        switch IOSVpnConnectionAuthority.connectionState() {
        case .connected: return "CONNECTED"
        case .connecting: return "PREPARING"
        case .disconnected: return "IDLE"
        default: return "IDLE"
        }
    }

    private func data(from value: KotlinByteArray) -> Data {
        let count = Int(value.size)
        return Data((0..<count).map { UInt8(bitPattern: value.get(index: Int32($0))) })
    }

    private func success(_ result: [String: Any]) -> String { json(["ok": true, "result": result]) }
    private func failure(_ code: String) -> String { json(["ok": false, "error": ["code": code]]) }
    private func json(_ object: [String: Any]) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: object), let text = String(data: data, encoding: .utf8) else {
            return "{\"ok\":false,\"error\":{\"code\":\"INTERNAL\"}}"
        }
        return text
    }
}
