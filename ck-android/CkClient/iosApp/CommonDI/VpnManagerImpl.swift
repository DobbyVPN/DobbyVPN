import app
import NetworkExtension

class VpnManagerImpl: VpnManager {
    
    private var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    private var dobbyName = "DobbyVPN"
    
    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    
    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    
    init(connectionRepository: ConnectionStateRepository) {
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
        
        observer = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: nil) { [weak self] notification in
            guard let self, let connection = notification.object as? NEVPNConnection else { return }
            state = connection.status
            if (connection.status == .connected) {
                if (self.vpnManager == nil) {
                    getOrCreateManager { (manager, _) in
                        self.vpnManager = manager
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
                NSLog("Created VPNManager is nil")
                return
            }
            print("self.vpnManager = \(manager)")
            self.vpnManager = manager
            self.vpnManager?.isEnabled = true
            do {
                print("starting tunnel !\(manager.connection.status)")
                // https://stackoverflow.com/a/47569982/934719 - TODO fix
                try manager.connection.startVPNTunnel()
            } catch {
                NSLog("Error staring VPNTunnel \(error)")
            }
        }
    }

    func stop() {
        guard state == .connected else { return }
        NSLog("Actually vpnManager is \(vpnManager)")
        vpnManager?.connection.stopVPNTunnel()
    }

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] (managers, error) in
            guard let self else { return }
            
            if let existingManager = managers?.first(where: { $0.localizedDescription == self.dobbyName }) {
                vpnManager = existingManager
                NSLog("Existing manager found.")
                completion(existingManager, nil)
            } else {
                NSLog("Existing manager not found.")
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
}
