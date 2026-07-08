import app
import NetworkExtension
import Sentry
import Foundation
import SystemConfiguration
import MyLibrary

public class VpnManagerImpl: VpnManager {
    private static let launchId = UUID().uuidString
    private static let disconnectingStartRetryDelay: TimeInterval = 0.5
    private static let disconnectingStartMaxRetries = 120
    private static let protocolRestartResponseTimeout: TimeInterval = 15
    private var logs = NativeModuleHolder.logsRepository

    public static var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    public static var dobbyName = "Dobby_VPN_4"

    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    private var suppressDisconnectedForPendingStart = false

    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    public let supportsVpnNetworkReadySignal: Bool = true

    init(connectionRepository: ConnectionStateRepository) {
//        VpnManagerImpl.startSentry()
        self.connectionRepository = connectionRepository
        getOrCreateManager { [weak self] manager, _ in
            guard let self else { return }
            if manager?.connection.status == .connected {
                self.state = manager?.connection.status ?? .invalid
                self.vpnManager = manager
            } else {
                self.state = manager?.connection.status ?? .invalid
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
        self.logs.writeLog(log: "call start launchId=\(Self.launchId) isProtocolProbe=\(isProtocolProbe)")
        self.logs.writeLog(log: "Routing table without vpn:")
        getOrCreateManager { manager, _ in
            self.handleStart(manager: manager, isProtocolProbe: isProtocolProbe)
        }
    }

    private func handleStart(manager: NETunnelProviderManager?, retryAttempt: Int = 0, isProtocolProbe: Bool) {
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
                    self.handleStart(manager: manager, retryAttempt: nextAttempt, isProtocolProbe: isProtocolProbe)
                }
            }
            return
        }
        if status == .connecting || status == .reasserting {
            self.logs.writeLog(log: "[start] Skip: connection is transitioning (\(status.rawValue))")
            return
        }
        if status == .connected {
            self.logs.writeLog(log: "[start] Tunnel already connected; requesting in-place protocol restart")
            self.suppressDisconnectedForPendingStart = false
            self.restartActiveProtocolInProvider(manager: manager, isProtocolProbe: isProtocolProbe)
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
                self.reloadManagerAndStartTunnel(fallbackManager: manager, isProtocolProbe: isProtocolProbe)
            }
        }
    }

    private func reloadManagerAndStartTunnel(fallbackManager: NETunnelProviderManager, isProtocolProbe: Bool) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] managers, loadError in
            guard let self else { return }
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

    private func restartActiveProtocolInProvider(manager: NETunnelProviderManager, isProtocolProbe: Bool) {
        guard let session = manager.connection as? NETunnelProviderSession else {
            self.logs.writeLog(log: "[start] In-place protocol restart failed: connection is not NETunnelProviderSession")
            self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
            self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
            return
        }

        let messageText = isProtocolProbe ? "restartActiveProtocol:probe" : "restartActiveProtocol"
        guard let message = messageText.data(using: .utf8) else {
            self.logs.writeLog(log: "[start] In-place protocol restart failed: message encoding failed")
            self.connectionRepository.tryUpdateVpnNetworkReady(isReady: false)
            self.connectionRepository.tryUpdateServiceStarted(isStarted: false)
            return
        }

        let logs = self.logs
        let connectionRepository = self.connectionRepository
        let completionLock = NSLock()
        var completed = false
        func finish(isStarted: Bool, source: String) {
            completionLock.lock()
            if completed {
                completionLock.unlock()
                logs.writeLog(log: "[start] In-place protocol restart late result ignored source=\(source) isStarted=\(isStarted)")
                return
            }
            completed = true
            completionLock.unlock()
            connectionRepository.tryUpdateVpnNetworkReady(isReady: isStarted)
            connectionRepository.tryUpdateServiceStarted(isStarted: isStarted)
        }

        let timeoutWorkItem = DispatchWorkItem {
            logs.writeLog(
                log: "[start] In-place protocol restart timed out after \(Self.protocolRestartResponseTimeout)s"
            )
            finish(isStarted: false, source: "timeout")
        }
        DispatchQueue.main.asyncAfter(
            deadline: .now() + Self.protocolRestartResponseTimeout,
            execute: timeoutWorkItem
        )

        do {
            try session.sendProviderMessage(message) { [weak self] response in
                guard let self else { return }
                timeoutWorkItem.cancel()
                let responseText = response.flatMap { String(data: $0, encoding: .utf8) } ?? "(nil)"
                let ok = responseText == "ok"
                self.logs.writeLog(log: "[start] In-place protocol restart response=\(responseText) ok=\(ok)")
                finish(isStarted: ok, source: "response")
            }
        } catch {
            timeoutWorkItem.cancel()
            self.logs.writeLog(log: "[start] In-place protocol restart send failed: \(error.localizedDescription)")
            finish(isStarted: false, source: "sendError")
        }
    }

    public func stop(isUserInitiated: Bool) {
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
