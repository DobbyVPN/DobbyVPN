import app
import NetworkExtension
import Sentry
import Foundation
import SystemConfiguration

public class VpnManagerImpl: VpnManager {
    private static let launchId = UUID().uuidString
    private var logs = NativeModuleHolder.logsRepository
    
    private let maxInitialRetries = 3
    private let maxRuntimeRetries = 3
    private let retryInterval: TimeInterval = 15
    
    private var initialRetryCount = 0
    private var runtimeRetryCount = 0
    
    private var dobbyBundleIdentifier = "vpn.dobby.app.tunnel"
    private var dobbyName = "Dobby_VPN_4"
    
    private var vpnManager: NETunnelProviderManager?
    private var connectionRepository: ConnectionStateRepository
    
    private var observer: NSObjectProtocol?
    @Published private(set) var state: NEVPNStatus = .invalid
    
    public static var isUserInitiatedStop = true
    
    private var internetFailureCount = 0
    private let maxInternetFailures = 3
    private var intertenConnectionTimer: DispatchSourceTimer?
    private var xpcHeartbeatTimer: DispatchSourceTimer?
    private let heartbeatInterval: TimeInterval = 10

    @available(iOS 9.0, *)
    private func startXPCHeartbeat(session: NETunnelProviderSession) {
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global())
        timer.schedule(deadline: .now() + 10, repeating: 10)

        timer.setEventHandler { [weak self] in
            guard let self else { return }

            do {
                try session.sendProviderMessage("heartbeat".data(using: .utf8)!) { response in
                    if let response = response,
                       let str = String(data: response, encoding: .utf8),
                       str == "alive" {
                        self.logs.writeLog(log: "XPC heartbeat: Tunnel alive")
                    } else {
                        self.logs.writeLog(log: "XPC heartbeat: Tunnel NOT responding")
                        if !VpnManagerImpl.isUserInitiatedStop {
                            self.handleRuntimeRetry()
                        }
                    }
                }
            } catch {
                self.logs.writeLog(log: "XPC heartbeat error: \(error)")
            }
            
        }

        timer.resume()
        self.xpcHeartbeatTimer = timer
    }

    private func stopXPCHeartbeat() {
        xpcHeartbeatTimer?.cancel()
        xpcHeartbeatTimer = nil
    }

    
    init(connectionRepository: ConnectionStateRepository) {
//        VpnManagerImpl.startSentry()
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
                VpnManagerImpl.isUserInitiatedStop = false
                self.initialRetryCount = 3
                if self.vpnManager == nil {
                    getOrCreateManager { manager, _ in
                        self.vpnManager = manager
                    }
                }
                connectionRepository.tryUpdate(isConnected: true)

            case .disconnected:
                self.logs.writeLog(log: "VPN disconnected.\(connection.status.rawValue)")
                connectionRepository.tryUpdate(isConnected: false)
                self.logs.writeLog(log: "isUserInitiatedStop = \(VpnManagerImpl.isUserInitiatedStop)")

                if !VpnManagerImpl.isUserInitiatedStop {
                    if self.initialRetryCount < self.maxInitialRetries {
                        self.handleInitialRetry()
                    } else if self.runtimeRetryCount < self.maxRuntimeRetries {
                        self.handleRuntimeRetry()
                    } else {
                        self.logs.writeLog(log: "Max retry count reached (init=\(self.initialRetryCount), runtime=\(self.runtimeRetryCount)). Stop auto-restart.")
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
    
    private func handleInitialRetry() {
        initialRetryCount += 1
        logs.writeLog(log: "Initial connection failed. Retry \(initialRetryCount)/\(maxInitialRetries) in \(Int(retryInterval))s")

        DispatchQueue.main.asyncAfter(deadline: .now() + retryInterval) {
            self.logs.writeLog(log: "Attempting initial reconnect (\(self.initialRetryCount)/\(self.maxInitialRetries))...")
            self.start()
        }
    }

    private func handleRuntimeRetry() {
        runtimeRetryCount += 1
        logs.writeLog(log: "VPN disconnected unexpectedly. Retry \(runtimeRetryCount)/\(maxRuntimeRetries) in \(Int(retryInterval))s")

        DispatchQueue.main.asyncAfter(deadline: .now() + retryInterval) {
            self.logs.writeLog(log: "Attempting runtime reconnect (\(self.runtimeRetryCount)/\(self.maxRuntimeRetries))...")
            self.start()
        }
    }
    
    public func start() {
        self.logs.writeLog(log: "call start")
        do {
            HealthCheck.shared.fullCheckUp()
        } catch {
            logs.writeLog(log: "[startTunnel] HealthCheck error: \(error.localizedDescription)")
        }
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
            VpnManagerImpl.isUserInitiatedStop = false
            manager.saveToPreferences { saveError in
                if let saveError = saveError {
                    self.logs.writeLog(log: "Failed to save VPN configuration: \(saveError)")
                } else {
                    self.logs.writeLog(log: "VPN configuration saved successfully!")
                    do {
                        self.logs.writeLog(log: "self.vpnManager = \(manager)")
                        self.logs.writeLog(log: "starting tunnel !\(manager.connection.status)")
                        try manager.connection.startVPNTunnel()
                        self.logs.writeLog(log: "Tunnel was started! changed connection.status")
                        self.logs.writeLog(log: "Tunnel was started! manager.connection.status = \(manager.connection.status)")
                        self.startInternetCheck()
                    } catch {
                        self.logs.writeLog(log: "Error starting VPNTunnel \(error)")
                    }
                }
            }
            if #available(iOS 9.0, *) {
                if let session = manager.connection as? NETunnelProviderSession {
                    self.startXPCHeartbeat(session: session)
                } else {
                    self.logs.writeLog(log: "Could not cast connection to NETunnelProviderSession")
                }
            }
        }
    }

    public func stop() {
        guard state == .connected else { return }
        VpnManagerImpl.isUserInitiatedStop = true
        self.logs.writeLog(log: "Actually vpnManager is \(vpnManager)")
        self.logs.writeLog(log: "[stop] User initiated stopVPNTunnel()")
        self.stopXPCHeartbeat()
        vpnManager?.connection.stopVPNTunnel()
        self.logs.writeLog(log: "[stop] stopVPNTunnel() called, waiting for .disconnecting")
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
    
    func startInternetCheck() {
        internetFailureCount = 0
        let timer = DispatchSource.makeTimerSource(
            queue: DispatchQueue.global(qos: .background)
        )
        timer.schedule(deadline: .now() + 5, repeating: 5)

        timer.setEventHandler { [weak self] in
            guard let self else { return }

            HealthCheck.shared.httpGetBody(
                urlString: "https://google.com/gen_204"
            ) { success, body, errorMessage in

                if success {
                    self.internetFailureCount = 0
                    self.logs.writeLog(
                        log: "[InternetCheck] Internet check - OK"
                    )
                    return
                }

                self.internetFailureCount += 1
                self.logs.writeLog(
                    log: "[InternetCheck] Internet check - FAIL \(self.internetFailureCount)/\(self.maxInternetFailures). Error: \(errorMessage ?? "Unknown")"
                )

                guard self.internetFailureCount >= self.maxInternetFailures else {
                    return
                }

                self.logs.writeLog(
                    log: "[InternetCheck] Internet lost. Stopping VPN"
                )

                self.intertenConnectionTimer?.cancel()
                self.intertenConnectionTimer = nil

                DispatchQueue.main.async {
                    VpnManagerImpl.isUserInitiatedStop = false
                    self.stop()
                }
            }
        }

        timer.resume()
        self.intertenConnectionTimer = timer
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
