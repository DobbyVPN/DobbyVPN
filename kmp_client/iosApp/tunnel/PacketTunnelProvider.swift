import NetworkExtension
import MyLibrary
import os
import app
import CommonDI
import Sentry
import Foundation
import Darwin
import SystemConfiguration
import Network

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    
    private var device = DeviceFacade()
    private var logs = NativeModuleHolder.logsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    
    private var packetContinuation: AsyncStream<(Data, NSNumber)>.Continuation!
    private lazy var packetStream: AsyncStream<(Data, NSNumber)> = {
        AsyncStream<(Data, NSNumber)>(bufferingPolicy: .bufferingOldest(20)) { continuation in
            self.packetContinuation = continuation
        }
    }()
    
    func reportMemoryUsageMB() -> Double {
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
            return usedMB
        }
        logs.writeLog(log: "[Memory] unable to get info")
        return 0.0
    }
    
    override func startTunnel(options: [String : NSObject]?) async throws {
        logs.writeLog(log: "startTunnel in PacketTunnelProvider, thread: \(Thread.current)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")
        let methodPassword = configsRepository.getMethodPasswordOutline()
        let serverPort = configsRepository.getServerPortOutline()
        let prefix = configsRepository.getPrefixOutline()
        let websocketEnabled = configsRepository.getIsWebsocketEnabled()
        let tcpPath = configsRepository.getTcpPathOutline()
        let udpPath = configsRepository.getUdpPathOutline()
        
        let config = buildOutlineConfig(
            methodPassword: methodPassword,
            serverPort: serverPort,
            prefix: prefix,
            websocketEnabled: websocketEnabled,
            tcpPath: tcpPath,
            udpPath: udpPath
        )
        logs.writeLog(log: "Outline config built (prefix=\(!prefix.isEmpty), ws=\(websocketEnabled), tcpPath=\(!tcpPath.isEmpty), udpPath=\(!udpPath.isEmpty))")
        if websocketEnabled {
            logs.writeLog(log: "WebSocket transport requested (wss)")
        }

        let cloakConfig = configsRepository.getCloakConfig()
        var excludedRoutes: [NEIPv4Route] = []
        if let hostOrIp = extractIP(from: serverPort),
           let ip = resolveIPv4IfNeeded(hostOrIp),
           let route = makeExcludedRoute(host: ip) {
            excludedRoutes.append(route)
        }
        if let remoteHost = extractRemoteHost(from: cloakConfig),
           let ip = resolveIPv4IfNeeded(remoteHost),
           let route = makeExcludedRoute(host: ip) {
            excludedRoutes.append(route)
        }
        if !excludedRoutes.isEmpty {
            let list = excludedRoutes.map { "\($0.destinationAddress)/\($0.destinationSubnetMask)" }.joined(separator: ", ")
            logs.writeLog(log: "Excluded IPv4 routes: \(list)")
        } else {
            logs.writeLog(log: "Excluded IPv4 routes: (none)")
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
                        
        logs.writeLog(log: "startTunnel: all packet loops started")
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "Stopping tunnel with reason: \(reason)")
        configsRepository.setIsUserInitStop(isUserInitStop: true)
        stopCloak()
        completionHandler()
    }
    
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            completionHandler?("Memory:\(reportMemoryUsageMB())".data(using: .utf8))
        } else {
            completionHandler?(messageData)
        }
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

        for await (packet, _) in packetStream {
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

    func buildOutlineConfig(
        methodPassword: String,
        serverPort: String,
        prefix: String = "",
        websocketEnabled: Bool = false,
        tcpPath: String = "",
        udpPath: String = ""
    ) -> String {
        let encoded = methodPassword.data(using: .utf8)?.base64EncodedString() ?? ""
        let baseUrl = "ss://\(encoded)@\(serverPort)"

        func extractHost(_ hostPortMaybeWithQuery: String) -> String {
            let hostPort = hostPortMaybeWithQuery.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: true).first.map(String.init) ?? hostPortMaybeWithQuery
            let trimmed = hostPort.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.hasPrefix("[") {
                // IPv6 в квадратных скобках: [2001:db8::1]:443
                if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                    return String(trimmed[trimmed.index(after: start)..<end])
                }
            }
            if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
                return String(trimmed[..<lastColon])
            }
            return trimmed
        }

        // Добавляем параметр prefix, если он задан (URL-encoding)
        let ssUrl: String
        if !prefix.isEmpty {
            let separator = serverPort.contains("?") ? "&" : "?"
            let encodedPrefix = prefix.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? prefix
            ssUrl = "\(baseUrl)\(separator)prefix=\(encodedPrefix)"
        } else {
            ssUrl = baseUrl
        }

        // Если включён WebSocket — оборачиваем в WebSocket over TLS transport (wss://)
        if websocketEnabled {
            let effectiveHost = extractHost(serverPort).trimmingCharacters(in: .whitespacesAndNewlines)

            var wsParams: [String] = []
            // outline-sdk v0.0.16: ws: не поддерживает опцию host= (будет "Unsupported option host")
            // Доменные параметры задаём через TLS SNI (tls:sni=...).
            if !tcpPath.isEmpty { wsParams.append("tcp_path=\(tcpPath)") }
            if !udpPath.isEmpty { wsParams.append("udp_path=\(udpPath)") }
            
            let wsParamsStr = wsParams.joined(separator: "&")
            // Используем tls:sni|ws: для WebSocket over TLS (wss://) через SNI
            let tlsPrefix = "tls:sni=\(effectiveHost)"
            if !wsParamsStr.isEmpty {
                return "\(tlsPrefix)|ws:\(wsParamsStr)|\(ssUrl)"
            } else {
                return "\(tlsPrefix)|ws:|\(ssUrl)"
            }
        } else {
            return ssUrl
        }
    }

    private func startCloak() {
        let localPort = String(configsRepository.getCloakLocalPort())
        logs.writeLog(log: "startCloakOutline: entering")
        
        if configsRepository.getIsCloakEnabled() {
            do {
                logs.writeLog(log: "startCloakOutline: starting cloak")
                Cloak_outlineStartCloakClient("127.0.0.1", localPort, configsRepository.getCloakConfig(), false)
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
    /// Извлекает IP из строки "ip:port"
    func extractIP(from serverPort: String) -> String? {
        guard !serverPort.isEmpty else { return nil }
        return serverPort.split(separator: ":").first.map(String.init)
    }

    /// Извлекает RemoteHost из Cloak JSON
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

    /// Преобразует host/IP в исключённый маршрут /32
    func makeExcludedRoute(host: String) -> NEIPv4Route? {
        return NEIPv4Route(destinationAddress: host, subnetMask: "255.255.255.255")
    }

    private func isValidIPv4(_ s: String) -> Bool {
        let parts = s.split(separator: ".")
        guard parts.count == 4 else { return false }
        for p in parts {
            guard let n = Int(p), (0...255).contains(n) else { return false }
        }
        return true
    }

    /// Если host не является IPv4-литералом, резолвит его в IPv4 (первый A-record). Возвращает nil при ошибке.
    private func resolveIPv4IfNeeded(_ host: String) -> String? {
        let trimmed = host.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        if isValidIPv4(trimmed) { return trimmed }

        var hints = addrinfo(
            ai_flags: AI_ADDRCONFIG,
            ai_family: AF_INET,
            ai_socktype: SOCK_STREAM,
            ai_protocol: 0,
            ai_addrlen: 0,
            ai_canonname: nil,
            ai_addr: nil,
            ai_next: nil
        )
        var res: UnsafeMutablePointer<addrinfo>?
        let rc = getaddrinfo(trimmed, nil, &hints, &res)
        guard rc == 0, let first = res else { return nil }
        defer { freeaddrinfo(res) }

        var addr = first.pointee.ai_addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee.sin_addr }
        var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
        let ptr = inet_ntop(AF_INET, &addr, &buffer, socklen_t(INET_ADDRSTRLEN))
        guard ptr != nil else { return nil }
        return String(cString: buffer)
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
            logs?.writeLog(log: "[DeviceFacade] Error: \(String(describing: err)))")
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
