import app
import NetworkExtension
import Sentry

class VpnManagerImpl: VpnManager {
    private static let launchId = UUID().uuidString
    private var logs = NativeModuleHolder.logsRepository
    private var lastRestartDate: Date?
    
    private var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    private var dobbyName = "DobbyVPN"
    
    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    
    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    
    private var isUserInitiatedStop = true
    
    init(connectionRepository: ConnectionStateRepository) {
        VpnManagerImpl.startSentry()
        self.connectionRepository = connectionRepository

        getOrCreateManager { (manager, error) in
            if (manager?.connection.status == .connected) {
                self.state = manager?.connection.status ?? .invalid
                connectionRepository.tryUpdate(isConnected: true)
                self.vpnManager = manager
            } else {
                self.state = manager?.connection.status ?? .invalid
                connectionRepository.tryUpdate(isConnected: false)
            }
        }

        observer = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: nil,
            queue: nil
        ) { [weak self] notification in
            guard let self,
                  let connection = notification.object as? NEVPNConnection else { return }

            state = connection.status
            switch connection.status {
            case .connected:
                self.logs.writeLog(log: "VPN connected. Update ui and isUserInitiatedStop and put manager")
                isUserInitiatedStop = false
                if self.vpnManager == nil {
                    getOrCreateManager { manager, _ in
                        self.vpnManager = manager
                    }
                }
                connectionRepository.tryUpdate(isConnected: true)

            case .disconnected:
                connectionRepository.tryUpdate(isConnected: false)

                if !self.isUserInitiatedStop {
                    self.logs.writeLog(
                            log: "VPN disconnected. Reason? state=\(connection.status.rawValue))"
                        )
                    let now = Date()
                    if let lastRestart = self.lastRestartDate,
                       now.timeIntervalSince(lastRestart) < 60 {
                        // Уже был рестарт в течение последней минуты → не перезапускаем
                        self.logs.writeLog(log: "VPN disconnected unexpectedly, but restart suppressed (too frequent)")
                    } else {
                        self.logs.writeLog(log: "VPN disconnected unexpectedly, restarting...")
                        self.lastRestartDate = now
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                            self.start()
                        }
                    }
                }
                
            case .connecting:
                self.logs.writeLog(log: "VPN is connecting…")

            case .reasserting:
                self.logs.writeLog(log: "VPN is reasserting…")

            case .disconnecting:
                self.logs.writeLog(log: "VPN is disconnecting…")

            case .invalid:
                self.logs.writeLog(log: "VPN status is invalid")

            @unknown default:
                self.logs.writeLog(log: "VPN status unknown: \(connection.status.rawValue)")
                break
            }
        }
    }
    
    deinit {
        if let observer {
            NotificationCenter.default.removeObserver(observer)
        }
    }
    
    func start() {
        self.logs.writeLog(log: "call start")
        getOrCreateManager { (manager, error) in
            guard let manager = manager else {
                self.logs.writeLog(log: "Created VPNManager is nil")
                return
            }
            if let proto = manager.protocolConfiguration as? NETunnelProviderProtocol {
                let address = proto.serverAddress ?? "nil"
                self.logs.writeLog(log: "VPN Manager serverAddress = \(address)")
            }
            self.logs.writeLog(log: "self.vpnManager = \(manager)")
            self.vpnManager = manager
            self.vpnManager?.isEnabled = true
            do {
                self.logs.writeLog(log: "starting tunnel !\(manager.connection.status)")
                // https://stackoverflow.com/a/47569982/934719 - TODO fix
                try manager.connection.startVPNTunnel()
                self.logs.writeLog(log: "Tunnel was started!\(manager.connection.status)")
            } catch {
                self.logs.writeLog(log: "Error staring VPNTunnel \(error)")
            }
        }
    }

    func stop() {
        guard state == .connected else { return }
        isUserInitiatedStop = true
        self.logs.writeLog(log: "Actually vpnManager is \(vpnManager)")
        vpnManager?.connection.stopVPNTunnel()
    }

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] (managers, error) in
            guard let self else { return }
            
            if let existingManager = managers?.first(where: { $0.localizedDescription == self.dobbyName }) {
                vpnManager = existingManager
                self.logs.writeLog(log: "Existing manager found.")
                completion(existingManager, nil)
            } else {
                self.logs.writeLog(log: "Existing manager not found.")
                self.vpnManager = self.makeManager()
                self.vpnManager?.saveToPreferences { (error) in
                    completion(self.vpnManager, error)
                }
            }
        }
    }

    private func makeManager() -> NETunnelProviderManager {
        let newVpnManager = NETunnelProviderManager()
        newVpnManager.localizedDescription = dobbyName
        
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = dobbyBundleIdentifier
        proto.serverAddress = "127.0.0.1:4009"
        proto.providerConfiguration = [:]
        newVpnManager.protocolConfiguration = proto
        newVpnManager.isEnabled = true
        return newVpnManager
    }
    
    
    static func startSentry() {
        SentrySDK.start { options in
            options.dsn = "https://1ebacdcb98b5a261d06aeb0216cdafc5@o4509873345265664.ingest.de.sentry.io/4509927590068304"
            options.debug = true

            options.sendDefaultPii = true

            options.tracesSampleRate = 1.0
            options.configureProfiling = {
                $0.sessionSampleRate = 1.0
                $0.lifecycle = .trace
            }
            
            options.experimental.enableLogs = true
        }
        
        SentrySDK.configureScope { scope in
            scope.setTag(value: VpnManagerImpl.launchId, key: "launch_id")
        }
        
        SentrySDK.capture(message: "Sentry started, launch_id: \(VpnManagerImpl.launchId)")
    }
}
