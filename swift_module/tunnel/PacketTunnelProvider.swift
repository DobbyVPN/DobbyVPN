import NetworkExtension
import DobbyVPNRuntime
import os
import app
import CommonDI
import Foundation
import Darwin
import SystemConfiguration
import Network
import CryptoKit
import CoreFoundation

private enum GomobileProviderSessionClient {
    static func configured(sessionID: String, rawConfiguration: Data, requestID: String) -> Data {
        Data(DobbyvpnConfigureSession(sessionID, requestID, rawConfiguration).utf8)
    }

    static func started(sessionID: String, requestID: String, mode: String, index: Int32) -> Data {
        Data(DobbyvpnStartSession(sessionID, requestID, mode, index).utf8)
    }
}

/// The Go callback is deliberately content-free. The ordered event payload is
/// retained by Go and is fetched through Observe; this signal only wakes any
/// extension-local observer and cannot leak profile or configuration data.
private final class IOSPlatformCallbacks: NSObject, DobbyvpnPlatformCallbacks {
    private let acquireHandler: (_ sessionID: String?, _ generation: Int64) -> Int32
    private let releaseHandler: (_ sessionID: String?, _ generation: Int64) -> Bool
    private let stateHandler: (
        _ sessionID: String?,
        _ generation: Int64,
        _ sequence: Int64,
        _ state: String?,
        _ failureCode: String?
    ) -> Void

    init(
        acquireHandler: @escaping (_ sessionID: String?, _ generation: Int64) -> Int32,
        releaseHandler: @escaping (_ sessionID: String?, _ generation: Int64) -> Bool,
        stateHandler: @escaping (
            _ sessionID: String?,
            _ generation: Int64,
            _ sequence: Int64,
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
        sequence: Int64,
        state: String?,
        profileIndex: Int32,
        profileProtocol: String?,
        failureCode: String?
    ) {
        NotificationCenter.default.post(name: .iosSessionEventAvailable, object: nil)
        let name = CFNotificationName(rawValue: IOSDarwinEventSink.notificationName as CFString)
        CFNotificationCenterPostNotification(
            CFNotificationCenterGetDarwinNotifyCenter(),
            name,
            nil,
            nil,
            true
        )
        stateHandler(sessionID, generation, sequence, state, failureCode)
    }
}

class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    private let tunnelId = String(UUID().uuidString.prefix(8))

    // The containing app writes opaque configuration bytes to this shared
    // encrypted Keychain mailbox before asking NetworkExtension to start; the
    // provider is the only process that hands them to Go's SessionV2 parser.
    private let sessionRawConfigurationKey = SharedKeychainSecretStore.sessionConfigurationMailboxKey

    private var logs = NativeModuleHolder.logsRepository
    private let secrets = SharedKeychainSecretStore.shared
    private let commandQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.session-command")
    private let settingsQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.settings")
    private static let settingsOperationTimeout: TimeInterval = 10
    private let settingsFence = IOSSettingsOperationFence()
    private lazy var callbackBridge = IOSPlatformCallbacks(
        acquireHandler: { [weak self] sessionID, generation in
            self?.acquireTunnel(sessionID: sessionID, generation: generation) ?? -1
        },
        releaseHandler: { [weak self] sessionID, generation in
            self?.releaseTunnel(sessionID: sessionID, generation: generation) ?? false
        },
        stateHandler: { [weak self] sessionID, generation, sequence, state, failureCode in
            self?.handleGoPublishedState(
                sessionID: sessionID,
                generation: generation,
                sequence: sequence,
                state: state,
                failureCode: failureCode
            )
        }
    )
    private let responseLock = NSLock()
    private var responseCache: [String: Data] = [:]
    private var responseOperations: [String: String] = [:]
    private var responseDigests: [String: Data] = [:]
    private var responseOrder: [String] = []
    private var inFlightRequests: [String: (operation: String, digest: Data)] = [:]
    private let settingsLock = NSLock()
    private var settingsEpoch: UInt64 = 0
    private var activeSettingsEpoch: UInt64?
    private var activeSettingsGeneration: Int64?
    private var settingsCleanupFailed = false

