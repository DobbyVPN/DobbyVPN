import app
import NetworkExtension

class VpnManagerImpl: VpnManager {
    
    private var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    private var dobbyName = "DobbyVPN"
    
    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    private var logger: LogsRepository
    
    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    
    init(
        connectionRepository: ConnectionStateRepository,
        logsRepository: LogsRepository
    ) {
        self.connectionRepository = connectionRepository
        self.logger = logsRepository

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
        
        observer = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: nil) { [weak self] notification in
            guard let connection = notification.object as? NEVPNConnection else { return }
            self?.state = connection.status
            if (connection.status == .connected) {
                if (self?.vpnManager == nil) {
                    self?.getOrCreateManager { (manager, error) in
                        self?.vpnManager = manager
                    }
                }
                connectionRepository.tryUpdate(isConnected: true)
            } else {
                connectionRepository.tryUpdate(isConnected: false)
            }
        }
    }
    
    deinit {
        if let observer {
            NotificationCenter.default.removeObserver(observer)
        }
    }
    
    func start() {
        getOrCreateManager { (manager, error) in
            guard let manager = manager else {
                self.log(message: "Created VPNManager is nil")
                return
            }
            self.vpnManager = manager
            self.vpnManager?.isEnabled = true
            do {
                self.log(message: "starting tunnel. Current status is \(manager.connection.status)")
                // https://stackoverflow.com/a/47569982/934719 - TODO fix
                try manager.connection.startVPNTunnel()
            } catch {
                self.log(message: "Error staring VPNTunnel \(error)")
            }
        }
    }

    func stop() {
        guard state == .connected else { return }
        self.log(message: "Stopping vpnManager: \(vpnManager)")
        vpnManager?.connection.stopVPNTunnel()
    }

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { (managers, error) in
            if let existingManager = managers?.first(where: { $0.localizedDescription == self.dobbyName }) {
                self.vpnManager = existingManager
                self.log(message: "Existing VPN Manager found.")
                completion(existingManager, nil)
            } else {
                self.log(message: "Existing VPN Manager not found.")
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
    
    private func log(message: String) {
        logger.writeLog(log: message)
        NSLog(message)
    }
}
