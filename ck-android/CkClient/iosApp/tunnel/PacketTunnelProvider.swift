import NetworkExtension
import os
import app
import CommonDI

class PacketTunnelProvider: NEPacketTunnelProvider {
    
    private var device = DeviceFacade()
    
    private var logs = LocalLogsRepository()

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
        log(message: "Tunnel settings applied")

        // Initialize the device
        device.initialize(config: config)
        
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsFromDevice()
        }
        DispatchQueue.global().async { [weak self] in
            self?.startReadPacketsAndForwardToDevice()
        }
    }
    
    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        log(message: "Stopping tunnel with reason: \(reason)")
        completionHandler()
    }
    
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        completionHandler?(messageData)
    }

    private func startReadPacketsFromDevice() {
        log(message: "Starting to read packets from device...")
        while true {
            let data = device.readFromDevice()
            let packets: [Data] = [data]
            let protocols: [NSNumber] = [NSNumber(value: AF_INET)] // IPv4

            let success = self.packetFlow.writePackets(packets, withProtocols: protocols)
            NSLog("self.packetFlow.writePackets - success")
            if !success {
                log(message: "Failed to write packets to the tunnel")
            }
        }
        log(message: "Finishing #startReadPacketsFromDevice")
    }
    
    private func startReadPacketsAndForwardToDevice() {
        self.packetFlow.readPackets { [weak self] (packets, protocols) in
            guard let self = self else { return }
            if !packets.isEmpty {
                self.forwardPacketsToDevice(packets, protocols: protocols)
            }
            self.startReadPacketsAndForwardToDevice()
        }
    }

    private func forwardPacketsToDevice(_ packets: [Data], protocols: [NSNumber]) {
        for packet in packets {
            NSLog("writing packet \(packet) to device")
            device.write(data: packet)
        }
    }
    
    private func log(message: String) {
        logs.writeLog(log: message)
        NSLog(message)
    }
}
