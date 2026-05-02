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
    private let tunnelMTU = 1200

    private let outlineInteractor: OutlineInteractor = OutlineInteractor()
    private let cloakInteractor: CloakInteractor = CloakInteractor()

    private var logs = NativeModuleHolder.logsRepository
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!

    private var pathMonitor: Network.NWPathMonitor?
    private var lastPathFingerprint: String?
    private var packetFlowBridge: PacketFlowBridge?

    deinit {
        logs.writeLog(log: "[tunnel:\(tunnelId)] deinit")
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

    func logInterfaces() {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, ifaddrPtr != nil else {
            logs.writeLog(log: "[DEBUG][Interfaces] getifaddrs failed")
            return
        }
        defer { freeifaddrs(ifaddrPtr) }

        var interfaces: [String: [String]] = [:]
        var ptr = ifaddrPtr
        while ptr != nil {
            guard let rawName = ptr?.pointee.ifa_name,
                  let addr = ptr?.pointee.ifa_addr else {
                ptr = ptr?.pointee.ifa_next
                continue
            }
            let name = String(cString: rawName)
            if name.starts(with: "utun") {
                let family = Int32(addr.pointee.sa_family)
                var values = interfaces[name] ?? []
                if family == AF_INET {
                    var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
                    var ip = addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) {
                        $0.pointee.sin_addr
                    }
                    if inet_ntop(AF_INET, &ip, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil {
                        values.append("ipv4=\(String(cString: buffer))")
                    }
                } else if family == AF_INET6 {
                    values.append("ipv6")
                } else {
                    values.append("family=\(family)")
                }
                interfaces[name] = values
            }
            ptr = ptr?.pointee.ifa_next
        }

        if interfaces.isEmpty {
            logs.writeLog(log: "[DEBUG][Interfaces] no utun interfaces visible")
            return
        }

        for name in interfaces.keys.sorted() {
            let details = (interfaces[name] ?? []).joined(separator: ",")
            logs.writeLog(log: "[DEBUG][Interfaces] active \(name) \(details)")
        }
    }

    override func startTunnel(options: [String : NSObject]?) async throws {
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        var startupStage = "entered"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")

        do {
            // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
            startupStage = "pre-start cleanup"
            await teardownForStop(reason: "pre-start cleanup")

            startupStage = "path monitor"
            startPathLogging()

            startupStage = "go logger init"
            let path = LogsRepository_iosKt.provideLogFilePath().normalized().description()
            logs.writeLog(log: "Start go logger init path = \(path)")
            Cloak_outlineInitLogger(path)
            logs.writeLog(log: "Finish go logger init")
            startupStage = "geo routing config"
            let geoRoutingConf = configsRepository.getGeoRoutingConf()
            logs.writeLog(log: "[DEBUG][Routing] applying geo routing config length=\(geoRoutingConf.count)")
            Cloak_outlineSetGeoRoutingConf(geoRoutingConf)

            startupStage = "route exclusions"
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
                        logs.writeLog(
                            log: "Excluded route for Outline host resolved: " +
                                "\(maskStr(value: trimmed)) → \(maskStr(value: ip))/32"
                        )
                    }
                } else {
                    logs.writeLog(log: "Excluded route for Outline host skipped (can't resolve to IPv4): \(maskStr(value: trimmed))")
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
                        logs.writeLog(
                            log: "Excluded route for Cloak RemoteHost resolved: " +
                                "\(maskStr(value: trimmed)) → \(maskStr(value: ip))/32"
                        )
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

            // Cloak must bind/connect before the full-tunnel route is installed; otherwise
            // iOS can route the upstream bootstrap traffic into a tunnel that is not ready yet.
            startupStage = "cloak startup"
            logs.writeLog(log: "[tunnel:\(tunnelId)] starting Cloak phase")
            try cloakInteractor.startCloak(outlineServerPort: configsRepository.getServerPortOutline())

            startupStage = "network settings build"
            let remoteAddress = "254.1.1.1"
            let localAddress = "198.18.0.1"
            let subnetMask = "255.255.0.0"
            let dnsServers = ["1.1.1.1", "8.8.8.8"]

            let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remoteAddress)
            settings.mtu = NSNumber(value: tunnelMTU)
            settings.ipv4Settings = NEIPv4Settings(
                addresses: [localAddress],
                subnetMasks: [subnetMask]
            )
            settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
            settings.ipv4Settings?.excludedRoutes = excludedRoutes
            settings.ipv6Settings = nil
            settings.dnsSettings = NEDNSSettings(servers: dnsServers)
            settings.dnsSettings?.matchDomains = [""]

            logNetworkSettings(
                localAddress: localAddress,
                remoteAddress: remoteAddress,
                subnetMask: subnetMask,
                mtu: tunnelMTU,
                dnsServers: dnsServers,
                excludedRoutes: excludedRoutes
            )
            startupStage = "setTunnelNetworkSettings"
            let settingsStart = Date()
            logs.writeLog(log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings begin")
            try await self.setTunnelNetworkSettings(settings)
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings applied in \(elapsedMs(since: settingsStart))ms"
            )

            logInterfaces()

            startupStage = "packetFlow bridge"
            let bridgeTunnelId = tunnelId
            let bridgeLogs = logs
            logs.writeLog(log: "[tunnel:\(tunnelId)] creating PacketFlowBridge mtu=\(tunnelMTU)")
            let bridge = try PacketFlowBridge(
                packetFlow: packetFlow,
                mtu: tunnelMTU,
                tunnelId: tunnelId,
                log: { message in
                    bridgeLogs.writeLog(log: "[tunnel:\(bridgeTunnelId)] \(message)")
                }
            )
            packetFlowBridge = bridge
            bridge.start()

            startupStage = "outline startup"
            logs.writeLog(log: "[tunnel:\(tunnelId)] starting Outline phase tunnelFD=\(bridge.tunnelFileDescriptor) mtu=\(tunnelMTU)")
            try outlineInteractor.startOutline(
                tunnelFileDescriptor: bridge.tunnelFileDescriptor,
                mtu: tunnelMTU,
                nativeClientCreated: {
                    bridgeLogs.writeLog(log: "[tunnel:\(bridgeTunnelId)] native Outline client created; releasing bridge fd to Go")
                    bridge.releaseTunnelFileDescriptor()
                }
            )
            logs.writeLog(log: "[tunnel:\(tunnelId)] Device initialized OK")

            logs.writeLog(log: "startTunnel: all packet loops started")
        } catch {
            let nsError = error as NSError
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] startTunnel failed: " +
                    "stage=\(startupStage) \(nsError.domain) code=\(nsError.code) desc=\(error.localizedDescription)"
            )
            await teardownForStop(reason: "startTunnel failure")
            throw error
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel reason=\(reason.rawValue) (\(reason))")
        configsRepository.setIsUserInitStop(isUserInitStop: true)
        Cloak_outlineClearGeoRoutingConf()
        Task {
            await teardownForStop(reason: "stopTunnel(\(reason))")
            logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel teardown complete; calling completionHandler")
            completionHandler()
            logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel completionHandler returned")
        }
    }

    override func cancelTunnelWithError(_ error: Error?) {
        if let error {
            logs.writeLog(log: "[tunnel:\(tunnelId)] cancelTunnelWithError: \(error.localizedDescription)")
        } else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] cancelTunnelWithError: nil")
        }
        super.cancelTunnelWithError(error)
    }

    override func sleep(completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "[tunnel:\(tunnelId)] sleep()")
        completionHandler()
    }

    override func wake() {
        logs.writeLog(log: "[tunnel:\(tunnelId)] wake()")
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory")
            completionHandler?("Memory:\(reportMemoryUsageMB())".data(using: .utf8))
        } else {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage echo bytes=\(messageData.count)")
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
            let ifaces = path.availableInterfaces.map { "\($0.type)" }.joined(separator: ",")
            let expensive = path.isExpensive
            let constrained = path.isConstrained
            let fingerprint = "status=\(path.status)|ifaces=\(ifaces)|expensive=\(expensive)|constrained=\(constrained)"
            if self.lastPathFingerprint != fingerprint {
                self.lastPathFingerprint = fingerprint
                self.logs.writeLog(
                    log: "[tunnel:\(self.tunnelId)] pathUpdate " +
                        "status=\(path.status) ifaces=[\(ifaces)] " +
                        "expensive=\(expensive) constrained=\(constrained)"
                )
            }
        }

        monitor.start(queue: q)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }

    @MainActor
    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))")

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] stopping PacketFlowBridge")
        packetFlowBridge?.stop()
        packetFlowBridge = nil

        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] stopping Outline")
            try outlineInteractor.stopOutline()
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] Outline stopped")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] could not stop outline: \(error.localizedDescription)")
        }

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] stopping Cloak")
        cloakInteractor.stopCloak()

        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] clearing tunnel network settings")
            try await self.setTunnelNetworkSettings(nil)
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] cleared tunnel network settings")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] failed to clear tunnel network settings: \(error.localizedDescription)")
        }

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] stopping NWPathMonitor")
        pathMonitor?.cancel()
        pathMonitor = nil
        lastPathFingerprint = nil

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason))")
    }

    private func logNetworkSettings(
        localAddress: String,
        remoteAddress: String,
        subnetMask: String,
        mtu: Int,
        dnsServers: [String],
        excludedRoutes: [NEIPv4Route]
    ) {
        let excluded = excludedRoutes
            .map { "\($0.destinationAddress)/\($0.destinationSubnetMask)" }
            .joined(separator: ",")
        logs.writeLog(
            log: "[DEBUG][tunnel:\(tunnelId)] network settings " +
            "local=\(localAddress) remote=\(remoteAddress) mask=\(subnetMask) mtu=\(mtu) " +
            "dns=\(dnsServers.joined(separator: ",")) included=default " +
            "excluded=\(excluded.isEmpty ? "(none)" : excluded) ipv6=disabled"
        )
    }

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }

    /// Extracts the host/IP part from a string like "ip:port" or "[ipv6]:port".
    func extractIP(from serverPort: String) -> String? {
        let host = OutlineInteractor.extractHost(from: serverPort).trimmingCharacters(in: .whitespacesAndNewlines)
        return host.isEmpty ? nil : host
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

        let start = Date()
        logs.writeLog(log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: trimmed))")

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
        guard rc == 0, let first = res else {
            logs.writeLog(
                log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: trimmed)) failed " +
                "rc=\(rc) error=\(String(cString: gai_strerror(rc))) elapsed=\(elapsedMs(since: start))ms"
            )
            return nil
        }
        defer { freeaddrinfo(res) }

        var addr = first.pointee.ai_addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee.sin_addr }
        var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
        let ptr = inet_ntop(AF_INET, &addr, &buffer, socklen_t(INET_ADDRSTRLEN))
        guard ptr != nil else { return nil }
        let value = String(cString: buffer)
        logs.writeLog(
            log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: trimmed)) ok " +
            "ip=\(maskStr(value: value)) elapsed=\(elapsedMs(since: start))ms"
        )
        return value
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
            logs.writeLog(
                log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: host)) " +
                "timed out after \(Int(timeout * 1000))ms"
            )
            return nil
        }
        lock.lock()
        let value = result
        lock.unlock()
        return value
    }
}
