import app
import NetworkExtension
import Foundation
import SystemConfiguration
import MyLibrary

/// Shared health state is useful for post-mortem diagnostics, but it is not proof that an iOS
/// packet tunnel is still connected.  NetworkExtension owns that fact.
enum IOSVpnConnectionAuthority {
    private static let lock = NSLock()
    private static var generation: UInt64 = 0
    private static var status: NEVPNStatus = .disconnected

    static func beginGeneration() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        generation &+= 1
        return generation
    }

    static func isCurrent(_ candidate: UInt64) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return generation == candidate
    }

    static func publish(_ newStatus: NEVPNStatus, generation candidate: UInt64) {
        lock.lock()
        defer { lock.unlock() }
        guard generation == candidate else { return }
        status = newStatus
    }

    static func currentGeneration() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        return generation
    }

    static func connectionState() -> VpnConnectionState {
        lock.lock()
        defer { lock.unlock() }
        switch status {
        case .connected: return .connected
        case .connecting, .reasserting, .disconnecting: return .connecting
        case .invalid, .disconnected: return .disconnected
        @unknown default: return .disconnected
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
    private var logs = NativeModuleHolder.logsRepository

    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"

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
                self.connectionRepository.tryUpdateVpnNetworkReady(isReady: true)
                self.connectionRepository.tryUpdateServiceStarted(isStarted: true)
                self.logs.writeLog(log: "VPN connected")

            case .disconnected:
                if self.suppressDisconnectedForPendingStart {
                    self.suppressDisconnectedForPendingStart = false
                    self.logs.writeLog(log: "[NEVPNStatusDidChange] disconnected belongs to previous stop; waiting for pending start retry")
                    return
                }
                self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
                self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
                self.logs.writeLog(log: "VPN disconnected")

            case .connecting:
                self.logs.writeLog(log: "VPN is connecting…")

            case .reasserting:
                self.logs.writeLog(log: "VPN is reasserting…")

            case .disconnecting:
                self.logs.writeLog(log: "VPN is disconnecting…")

            case .invalid:
                self.suppressDisconnectedForPendingStart = false
                self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
                self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
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

    public func start(isProtocolProbe: Bool) {
        let generation = IOSVpnConnectionAuthority.beginGeneration()
        activeGeneration = generation
        self.logs.writeLog(log: "call start launchId=\(Self.launchId) isProtocolProbe=\(isProtocolProbe)")
        self.logs.writeLog(log: "Routing table without vpn:")
        getOrCreateManager { manager, _ in
            guard IOSVpnConnectionAuthority.isCurrent(generation) else {
                self.logs.writeLog(log: "[start] stale generation=\(generation) ignored before manager start")
                return
            }
            self.handleStart(manager: manager, isProtocolProbe: isProtocolProbe, generation: generation)
        }
    }

    private func handleStart(manager: NETunnelProviderManager?, retryAttempt: Int = 0, isProtocolProbe: Bool, generation: UInt64) {
        guard IOSVpnConnectionAuthority.isCurrent(generation) else { return }
        guard let manager = manager else {
            self.logs.writeLog(log: "Created VPNManager is nil")
            return
        }
        let status = manager.connection.status
        self.logs.writeLog(log: "[start] manager loaded status=\(statusName(status)) raw=\(status.rawValue)")
        if status == .disconnecting {
            self.suppressDisconnectedForPendingStart = true
            guard retryAttempt < Self.disconnectingStartMaxRetries else {
                self.logs.writeLog(log: "[start] Give up: connection stayed disconnecting after \(retryAttempt) retries")
                self.suppressDisconnectedForPendingStart = false
                self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
                self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
                return
            }

            let nextAttempt = retryAttempt + 1
            self.logs.writeLog(log: "[start] Connection is disconnecting; retry start after 500ms (attempt \(nextAttempt)/\(Self.disconnectingStartMaxRetries))")
            DispatchQueue.main.asyncAfter(deadline: .now() + Self.disconnectingStartRetryDelay) { [weak self] in
                guard let self else { return }
                self.getOrCreateManager { manager, _ in
                    self.handleStart(manager: manager, retryAttempt: nextAttempt, isProtocolProbe: isProtocolProbe, generation: generation)
                }
            }
            return
        }
        if status == .connecting || status == .reasserting {
            self.logs.writeLog(log: "[start] Skip: connection is transitioning (\(status.rawValue))")
            return
        }
        if status == .connected {
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
                        isProtocolProbe: isProtocolProbe,
                        generation: generation
                    )
                }
            }
            return
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
                self.reloadManagerAndStartTunnel(fallbackManager: manager, isProtocolProbe: isProtocolProbe, generation: generation)
            }
        }
    }

    private func reloadManagerAndStartTunnel(fallbackManager: NETunnelProviderManager, isProtocolProbe: Bool, generation: UInt64) {
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
                try managerToStart.connection.startVPNTunnel(options: [
                    "dobbyProtocolProbe": NSNumber(value: isProtocolProbe)
                ])
                self.logs.writeLog(log: "startVPNTunnel returned; manager.connection.status = \(self.statusName(managerToStart.connection.status)) raw=\(managerToStart.connection.status.rawValue)")
            } catch {
                self.logs.writeLog(log: "Error starting VPNTunnel \(error)")
                self.suppressDisconnectedForPendingStart = false
                self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
                self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
            }
        }
    }

    public func stop(isUserInitiated: Bool) {
        // Invalidate any asynchronous preference/retry/restart completion before stopping.
        activeGeneration = IOSVpnConnectionAuthority.beginGeneration()
        IOSVpnConnectionAuthority.publish(.disconnecting, generation: activeGeneration)
        if !isUserInitiated {
            DobbyConfigsRepositoryImpl.shared.setIsUserInitStop(isUserInitStop: false)
        }
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
        if isUserInitiated {
            DobbyConfigsRepositoryImpl.shared.setIsUserInitStop(isUserInitStop: true)
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

    private func makeManager() -> NETunnelProviderManager {
        let newVpnManager = NETunnelProviderManager()
        newVpnManager.localizedDescription = Self.dobbyName

        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.dobbyBundleIdentifier
        proto.serverAddress = "159.69.19.209:443"
        proto.providerConfiguration = [:]
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
        newVpnManager.protocolConfiguration = proto
        newVpnManager.isEnabled = true
        return newVpnManager
    }

    private func applyProtocolDefaults(manager: NETunnelProviderManager) {
        guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol else { return }
        proto.providerBundleIdentifier = Self.dobbyBundleIdentifier
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
        manager.protocolConfiguration = proto
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

}
