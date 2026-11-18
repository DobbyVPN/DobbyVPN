import Foundation
import NetworkExtension

/// Serial production preference workflow. Tests replace native API boundaries,
/// retaining real manager/protocol objects without invoking OS preference storage.
final class VPNPreferenceCoordinator {
    typealias Inventory = (@escaping ([NETunnelProviderManager]?, Error?) -> Void) -> Void
    typealias Operation = NativeVPNManagerPreferences.PreferenceOperation
    private let providerIdentifier: String
    private let name: String
    private let serverAddress: String
    private let loadInventory: Inventory
    private let save: Operation
    private let reload: Operation
    private let status: (NETunnelProviderManager) -> NEVPNStatus
    private let bind: (NETunnelProviderManager) -> Void
    private let log: (String, String) -> Void
    private let describeError: (Error) -> String
    private let preferenceQueue: DispatchQueue
    private var preferenceOperations: [(@escaping () -> Void) -> Void] = []
    private var createManager: () -> NETunnelProviderManager
    private var applyDefaults: (NETunnelProviderManager) -> Void

    init(providerIdentifier: String, name: String, serverAddress: String,
         queue: DispatchQueue = DispatchQueue(label: "vpn.dobby.preferences"),
         load: @escaping Inventory = { NETunnelProviderManager.loadAllFromPreferences(completionHandler: $0) },
         save: @escaping Operation = { $0.saveToPreferences(completionHandler: $1) },
         reload: @escaping Operation = { $0.loadFromPreferences(completionHandler: $1) },
         status: @escaping (NETunnelProviderManager) -> NEVPNStatus = { $0.connection.status },
         create: (() -> NETunnelProviderManager)? = nil,
         apply: ((NETunnelProviderManager) -> Void)? = nil,
         bind: @escaping (NETunnelProviderManager) -> Void,
         log: @escaping (String, String) -> Void,
         describeError: @escaping (Error) -> String) {
        self.preferenceQueue = queue
        self.providerIdentifier = providerIdentifier
        self.name = name
        self.serverAddress = serverAddress
        self.loadInventory = load
        self.save = save
        self.reload = reload
        self.status = status
        self.bind = bind
        self.log = log
        self.describeError = describeError
        self.createManager = { NETunnelProviderManager() }
        self.applyDefaults = { _ in }
        self.createManager = create ?? { [unowned self] in self.makeManager() }
        self.applyDefaults = apply ?? { [unowned self] in self.applyProtocolDefaults(manager: $0) }
    }

    /// On Demand is saved off before Go Stop, so a tunnel that ends during Stop
    /// is not restarted by the OS. The wait takes at most half of the remaining
    /// deadline; a failed or slow save is reported but Go Stop is still sent.
    func performStop<T>(until deadline: TimeInterval, stopGo: () throws -> T) rethrows -> (T, String?) {
        log("INFO", "[provider] Stop On Demand disable begin deadline=\(deadline)")
        let now = ProcessInfo.processInfo.systemUptime
        let waitDeadline = now + max(0, deadline - now) / 2
        let failure = beginDisableOnDemandForStop(until: waitDeadline)()
        log(failure == nil ? "INFO" : "ERROR", "[provider] Stop On Demand wait end error=\(failure ?? "none")")
        return (try stopGo(), failure)
    }

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
        self.loadInventory { [weak self] managers, error in
            guard let self else { completion(nil, error); return }
            if let error {
                self.log("ERROR", "[provider] preference load failed:\n\(self.describeError(error))")
                completion(nil, error)
                return
            }
            let matching = self.matchingManagers(managers ?? [])
            let selected: NETunnelProviderManager?
            do { selected = try self.selectManager(matching) }
            catch { completion(nil, error); return }
            if let existing = selected {
                self.applyDefaults(existing)
                // Persist control-mode routing defaults before any start. An
                // in-memory fix is not sufficient because NetworkExtension
                // may reload the saved manager for the provider process.
                self.saveAndReload(existing, completion: completion)
                return
            }
            let created = self.createManager()
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
        NativeVPNManagerPreferences.saveAndReload(
            manager,
            save: save, reload: reload,
            bind: bind ? self.bind : nil,
            completion: { saved, phase, error in
                if let error {
                    let operation = phase == .save ? "save" : "reload"
                    self.log("ERROR", "[provider] preference \(operation) failed:\n\(self.describeError(error))")
                }
                completion(saved, error)
            }
        )
    }

