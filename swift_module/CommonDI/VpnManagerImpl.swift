import app
import NetworkExtension
import Sentry
import Foundation
import SystemConfiguration
import MyLibrary

public class VpnManagerImpl: VpnManager {
    private static let launchId = UUID().uuidString
    private var logs = NativeModuleHolder.logsRepository

    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"

    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository

    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid

    init(connectionRepository: ConnectionStateRepository) {
        let path = LogsRepository_iosKt.provideLogFilePath().normalized().description()
        logs.writeLog(log: "Start go logger init path = \(path)")
        Cloak_outlineInitLogger(path)
        logs.writeLog(log: "Finish go logger init")

//        VpnManagerImpl.startSentry()
        self.connectionRepository = connectionRepository
        getOrCreateManager { [weak self] manager, _ in
            guard let self else { return }
            if manager?.connection.status == .connected {
                self.state = manager?.connection.status ?? .invalid
                connectionRepository.tryUpdateVpnStarted(isStarted: true)
                self.vpnManager = manager
            } else {
                self.state = manager?.connection.status ?? .invalid
                connectionRepository.tryUpdateVpnStarted(isStarted: false)
            }
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
            self.logs.writeLog(log: "[NEVPNStatusDidChange] \(self.statusName(previous))(\(previous.rawValue)) -> \(self.statusName(connection.status))(\(connection.status.rawValue))")

            switch connection.status {
            case .connected:
                self.connectionRepository.tryUpdateVpnStarted(isStarted: true)
                self.connectionRepository.tryUpdateStatus(isConnected: true)
                self.logs.writeLog(log: "VPN connected")

            case .disconnected:
                self.connectionRepository.tryUpdateVpnStarted(isStarted: false)
                self.connectionRepository.tryUpdateStatus(isConnected: false)
                self.logs.writeLog(log: "VPN disconnected")

            case .connecting:
                self.logs.writeLog(log: "VPN is connecting…")

            case .reasserting:
                self.logs.writeLog(log: "VPN is reasserting…")

            case .disconnecting:
                self.connectionRepository.tryUpdateStatus(isConnected: false)
                self.logs.writeLog(log: "VPN is disconnecting…")

            case .invalid:
                self.connectionRepository.tryUpdateVpnStarted(isStarted: false)
                self.connectionRepository.tryUpdateStatus(isConnected: false)
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
        self.logs.writeLog(log: "call start launchId=\(Self.launchId)")
        self.logs.writeLog(log: "Routing table without vpn:")
        getOrCreateManager { manager, _ in
            self.handleStart(manager: manager)
        }
    }

    private func handleStart(manager: NETunnelProviderManager?, retryAttempt: Int = 0) {
        guard let manager = manager else {
            self.logs.writeLog(log: "Created VPNManager is nil")
            return
        }
        let status = manager.connection.status
        self.logs.writeLog(log: "[start] manager loaded status=\(statusName(status)) raw=\(status.rawValue)")
        if status == .disconnecting {
            let maxRetries = 30
            guard retryAttempt < maxRetries else {
                self.logs.writeLog(log: "[start] Give up: connection stayed disconnecting after \(retryAttempt) retries")
                self.connectionRepository.tryUpdateVpnStarted(isStarted: false)
                self.connectionRepository.tryUpdateStatus(isConnected: false)
                return
            }

            let nextAttempt = retryAttempt + 1
            self.logs.writeLog(log: "[start] Connection is disconnecting; retry start after 500ms (attempt \(nextAttempt)/\(maxRetries))")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                guard let self else { return }
                self.getOrCreateManager { manager, _ in
                    self.handleStart(manager: manager, retryAttempt: nextAttempt)
                }
            }
            return
        }
        if status == .connecting || status == .reasserting {
            self.logs.writeLog(log: "[start] Skip: connection is transitioning (\(status.rawValue))")
            return
        }
        if status == .connected {
            self.logs.writeLog(log: "[start] Skip: already connected")
            return
        }
        if let proto = manager.protocolConfiguration as? NETunnelProviderProtocol {
            let address = proto.serverAddress ?? "nil"
            self.logs.writeLog(log: "VPN Manager serverAddress = \(address)")
        }
        self.vpnManager = manager
        self.vpnManager?.isEnabled = true
        manager.saveToPreferences { saveError in
            if let saveError = saveError {
                self.logs.writeLog(log: "Failed to save VPN configuration: \(saveError)")
            } else {
                self.logs.writeLog(log: "VPN configuration saved successfully!")
                do {
                    self.logs.writeLog(log: "self.vpnManager = \(manager)")
                    self.logs.writeLog(log: "starting tunnel status=\(self.statusName(manager.connection.status)) raw=\(manager.connection.status.rawValue)")
                    try manager.connection.startVPNTunnel()
                    self.logs.writeLog(log: "startVPNTunnel returned; manager.connection.status = \(self.statusName(manager.connection.status)) raw=\(manager.connection.status.rawValue)")
                } catch {
                    self.logs.writeLog(log: "Error starting VPNTunnel \(error)")
                    self.connectionRepository.tryUpdateVpnStarted(isStarted: false)
                    self.connectionRepository.tryUpdateStatus(isConnected: false)
                }
            }
        }
    }

    public func stop() {
        self.logs.writeLog(log: "Actually vpnManager is \(String(describing: vpnManager))")
        guard let manager = vpnManager else {
            self.logs.writeLog(log: "[stop] Skip: vpnManager is nil")
            return
        }
        let status = manager.connection.status
        self.logs.writeLog(log: "[stop] stopVPNTunnel requested status=\(statusName(status)) raw=\(status.rawValue)")
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

//    static func startSentry() {
//        SentrySDK.start { options in
//            options.dsn = "https://1ebacdcb98b5a261d06aeb0216cdafc5@o4509873345265664.ingest.de.sentry.io/4509927590068304"
//            options.debug = true
//
//            options.sendDefaultPii = true
//
//            options.tracesSampleRate = 1.0
//            options.configureProfiling = {
//                $0.sessionSampleRate = 1.0
//                $0.lifecycle = .trace
//            }
//
//            options.experimental.enableLogs = true
//        }
//
//        SentrySDK.configureScope { scope in
//            scope.setTag(value: VpnManagerImpl.launchId, key: "launch_id")
//        }
//
//        SentrySDK.capture(message: "Sentry started, launch_id: \(VpnManagerImpl.launchId)")
//    }

}
