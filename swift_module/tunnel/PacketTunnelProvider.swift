import NetworkExtension
import DobbyVPNRuntime
import os
import app
import CommonDI
import Foundation
import Darwin
import SystemConfiguration
import Network

private final class GomobileProviderSessionClient: IOSProviderSessionClient {
    private let launchID: String

    init(launchID: String) {
        self.launchID = launchID
    }

    func create() throws -> String {
        if let recovered = try? result(DobbyvpnRecoverActiveSession()),
           let sessionID = recovered["session_id"] as? String,
           !sessionID.isEmpty {
            return sessionID
        }
        try string(
            result(DobbyvpnCreateSession()),
            key: "session_id"
        )
    }

    func configure(sessionID: String, rawConfiguration: Data) throws {
        _ = try result(
            DobbyvpnConfigureSession(
                sessionID,
                commandID("configure"),
                rawConfiguration
            )
        )
    }

    func start(sessionID: String) throws -> Int64 {
        try int64(
            result(
                DobbyvpnStartSession(
                    sessionID,
                    commandID("start"),
                    "AUTO_SELECT",
                    0
                )
            ),
            key: "generation"
        )
    }

    func snapshot(sessionID: String) throws -> IOSProviderSessionSnapshot {
        let snapshot = try result(DobbyvpnSnapshotSession(sessionID))
        return IOSProviderSessionSnapshot(
            generation: (snapshot["generation"] as? NSNumber)?.int64Value ?? 0,
            state: snapshot["state"] as? String ?? "",
            cleanupComplete: snapshot["cleanup_complete"] as? Bool ?? false
        )
    }

    func stop(sessionID: String, generation: Int64) throws {
        _ = try result(
            DobbyvpnStopSession(
                sessionID,
                commandID("stop"),
                generation
            )
        )
    }

    func destroy(sessionID: String) throws {
        _ = try result(DobbyvpnDestroySession(sessionID))
    }

    private func commandID(_ operation: String) -> String {
        "ios-\(launchID)-\(operation)-\(UUID().uuidString)"
    }

    private func result(_ payload: String) throws -> [String: Any] {
        guard let data = payload.data(using: .utf8),
              let root = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              root["ok"] as? Bool == true,
              let result = root["result"] as? [String: Any] else {
            throw error("SESSIONAPI_REJECTED")
        }
        return result
    }

    private func string(_ result: [String: Any], key: String) throws -> String {
        guard let value = result[key] as? String, !value.isEmpty else {
            throw error("SESSIONAPI_MALFORMED")
        }
        return value
    }

    private func int64(_ result: [String: Any], key: String) throws -> Int64 {
        guard let number = result[key] as? NSNumber, number.int64Value > 0 else {
            throw error("SESSIONAPI_MALFORMED")
        }
        return number.int64Value
    }