    private func matchingManagers(_ managers: [NETunnelProviderManager]) -> [NETunnelProviderManager] {
        NativeVPNManagerPreferences.matching(managers, provider: providerIdentifier)
    }

    private func selectManager(_ matching: [NETunnelProviderManager]) throws -> NETunnelProviderManager? {
        try NativeVPNManagerPreferences.select(matching, status: status)
    }

    private func makeManager() -> NETunnelProviderManager {
        let manager = NETunnelProviderManager()
        manager.localizedDescription = name
        manager.protocolConfiguration = makeDefaultProtocol()
        manager.isEnabled = true
        applyOnDemandRules(manager)
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
        proto.providerBundleIdentifier = providerIdentifier
        proto.serverAddress = serverAddress
        proto.providerConfiguration = ["mode": "control"]
        // Control mode must not claim all traffic before Go owns a generation
        // and the provider applies settings at its AcquireTunnel callback.
        proto.includeAllNetworks = false
        proto.excludeLocalNetworks = false
        proto.enforceRoutes = false
        if #available(iOS 16.4, macOS 13.3, *) {
            proto.excludeCellularServices = false
            proto.excludeAPNs = false
        }
        if #available(iOS 17.4, macOS 14.4, *) { proto.excludeDeviceCommunication = false }
    }

    func changeOnDemand(_ enabled: Bool, completion: @escaping (Error?) -> Void) {
        withPreferences { finished in
            self.performOnDemandChange(enabled) { error in
                completion(error)
                finished()
            }
        }
    }

    private func performOnDemandChange(_ enabled: Bool, completion: @escaping (Error?) -> Void) {
        self.loadInventory { [weak self] managers, error in
            guard let self else { completion(error); return }
            if let error { completion(error); return }
            let matching = self.matchingManagers(managers ?? [])
            if !enabled {
                self.disableOnDemand(matching, completion: completion)
                return
            }
            let manager: NETunnelProviderManager
            do {
                guard let selected = try self.selectManager(matching) else {
                    completion(NSError(domain: "DobbyVPN.OnDemand", code: 2,
                        userInfo: [NSLocalizedDescriptionKey: "Connect once to create the VPN configuration."]))
                    return
                }
                manager = selected
            } catch { completion(error); return }
            self.applyOnDemandRules(manager)
            self.saveOnDemand(manager, enabled: true, completion: completion)
        }
    }

    private func applyOnDemandRules(_ manager: NETunnelProviderManager) {
        let connect = NEOnDemandRuleConnect()
        connect.interfaceTypeMatch = .any
        manager.onDemandRules = [connect]
        manager.isOnDemandEnabled = true
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
            self.log("INFO", "[provider] On Demand enabled=\(enabled)")
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
                    self.log("ERROR", "[provider] On Demand disable failed manager=\(index):\n\(self.describeError(error))")
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
                self.log("ERROR", "[provider] Stop could not disable On Demand:\n\(self.describeError(error))")
            }
            completed.signal()
        }
        return {
            let remaining = max(0, deadline - ProcessInfo.processInfo.systemUptime)
            guard completed.wait(timeout: .now() + remaining) == .success else {
                return "On Demand preference update did not complete before the Stop deadline"
            }
            lock.lock(); defer { lock.unlock() }
            return failure.map { self.describeError($0) }
        }
    }
}
