import NetworkExtension
import MyLibrary
import os
import app
import CommonDI

class PacketTunnelProvider: NEPacketTunnelProvider {
    
    private var device = DeviceFacade()
    private var logs = NativeModuleHolder.logsRepository
    private var configs = configsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!

    override func startTunnel(options: [String : NSObject]?) async throws {
        let config = configsRepository.getOutlineKey()

        let remoteAddress = "254.1.1.1"
        let localAddress = "198.18.0.1"
        let subnetMask = "255.255.0.0"
        let dnsServers = ["1.1.1.1", "8.8.8.8"]
        
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remoteAddress)
        settings.mtu = 1500
        settings.ipv4Settings = NEIPv4Settings(addresses: [localAddress], subnetMasks: [subnetMask])
        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.dnsSettings = NEDNSSettings(servers: dnsServers)

        try await self.setTunnelNetworkSettings(settings)
        logs.writeLog(log: "Tunnel settings applied")

        // Initialize the device
        device.initialize(config: config)
        startCloak()
        
        
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsFromDevice()
        }
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsAndForwardToDevice()
        }
    }
    
    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "Stopping tunnel with reason: \(reason)")
        completionHandler()
    }
    
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        completionHandler?(messageData)
    }

    private func startReadPacketsFromDevice() {
        logs.writeLog(log: "Starting to read packets from device...")
        while true {
            autoreleasepool {
                let data = device.readFromDevice()
                let packets: [Data] = [data]
                let protocols: [NSNumber] = [NSNumber(value: AF_INET)] // IPv4

                let success = self.packetFlow.writePackets(packets, withProtocols: protocols)
                NSLog("self.packetFlow.writePackets - succ")
                if !success {
                    NSLog("Failed to write packets to the tunnel")
                }
            }
        }
        logs.writeLog(log: "Finishing #startReadPacketsFromDevice")
    }
    
    private func startReadPacketsAndForwardToDevice() {
        self.packetFlow.readPackets { [weak self] (packets, protocols) in
            guard let self else { return }
            if !packets.isEmpty {
                forwardPacketsToDevice(packets, protocols: protocols)
            }
            startReadPacketsAndForwardToDevice()
        }
    }

    private func forwardPacketsToDevice(_ packets: [Data], protocols: [NSNumber]) {
        for packet in packets {
            NSLog("writing packet \(packet) to device")
            device.write(data: packet)
        }
    }
    
    private func startCloak() {
        var apiKey = configsRepository.getOutlineKey()
        var localHost = "127.0.0.1"
        var localPort = "1984"
        logs.writeLog(log: "startCloakOutline with key: $apiKey")
        if (configsRepository.getIsCloakEnabled()) {
            Cloak_outlineStartCloakClient(localHost, localPort, configsRepository.getCloakConfig(), false)
        }
    }
    
    private func stopCloak() {
        Cloak_outlineStopCloakClient()
    }
}

class DeviceFacade {

    private var device: Cloak_outlineOutlineDevice? = nil

    func initialize(config: String) {
        device = Cloak_outlineOutlineDevice(config)
        NSLog("Device initiaization finished")
    }
    
    func write(data: Data) {
        do {
            var ret0_: Int = 0
            try device?.write(data, ret0_: &ret0_)
        } catch let error {
            NSLog("error is \(error)")
        }
    }
    
    func readFromDevice() -> Data {
        do {
            let data = try device?.read()
            return data!
        } catch let error {
            NSLog("error is \(error)")
            return Data()
        }
    }
    
//    func close() {
//        do {
//            try device?.close()
//        } catch {}
//        device = nil
//    }
}
