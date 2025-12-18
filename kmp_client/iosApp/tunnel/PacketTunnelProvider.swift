import NetworkExtension
import MyLibrary
import os
import app
import CommonDI
import Sentry
import Foundation
import SystemConfiguration
import Network

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    
    private var device = DeviceFacade()
    private var logs = NativeModuleHolder.logsRepository
    private var configs = configsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    
    private var packetContinuation: AsyncStream<(Data, NSNumber)>.Continuation!
    private lazy var packetStream: AsyncStream<(Data, NSNumber)> = {
        AsyncStream<(Data, NSNumber)>(bufferingPolicy: .bufferingOldest(20)) { continuation in
            self.packetContinuation = continuation
        }
    }()
    
    private var memoryTimer: DispatchSourceTimer?
    private var healthTimer: DispatchSourceTimer?
    
    func startMemoryLogging() {
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global(qos: .background))
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [weak self] in
            self?.reportMemoryUsageMB()
        }
        timer.resume()
        memoryTimer = timer
    }
    
    func reportMemoryUsageMB() {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.stride / MemoryLayout<natural_t>.stride)

        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }

        if result == KERN_SUCCESS {
            let usedBytes = info.phys_footprint
            let usedMB = Double(usedBytes) / 1024.0 / 1024.0
            logs.writeLog(log: "[Memory] VPN use: \(String(format: "%.2f", usedMB)) MB")
        } else {
            logs.writeLog(log: "[Memory] unable to get info")
        }
    }
    
    override func startTunnel(options: [String : NSObject]?) async throws {
        logs.writeLog(log: "startTunnel in PacketTunnelProvider, thread: \(Thread.current)")
        
        do {
            HealthCheck.shared.fullCheckUp()
        } catch {
            logs.writeLog(log: "[startTunnel] HealthCheck error: \(error.localizedDescription)")
        }

        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")
        let methodPassword = configsRepository.getMethodPasswordOutline()
        let serverPort = configsRepository.getServerPortOutline()
        let config = buildOutlineConfig(methodPassword: methodPassword, serverPort: serverPort)
        let cloakConfig = configsRepository.getCloakConfig()

        var excludedRoutes: [NEIPv4Route] = []

        if let ip = extractIP(from: serverPort),
           let route = makeExcludedRoute(host: ip) {
            excludedRoutes.append(route)
        }

        if let remoteHost = extractRemoteHost(from: cloakConfig),
           let route = makeExcludedRoute(host: remoteHost) {
            excludedRoutes.append(route)
        }

        let remoteAddress = "254.1.1.1"
        let localAddress = "198.18.0.1"
        let subnetMask = "255.255.0.0"
        let dnsServers = ["1.1.1.1", "8.8.8.8"]

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remoteAddress)
        settings.mtu = 1200

        settings.ipv4Settings = NEIPv4Settings(
            addresses: [localAddress],
            subnetMasks: [subnetMask]
        )

        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings?.excludedRoutes = excludedRoutes

        settings.ipv6Settings = nil
        settings.dnsSettings = NEDNSSettings(servers: dnsServers)
        settings.dnsSettings?.matchDomains = [""]

        
        logs.writeLog(log: "Settings are ready:")
        try await self.setTunnelNetworkSettings(settings)
        logs.writeLog(log: "Tunnel settings applied")
        
        let path = LogsRepository_iosKt.provideLogFilePath().normalized().description()
        logs.writeLog(log: "Start go logger init path = \(path)")
        Cloak_outlineInitLogger(path)
        logs.writeLog(log: "Finish go logger init")
        device.initialize(config: config, _logs: logs)
        startCloak()
        
        Task { await self.readPacketsFromTunnel() }
        Task { await self.processPacketsToDevice() }
        Task { await self.processPacketsFromDevice() }
        
        startMemoryLogging()
        
        self.startRepeatingHealthCheck()
        
        logs.writeLog(log: "startTunnel: all packet loops started")
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "Stopping tunnel with reason: \(reason)")
        VpnManagerImpl.isUserInitiatedStop = true
        stopCloak()
        completionHandler()
    }
    
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        if let msg = String(data: messageData, encoding: .utf8), msg == "heartbeat" {
            completionHandler?("alive".data(using: .utf8))
        } else {
            completionHandler?(messageData)
        }
    }

    private func startRepeatingHealthCheck(maxRepeats: Int = 3) {
        var repeats = 0
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global())
        
        timer.schedule(deadline: .now(), repeating: 10)
        timer.setEventHandler { [weak self] in
            guard let self else { return }
            do {
                HealthCheck.shared.fullCheckUp()
                repeats += 1
                if repeats >= maxRepeats { timer.cancel() }
            } catch {
                self.logs.writeLog(log: "[HealthCheck] Error: \(error.localizedDescription)")
                repeats += 1
                if repeats >= maxRepeats { timer.cancel() }
            }
        }
        
        timer.resume()
        self.healthTimer = timer
    }
    
    private func readPacketsFromTunnel() async {
        logs.writeLog(log: "Starting async readPacketsFromTunnel()…")

        while true {
            do {
                let (packets, protocols) = try await packetFlow.readPacketsAsync()
                
                guard !packets.isEmpty else { continue }

                for i in 0..<packets.count {
                    packetContinuation.yield((packets[i], protocols[i]))
                }
            } catch {
                logs.writeLog(log: "[readPacketsFromTunnel] Error: \(error.localizedDescription)")
            }
        }
    }

    private func processPacketsToDevice() async {
        logs.writeLog(log: "Starting async processPacketsToDevice()…")

        for await (packet, proto) in packetStream {
            device.write(data: packet)
        }
    }

    private func processPacketsFromDevice() async {
        logs.writeLog(log: "Starting async processPacketsFromDevice()…")

        while true {
            autoreleasepool {
                let data = device.readFromDevice()
                
                let ok = packetFlow.writePackets([data], withProtocols: [NSNumber(value: AF_INET)])
                if !ok {
                    logs.writeLog(log: "Failed to write packets to NEPacketFlow")
                }
            }
        }
    }

    func buildOutlineConfig(methodPassword: String, serverPort: String) -> String {
        let encoded = methodPassword.data(using: .utf8)?.base64EncodedString() ?? ""
        return "ss://\(encoded)@\(serverPort)"
    }

    private func startCloak() {
        let localHost = "127.0.0.1"
        let localPort = "1984"
        logs.writeLog(log: "startCloakOutline: entering")
        
        if configsRepository.getIsCloakEnabled() {
            do {
                logs.writeLog(log: "startCloakOutline: starting cloak")
                Cloak_outlineStartCloakClient(localHost, localPort, configsRepository.getCloakConfig(), false)
                logs.writeLog(log: "startCloakOutline: started")
            } catch {
                logs.writeLog(log: "startCloakOutline error: \(error)")
            }
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled")
        }
    }

    private func stopCloak() {
        if configsRepository.getIsCloakEnabled() {
            logs.writeLog(log: "stopCloakOutline")
            Cloak_outlineStopCloakClient()
        }
    }

    func parseIPv4Packet(_ data: Data) -> String {
        guard data.count >= 20 else { return "Invalid IPv4 packet" }
        let sourceIP = data[12..<16].map { String($0) }.joined(separator: ".")
        let destinationIP = data[16..<20].map { String($0) }.joined(separator: ".")
        let proto = data[9]
        return " route \(sourceIP) → \(destinationIP), proto: \(proto)"
    }
    
    /// Extract IP from "ip:port"
    func extractIP(from serverPort: String) -> String? {
        guard !serverPort.isEmpty else { return nil }
        return serverPort.split(separator: ":").first.map(String.init)
    }

    /// Extract RemoteHost from cloak JSON
    func extractRemoteHost(from cloakConfig: String) -> String? {
        guard
            !cloakConfig.isEmpty,
            let data = cloakConfig.data(using: .utf8),
            let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let remoteHost = json["RemoteHost"] as? String,
            !remoteHost.isEmpty
        else {
            return nil
        }
        return remoteHost
    }

    /// Convert host/IP to /32 excluded route
    func makeExcludedRoute(host: String) -> NEIPv4Route? {
        return NEIPv4Route(destinationAddress: host, subnetMask: "255.255.255.255")
    }
}


