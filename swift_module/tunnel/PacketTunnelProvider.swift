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
    private var lastPathSignature: String?
    private var resourceSnapshotTimer: DispatchSourceTimer?
    private let memoryHighWaterLock = NSLock()
    private var memoryHighWaterMarkMB = 0.0
    private var tunnelStartedAt = Date()

    private struct MemorySnapshot {
        let footprintMB: Double
        let residentMB: Double
        let virtualMB: Double
    }

    private func fixedCString<T>(_ value: inout T) -> String {
        withUnsafePointer(to: &value) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: MemoryLayout<T>.size) { cString in
                String(cString: cString)
            }
        }
    }

    private func logSystemInfo(osVersionString: String) {
        let processInfo = ProcessInfo.processInfo
        var sysname = "unknown"
        var release = "unknown"
        var version = "unknown"
        var machine = "unknown"

        var uts = utsname()
        if uname(&uts) == 0 {
            var utsSysname = uts.sysname
            var utsRelease = uts.release
            var utsVersion = uts.version
            var utsMachine = uts.machine
            sysname = fixedCString(&utsSysname)
            release = fixedCString(&utsRelease)
            version = fixedCString(&utsVersion)
            machine = fixedCString(&utsMachine)
        }

        let physicalMemoryMB = processInfo.physicalMemory / 1024 / 1024
        logs.writeLog(
            log: "[tunnel:\(tunnelId)] OS platform=iOS osVersion=\(osVersionString) " +
                "osDescription=\(processInfo.operatingSystemVersionString) " +
                "process=\(processInfo.processName) kernel=\(sysname) " +
                "kernelRelease=\(release) kernelVersion=\(version) " +
                "machine=\(machine) physicalMemoryMB=\(physicalMemoryMB)"
        )
    }

    private func bytesToMB<T: BinaryInteger>(_ bytes: T) -> Double {
        Double(bytes) / 1024.0 / 1024.0
    }

    private func readMemorySnapshot() -> MemorySnapshot? {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.stride / MemoryLayout<natural_t>.stride)

        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }

        if result == KERN_SUCCESS {
            return MemorySnapshot(
                footprintMB: bytesToMB(info.phys_footprint),
                residentMB: bytesToMB(info.resident_size),
                virtualMB: bytesToMB(info.virtual_size)
            )
        }
        logs.writeLog(log: "[Memory] task_info(TASK_VM_INFO) failed kern=\(result)")
        return nil
    }

    func reportMemoryUsageMB() -> Double {
        guard let snapshot = readMemorySnapshot() else {
            return 0.0
        }

        let usedMB = snapshot.footprintMB
        memoryHighWaterLock.lock()
        if usedMB > memoryHighWaterMarkMB { memoryHighWaterMarkMB = usedMB }
        let highWater = memoryHighWaterMarkMB
        memoryHighWaterLock.unlock()
        logs.writeLog(
            log: "[Memory] footprintMB=\(String(format: "%.2f", snapshot.footprintMB)) " +
                "residentMB=\(String(format: "%.2f", snapshot.residentMB)) " +
                "virtualMB=\(String(format: "%.2f", snapshot.virtualMB)) " +
                "highWaterMB=\(String(format: "%.2f", highWater))"
        )
        return usedMB
    }

    func logInterfaces() {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        getifaddrs(&ifaddrPtr)
        var ptr = ifaddrPtr
        while ptr != nil {
            if let name = ptr?.pointee.ifa_name {
                let s = String(cString: name)
                if s.starts(with: "utun") {
                    logs.writeLog(log: "Active interface: \(s)")
                }
            }
            ptr = ptr?.pointee.ifa_next
        }
        freeifaddrs(ifaddrPtr)
    }

    func logInterfacesDetailed(label: String) {
        logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: \(label) ==========")
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else {
            logs.writeLog(log: "[DEBUG][Interfaces] getifaddrs failed errno=\(errno)")
            logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: END_\(label) ==========")
            return
        }
        defer {
            freeifaddrs(ifaddrPtr)
            logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: END_\(label) ==========")
        }

        var ptr: UnsafeMutablePointer<ifaddrs>? = first
        var count = 0
        while let current = ptr {
            count += 1
            let name = String(cString: current.pointee.ifa_name)
            let flags = current.pointee.ifa_flags
            let family = current.pointee.ifa_addr?.pointee.sa_family
            let familyDescription = family.map { String($0) } ?? "nil"
            let address = addressDescription(current.pointee.ifa_addr)
            logs.writeLog(
                log: "[DEBUG][Interfaces] \(label) name=\(name) family=\(familyDescription) " +
                    "flags=0x\(String(flags, radix: 16)) address=\(address)"
            )
            ptr = current.pointee.ifa_next
        }
        if count == 0 {
            logs.writeLog(log: "[DEBUG][Interfaces] \(label) no interfaces visible")
        }
    }

    private func addressDescription(_ addr: UnsafePointer<sockaddr>?) -> String {
        guard let addr else { return "nil" }
        var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        let length: socklen_t
        switch Int32(addr.pointee.sa_family) {
        case AF_INET:
            length = socklen_t(MemoryLayout<sockaddr_in>.size)
        case AF_INET6:
            length = socklen_t(MemoryLayout<sockaddr_in6>.size)
        default:
            return "family=\(addr.pointee.sa_family)"
        }
        if getnameinfo(addr, length, &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 {
            return String(cString: host)
        }
        return "family=\(addr.pointee.sa_family) getnameinfoErr=\(errno)"
    }

    override func startTunnel(options: [String : NSObject]?) async throws {
        tunnelStartedAt = Date()
        memoryHighWaterLock.lock()
        memoryHighWaterMarkMB = 0
        memoryHighWaterLock.unlock()
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logSystemInfo(osVersionString: osVersionString)
        logs.writeLog(log: "[iOS26-RESEARCH] iOS version: \(osVersionString)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")
        logInterfacesDetailed(label: "BEFORE_VPN_TUNNEL")

        // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
        await teardownForStop(reason: "pre-start cleanup")

        startPathLogging()
        startResourceSnapshotLogging()
        logInitialNetworkPath(timeout: 1.0)

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
        logs.writeLog(log: "[tunnel:\(tunnelId)] settings mtu=\(settings.mtu?.stringValue ?? "nil") ipv4=\(localAddress)/\(subnetMask) remote=\(remoteAddress) dns=\(dnsServers.joined(separator: ",")) excludedRoutes=\(excludedRoutes.count)")
        do {
            try await self.setTunnelNetworkSettings(settings)
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings failed: \(error.localizedDescription)")
            throw error
        }
        logs.writeLog(log: "Tunnel settings applied")

        logInterfaces()
        logInterfacesDetailed(label: "AFTER_VPN_TUNNEL")

        let path = LogsRepository_iosKt.provideLogFilePath().normalized().description()
        logs.writeLog(log: "Start go logger init path = \(path)")
        Cloak_outlineInitLogger(path)
        logs.writeLog(log: "Finish go logger init")
        Cloak_outlineSetGeoRoutingConf(configsRepository.getGeoRoutingConf())
        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] startOutline begin")
            try outlineInteractor.startOutline()
            logs.writeLog(log: "[tunnel:\(tunnelId)] startOutline success")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] startOutline failed: \(error.localizedDescription)")
            await teardownForStop(reason: "startOutline failure")
            throw error
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] Device initialized OK")
        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] startCloak begin")
            try cloakInteractor.startCloak(outlineServerPort: configsRepository.getServerPortOutline())
            logs.writeLog(log: "[tunnel:\(tunnelId)] startCloak success")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] startCloak failed: \(error.localizedDescription)")
            await teardownForStop(reason: "startCloak failure")
            throw error
        }

        logs.writeLog(log: "startTunnel: all packet loops started")
        logInterfacesDetailed(label: "AFTER_PROTOCOL_STARTUP")
        logResourceSnapshot(label: "AFTER_PROTOCOL_STARTUP")
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel reason=\(reason.rawValue) (\(reason))")
        configsRepository.setIsUserInitStop(isUserInitStop: true)
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel clearing geo routing config")
        Cloak_outlineClearGeoRoutingConf()
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel geo routing clear returned")
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
        logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage bytes=\(messageData.count)")
        if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory")
            let response = "Memory:\(reportMemoryUsageMB())".data(using: .utf8)
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory responseBytes=\(response?.count ?? -1)")
            completionHandler?(response)
        } else {
            if let msg = String(data: messageData, encoding: .utf8) {
                logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage unknown='\(msg)' echo bytes=\(messageData.count)")
            } else {
                logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage nonUtf8 echo bytes=\(messageData.count)")
            }
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
            let ifaces = path.availableInterfaces.map { "\($0.name)[\(self.interfaceTypeKey($0.type))]" }.joined(separator: ",")
            let expensive = path.isExpensive
            let constrained = path.isConstrained
            let signature = "status=\(status) ifaces=[\(ifaces)] expensive=\(expensive) constrained=\(constrained) supportsIPv4=\(path.supportsIPv4) supportsIPv6=\(path.supportsIPv6)"
            if self.lastPathSignature != signature {
                let previous = self.lastPathSignature ?? "(none)"
                self.lastPathSignature = signature
                if previous != "(none)" {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] NETWORK_CHANGED: \(previous) -> \(signature)")
                }
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] PATH_UPDATE \(signature)")
                if expensive && constrained {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] WARNING: path is both expensive and constrained")
                }
                if status == .unsatisfied {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] WARNING: path is unsatisfied")
                }
                for iface in path.availableInterfaces {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] INTERFACE name=\(iface.name) type=\(self.interfaceTypeKey(iface.type)) raw=\(iface.type)")
                }
            }
        }

        monitor.start(queue: q)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }

    private func logInitialNetworkPath(timeout: TimeInterval) {
        let monitor = Network.NWPathMonitor()
        let q = DispatchQueue(label: "vpn.dobby.app.tunnel.startup-path.\(tunnelId)")
        let semaphore = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var captured = false

        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            lock.lock()
            if captured {
                lock.unlock()
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP_NETWORK: duplicate path update ignored")
                return
            }
            captured = true
            lock.unlock()

            let ifaces = path.availableInterfaces.map { "\($0.name):\(self.interfaceTypeKey($0.type))" }.joined(separator: ",")
            self.logs.writeLog(
                log: "[tunnel:\(self.tunnelId)] STARTUP_NETWORK status=\(path.status) ifaces=[\(ifaces)] " +
                    "expensive=\(path.isExpensive) constrained=\(path.isConstrained) " +
                    "supportsIPv4=\(path.supportsIPv4) supportsIPv6=\(path.supportsIPv6)"
            )
            semaphore.signal()
        }

        logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: starting temporary NWPathMonitor timeoutMs=\(Int(timeout * 1000))")
        monitor.start(queue: q)
        if semaphore.wait(timeout: .now() + timeout) == .timedOut {
            logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_WARNING: timed out waiting for initial network path")
        } else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: initial path captured")
        }
        monitor.cancel()
        logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: temporary NWPathMonitor cancelled")
    }

    private func interfaceTypeKey(_ type: Network.NWInterface.InterfaceType) -> String {
        switch type {
        case .wifi:
            return "wifi"
        case .cellular:
            return "cellular"
        case .wiredEthernet:
            return "ethernet"
        case .loopback:
            return "loopback"
        case .other:
            return "other"
        @unknown default:
            return "unknown"
        }
    }

    private func logResourceSnapshot(label: String) {
        let memory = reportMemoryUsageMB()
        memoryHighWaterLock.lock()
        let highWater = memoryHighWaterMarkMB
        memoryHighWaterLock.unlock()
        let uptimeMs = elapsedMs(since: tunnelStartedAt)
        let path = lastPathSignature ?? "(none)"
        logs.writeLog(
            log: "[tunnel:\(tunnelId)] RESOURCE \(label) uptimeMs=\(uptimeMs) " +
                "memoryMB=\(String(format: "%.2f", memory)) " +
                "memoryHighWaterMB=\(String(format: "%.2f", highWater)) " +
                "fd={\(fileDescriptorSummary())} " +
                "path=\(path) interfaces={\(dobbyInterfaceSummary())}"
        )
    }

    private func startResourceSnapshotLogging() {
        stopResourceSnapshotLogging(reason: "restart")

        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue(label: "vpn.dobby.app.tunnel.resources.\(tunnelId)"))
        timer.schedule(deadline: .now() + 1.0, repeating: 1.0)
        timer.setEventHandler { [weak self] in
            self?.logResourceSnapshot(label: "PERIODIC")
        }
        resourceSnapshotTimer = timer
        timer.resume()
        logs.writeLog(log: "[tunnel:\(tunnelId)] resource snapshot timer started intervalMs=1000")
    }

    private func stopResourceSnapshotLogging(reason: String) {
        guard let timer = resourceSnapshotTimer else { return }
        timer.cancel()
        resourceSnapshotTimer = nil
        logs.writeLog(log: "[tunnel:\(tunnelId)] resource snapshot timer stopped reason=\(reason)")
    }

    private func fileDescriptorSummary() -> String {
        let limit: Int32 = 1024
        var openCount = 0
        var socketCount = 0
        var highestOpen: Int32 = -1

        for fd in 0..<limit {
            errno = 0
            if fcntl(fd, F_GETFD, 0) == -1 && errno == EBADF {
                continue
            }

            openCount += 1
            highestOpen = fd

            var socketType: Int32 = 0
            var length = socklen_t(MemoryLayout<Int32>.size)
            if getsockopt(fd, SOL_SOCKET, SO_TYPE, &socketType, &length) == 0 {
                socketCount += 1
            }
        }

        return "open=\(openCount) sockets=\(socketCount) highest=\(highestOpen) scanLimit=\(limit)"
    }

    private func dobbyInterfaceSummary() -> String {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else {
            return "scanFailed errno=\(errno)"
        }
        defer { freeifaddrs(ifaddrPtr) }

        var dobbyMatches: [String] = []
        var vpnInterfaces: [String] = []
        var ptr: UnsafeMutablePointer<ifaddrs>? = first
        while let current = ptr {
            let rawName = String(cString: current.pointee.ifa_name)
            let lowerName = rawName.lowercased()
            let address = addressDescription(current.pointee.ifa_addr)
            let flags = current.pointee.ifa_flags
            let detail = "\(rawName)(\(address),flags=0x\(String(flags, radix: 16)))"

            if isVPNInterfaceName(lowerName) {
                vpnInterfaces.append(detail)
            }
            if address == "198.18.0.1" {
                dobbyMatches.append(detail)
            }
            ptr = current.pointee.ifa_next
        }

        let vpnPrefix = Array(vpnInterfaces.prefix(10)).joined(separator: ",")
        let vpnSuffix = vpnInterfaces.count > 10 ? ",truncated=\(vpnInterfaces.count - 10)" : ""
        let dobby = dobbyMatches.isEmpty ? "none" : dobbyMatches.joined(separator: ",")
        let vpn = vpnInterfaces.isEmpty ? "none" : "\(vpnPrefix)\(vpnSuffix)"
        return "dobbyIPv4=\(dobby) vpnInterfaces=\(vpn)"
    }

    private func isVPNInterfaceName(_ lowerName: String) -> Bool {
        lowerName.contains("utun") ||
            lowerName.contains("tun") ||
            lowerName.contains("tap") ||
            lowerName.contains("ppp") ||
            lowerName.contains("ipsec")
    }

    @MainActor
    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))")
        logResourceSnapshot(label: "TEARDOWN_BEGIN reason=\(reason)")
        stopResourceSnapshotLogging(reason: reason)

        do {
            try cloakInteractor.stopCloak()
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] could not stop cloak: \(error.localizedDescription)")
        }

        do {
            try outlineInteractor.stopOutline()
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] outline stopped")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] could not stop outline: \(error.localizedDescription)")
        }

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] clearing geo routing config")
        Cloak_outlineClearGeoRoutingConf()
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] geo routing clear returned")

        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] clearing tunnel network settings")
            try await self.setTunnelNetworkSettings(nil)
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] cleared tunnel network settings")
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] failed to clear tunnel network settings: \(error.localizedDescription)")
        }

        pathMonitor?.cancel()
        pathMonitor = nil
        lastPathSignature = nil

        logResourceSnapshot(label: "TEARDOWN_END reason=\(reason)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason))")
    }

    /// Extracts the host/IP part from a string like "ip:port" or "[ipv6]:port".
    func extractIP(from serverPort: String) -> String? {
        let host = OutlineInteractor.extractHost(from: serverPort).trimmingCharacters(in: .whitespacesAndNewlines)
        if host.isEmpty {
            logs.writeLog(log: "[DEBUG][Routing] Outline host extraction skipped: serverPort empty")
            return nil
        }
        logs.writeLog(log: "[DEBUG][Routing] Outline host extracted host=\(maskStr(value: host))")
        return host
    }

    /// Extracts `RemoteHost` from Cloak JSON
    func extractRemoteHost(from cloakConfig: String) -> String? {
        guard !cloakConfig.isEmpty else {
            logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction skipped: config empty")
            return nil
        }
        guard let data = cloakConfig.data(using: .utf8) else {
            logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction failed: config is not UTF-8")
            return nil
        }
        do {
            guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction failed: JSON root is not object")
                return nil
            }
            guard let remoteHost = json["RemoteHost"] as? String else {
                logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction skipped: RemoteHost key missing")
                return nil
            }
            let trimmed = remoteHost.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else {
                logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction skipped: RemoteHost empty")
                return nil
            }
            logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extracted host=\(maskStr(value: trimmed))")
            return trimmed
        } catch {
            logs.writeLog(log: "[DEBUG][Routing] Cloak RemoteHost extraction failed: \(error.localizedDescription)")
            return nil
        }
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
        guard ptr != nil else {
            let ntopErrno = errno
            logs.writeLog(
                log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: trimmed)) inet_ntop failed " +
                    "errno=\(ntopErrno) \(String(cString: strerror(ntopErrno))) elapsed=\(elapsedMs(since: start))ms"
            )
            return nil
        }
        let value = String(cString: buffer)
        logs.writeLog(
            log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: trimmed)) ok " +
                "ip=\(maskStr(value: value)) elapsed=\(elapsedMs(since: start))ms"
        )
        return value
    }

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }

    private func resolveIPv4IfNeededWithTimeout(_ host: String, timeout: TimeInterval) -> String? {
        var result: String?
        let sema = DispatchSemaphore(value: 0)
        DispatchQueue.global(qos: .userInitiated).async {
            result = self.resolveIPv4IfNeeded(host)
            sema.signal()
        }
        if sema.wait(timeout: .now() + timeout) == .timedOut {
            logs.writeLog(log: "[DEBUG][Routing] resolving IPv4 host=\(maskStr(value: host)) timed out after \(Int(timeout * 1000))ms")
            return nil
        }
        return result
    }
}
