import NetworkExtension
import DobbyVPNRuntime
import os
import CommonDI
import Foundation
import Darwin
import SystemConfiguration
import Network

private enum GomobileProviderSessionClient {
    static func configured(sessionID: String, sequence: Int64, rawConfiguration: Data) -> Data {
        Data(DobbyvpnConfigureSession(sessionID, sequence, rawConfiguration).utf8)
    }

    static func started(sessionID: String, sequence: Int64, mode: String, index: Int32) -> Data {
        Data(DobbyvpnStartSession(sessionID, sequence, mode, index).utf8)
    }
}

/// Go snapshots are authoritative; callbacks synchronize native tunnel state.
// gomobile emits both an Objective-C protocol and a proxy class with the
// same name. Swift imports the protocol as `DobbyvpnPlatformCallbacksProtocol`
// to disambiguate it from the proxy class; conforming to the class name would
// be interpreted as illegal multiple class inheritance on Simulator builds.
private final class IOSPlatformCallbacks: NSObject, DobbyvpnPlatformCallbacksProtocol {
    private let acquireHandler: (_ sessionID: String?, _ generation: Int64) -> Int32
    private let releaseHandler: (_ sessionID: String?, _ generation: Int64) -> Bool
    private let stateHandler: (
        _ sessionID: String?,
        _ generation: Int64,
        _ state: String?,
        _ failureCode: String?
    ) -> Void

    init(
        acquireHandler: @escaping (_ sessionID: String?, _ generation: Int64) -> Int32,
        releaseHandler: @escaping (_ sessionID: String?, _ generation: Int64) -> Bool,
        stateHandler: @escaping (
            _ sessionID: String?,
            _ generation: Int64,
            _ state: String?,
            _ failureCode: String?
        ) -> Void
    ) {
        self.acquireHandler = acquireHandler
        self.releaseHandler = releaseHandler
        self.stateHandler = stateHandler
        super.init()
    }

    func acquireTunnel(_ sessionID: String?, generation: Int64) -> Int32 {
        acquireHandler(sessionID, generation)
    }

    func releaseTunnel(_ sessionID: String?, generation: Int64, fd: Int32) -> Bool {
        // Go owns and closes the duplicated descriptor before this callback.
        // The callback remains synchronous so OS routes are removed before Go
        // can publish cleanup-complete IDLE.
        return releaseHandler(sessionID, generation)
    }

    func protectSocket(_ sessionID: String?, generation: Int64, fd: Int32) -> Bool {
        var enabled: Int32 = 1
        return withUnsafePointer(to: &enabled) { value in
            setsockopt(
                fd,
                SOL_SOCKET,
                0x1101,
                value,
                socklen_t(MemoryLayout<Int32>.size)
            ) == 0
        }
    }

    func publishState(
        _ sessionID: String?,
        generation: Int64,
        state: String?,
        failureCode: String?
    ) {
        stateHandler(sessionID, generation, state, failureCode)
    }

    func loadSourceURL() -> String {
        DobbyConfigsRepositoryImpl.shared.getConnectionURL()
    }

    func saveSourceURL(_ value: String?) -> Bool {
        guard let value else { return false }
        return DobbyConfigsRepositoryImpl.shared.setConnectionURL(connectionURL: value)
    }

