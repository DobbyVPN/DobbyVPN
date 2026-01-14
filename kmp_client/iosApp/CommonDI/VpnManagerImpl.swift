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
        getOrCreateManager { (manager, error) in
            if (manager?.connection.status == .connected) {
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

            switch connection.status {
            case .connected:
                self.logs.writeLog(log: "VPN connected")

            case .disconnected:
                self.logs.writeLog(log: "VPN disconnected")
                
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
    
    public func start() {
        self.logs.writeLog(log: "call start")
        HealthCheckImpl.shared.isConnected()
        self.logs.writeLog(log: "Routing table without vpn:")
        getOrCreateManager { (manager, error) in
            guard let manager = manager else {
                self.logs.writeLog(log: "Created VPNManager is nil")
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
                        self.logs.writeLog(log: "starting tunnel !\(manager.connection.status)")
                        try manager.connection.startVPNTunnel()
                        self.logs.writeLog(log: "Tunnel was started! manager.connection.status = \(manager.connection.status)")
                    } catch {
                        self.logs.writeLog(log: "Error starting VPNTunnel \(error)")
                    }
                }
            }
        }
    }

    public func stop() {
        self.logs.writeLog(log: "Actually vpnManager is \(vpnManager)")
        self.logs.writeLog(log: "[stop] User initiated stopVPNTunnel()")
        vpnManager?.connection.stopVPNTunnel()
        self.logs.writeLog(log: "[stop] stopVPNTunnel() called, waiting for .disconnecting")
    }

    private func getOrCreateManager(completion: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] (managers, error) in
            guard let self else { return }
            
            if let existingManager = managers?.first(where: { $0.localizedDescription == VpnManagerImpl.dobbyName }) {
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
        newVpnManager.localizedDescription = VpnManagerImpl.dobbyName
        
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = VpnManagerImpl.dobbyBundleIdentifier
        proto.serverAddress = "159.69.19.209:443"
        proto.providerConfiguration = [:]
        proto.includeAllNetworks = true
        if #available(iOS 17.4, *) {
            proto.excludeDeviceCommunication = false
        }
        newVpnManager.protocolConfiguration = proto
        newVpnManager.isEnabled = true
        return newVpnManager
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
