import Foundation

/// Native preference inventories may contain renamed or duplicate configurations.
/// Only provider identity and a uniquely active connection can select an owner.
enum VPNManagerIdentity {
    static func select(matchingActiveStates: [Bool]) throws -> Int? {
        if matchingActiveStates.isEmpty { return nil }
        if matchingActiveStates.count == 1 { return 0 }
        let active = matchingActiveStates.indices.filter { matchingActiveStates[$0] }
        guard active.count == 1 else {
            throw NSError(domain: "DobbyVPN.preferences", code: 4,
                userInfo: [NSLocalizedDescriptionKey:
                    "Multiple VPN configurations match this provider; select one unambiguous active configuration before connecting or enabling On Demand."])
        }
        return active[0]
    }
}

/// The preference APIs are asynchronous; one operation retains the exact
/// native manager across save and reload and never binds a failed operation.
enum VPNPreferenceOperations {
    enum Phase { case save, reload }

    static func matching<T>(_ values: [T], provider: String, identity: (T) -> String?) -> [T] {
        values.filter { identity($0) == provider }
    }

    static func saveAndReload<T: AnyObject>(
        _ manager: T,
        save: @escaping (T, @escaping (Error?) -> Void) -> Void,
        reload: @escaping (T, @escaping (Error?) -> Void) -> Void,
        bind: ((T) -> Void)?,
        completion: @escaping (T?, Phase?, Error?) -> Void
    ) {
        save(manager) { error in
            if let error { completion(nil, .save, error); return }
            reload(manager) { error in
                if let error { completion(nil, .reload, error); return }
                bind?(manager)
                completion(manager, nil, nil)
            }
        }
    }

    static func performSequentially<T>(
        _ values: [T],
        perform: @escaping (Int, T, @escaping (Error?) -> Void) -> Void,
        completion: @escaping ([Error]) -> Void
    ) {
        func next(_ index: Int, _ errors: [Error]) {
            guard index < values.count else { completion(errors); return }
            perform(index, values[index]) { error in
                next(index + 1, errors + (error.map { [$0] } ?? []))
            }
        }
        next(0, [])
    }
}

#if canImport(NetworkExtension)
import NetworkExtension

/// The production identity and preference operations over native manager objects.
enum NativeVPNManagerPreferences {
    typealias PreferenceOperation = (NETunnelProviderManager, @escaping (Error?) -> Void) -> Void

    static func matching(_ managers: [NETunnelProviderManager], provider: String) -> [NETunnelProviderManager] {
        VPNPreferenceOperations.matching(managers, provider: provider) {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier
        }
    }

    static func isActive(_ status: NEVPNStatus) -> Bool {
        switch status {
        case .connecting, .connected, .reasserting, .disconnecting: return true
        default: return false
        }
    }

    static func select(
        _ matching: [NETunnelProviderManager],
        status: (NETunnelProviderManager) -> NEVPNStatus = { $0.connection.status }
    ) throws -> NETunnelProviderManager? {
        let index = try VPNManagerIdentity.select(matchingActiveStates: matching.map { isActive(status($0)) })
        return index.map { matching[$0] }
    }

    static func saveAndReload(
        _ manager: NETunnelProviderManager,
        save: @escaping PreferenceOperation = { manager, done in manager.saveToPreferences(completionHandler: done) },
        reload: @escaping PreferenceOperation = { manager, done in manager.loadFromPreferences(completionHandler: done) },
        bind: ((NETunnelProviderManager) -> Void)?,
        completion: @escaping (NETunnelProviderManager?, VPNPreferenceOperations.Phase?, Error?) -> Void
    ) {
        VPNPreferenceOperations.saveAndReload(manager, save: save, reload: reload, bind: bind, completion: completion)
    }
}
#endif
