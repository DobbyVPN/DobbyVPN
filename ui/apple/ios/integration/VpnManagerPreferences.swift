import Foundation
import NetworkExtension

extension VpnManagerImpl {
    func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        preferences.getOrCreateManager(completion: completion)
    }

    public func onDemandEnabled() async throws -> Bool {
        try await withCheckedThrowingContinuation { continuation in
            preferences.onDemandEnabled { enabled, error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: enabled ?? false) }
            }
        }
    }

    public func setOnDemandEnabled(_ enabled: Bool) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            preferences.changeOnDemand(enabled) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
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
