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
    
    private let outlineInteractor: OutlineInteractor = OutlineInteractor()
    private let cloakInteractor: CloakInteractor = CloakInteractor()
    
    private var logs = NativeModuleHolder.logsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!

    private var pathMonitor: Network.NWPathMonitor?
    private var lastPathStatus: Network.NWPath.Status?
    
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
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")
        
        // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
        await teardownForStop(reason: "pre-start cleanup")

        startPathLogging()

        configsRepository.sync()
        

        let cloakConfig = configsRepository.getCloakConfig()
        // Excluding the remote server route helps avoid routing loops (especially with WSS/domain hosts).
        // DNS resolution at tunnel start can hang in offline/captive-portal cases, so we do it with a hard timeout.
        var excludedRoutes: [NEIPv4Route] = []
        if let hostOrIp = extractIP(from: configsRepository.getServerPortOutline()) {
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
                    logs.writeLog(log: "Excluded route for Cloak RemoteHost resolved: \(maskStr(value: trimmed)) → \(maskStr(value: ip))/32")
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
        try outlineInteractor.startOutline()
        logs.writeLog(log: "[tunnel:\(tunnelId)] Device initialized OK")
        try cloakInteractor.startCloak(outlineServerPort: configsRepository.getServerPortOutline())
                        
        logs.writeLog(log: "startTunnel: all packet loops started")
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
            completionHandler?("Memory:\(reportMemoryUsageMB())".data(using: .utf8))
        } else {
            completionHandler?(messageData)
        }
    }

    private func startPathLogging() {
        // Logs-only: helps correlate "Wi‑Fi off/on" with tunnel lifecycle and health-check decisions.
        let monitor = Network.NWPathMonitor()
        let q = DispatchQueue(label: "vpn.dobby.app.tunnel.path.\(tunnelId)")
        pathMonitor = monitor

        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let status = path.status
            if self.lastPathStatus != status {
                self.lastPathStatus = status
                let ifaces = path.availableInterfaces.map { "\($0.type)" }.joined(separator: ",")
                let expensive = path.isExpensive
                let constrained = path.isConstrained
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] pathUpdate status=\(status) ifaces=[\(ifaces)] expensive=\(expensive) constrained=\(constrained)")
            }
        }

        monitor.start(queue: q)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }
    
    @MainActor
    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason)")
                
        do {
            try cloakInteractor.stopCloak()
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] could not stop cloak: \(error.localizedDescription)")
        }

        do {
            try await self.setTunnelNetworkSettings(nil)
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] cleared tunnel network settings")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] failed to clear tunnel network settings: \(error.localizedDescription)")
        }

        pathMonitor?.cancel()
        pathMonitor = nil
        lastPathStatus = nil
        
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason))")
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

    private func isValidIPv4(_ s: String) -> Bool {
        let parts = s.split(separator: ".")
        guard parts.count == 4 else { return false }
        for p in parts {
            guard let n = Int(p), (0...255).contains(n) else { return false }
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
        var result: String? = nil

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