    private func error(_ code: String) -> NSError {
        NSError(
            domain: "PacketTunnelProvider.sessionapi",
            code: -7,
            userInfo: [NSLocalizedDescriptionKey: code]
        )
    }
}

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    private let tunnelId = String(UUID().uuidString.prefix(8))

    // The extension owns a sessionapi process of its own.  The containing app
    // writes the opaque configuration bytes to the App Group before asking
    // NetworkExtension to start; this process is the only one that interprets
    // them (through Go's sessionapi/v2).
    private let sessionRawConfigurationKey = "sessionapi.v2.rawConfiguration"
    private lazy var sessionCoordinator = IOSProviderSessionCoordinator(
        client: GomobileProviderSessionClient(launchID: launchId)
    )

    private var logs = NativeModuleHolder.logsRepository
    private let secrets = SharedKeychainSecretStore.shared
    private var pathMonitor: Network.NWPathMonitor?
    private var lastPathSignature: String?
    private var loadSampler: DispatchSourceTimer?
    private var isProtocolProbeStart = false
    private let memoryHighWaterLock = NSLock()
    private var memoryHighWaterMarkMB = 0.0
    private var tunnelStartedAt = Date()

    private struct MemorySnapshot {
        let physFootprintMB: Double
        let residentMB: Double
        let virtualMB: Double
        let compressedMB: Double
        let highWaterMB: Double
    }

    private struct FileDescriptorSnapshot {
        let open: Int
        let sockets: Int
        let streamSockets: Int
        let datagramSockets: Int
        let otherSockets: Int
        let scannedLimit: Int
        let truncated: Bool
    }

    private struct RUsageSnapshot {
        let userCpuMs: Int64
        let systemCpuMs: Int64
        let maxRssKB: Int64
        let voluntaryContextSwitches: Int64
        let involuntaryContextSwitches: Int64
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

    func reportMemoryUsageMB() -> Double {
        guard let snapshot = memorySnapshot() else {
            logs.writeLog(log: "[Memory] unable to get info")
            return 0.0
        }
        logs.writeLog(
            log: "[Memory] VPN use: \(formatMB(snapshot.physFootprintMB)) MB " +
                "residentMB=\(formatMB(snapshot.residentMB)) " +
                "virtualMB=\(formatMB(snapshot.virtualMB)) " +
                "compressedMB=\(formatMB(snapshot.compressedMB)) " +
                "highWaterMB=\(formatMB(snapshot.highWaterMB))"
        )
        return snapshot.physFootprintMB
    }

    private func memorySnapshot() -> MemorySnapshot? {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.stride / MemoryLayout<natural_t>.stride)

        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }

        if result == KERN_SUCCESS {
            let usedMB = bytesToMB(info.phys_footprint)
            let highWater = memoryHighWaterLock.withLock { () -> Double in
                if usedMB > memoryHighWaterMarkMB { memoryHighWaterMarkMB = usedMB }
                return memoryHighWaterMarkMB
            }
            return MemorySnapshot(
                physFootprintMB: usedMB,
                residentMB: bytesToMB(info.resident_size),
                virtualMB: bytesToMB(info.virtual_size),
                compressedMB: bytesToMB(info.compressed),
                highWaterMB: highWater
            )
        }
        return nil
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
        logs.writeLog(log: "[Interfaces] ========== INTERFACES: \(label) ==========")
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else {
            logs.writeLog(log: "[DEBUG][Interfaces] getifaddrs failed errno=\(errno)")
            logs.writeLog(log: "[Interfaces] ========== INTERFACES: END_\(label) ==========")
            return
        }
        defer {
            freeifaddrs(ifaddrPtr)
            logs.writeLog(log: "[Interfaces] ========== INTERFACES: END_\(label) ==========")
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
        isProtocolProbeStart = (options?["dobbyProtocolProbe"] as? NSNumber)?.boolValue == true
        tunnelStartedAt = Date()
        memoryHighWaterLock.withLock { memoryHighWaterMarkMB = 0 }
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logs.cleanupOldLogs()
        logSystemInfo(osVersionString: osVersionString)
        logs.writeLog(log: "[Interfaces] iOS version: \(osVersionString)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys) isProtocolProbe=\(isProtocolProbeStart)")
        guard let rawConfiguration = secrets.data(for: sessionRawConfigurationKey),
              !rawConfiguration.isEmpty else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] missing opaque sessionapi configuration bytes")
            throw sessionError("CONFIGURATION_UNAVAILABLE")
        }
        logInterfacesDetailed(label: "BEFORE_VPN_TUNNEL")

        // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
        await teardownForStop(reason: "pre-start cleanup")
        guard sessionCoordinator.sessionID == nil else {
            throw sessionError("SESSIONAPI_CLEANUP_PENDING")
        }

        startPathLogging()
        logInitialNetworkPath(timeout: 1.0)
        startLoadSampler()
        // This is a fixed packet-tunnel policy. Go parses the opaque config and
        // owns profile ordering, DNS/routing inputs, probing, failover and all
        // protocol/tun2socks lifecycle decisions.
        let remoteAddress = "254.1.1.1"
        let localAddress = "198.18.0.1"
        let subnetMask = "255.255.0.0"
        let ipv6Address = "fd00:dbb::1"
        let ipv6PrefixLength = 128
        let dnsServers = ["1.1.1.1", "8.8.8.8"]

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: remoteAddress)
        settings.mtu = 1200
        settings.ipv4Settings = NEIPv4Settings(
            addresses: [localAddress],
            subnetMasks: [subnetMask]
        )
        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.ipv6Settings = NEIPv6Settings(
            addresses: [ipv6Address],
            networkPrefixLengths: [NSNumber(value: ipv6PrefixLength)]
        )
        settings.ipv6Settings?.includedRoutes = [NEIPv6Route.default()]
        settings.dnsSettings = NEDNSSettings(servers: dnsServers)
        settings.dnsSettings?.matchDomains = [""]

        logs.writeLog(log: "Settings are ready:")
        logs.writeLog(log: "[tunnel:\(tunnelId)] fixed TUN policy prepared mtu=\(settings.mtu?.stringValue ?? "nil") ipv4=\(localAddress)/\(subnetMask) ipv6=\(ipv6Address)/\(ipv6PrefixLength)")
        do {
            try await self.setTunnelNetworkSettings(settings)
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings failed: \(error.localizedDescription)")
            throw error
        }
        logs.writeLog(log: "Tunnel settings applied")

        logInterfaces()
        logInterfacesDetailed(label: "AFTER_VPN_TUNNEL")

        let path = LogsRepository_iosKt.provideGoLogFilePath().normalized().description()
        logs.writeLog(log: "Starting Go tunnel logger using owner-only local storage")
        guard DobbyvpnInitLogger(path) else {
            logs.writeLog(log: "[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            throw sessionError("LOGGER_INITIALIZATION_FAILED")
        }
        logs.writeLog(log: "service_logger_init result=success state=ready")
        do {
            try await startGoSession(rawConfiguration)
        } catch {
            await teardownForStop(reason: "sessionapi start failed")
            throw error
        }
        logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi start accepted generation=\(sessionCoordinator.generation)")
        logInterfacesDetailed(label: "AFTER_SESSIONAPI_START")
        logResourceSnapshot(label: "AFTER_SESSIONAPI_START")
    }

    /// Commands are kept in the extension process: gomobile exports are
    /// process-local, while the app process merely persists opaque bytes and
    /// asks NetworkExtension to launch us.  This avoids accidentally creating
    /// a second authoritative Go manager in the containing app.
    private func startGoSession(_ rawConfiguration: Data) async throws {
        try await sessionCoordinator.start(
            rawConfiguration: rawConfiguration
        )
    }

    private func stopGoSession(reason: String) async {
        let generation = sessionCoordinator.generation
        do {
            try await sessionCoordinator.stop()
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi stop accepted generation=\(generation) reason=\(reason)")
        } catch {
            // Teardown still clears NetworkExtension settings. The Go manager
            // has generation fencing, so a later process shutdown cannot turn
            // this into a stale reconnect.
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi stop failed reason=\(reason) code=\(error.localizedDescription)")
        }
    }

    private func sessionError(_ code: String) -> NSError {
        NSError(
            domain: "PacketTunnelProvider.sessionapi",
            code: -7,
            userInfo: [NSLocalizedDescriptionKey: code]
        )
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logs.writeLog(log: "[tunnel] stopTunnel teardown=begin")
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
        if let msg = String(data: messageData, encoding: .utf8), msg == "restartActiveProtocol" || msg == "restartActiveProtocol:probe" {
            let isProtocolProbe = msg == "restartActiveProtocol:probe"
            logs.writeLog(log: "[tunnel:\(tunnelId)] handleAppMessage restartActiveProtocol isProtocolProbe=\(isProtocolProbe)")
            Task { @MainActor in
                let ok = await self.restartActiveProtocolFromAppMessage(isProtocolProbe: isProtocolProbe)
                let response = (ok ? "ok" : "error").data(using: .utf8)
                completionHandler?(response)
            }
        } else if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory")
            let response = "Memory:\(reportMemoryUsageMB())".data(using: .utf8)
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory responseBytes=\(response?.count ?? -1)")
            completionHandler?(response)
        } else {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage unknown payload bytes=\(messageData.count)")
            completionHandler?(messageData)
        }
    }

    @MainActor
    private func restartActiveProtocolFromAppMessage(isProtocolProbe: Bool) async -> Bool {
        guard let raw = secrets.data(for: sessionRawConfigurationKey), !raw.isEmpty else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi restart rejected: no raw configuration")
            return false
        }
        // Probe policy is intentionally not selected by Swift. Go receives an
        // AUTO_SELECT command and serializes its own cleanup/failover.
        await stopGoSession(reason: "appMessage restart")
        guard sessionCoordinator.sessionID == nil else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi restart deferred until prior cleanup completes")
            return false
        }
        do {
            try await startGoSession(raw)
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi restart accepted generation=\(sessionCoordinator.generation)")
            return true
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] sessionapi restart rejected code=\(error.localizedDescription)")
            return false
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
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] [Interfaces] NETWORK_CHANGED: \(previous) -> \(signature)")
                }
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] PATH_UPDATE \(signature)")
                if expensive && constrained {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] WARNING: path is both expensive and constrained")
                }
                if status == .unsatisfied {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] WARNING: path is unsatisfied")
                }
                for iface in path.availableInterfaces {
                    self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] [Interfaces] INTERFACE name=\(iface.name) type=\(self.interfaceTypeKey(iface.type)) raw=\(iface.type)")
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
        let uptimeMs = elapsedMs(since: tunnelStartedAt)
        let path = lastPathSignature ?? "(none)"
        logs.writeLog(
            log: "[tunnel:\(tunnelId)] RESOURCE \(label) uptimeMs=\(uptimeMs) " +
                "\(loadSnapshotDetails()) " +
                "path=\(path) interfaces={\(dobbyInterfaceSummary())}"
        )
    }

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }

    private func startLoadSampler() {
        stopLoadSampler(reason: "restart")

        let queue = DispatchQueue(label: "vpn.dobby.app.tunnel.load.\(tunnelId)", qos: .utility)
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 1.0, repeating: 1.0, leeway: .milliseconds(100))
        timer.setEventHandler { [weak self] in
            self?.logResourceSnapshot(label: "PERIODIC")
        }
        loadSampler = timer
        timer.resume()
        logs.writeLog(log: "[tunnel:\(tunnelId)] LOAD_SAMPLER started intervalMs=1000")
    }

    private func stopLoadSampler(reason: String) {
        guard let timer = loadSampler else { return }
        loadSampler = nil
        timer.setEventHandler {}
        timer.cancel()
        logs.writeLog(log: "[tunnel:\(tunnelId)] LOAD_SAMPLER stopped reason=\(reason)")
    }

    private func loadSnapshotDetails() -> String {
        let memoryDetails: String
        if let memory = memorySnapshot() {
            memoryDetails = "memoryMB=\(formatMB(memory.physFootprintMB)) " +
                "residentMB=\(formatMB(memory.residentMB)) " +
                "virtualMB=\(formatMB(memory.virtualMB)) " +
                "compressedMB=\(formatMB(memory.compressedMB)) " +
                "memoryHighWaterMB=\(formatMB(memory.highWaterMB))"
        } else {
            memoryDetails = "memoryMB=unavailable"
        }

        let fds = fileDescriptorSnapshot()
        let fdDetails = "openFDs=\(fds.open) sockets=\(fds.sockets) " +
            "streamSockets=\(fds.streamSockets) datagramSockets=\(fds.datagramSockets) " +
            "otherSockets=\(fds.otherSockets) fdScanLimit=\(fds.scannedLimit) fdScanTruncated=\(fds.truncated)"

        let usageDetails: String
        if let usage = rusageSnapshot() {
            usageDetails = "cpuUserMs=\(usage.userCpuMs) cpuSystemMs=\(usage.systemCpuMs) " +
                "maxRssKB=\(usage.maxRssKB) ctxSwitchVoluntary=\(usage.voluntaryContextSwitches) " +
                "ctxSwitchInvoluntary=\(usage.involuntaryContextSwitches)"
        } else {
            usageDetails = "rusage=unavailable"
        }

        return "\(memoryDetails) threads=\(threadCount()) \(fdDetails) \(usageDetails)"
    }

    private func fileDescriptorSnapshot() -> FileDescriptorSnapshot {
        let reportedLimit = max(0, Int(getdtablesize()))
        let scanLimit = min(reportedLimit, 4096)
        var open = 0
        var sockets = 0
        var streamSockets = 0
        var datagramSockets = 0
        var otherSockets = 0

        for fd in 0..<scanLimit {
            if fcntl(Int32(fd), F_GETFD) == -1 {
                continue
            }

            open += 1
            var socketType: Int32 = 0
            var socketTypeLength = socklen_t(MemoryLayout<Int32>.size)
            if getsockopt(Int32(fd), SOL_SOCKET, SO_TYPE, &socketType, &socketTypeLength) == 0 {
                sockets += 1
                switch socketType {
                case SOCK_STREAM:
                    streamSockets += 1
                case SOCK_DGRAM:
                    datagramSockets += 1
                default:
                    otherSockets += 1
                }
            }
        }

        return FileDescriptorSnapshot(
            open: open,
            sockets: sockets,
            streamSockets: streamSockets,
            datagramSockets: datagramSockets,
            otherSockets: otherSockets,
            scannedLimit: scanLimit,
            truncated: reportedLimit > scanLimit
        )
    }

    private func threadCount() -> Int {
        var threads: thread_act_array_t?
        var count = mach_msg_type_number_t(0)
        let result = task_threads(mach_task_self_, &threads, &count)
        guard result == KERN_SUCCESS, let threads else {
            return -1
        }

        let size = vm_size_t(Int(count) * MemoryLayout<thread_t>.stride)
        vm_deallocate(mach_task_self_, vm_address_t(UInt(bitPattern: threads)), size)
        return Int(count)
    }

    private func rusageSnapshot() -> RUsageSnapshot? {
        var usage = rusage()
        guard getrusage(RUSAGE_SELF, &usage) == 0 else {
            return nil
        }
        return RUsageSnapshot(
            userCpuMs: timevalToMs(usage.ru_utime),
            systemCpuMs: timevalToMs(usage.ru_stime),
            maxRssKB: Int64(usage.ru_maxrss),
            voluntaryContextSwitches: Int64(usage.ru_nvcsw),
            involuntaryContextSwitches: Int64(usage.ru_nivcsw)
        )
    }

    private func timevalToMs(_ value: timeval) -> Int64 {
        Int64(value.tv_sec) * 1000 + Int64(value.tv_usec) / 1000
    }

    private func bytesToMB<T: BinaryInteger>(_ bytes: T) -> Double {
        Double(UInt64(bytes)) / 1024.0 / 1024.0
    }

    private func formatMB(_ value: Double) -> String {
        String(format: "%.2f", value)
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
    private func stopProtocols(reason: String) async {
        // Do not dispatch per-protocol stops here. sessionapi owns protocol,
        // tun2socks, DNS/routing cleanup remains one transactional lease.
        await stopGoSession(reason: reason)
    }

    @MainActor
    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))")
        logResourceSnapshot(label: "TEARDOWN_BEGIN reason=\(reason)")
        stopLoadSampler(reason: reason)
        await stopProtocols(reason: reason)

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

}