    private final class DataBox {
        var value: Data?
    }
    private var pathMonitor: Network.NWPathMonitor?
    private var lastPathSignature: String?
    private var loadSampler: DispatchSourceTimer?
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
        tunnelStartedAt = Date()
        memoryHighWaterLock.withLock { memoryHighWaterMarkMB = 0 }
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logs.cleanupOldLogs()
        logSystemInfo(osVersionString: osVersionString)
        logs.writeLog(log: "[Interfaces] iOS version: \(osVersionString)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logInterfacesDetailed(label: "BEFORE_VPN_TUNNEL")

        // The provider first starts in control mode. No routes, DNS settings,
        // or Go session are installed here, so configure cannot black-hole
        // traffic and NetworkExtension status cannot become product state.
        let clearedStaleSettings = settingsQueue.sync {
            runSettingsOperation {
                try await self.setTunnelNetworkSettings(nil)
            }
        }
        guard clearedStaleSettings else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] failed to clear stale control-mode settings; refusing provider readiness")
            throw sessionError("NETWORK_SETTINGS_CLEANUP_FAILED")
        }
        markSettingsCleared()
        DobbyvpnRegisterSessionPlatform(callbackBridge)
        logs.writeLog(log: "[tunnel:\(tunnelId)] control mode ready; waiting for authenticated SessionV2 command")

        startPathLogging()
        logInitialNetworkPath(timeout: 1.0)
        startLoadSampler()
        let path = LogsRepository_iosKt.provideGoLogFilePath().normalized().description()
        logs.writeLog(log: "Starting Go tunnel logger using owner-only local storage")
        guard DobbyvpnInitLogger(path) else {
            logs.writeLog(log: "[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            throw sessionError("LOGGER_INITIALIZATION_FAILED")
        }
        logs.writeLog(log: "service_logger_init result=success state=ready")
        logs.writeLog(log: "[tunnel:\(tunnelId)] control-mode logger ready")
    }

    private func stopGoSession(reason: String) async {
        // Ordinary Stop and Destroy are sent by the app before the provider is
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
        // Keep the provider message path fixed and authenticated. The command
        // bytes themselves are never logged because they may contain request
        // identifiers and are a control credential boundary.
        commandQueue.async { [weak self] in
            guard let self else { completionHandler?(nil); return }
            let semaphore = DispatchSemaphore(value: 0)
            let box = DataBox()
            Task {
                box.value = await self.dispatchProviderCommand(messageData)
                semaphore.signal()
            }
            if semaphore.wait(timeout: .now() + IOSProviderTiming.providerCommandCompletionTimeout) == .timedOut {
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] provider command completion timed out after \(Int(IOSProviderTiming.providerCommandCompletionTimeout))s")
                completionHandler?(self.timeoutResponse(for: messageData))
                return
            }
            completionHandler?(box.value)
        }
    }

    private func timeoutResponse(for messageData: Data) -> Data {
        guard let secret = secrets.data(for: SharedKeychainSecretStore.sessionBridgeHMACKey),
              let command = try? IOSProviderCommand.decode(messageData, using: secret) else {
            return VpnManagerImpl.transportFailure("SESSIONAPI_TIMEOUT")
        }
        return authenticatedProviderResponse(
            requestID: command.requestID,
            kind: .transport,
            goResponse: VpnManagerImpl.transportFailure("SESSIONAPI_TIMEOUT")
        )
    }

    private func dispatchProviderCommand(_ messageData: Data) async -> Data {
        guard let secret = secrets.data(for: SharedKeychainSecretStore.sessionBridgeHMACKey) else {
            return VpnManagerImpl.transportFailure(IOSProviderMessageError.unauthenticated.rawValue)
        }
        guard let command = try? IOSProviderCommand.decode(messageData, using: secret) else {
            return VpnManagerImpl.transportFailure(IOSProviderMessageError.malformed.rawValue)
        }
        guard !settingsFence.isPoisoned else {
            return authenticatedProviderResponse(
                requestID: command.requestID,
                kind: .transport,
                goResponse: VpnManagerImpl.transportFailure("SESSIONAPI_PROVIDER_POISONED")
            )
        }
        let requestDigest = Data(SHA256.hash(data: messageData))

        responseLock.lock()
        if let cached = responseCache[command.requestID] {
            let sameCommand = responseOperations[command.requestID] == command.operation.rawValue &&
                responseDigests[command.requestID] == requestDigest
            responseLock.unlock()
            return sameCommand ? cached : authenticatedProviderResponse(
                requestID: command.requestID,
                kind: .transport,
                goResponse: VpnManagerImpl.transportFailure("CONFLICT")
            )
        }
        if let inFlight = inFlightRequests[command.requestID] {
            responseLock.unlock()
            let sameCommand = inFlight.operation == command.operation.rawValue && inFlight.digest == requestDigest
            return authenticatedProviderResponse(
                requestID: command.requestID,
                kind: .transport,
                goResponse: VpnManagerImpl.transportFailure(sameCommand ? "SESSIONAPI_IN_FLIGHT" : "CONFLICT")
            )
        }
        inFlightRequests[command.requestID] = (command.operation.rawValue, requestDigest)
        responseLock.unlock()
        defer {
            responseLock.lock()
            inFlightRequests.removeValue(forKey: command.requestID)
            responseLock.unlock()
        }

        let outcome: (payload: Data, kind: IOSProviderResponseKind)
        switch command.operation {
        case .create:
            outcome = (Data(DobbyvpnCreateSession().utf8), .go)
        case .recover:
            outcome = (Data(DobbyvpnRecoverActiveSession().utf8), .go)
        case .configure:
            outcome = await configure(command)
        case .start:
            outcome = await start(command)
        case .snapshot:
            outcome = (Data(DobbyvpnSnapshotSession(command.sessionID ?? "").utf8), .go)
        case .observe:
            if let afterSequence = command.afterSequence {
                outcome = (Data(DobbyvpnObserveSession(command.sessionID ?? "", afterSequence).utf8), .go)
            } else {
                outcome = (VpnManagerImpl.transportFailure(IOSProviderMessageError.malformed.rawValue), .transport)
            }
        case .stop:
            outcome = (Data(DobbyvpnStopSession(command.sessionID ?? "", command.requestID, command.generation ?? 0).utf8), .go)
        case .destroy:
            outcome = (Data(DobbyvpnDestroySession(command.sessionID ?? "").utf8), .go)
        }
        let response = authenticatedProviderResponse(requestID: command.requestID, kind: outcome.kind, goResponse: outcome.payload)

        responseLock.lock()
        responseCache[command.requestID] = response
        responseOperations[command.requestID] = command.operation.rawValue
        responseDigests[command.requestID] = requestDigest
        responseOrder.append(command.requestID)
        while responseOrder.count > 64 {
            let old = responseOrder.removeFirst()
            responseCache.removeValue(forKey: old)
            responseOperations.removeValue(forKey: old)
            responseDigests.removeValue(forKey: old)
        }
        responseLock.unlock()
        return response
    }

    private func authenticatedProviderResponse(requestID: String, kind: IOSProviderResponseKind, goResponse: Data) -> Data {
        guard let secret = secrets.data(for: SharedKeychainSecretStore.sessionBridgeHMACKey),
              let envelope = try? IOSProviderResponse(requestID: requestID, kind: kind, payload: goResponse),
              let encoded = try? envelope.encoded(using: secret) else {
            // A valid command could not be wrapped only if shared Keychain
            // state disappeared during the provider lifetime. The app will
            // reject this unauthenticated transport response and retain any
            // sensitive mailbox for recovery.
            return VpnManagerImpl.transportFailure("SESSIONAPI_RESPONSE_UNAVAILABLE")
        }
        return encoded
    }

    private func configure(_ command: IOSProviderCommand) async -> (payload: Data, kind: IOSProviderResponseKind) {
        guard let rawConfiguration = secrets.data(for: sessionRawConfigurationKey) else {
            return (VpnManagerImpl.transportFailure("CONFIGURATION_UNAVAILABLE"), .transport)
        }
        guard !rawConfiguration.isEmpty else {
            return (VpnManagerImpl.transportFailure("CONFIGURATION_UNAVAILABLE"), .transport)
        }
        guard rawConfiguration.count <= IOSProviderCommand.maximumConfigurationBytes else {
            return (VpnManagerImpl.transportFailure("SESSIONAPI_CONFIGURATION_TOO_LARGE"), .transport)
        }
        let response = GomobileProviderSessionClient.configured(
            sessionID: command.sessionID ?? "",
            rawConfiguration: rawConfiguration,
            requestID: command.requestID
        )
        // The containing app consumes the mailbox after it receives this
        // authenticated Go result, success or typed failure. Keeping it until
        // then lets a provider crash or a lost response be retried safely with
        // the same Go command ID.
        return (response, .go)
    }

    private func start(_ command: IOSProviderCommand) async -> (payload: Data, kind: IOSProviderResponseKind) {
        do {
            // A prior cleanup failure leaves the old settings fence intact.
            // Retry clearing them before accepting a new generation; this is
            // deliberately serialized with Start on commandQueue.
            try retryFailedSettingsCleanupIfNeeded()
            // Go owns the transition into PROBING/PREPARING. Fixed routes are
            // installed only by the Go-owned AcquireTunnel callback after Go
            // has selected a generation and requested its packet-flow FD.
            let response = GomobileProviderSessionClient.started(
                sessionID: command.sessionID ?? "",
                requestID: command.requestID,
                mode: command.mode ?? "",
                index: command.index ?? 0
            )
            // A rejected Start never acquired a TUN, so no fixed routes were
            // installed by this command and no synthetic cleanup is needed.
            return (response, .go)
        } catch {
            logs.writeLog(log: "[tunnel:\(tunnelId)] Go start dispatch failed: \(error.localizedDescription)")
            return (VpnManagerImpl.transportFailure("PLATFORM_FAILED"), .transport)
        }
    }

    private func retryFailedSettingsCleanupIfNeeded() throws {
        settingsLock.lock()
        let failed = settingsCleanupFailed
        settingsLock.unlock()
        guard failed else { return }
        try clearSettingsSynchronously()
        markSettingsCleared()
        logs.writeLog(log: "[tunnel:\(tunnelId)] recovered a prior settings cleanup failure before Start")
    }

    private func markSettingsCleanupFailed() {
        settingsLock.lock()
        settingsCleanupFailed = true
        settingsLock.unlock()
    }

    private func markSettingsCleared() {
        settingsLock.lock()
        activeSettingsEpoch = nil
        activeSettingsGeneration = nil
        settingsCleanupFailed = false
        settingsLock.unlock()
    }

    private func handleGoPublishedState(
        sessionID: String?,
        generation: Int64,
        sequence: Int64,
        state: String?,
        failureCode: String?
    ) {
        _ = sessionID
        _ = sequence
        // IDLE and DESTROYED are positive Go cleanup completion signals. A
        // FAILED event is clearable only when its failure is not cleanup
        // failure; CLEANUP_FAILED deliberately keeps the fence and routes
        // until an explicit retry can prove they were removed.
        let cleanupCompleted = state == "IDLE" || state == "DESTROYED" ||
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
            let hasSettings = activeSettingsEpoch != nil
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
        guard !settingsFence.isPoisoned else { return -1 }
        return settingsQueue.sync {
            guard applyFixedTunnelSettings(generation: generation) else { return -1 }
            let rawDescriptor = DobbyvpnGetTunnelFileDescriptor()
            guard rawDescriptor >= 0, rawDescriptor <= Int(Int32.max) else {
                _ = clearSettingsOnCurrentQueue(reason: "TUN descriptor unavailable")
                return -1
            }
            let duplicated = dup(Int32(rawDescriptor))
            guard duplicated >= 0 else {
                _ = clearSettingsOnCurrentQueue(reason: "TUN descriptor duplication failed")
                return -1
            }
            return duplicated
        }
    }

    /// Go closes its descriptor before this callback. Keep the callback
    /// synchronous so fixed routes are gone before Go emits cleanup-complete
    /// IDLE/DESTROYED and before a subsequent generation can be acquired.
    private func releaseTunnel(sessionID: String?, generation: Int64) -> Bool {
        _ = sessionID
        if settingsFence.isPoisoned { return false }
        return settingsQueue.sync {
            settingsLock.lock()
            let activeGeneration = activeSettingsGeneration
            settingsLock.unlock()
            guard activeGeneration == nil || activeGeneration == generation else { return false }
            return clearSettingsOnCurrentQueue(reason: "Go ReleaseTunnel")
        }
    }

    /// This function must run on settingsQueue, which serializes it with
    /// release/terminal cleanup. It returns false on a bounded NetworkExtension
    /// failure and leaves the conservative cleanup-failed fence set.
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
        guard runSettingsOperation {
            try await self.setTunnelNetworkSettings(settings)
        } else {
            markSettingsCleanupFailed()
            logs.writeLog(log: "[tunnel:\(tunnelId)] failed to apply fixed settings before AcquireTunnel generation=\(generation)")
            return false
        }
        settingsLock.lock()
        settingsEpoch &+= 1
        activeSettingsEpoch = settingsEpoch
        activeSettingsGeneration = generation
        settingsCleanupFailed = false
        settingsLock.unlock()
        logs.writeLog(log: "[tunnel:\(tunnelId)] fixed tunnel settings applied at Go AcquireTunnel generation=\(generation)")
        logInterfaces()
        logInterfacesDetailed(label: "AFTER_VPN_TUNNEL")
        return true
    }

    private func clearSettingsSynchronously() throws {
        guard settingsQueue.sync(execute: { clearSettingsOnCurrentQueue(reason: "retry") }) else {
            throw sessionError("NETWORK_SETTINGS_CLEANUP_FAILED")
        }
    }

    /// Must run on settingsQueue. Every route clear is serialized with an
    /// AcquireTunnel installation, and failed clears block a new Start until a
    /// later bounded retry proves NetworkExtension accepted the clear.
    @discardableResult
    private func clearSettingsOnCurrentQueue(reason: String) -> Bool {
        let succeeded = runSettingsOperation {
            try await self.setTunnelNetworkSettings(nil)
        }
        if succeeded {
            markSettingsCleared()
            logs.writeLog(log: "[tunnel:\(tunnelId)] fixed settings cleared reason=\(reason)")
        } else {
            markSettingsCleanupFailed()
            logs.writeLog(log: "[tunnel:\(tunnelId)] fixed settings cleanup failed reason=\(reason)")
        }
        return succeeded
    }

    /// Runs an async NetworkExtension operation from the synchronous Go
    /// callback boundary with a strict bound. Cancellation is best-effort; a
    /// timeout therefore poisons and terminates this provider before any later
    /// command can start another settings operation.
    private func runSettingsOperation(_ operation: @escaping () async throws -> Void) -> Bool {
        guard let operationEpoch = settingsFence.begin() else { return false }
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
            settingsFence.poison()
            task.cancel()
            logs.writeLog(log: "[tunnel:\(tunnelId)] NetworkExtension settings operation timed out after \(Int(Self.settingsOperationTimeout))s")
            // Cancellation is best-effort. Poison the command boundary and
            // terminate this provider process so a late NetworkExtension
            // completion cannot be followed by another command or Start.
            cancelTunnelWithError(sessionError("NETWORK_SETTINGS_OPERATION_TIMEOUT"))
            return false
        }
        if let error = result.error {
            logs.writeLog(log: "[tunnel:\(tunnelId)] NetworkExtension settings operation failed: \(error.localizedDescription)")
        }
        guard settingsFence.canCommit(operationEpoch) else { return false }
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

    private func teardownForStop(reason: String) async {
        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] begin (\(reason))")
        logResourceSnapshot(label: "TEARDOWN_BEGIN reason=\(reason)")
        stopLoadSampler(reason: reason)
        await stopGoSession(reason: reason)

        do {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] clearing tunnel network settings")
            try clearSettingsSynchronously()
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
