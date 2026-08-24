import app
import NetworkExtension
import Foundation
import SystemConfiguration
import DobbyVPNRuntime

/// Shared health state is useful for post-mortem diagnostics, but it is not proof that an iOS
/// packet tunnel is still connected.  NetworkExtension owns that fact.
enum IOSVpnConnectionAuthority {
    private static let lock = NSLock()
    private static var lifecycle = IOSLifecycleState()

    static func beginStart() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        return lifecycle.beginStart()
    }

    static func beginStop() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        return lifecycle.beginStop()
    }

    static func isCurrent(_ candidate: UInt64) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return lifecycle.isCurrent(candidate)
    }

    static func publish(_ newStatus: NEVPNStatus, generation candidate: UInt64) {
        lock.lock()
        defer { lock.unlock() }
        _ = lifecycle.receive(lifecycleState(newStatus), generation: candidate)
    }

    static func currentGeneration() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        return lifecycle.generation
    }

    static func connectionState() -> VpnConnectionState {
        lock.lock()
        defer { lock.unlock() }
        switch lifecycle.extensionState.presentedState {
        case .connected: return .connected
        case .connecting: return .connecting
        case .disconnected: return .disconnected
        }
    }

    private static func lifecycleState(_ status: NEVPNStatus) -> NetworkExtensionState {
        switch status {
        case .invalid: return .invalid
        case .disconnected: return .disconnected
        case .connecting: return .connecting
        case .connected: return .connected
        case .reasserting: return .reasserting
        case .disconnecting: return .disconnecting
        @unknown default: return .invalid
        }
    }
}

/// NetworkExtension-only transport shell for `IOSSessionShell`.
///
/// This deliberately is not a KMP lifecycle implementation: Go owns session
/// selection, probing, failover, and resource lifecycle in the extension.
public class VpnManagerImpl {
    private static let launchId = UUID().uuidString
    private static let disconnectingStartRetryDelay: TimeInterval = 0.5
    private static let disconnectingStartMaxRetries = 120
    private static let startPolicy = IOSStartPolicy(
        maximumRetries: disconnectingStartMaxRetries
    )
    private var logs = NativeModuleHolder.logsRepository

    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"
    // NetworkExtension requires a non-empty display address. It is not a VPN endpoint:
    // the tunnel receives opaque configuration through the App Group instead.
    private static let defaultServerAddress = "Dobby VPN"

    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    private var suppressDisconnectedForPendingStart = false
    private var activeGeneration: UInt64 = 0

    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    public let supportsVpnNetworkReadySignal: Bool = true