    func clearSourceURL() -> Bool {
        DobbyConfigsRepositoryImpl.shared.clearConnectionURL()
    }
}

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    private let tunnelId = UUID().uuidString

    // The containing app writes opaque configuration bytes to this shared
    // encrypted Keychain mailbox before asking NetworkExtension to start; the
    // provider is the only process that hands them to Go's configuration parser.
    private let sessionRawConfigurationKey = SharedKeychainSecretStore.sessionConfigurationMailboxKey

    private var logs = IOSAppCompositionRoot.logsRepository
    private let secrets = SharedKeychainSecretStore.shared
    private let commandQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.session-command")
    private let settingsQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.settings")
    private static let settingsOperationTimeout: TimeInterval = 10
    private lazy var callbackBridge = IOSPlatformCallbacks(
        acquireHandler: { [weak self] sessionID, generation in
            self?.acquireTunnel(sessionID: sessionID, generation: generation) ?? -1
        },
        releaseHandler: { [weak self] sessionID, generation in
            self?.releaseTunnel(sessionID: sessionID, generation: generation) ?? false
        },
        stateHandler: { [weak self] sessionID, generation, state, failureCode in
            self?.handleGoPublishedState(
                sessionID: sessionID,
                generation: generation,
                state: state,
                failureCode: failureCode
            )
        }
    )
    private let settingsLock = NSLock()
    private var activeSettingsGeneration: Int64?

    private var pathMonitor: Network.NWPathMonitor?
    private var lastPathSignature: String?

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
        } else {
            let code = errno
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] uname failed errno=\(code) error=\(String(cString: strerror(code)))"
            )
        }

        logs.writeLog(
            log: "[tunnel:\(tunnelId)] OS platform=iOS osVersion=\(osVersionString) " +
                "osDescription=\(processInfo.operatingSystemVersionString) " +
                "process=\(processInfo.processName) kernel=\(sysname) " +
                "kernelRelease=\(release) kernelVersion=\(version) " +
                "machine=\(machine)"
        )
    }

    func logInterfaces() {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0 else {
            let code = errno
            logs.writeLog(
                log: "[Interfaces] getifaddrs failed errno=\(code) error=\(String(cString: strerror(code)))"
            )
            return
        }
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
            let code = errno
            logs.writeLog(
                log: "[DEBUG][Interfaces] getifaddrs failed errno=\(code) error=\(String(cString: strerror(code)))"
            )
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
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logSystemInfo(osVersionString: osVersionString)
        logs.writeLog(log: "[Interfaces] iOS version: \(osVersionString)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logInterfacesDetailed(label: "BEFORE_VPN_TUNNEL")

        // The provider first starts in control mode. No routes, DNS settings,
        // or Go session are installed here, so configure cannot black-hole
        // traffic and NetworkExtension status cannot become product state.
        _ = settingsQueue.sync {
            runSettingsOperation {
                try await self.setTunnelNetworkSettings(nil)
            }
        }
        DobbyvpnRegisterSessionPlatform(callbackBridge)
        logs.writeLog(log: "[tunnel:\(tunnelId)] control mode ready; waiting for session command")

        startPathLogging()
        logInitialNetworkPath(timeout: 1.0)
        let path = IOSAppCompositionRoot.goLogFilePath().path
        logs.writeLog(log: "Starting Go tunnel logger using local storage")
        guard DobbyvpnInitLogger(path) else {
            logs.writeLog(log: "[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            throw sessionError("LOGGER_INITIALIZATION_FAILED")
        }
        logs.writeLog(log: "service_logger_init result=success state=ready")
        logs.writeLog(log: "[tunnel:\(tunnelId)] control-mode logger ready")
    }

    private func stopGoSession(reason: String) async {
        // Stop is sent by the app before the provider is
        // stopped. An unexpected provider stop has no safe generation to
        // invent, so it only tears down NetworkExtension state.
        logs.writeLog(log: "[tunnel:\(tunnelId)] provider stop reason=\(reason); Go owns any recorded cleanup")
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
            logs.writeLog(log: "[tunnel:\(tunnelId)] cancelTunnelWithError: \(String(reflecting: error))")
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
        commandQueue.async { [weak self] in
            guard let self else { completionHandler?(nil); return }
            completionHandler?(self.dispatchProviderCommand(messageData))
        }
    }

    private func dispatchProviderCommand(_ messageData: Data) -> Data {
        let command: IOSProviderCommand
        do {
            command = try IOSProviderCommand.decode(messageData)
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] provider command decode failed: \(String(reflecting: error))")
            return VpnManagerImpl.transportFailure("INTERNAL", message: String(reflecting: error))
        }

        let outcome: (payload: Data, kind: IOSProviderResponseKind)
        switch command.operation {
        case .configure:
            outcome = configure(command)
        case .start:
            outcome = start(command)
        case .snapshot:
            outcome = (Data(DobbyvpnSnapshotSession(command.sessionID ?? "").utf8), .go)
        case .stop:
            outcome = (Data(DobbyvpnStopSession(command.sessionID ?? "", command.generation ?? 0).utf8), .go)
        case .reset:
            outcome = (Data(DobbyvpnResetSession(command.sessionID ?? "", command.expectedSequence ?? 0).utf8), .go)
        }
        return providerResponse(requestID: command.requestID, kind: outcome.kind, goResponse: outcome.payload)
    }

    private func providerResponse(requestID: String, kind: IOSProviderResponseKind, goResponse: Data) -> Data {
        do {
            let envelope = try IOSProviderResponse(requestID: requestID, kind: kind, payload: goResponse)
            return try envelope.encoded()
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] provider response encoding failed: \(String(reflecting: error))")
            return VpnManagerImpl.transportFailure("PLATFORM_FAILED", message: String(reflecting: error))
        }
    }

    private func configure(_ command: IOSProviderCommand) -> (payload: Data, kind: IOSProviderResponseKind) {
        guard let rawConfiguration = secrets.data(for: sessionRawConfigurationKey) else {
            return (
                VpnManagerImpl.transportFailure(
                    "PLATFORM_FAILED",
                    message: "configuration mailbox is unavailable"
                ),
                .transport
            )
        }
        guard !rawConfiguration.isEmpty else {
            return (
                VpnManagerImpl.transportFailure(
                    "PLATFORM_FAILED",
                    message: "configuration mailbox is empty"
                ),
                .transport
            )
        }
        let response = GomobileProviderSessionClient.configured(
            sessionID: command.sessionID ?? "",
            sequence: command.expectedSequence ?? 0,
            rawConfiguration: rawConfiguration
        )
        // The containing app consumes the mailbox after it receives this Go
        // result, success or typed failure.
        return (response, .go)
    }

    private func start(_ command: IOSProviderCommand) -> (payload: Data, kind: IOSProviderResponseKind) {
        // Go owns the transition into PROBING/PREPARING. Fixed routes are
        // installed only by the Go-owned AcquireTunnel callback after Go
        // has selected a generation and requested its packet-flow FD.
        let response = GomobileProviderSessionClient.started(
            sessionID: command.sessionID ?? "",
            sequence: command.expectedSequence ?? 0,
            mode: command.mode ?? "",
            index: command.index ?? 0
        )
        return (response, .go)
    }

    private func markSettingsCleared() {
        settingsLock.lock()
        activeSettingsGeneration = nil
        settingsLock.unlock()
    }

    private func handleGoPublishedState(
        sessionID: String?,
        generation: Int64,
        state: String?,
        failureCode: String?
    ) {
        _ = sessionID
        // IDLE is a positive Go cleanup completion signal. A FAILED event
        // is clearable only when its failure is not cleanup
        // failure; CLEANUP_FAILED keeps the failure state intact.
        let cleanupCompleted = state == "IDLE" ||
            (state == "FAILED" && failureCode != "CLEANUP_FAILED")
        guard cleanupCompleted else { return }
        commandQueue.async { [weak self] in
            guard let self else { return }
            // Keep commandQueue fenced until the serialized settings queue
            // confirms the clear. A later Start therefore cannot race it.
            self.clearFixedSettingsAfterGoCleanup(generation: generation, state: state ?? "UNKNOWN")
        }
    }

    private func clearFixedSettingsAfterGoCleanup(generation: Int64, state: String) {
        settingsQueue.sync {
            settingsLock.lock()
            let activeGeneration = activeSettingsGeneration
            let hasSettings = activeSettingsGeneration != nil
            settingsLock.unlock()
            guard hasSettings else { return }
            // If a later Start has already acquired a different generation,
            // this delayed callback must not clear those routes.
            if let activeGeneration, activeGeneration != generation { return }
            _ = clearSettingsOnCurrentQueue(reason: "Go \(state) generation=\(generation)")
        }
    }

    private final class SettingsOperationResult {
        var succeeded = false
        var error: Error?
    }

    /// AcquireTunnel is the first point where Go owns a concrete generation.
    /// Install routes immediately before duplicating that generation's TUN;
    /// PROBING and failed Start therefore run without a routing black hole.
    private func acquireTunnel(sessionID: String?, generation: Int64) -> Int32 {
        _ = sessionID
        return settingsQueue.sync {
            guard applyFixedTunnelSettings(generation: generation) else { return -1 }
            let rawDescriptor = DobbyvpnGetTunnelFileDescriptor()
            guard rawDescriptor >= 0, rawDescriptor <= Int(Int32.max) else {
                logs.writeLog(log: "[tunnel:\(tunnelId)] AcquireTunnel received invalid descriptor=\(rawDescriptor) generation=\(generation)")
                _ = clearSettingsOnCurrentQueue(reason: "TUN descriptor unavailable")
                return -1
            }
            let duplicated = dup(Int32(rawDescriptor))
            guard duplicated >= 0 else {
                let code = errno
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] TUN descriptor duplication failed errno=\(code) error=\(String(cString: strerror(code))) generation=\(generation)"
                )
                _ = clearSettingsOnCurrentQueue(reason: "TUN descriptor duplication failed")
                return -1
            }
            return duplicated
        }
    }

    /// Go closes its descriptor before this callback. Keep the callback
    /// synchronous so fixed routes are gone before Go emits cleanup-complete
    /// IDLE and before a subsequent generation can be acquired.
    private func releaseTunnel(sessionID: String?, generation: Int64) -> Bool {
        _ = sessionID
        return settingsQueue.sync {
            settingsLock.lock()
            let activeGeneration = activeSettingsGeneration
            settingsLock.unlock()
            guard activeGeneration == nil || activeGeneration == generation else {
                logs.writeLog(log: "[tunnel:\(tunnelId)] ReleaseTunnel generation mismatch requested=\(generation) active=\(activeGeneration ?? -1)")
                return false
            }
            return clearSettingsOnCurrentQueue(reason: "Go ReleaseTunnel")
        }
    }

    /// This function must run on settingsQueue, which serializes it with
    /// release/terminal cleanup. It returns false on a bounded NetworkExtension
    /// failure.
    private func applyFixedTunnelSettings(generation: Int64) -> Bool {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "254.1.1.1")
        settings.mtu = 1200
        settings.ipv4Settings = NEIPv4Settings(
            addresses: ["198.18.0.1"],
            subnetMasks: ["255.255.0.0"]
        )
        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.ipv6Settings = NEIPv6Settings(
            addresses: ["fd00:dbb::1"],
            networkPrefixLengths: [NSNumber(value: 128)]
        )
        settings.ipv6Settings?.includedRoutes = [NEIPv6Route.default()]
        settings.dnsSettings = NEDNSSettings(servers: ["1.1.1.1", "8.8.8.8"])
        settings.dnsSettings?.matchDomains = [""]
        guard runSettingsOperation({
            try await self.setTunnelNetworkSettings(settings)
        }) else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] failed to apply fixed settings before AcquireTunnel generation=\(generation)")
            return false
        }
        settingsLock.lock()
        activeSettingsGeneration = generation
        settingsLock.unlock()
        logs.writeLog(log: "[tunnel:\(tunnelId)] fixed tunnel settings applied at Go AcquireTunnel generation=\(generation)")
        logInterfaces()
        logInterfacesDetailed(label: "AFTER_VPN_TUNNEL")
        return true
    }

    /// Must run on settingsQueue. Every route clear is serialized with an
    /// AcquireTunnel installation.
    @discardableResult
    private func clearSettingsOnCurrentQueue(reason: String) -> Bool {
        let succeeded = runSettingsOperation {
            try await self.setTunnelNetworkSettings(nil)
        }
        if succeeded {
            markSettingsCleared()
            logs.writeLog(log: "[tunnel:\(tunnelId)] fixed settings cleared reason=\(reason)")
        } else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] fixed settings cleanup failed reason=\(reason)")
        }
        return succeeded
    }

    /// Runs an async NetworkExtension operation from the synchronous Go callback.
    private func runSettingsOperation(_ operation: @escaping () async throws -> Void) -> Bool {
        let completion = DispatchSemaphore(value: 0)
        let result = SettingsOperationResult()
        let task = Task {
            do {
                try await operation()
                result.succeeded = true
            } catch {
                result.error = error
            }
            completion.signal()
        }
        guard completion.wait(timeout: .now() + Self.settingsOperationTimeout) == .success else {
            task.cancel()
            logs.writeLog(log: "[tunnel:\(tunnelId)] NetworkExtension settings operation timed out after \(Int(Self.settingsOperationTimeout))s")
            return false
        }
        if let error = result.error {
            logs.writeLog(log: "[tunnel:\(tunnelId)] NetworkExtension settings operation failed: \(String(reflecting: error))")
        }
        return result.succeeded
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

    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))")
        await stopGoSession(reason: reason)

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] clearing tunnel network settings")
        _ = settingsQueue.sync { clearSettingsOnCurrentQueue(reason: "provider stop") }

        pathMonitor?.cancel()
        pathMonitor = nil
        lastPathSignature = nil

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason))")
    }

}
