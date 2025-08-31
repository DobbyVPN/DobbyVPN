import NetworkExtension
import MyLibrary
import os
import app
import CommonDI
import Sentry

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    
    private var device = DeviceFacade()
    private var logs = NativeModuleHolder.logsRepository
    private var configs = configsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    private var memoryTimer: DispatchSourceTimer?

    override func startTunnel(options: [String : NSObject]?) async throws {
        logs.writeLog(log: "startTunnel in PacketTunnelProvider")
        self.startSentry()
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")
        let config = configsRepository.getOutlineKey()

        let remoteAddress = "254.1.1.1"
        let localAddress = "198.18.0.1"
        let subnetMask = "255.255.0.0"
        let dnsServers = ["1.1.1.1", "8.8.8.8"]
        
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remoteAddress)
        settings.mtu = 1200
        settings.ipv4Settings = NEIPv4Settings(addresses: [localAddress], subnetMasks: [subnetMask])
        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.dnsSettings = NEDNSSettings(servers: dnsServers)
        
        logs.writeLog(log: "Settings are ready:\n \(settings)")
        try await self.setTunnelNetworkSettings(settings)
        logs.writeLog(log: "Tunnel settings applied")
        
        device.initialize(config: config, _logs: logs)
        startCloak()
        
        
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsFromDevice()
        }
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsAndForwardToDevice()
        }
        logs.writeLog(log: "startTunnel: packet loops started")
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "Stopping tunnel with reason: \(reason)")
        stopCloak()
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        completionHandler?(messageData)
    }

    private func startReadPacketsFromDevice() {
        logs.writeLog(log: "Starting to read packets from device... \(Thread.current)")
        while true {
            logs.writeLog(log: "Reading packets from device... \(Thread.current)")
            autoreleasepool {
                let data = device.readFromDevice()
                let packets: [Data] = [data]
                let protocols: [NSNumber] = [NSNumber(value: AF_INET)]

                let success = self.packetFlow.writePackets(packets, withProtocols: protocols)
                if !success {
                    logs.writeLog(log: "Failed to write packets to the tunnel")
                }
            }
        }
        logs.writeLog(log: "Finishing #startReadPacketsFromDevice")
    }
    
    private func startReadPacketsAndForwardToDevice() {
        logs.writeLog(log: "Starting to read packets from tunnel... \(Thread.current)")
        self.packetFlow.readPackets { [weak self] (packets, protocols) in
            NativeModuleHolder.logsRepository.writeLog(log: "Read packets from tunnel: \(packets.count) protocols: \(protocols) \(Thread.current)")
            guard let self else { return }
            NativeModuleHolder.logsRepository.writeLog(log: "Continue reading packets from tunnel... \(Thread.current)")
            if !packets.isEmpty {
                forwardPacketsToDevice(packets, protocols: protocols)
            }
            startReadPacketsAndForwardToDevice()
        }
    }

    private func forwardPacketsToDevice(_ packets: [Data], protocols: [NSNumber]) {
        for packet in packets {
            device.write(data: packet)
        }
    }
    
    private func startCloak() {
        let localHost = "127.0.0.1"
        let localPort = "1984"
        logs.writeLog(log: "startCloakOutline: entering")
        if (configsRepository.getIsCloakEnabled()) {
            do {
                logs.writeLog(log: "startCloakOutline: about to start Cloak client")
                Cloak_outlineStartCloakClient(localHost, localPort, configsRepository.getCloakConfig(), false)
                logs.writeLog(log: "startCloakOutline: Cloak client started successfully")
            } catch {
                logs.writeLog(log: "startCloakOutline: Cloak client failed with error: \(error)")
            }
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled in configs")
        }
    }

    
    private func stopCloak() {
        if (configsRepository.getIsCloakEnabled()) {
            logs.writeLog(log: "stopCloakOutline")
            Cloak_outlineStopCloakClient()
        }
    }
    
    func startSentry() {
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
            scope.setTag(value: self.launchId, key: "launch_id")
        }
        
        SentrySDK.capture(message: "Sentry started, launch_id: \(self.launchId)")
    }
}

class DeviceFacade {
    private var device: Cloak_outlineOutlineDevice? = nil
    private var logs: LogsRepository? = nil

    func initialize(config: String, _logs: LogsRepository) {
        var err: NSErrorPointer = nil
        device = Cloak_outlineNewOutlineDevice(config, err)
        logs = _logs
        logs?.writeLog(log: "Device initiaization finished (error is:\(err == nil))")
    }
    
    func write(data: Data) {
        do {
            var ret0_: Int = 0
            try device?.write(data, ret0_: &ret0_)
            logs?.writeLog(log: "DeviceFacade: wrote \(data.count) bytes")
        } catch let error {
            logs?.writeLog(log: "DeviceFacade write error: \(error)")
        }
    }

    func readFromDevice() -> Data {
        do {
            let data = try device?.read()
            logs?.writeLog(log: "DeviceFacade: read \(data?.count ?? 0) bytes")
            return data!
        } catch let error {
            logs?.writeLog(log: "DeviceFacade read error: \(error)")
            return Data()
        }
    }

}