    init(connectionRepository: ConnectionStateRepository) {
        self.connectionRepository = connectionRepository
        getOrCreateManager { [weak self] manager, _ in
            guard let self else { return }
            if manager?.connection.status == .connected {
                self.state = manager?.connection.status ?? .invalid
                self.vpnManager = manager
            } else {
                self.state = manager?.connection.status ?? .invalid
            }
            IOSVpnConnectionAuthority.publish(self.state, generation: self.activeGeneration)
        }

        observer = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: nil,
            queue: nil
        ) { [weak self] notification in
            guard let self,
                  let connection = notification.object as? NEVPNConnection else { return }

            if let myConnection = self.vpnManager?.connection, myConnection !== connection {
                self.logs.writeLog(log: "[NEVPNStatusDidChange] ignoring non-Dobby connection status=\(self.statusName(connection.status)) raw=\(connection.status.rawValue)")
                return
            }

            let previous = self.state
            self.state = connection.status
            IOSVpnConnectionAuthority.publish(connection.status, generation: self.activeGeneration)
            self.logs.writeLog(log: "[NEVPNStatusDidChange] \(self.statusName(previous))(\(previous.rawValue)) -> \(self.statusName(connection.status))(\(connection.status.rawValue))")

            switch connection.status {
            case .connected:
                self.suppressDisconnectedForPendingStart = false
                self.publishSessionEvent(state: "CONNECTED")
                self.logs.writeLog(log: "VPN connected")

            case .disconnected:
                if self.suppressDisconnectedForPendingStart {
                    self.suppressDisconnectedForPendingStart = false
                    self.logs.writeLog(log: "[NEVPNStatusDidChange] disconnected belongs to previous stop; waiting for pending start retry")
                    return
                }
                self.publishSessionEvent(state: "IDLE")
                self.logs.writeLog(log: "VPN disconnected")

            case .connecting:
                self.logs.writeLog(log: "VPN is connecting…")

            case .reasserting:
                self.logs.writeLog(log: "VPN is reasserting…")

            case .disconnecting:
                self.logs.writeLog(log: "VPN is disconnecting…")

            case .invalid:
                self.suppressDisconnectedForPendingStart = false
                self.publishSessionEvent(state: "IDLE")
                self.logs.writeLog(log: "VPN status is invalid")

            @unknown default:
                self.logs.writeLog(log: "VPN status unknown: \(connection.status.rawValue)")
            }
        }
    }

    deinit {
        if let observer {
            NotificationCenter.default.removeObserver(observer)
        }
    }

    public func start() {
        let generation = IOSVpnConnectionAuthority.beginStart()
        activeGeneration = generation
        publishSessionEvent(state: "PREPARING")
        self.logs.writeLog(log: "call start launchId=\(Self.launchId)")
        self.logs.writeLog(log: "Routing table without vpn:")
        getOrCreateManager { manager, _ in
            guard IOSVpnConnectionAuthority.isCurrent(generation) else {
                self.logs.writeLog(log: "[start] stale generation=\(generation) ignored before manager start")
                return
            }
            self.handleStart(manager: manager, generation: generation)
        }
    }

    private func handleStart(manager: NETunnelProviderManager?, retryAttempt: Int = 0, generation: UInt64) {
        guard IOSVpnConnectionAuthority.isCurrent(generation) else { return }
        guard let manager = manager else {
            self.logs.writeLog(log: "Created VPNManager is nil")
            return
        }
        let status = manager.connection.status
        self.logs.writeLog(log: "[start] manager loaded status=\(statusName(status)) raw=\(status.rawValue)")
        switch Self.startPolicy.action(for: lifecycleState(status), retryAttempt: retryAttempt) {
        case .retry:
            self.suppressDisconnectedForPendingStart = true
            let nextAttempt = retryAttempt + 1
            self.logs.writeLog(log: "[start] Connection is disconnecting; retry start after 500ms (attempt \(nextAttempt)/\(Self.disconnectingStartMaxRetries))")
            DispatchQueue.main.asyncAfter(deadline: .now() + Self.disconnectingStartRetryDelay) { [weak self] in
                guard let self else { return }
                self.getOrCreateManager { manager, _ in
                    self.handleStart(manager: manager, retryAttempt: nextAttempt, generation: generation)
                }
            }
            return

        case .waitForTransition:
            self.logs.writeLog(log: "[start] Skip: connection is transitioning (\(status.rawValue))")
            return

        case .stopThenRetry:
            // A probe/selected profile must never inherit a running packet tunnel.  Stop the
            // current NE generation and let the normal disconnecting retry path create a fresh
            // tunnel once NetworkExtension confirms cleanup.
            self.logs.writeLog(log: "[start] Tunnel already connected; stopping before fresh generation start")
            self.suppressDisconnectedForPendingStart = true
            manager.connection.stopVPNTunnel()
            DispatchQueue.main.asyncAfter(deadline: .now() + Self.disconnectingStartRetryDelay) { [weak self] in
                guard let self, IOSVpnConnectionAuthority.isCurrent(generation) else { return }
                self.getOrCreateManager { nextManager, _ in
                    self.handleStart(
                        manager: nextManager,
                        retryAttempt: retryAttempt + 1,
                        generation: generation
                    )
                }
            }
            return

        case .fail:
            self.logs.writeLog(log: "[start] Give up: connection stayed \(statusName(status)) after \(retryAttempt) retries")
            self.suppressDisconnectedForPendingStart = false
            self.publishSessionEvent(state: "FAILED", failureCode: "PLATFORM_FAILED")
            return

        case .start:
            break
        }
        if let proto = manager.protocolConfiguration as? NETunnelProviderProtocol {
            self.logs.writeLog(log: "VPN Manager server address configured=\(proto.serverAddress != nil)")
        }
        self.vpnManager = manager
        self.vpnManager?.isEnabled = true
        manager.saveToPreferences { saveError in
            if let saveError = saveError {
                self.logs.writeLog(log: "Failed to save VPN configuration: \(saveError)")
            } else {
                self.logs.writeLog(log: "VPN configuration saved successfully!")
                self.reloadManagerAndStartTunnel(fallbackManager: manager, generation: generation)
            }
        }
    }

    private func reloadManagerAndStartTunnel(fallbackManager: NETunnelProviderManager, generation: UInt64) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, loadError in
            guard let self else { return }
            guard IOSVpnConnectionAuthority.isCurrent(generation) else {
                self.logs.writeLog(log: "[start] stale generation=\(generation) ignored after preferences reload")
                return
            }
            if let loadError {
                self.logs.writeLog(log: "[start] Failed to reload VPN configuration after save: \(loadError.localizedDescription)")
            }

            let reloadedManager = managers?.first(where: { $0.localizedDescription == Self.dobbyName })
            let managerToStart = reloadedManager ?? fallbackManager
            if reloadedManager == nil {
                self.logs.writeLog(log: "[start] Reloaded VPN manager not found after save; starting saved manager instance")
            } else {
                self.logs.writeLog(
                    log: "[start] Reloaded VPN manager after save status=" +
                        "\(self.statusName(managerToStart.connection.status)) raw=\(managerToStart.connection.status.rawValue)"
                )
            }

            self.vpnManager = managerToStart

            do {
                self.logs.writeLog(log: "self.vpnManager = \(managerToStart)")
                self.logs.writeLog(log: "starting tunnel status=\(self.statusName(managerToStart.connection.status)) raw=\(managerToStart.connection.status.rawValue)")
                try managerToStart.connection.startVPNTunnel(options: nil)
                self.logs.writeLog(log: "startVPNTunnel returned; manager.connection.status = \(self.statusName(managerToStart.connection.status)) raw=\(managerToStart.connection.status.rawValue)")
            } catch {
                self.logs.writeLog(log: "Error starting VPNTunnel \(error)")
                self.suppressDisconnectedForPendingStart = false
                self.publishSessionEvent(state: "FAILED", failureCode: "PLATFORM_FAILED")
            }
        }
    }

    public func stop(isUserInitiated: Bool) {
        // Invalidate any asynchronous preference/retry/restart completion before stopping.
        activeGeneration = IOSVpnConnectionAuthority.beginStop()
        IOSVpnConnectionAuthority.publish(.disconnecting, generation: activeGeneration)
        publishSessionEvent(state: "STOPPING")
        self.logs.writeLog(log: "Actually vpnManager is \(String(describing: vpnManager))")
        guard let manager = vpnManager else {
            self.logs.writeLog(log: "[stop] Skip: vpnManager is nil")
            return
        }
        let status = manager.connection.status
        self.logs.writeLog(log: "[stop] stopVPNTunnel requested status=\(statusName(status)) raw=\(status.rawValue) isUserInitiated=\(isUserInitiated)")
        if status == .disconnected || status == .invalid {
            self.logs.writeLog(log: "[stop] Skip: tunnel is already \(statusName(status))")
            return
        }
        manager.connection.stopVPNTunnel()
        self.logs.writeLog(log: "[stop] stopVPNTunnel() called, waiting for .disconnecting")
    }

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, error in
            guard let self else { return }
            if let error {
                self.logs.writeLog(log: "Failed to load VPN preferences: \(error.localizedDescription)")
            }
            self.logs.writeLog(log: "Loaded VPN managers count=\(managers?.count ?? 0)")

            if let existingManager = managers?.first(where: { $0.localizedDescription == Self.dobbyName }) {
                vpnManager = existingManager
                self.logs.writeLog(log: "Existing manager found status=\(self.statusName(existingManager.connection.status)) raw=\(existingManager.connection.status.rawValue)")
                self.applyProtocolDefaults(manager: existingManager)
                completion(existingManager, nil)
            } else {
                self.logs.writeLog(log: "Existing manager not found.")
                self.vpnManager = self.makeManager()
                self.vpnManager?.saveToPreferences { [weak self] error in
                    completion(self?.vpnManager, error)
                }
            }
        }
    }

    private func publishSessionEvent(state: String, failureCode: String = "") {
        connectionRepository.tryPublishSessionEvent(
            sessionId: "ios",
            generation: Int64(activeGeneration),
            sequence: 0,
            state: state,
            failureCode: failureCode
        )
    }

    private func makeManager() -> NETunnelProviderManager {
        let newVpnManager = NETunnelProviderManager()
        newVpnManager.localizedDescription = Self.dobbyName

        newVpnManager.protocolConfiguration = makeDefaultProtocol()
        newVpnManager.isEnabled = true
        return newVpnManager
    }

    private func applyProtocolDefaults(manager: NETunnelProviderManager) {
        guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol else { return }
        applyProtocolDefaults(proto)
        manager.protocolConfiguration = proto
    }

    private func makeDefaultProtocol() -> NETunnelProviderProtocol {
        let proto = NETunnelProviderProtocol()
        proto.providerConfiguration = [:]
        applyProtocolDefaults(proto)
        return proto
    }

    private func applyProtocolDefaults(_ proto: NETunnelProviderProtocol) {
        proto.providerBundleIdentifier = Self.dobbyBundleIdentifier
        proto.serverAddress = Self.defaultServerAddress
        proto.includeAllNetworks = true
        proto.excludeLocalNetworks = true
        if #available(iOS 16.4, *) {
            proto.excludeCellularServices = false
            proto.excludeAPNs = false
        }
        proto.enforceRoutes = false
        if #available(iOS 17.4, *) {
            proto.excludeDeviceCommunication = false
        }
    }

    private func statusName(_ status: NEVPNStatus) -> String {
        switch status {
        case .invalid:
            return "invalid"
        case .disconnected:
            return "disconnected"
        case .connecting:
            return "connecting"
        case .connected:
            return "connected"
        case .reasserting:
            return "reasserting"
        case .disconnecting:
            return "disconnecting"
        @unknown default:
            return "unknown"
        }
    }

    private func lifecycleState(_ status: NEVPNStatus) -> NetworkExtensionState {
        switch status {
        case .invalid: return .invalid
        case .disconnected: return .disconnected
        case .connecting: return .connecting
        case .connected: return .connected
        case .reasserting: return .reasserting
        case .disconnecting: return .disconnecting
        @unknown default: return .invalid
        }
    }

}
