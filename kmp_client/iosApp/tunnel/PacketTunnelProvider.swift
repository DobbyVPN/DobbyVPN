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
    private let tunnelId = String(UUID().uuidString.prefix(8))

    private var device = DeviceFacade()
    private var logs = NativeModuleHolder.logsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!

    private var readPacketsTask: Task<Void, Never>?
    private var processToDeviceTask: Task<Void, Never>?
    private var processFromDeviceTask: Task<Void, Never>?

    private var packetContinuation: AsyncStream<(Data, NSNumber)>.Continuation?
    private lazy var packetStream: AsyncStream<(Data, NSNumber)> = {
        AsyncStream<(Data, NSNumber)>(bufferingPolicy: .bufferingOldest(20)) { continuation in
            self.packetContinuation = continuation
        }
    }()

    private var cloakStarted: Bool = false
    private var pathMonitor: Network.NWPathMonitor?
    private var pathMonitorQueue: DispatchQueue?
    private var lastPathStatus: Network.NWPath.Status?

    private func extractHost(from hostPortMaybeWithQuery: String) -> String {
        let hostPort = hostPortMaybeWithQuery
            .split(separator: "?", maxSplits: 1, omittingEmptySubsequences: true)
            .first.map(String.init) ?? hostPortMaybeWithQuery
        let trimmed = hostPort.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("[") {
            // IPv6 in brackets: [2001:db8::1]:443
            if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                return String(trimmed[trimmed.index(after: start)..<end])
            }
        }
        if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
            return String(trimmed[..<lastColon])
        }
        return trimmed
    }

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

    override func startTunnel(options: [String: NSObject]?) async throws {
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")

        // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
        await teardownForStop(reason: "pre-start cleanup")

        startPathLogging()

        configsRepository.sync()

        let methodPassword = configsRepository.getMethodPasswordOutline()
        let serverPort = configsRepository.getServerPortOutline()
        let prefix = configsRepository.getPrefixOutline()
        let websocketEnabled = configsRepository.getIsWebsocketEnabled()
        let tcpPath = configsRepository.getTcpPathOutline()
        let udpPath = configsRepository.getUdpPathOutline()
        logs.writeLog(log: "[tunnel:\(tunnelId)] config snapshot:" +
            " serverPort.len=\(serverPort.count) methodPassword.len=\(methodPassword.count)" +
            " ws=\(websocketEnabled) tcpPath.len=\(tcpPath.count) udpPath.len=\(udpPath.count)")

        // Validate config early (prevents passing empty config into native layer).
        if methodPassword.isEmpty || serverPort.isEmpty {
            logs.writeLog(log: "[startTunnel] Empty Outline config (methodPassword/serverPort) → abort")
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Empty Outline configuration"]
            )
        }

        let config = buildOutlineConfig(
            methodPassword: methodPassword,
            serverPort: serverPort,
            prefix: prefix,
            websocketEnabled: websocketEnabled,
            tcpPath: tcpPath,
            udpPath: udpPath
        )
        logs.writeLog(log: "Outline config built" +
            " (prefix=\(!prefix.isEmpty), ws=\(websocketEnabled)," +
            " tcpPath=\(!tcpPath.isEmpty), udpPath=\(!udpPath.isEmpty))")
        if websocketEnabled {
            logs.writeLog(log: "WebSocket transport requested (wss)")
        }

        let cloakConfig = configsRepository.getCloakConfig()
        let excludedRoutes = buildExcludedRoutes(
            serverPort: serverPort,
            cloakConfig: cloakConfig
        )

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
        if !device.initialize(config: config, logs: logs) {
            logs.writeLog(log: "[startTunnel] Device initialization failed; aborting startTunnel")
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: "Outline device initialization failed"]
            )
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] Device initialized OK")
        try startCloak(outlineServerPort: serverPort)

        readPacketsTask = Task { await self.readPacketsFromTunnel() }
        processToDeviceTask = Task { await self.processPacketsToDevice() }
        processFromDeviceTask = Task { await self.processPacketsFromDevice() }

        logs.writeLog(log: "startTunnel: all packet loops started")
    }

    private func buildExcludedRoutes(serverPort: String, cloakConfig: String) -> [NEIPv4Route] {
        var excludedRoutes: [NEIPv4Route] = []
        if let hostOrIp = extractIP(from: serverPort) {
            let trimmed = hostOrIp.trimmingCharacters(in: .whitespacesAndNewlines)
            if let ip = resolveIPv4IfNeededWithTimeout(trimmed, timeout: 1.0),
               let route = makeExcludedRoute(host: ip) {
                excludedRoutes.append(route)
                if ip == trimmed {
                    logs.writeLog(log: "Excluded route for Outline host: \(maskStr(value: ip))/32")
                } else {
                    logs.writeLog(log: "Excluded route for Outline host resolved: \(maskStr(value: trimmed)) → \(maskStr(value: ip))/32")
                }
            } else {
                logs.writeLog(log: "Excluded route for Outline host skipped (can't resolve to IPv4): \(trimmed)")
            }
        }
        if let remoteHost = extractRemoteHost(from: cloakConfig) {
            let trimmed = remoteHost.trimmingCharacters(in: .whitespacesAndNewlines)
            if let ip = resolveIPv4IfNeededWithTimeout(trimmed, timeout: 1.0),
               let route = makeExcludedRoute(host: ip) {
                excludedRoutes.append(route)
                if ip == trimmed {
                    logs.writeLog(log: "Excluded route for Cloak RemoteHost: \(maskStr(value: ip))/32")
                } else {
                    logs.writeLog(log: "Excluded route for Cloak RemoteHost resolved:" +
                        " \(maskStr(value: trimmed)) → \(maskStr(value: ip))/32")
                }
            } else {
                logs.writeLog(log: "Excluded route for Cloak RemoteHost skipped (can't resolve to IPv4): \(maskStr(value: trimmed))")
            }
        }
        if !excludedRoutes.isEmpty {
            let list = excludedRoutes.map { "\($0.destinationAddress)/\($0.destinationSubnetMask)" }.joined(separator: ", ")
            logs.writeLog(log: "Excluded IPv4 routes: \(list)")
        } else {
            logs.writeLog(log: "Excluded IPv4 routes: (none)")
        }
        return excludedRoutes
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel reason=\(reason.rawValue) (\(reason))")
        configsRepository.setIsUserInitStop(isUserInitStop: true)
        Task {
            await teardownForStop(reason: "stopTunnel(\(reason))")
            completionHandler()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            completionHandler?(Data("Memory:\(reportMemoryUsageMB())".utf8))
        } else {
            completionHandler?(messageData)
        }
    }

    private func readPacketsFromTunnel() async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] readPacketsFromTunnel(): start")

        while !Task.isCancelled {
            do {
                let (packets, protocols) = try await packetFlow.readPacketsAsync()

                guard !packets.isEmpty else { continue }

                for i in 0..<packets.count {
                    packetContinuation?.yield((packets[i], protocols[i]))
                }
            } catch {
                if Task.isCancelled { break }
                logs.writeLog(log: "[readPacketsFromTunnel] Error: \(error.localizedDescription)")
            }
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] readPacketsFromTunnel(): end cancelled=\(Task.isCancelled)")
    }

    private func processPacketsToDevice() async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] processPacketsToDevice(): start")

        for await (packet, _) in packetStream {
            if Task.isCancelled { break }
            device.write(data: packet)
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] processPacketsToDevice(): end cancelled=\(Task.isCancelled)")
    }

    private func processPacketsFromDevice() async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] processPacketsFromDevice(): start")

        while !Task.isCancelled {
            autoreleasepool {
                let data = device.readFromDevice()

                let ok = packetFlow.writePackets([data], withProtocols: [NSNumber(value: AF_INET)])
                if !ok {
                    logs.writeLog(log: "[tunnel:\(tunnelId)] Failed to write packets to NEPacketFlow")
                }
            }
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] processPacketsFromDevice(): end cancelled=\(Task.isCancelled)")
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
            let hostPort = hostPortMaybeWithQuery
                .split(separator: "?", maxSplits: 1, omittingEmptySubsequences: true)
                .first.map(String.init) ?? hostPortMaybeWithQuery
            let trimmed = hostPort.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.hasPrefix("[") {
                // IPv6 wrapped in square brackets: [2001:db8::1]:443
                if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                    return String(trimmed[trimmed.index(after: start)..<end])
                }
            }
            if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
                return String(trimmed[..<lastColon])
            }
            return trimmed
        }

        // Add the `prefix` query param if present (URL-encoded)
        let ssUrl: String
        if !prefix.isEmpty {
            let separator = serverPort.contains("?") ? "&" : "?"
            let encodedPrefix = prefix.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? prefix
            ssUrl = "\(baseUrl)\(separator)prefix=\(encodedPrefix)"
        } else {
            ssUrl = baseUrl
        }

        // If WebSocket is enabled, wrap the Shadowsocks URL into WebSocket-over-TLS transport (wss://)
        if websocketEnabled {
            let effectiveHost = extractHost(serverPort).trimmingCharacters(in: .whitespacesAndNewlines)

            var wsParams: [String] = []
            // outline-sdk v0.0.16: ws: does not support the `host=` option ("Unsupported option host")
            // Domain-related routing should be configured via TLS SNI (tls:sni=...).
            if !tcpPath.isEmpty { wsParams.append("tcp_path=\(tcpPath)") }
            if !udpPath.isEmpty { wsParams.append("udp_path=\(udpPath)") }

            let wsParamsStr = wsParams.joined(separator: "&")
            // Use tls:sni|ws: for WebSocket-over-TLS (wss://) via SNI
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

    private func startCloak(outlineServerPort: String) throws {
        let localPort = String(configsRepository.getCloakLocalPort())
        logs.writeLog(log: "startCloakOutline: entering")

        if configsRepository.getIsCloakEnabled() {
            let cloakConfig = configsRepository.getCloakConfig()
            if cloakConfig.isEmpty {
                let host = extractHost(from: outlineServerPort).lowercased()
                let cloakRequired = (host == "127.0.0.1" || host == "localhost")
                logs.writeLog(log: "startCloakOutline: enabled but config empty (required=\(cloakRequired), host=\(host))")
                if cloakRequired {
                    throw NSError(
                        domain: "PacketTunnelProvider",
                        code: -3,
                        userInfo: [NSLocalizedDescriptionKey: "Cloak enabled but config is empty"]
                    )
                }
                return
            }
            logs.writeLog(log: "startCloakOutline: starting cloak")
            Cloak_outlineStartCloakClient("127.0.0.1", localPort, cloakConfig, false)
            cloakStarted = true
            logs.writeLog(log: "startCloakOutline: started")
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled")
        }
    }

    private func stopCloak() {
        if cloakStarted {
            logs.writeLog(log: "stopCloakOutline")
            Cloak_outlineStopCloakClient()
            cloakStarted = false
        }
    }

    private func startPathLogging() {
        // Logs-only: helps correlate "Wi‑Fi off/on" with tunnel lifecycle and health-check decisions.
        let monitor = Network.NWPathMonitor()
        let queue = DispatchQueue(label: "vpn.dobby.app.tunnel.path.\(tunnelId)")
        pathMonitor = monitor
        pathMonitorQueue = queue

        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let status = path.status
            if self.lastPathStatus != status {
                self.lastPathStatus = status
                let ifaces = path.availableInterfaces.map { "\($0.type)" }.joined(separator: ",")
                let expensive = path.isExpensive
                let constrained = path.isConstrained
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] pathUpdate" +
                    " status=\(status) ifaces=[\(ifaces)]" +
                    " expensive=\(expensive) constrained=\(constrained)")
            }
        }

        monitor.start(queue: queue)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }

    @MainActor
    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))" +
            " tasks(read=\(readPacketsTask != nil) toDev=\(processToDeviceTask != nil)" +
            " fromDev=\(processFromDeviceTask != nil)) cloakStarted=\(cloakStarted)")

        packetContinuation?.finish()

        readPacketsTask?.cancel()
        processToDeviceTask?.cancel()
        processFromDeviceTask?.cancel()

        stopCloak()
        Cloak_outlineStopHealthCheck()

        do {
            try await self.setTunnelNetworkSettings(nil)
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] cleared tunnel network settings")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] failed to clear tunnel network settings: \(error.localizedDescription)")
        }

        device.close()

        pathMonitor?.cancel()
        pathMonitor = nil
        pathMonitorQueue = nil
        lastPathStatus = nil

        readPacketsTask = nil
        processToDeviceTask = nil
        processFromDeviceTask = nil

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason))")
    }

    func parseIPv4Packet(_ data: Data) -> String {
        guard data.count >= 20 else { return "Invalid IPv4 packet" }
        let sourceIP = data[12..<16].map { String($0) }.joined(separator: ".")
        let destinationIP = data[16..<20].map { String($0) }.joined(separator: ".")
        let proto = data[9]
        return " route \(sourceIP) → \(destinationIP), proto: \(proto)"
    }
    /// Extracts the host/IP part from a string like "ip:port"
    func extractIP(from serverPort: String) -> String? {
        guard !serverPort.isEmpty else { return nil }
        return serverPort.split(separator: ":").first.map(String.init)
    }

    /// Extracts `RemoteHost` from Cloak JSON
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

    /// Converts host/IP into an excluded /32 route
    func makeExcludedRoute(host: String) -> NEIPv4Route? {
        return NEIPv4Route(destinationAddress: host, subnetMask: "255.255.255.255")
    }

    private func isValidIPv4(_ address: String) -> Bool {
        let parts = address.split(separator: ".")
        guard parts.count == 4 else { return false }
        for part in parts {
            guard let num = Int(part), (0...255).contains(num) else { return false }
        }
        return true
    }

    /// If `host` is not an IPv4 literal, resolves it to IPv4 (first A record). Returns nil on error.
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

    private func resolveIPv4IfNeededWithTimeout(_ host: String, timeout: TimeInterval) -> String? {
        let group = DispatchGroup()
        group.enter()
        let lock = NSLock()
        var result: String?

        DispatchQueue.global(qos: .userInitiated).async {
            let ip = self.resolveIPv4IfNeeded(host)
            lock.lock()
            result = ip
            lock.unlock()
            group.leave()
        }

        let wait = group.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            return nil
        }
        lock.lock()
        let value = result
        lock.unlock()
        return value
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
    private var device: Cloak_outlineOutlineDevice?
    private var logs: LogsRepository?

    func initialize(config: String, logs logRepo: LogsRepository) -> Bool {
        logs?.writeLog(log: "[DeviceFacade] Device initiaization started with config: \(config)")
        var err: NSError?
        device = Cloak_outlineNewOutlineDevice(config, &err)

        logs = logRepo
        logs?.writeLog(log: "[DeviceFacade] Device initialization finished (has error:\(err != nil))")
        if let err {
            logs?.writeLog(log: "[DeviceFacade] Error: \(err)")
        }

        return device != nil && err == nil
    }

    func write(data: Data) {
        do {
            var bytesWritten: Int = 0
            try device?.write(data, ret0_: &bytesWritten)
//            logs?.writeLog(log: "[DeviceFacade] wrote \(data.count) bytes")
        } catch let error {
            logs?.writeLog(log: "[DeviceFacade] write error: \(error)")
        }
    }

    func readFromDevice() -> Data {
        do {
            let data = try device?.read()
//            logs?.writeLog(log: "[DeviceFacade] read \(data?.count ?? 0) bytes")
            return data ?? Data()
        } catch let error {
            logs?.writeLog(log: "[DeviceFacade] read error: \(error)")
            return Data()
        }
    }

    func close() {
        guard let device else { return }
        do {
            try device.close()
        } catch {
            logs?.writeLog(log: "[DeviceFacade] close error: \(error)")
        }
        self.device = nil
    }
}