extension NEPacketTunnelFlow {
    func readPacketsAsync() async throws -> ([Data], [NSNumber]) {
        try await withCheckedThrowingContinuation { cont in
            self.readPackets { packets, protocols in
                cont.resume(returning: (packets, protocols))
            }
        }
    }
}


class DeviceFacade {
    private var device: Cloak_outlineOutlineDevice? = nil
    private var logs: LogsRepository? = nil

    func initialize(config: String, _logs: LogsRepository) {
        logs?.writeLog(log: "[DeviceFacade] Device initiaization started with config: \(config)")
        var err: NSErrorPointer = nil
        device = Cloak_outlineNewOutlineDevice(config, err)
        
        logs = _logs
        logs?.writeLog(log: "[DeviceFacade] Device initiaization finished (has error:\(err != nil))")
        if (err != nil) {
            logs?.writeLog(log: "[DeviceFacade] Error: \(err))")
        }
    }
    
    func write(data: Data) {
        do {
            var ret0_: Int = 0
            try device?.write(data, ret0_: &ret0_)
//            logs?.writeLog(log: "[DeviceFacade] wrote \(data.count) bytes")
        } catch let error {
            logs?.writeLog(log: "[DeviceFacade] write error: \(error)")
        }
    }

    func readFromDevice() -> Data {
        do {
            let data = try device?.read()
//            logs?.writeLog(log: "[DeviceFacade] read \(data?.count ?? 0) bytes")
            return data!
        } catch let error {
            logs?.writeLog(log: "[DeviceFacade] read error: \(error)")
            return Data()
        }
    }

}
