import NetworkExtension
import DobbyVPNRuntime
import os
import IOSIntegration
import Foundation
import Darwin
import SystemConfiguration
import Network

private enum GomobileProviderSessionClient {
    static func call(method: String, params: Data, rawConfiguration: Data?) -> Data {
        let response = DobbyvpnCallSessionJSON(
            method,
            params,
            rawConfiguration ?? Data(),
            rawConfiguration != nil
        )
        return Data(response.utf8)
    }
}

// gomobile emits both an Objective-C protocol and a proxy class with the
// same name. Swift imports the protocol as `DobbyvpnPlatformCallbacksProtocol`
// to disambiguate it from the proxy class; conforming to the class name would
// be interpreted as illegal multiple class inheritance on Simulator builds.
/// Go snapshots are authoritative; callbacks synchronize native tunnel state.
private final class IOSPlatformCallbacks: NSObject, DobbyvpnPlatformCallbacksProtocol {
    private let acquireHandler: (_ sessionID: String?, _ generation: Int64) -> String
    private let releaseHandler: (_ sessionID: String?, _ generation: Int64, _ timeoutMillis: Int64) -> String
    private let stateHandler: (
        _ sessionID: String?,
        _ generation: Int64,
        _ state: String?,
        _ failureCode: String?
    ) -> Void

    init(
        acquireHandler: @escaping (_ sessionID: String?, _ generation: Int64) -> String,
        releaseHandler: @escaping (_ sessionID: String?, _ generation: Int64, _ timeoutMillis: Int64) -> String,
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

    func acquireTunnel(_ sessionID: String?, generation: Int64) -> String {
        acquireHandler(sessionID, generation)
    }

    func releaseTunnel(_ sessionID: String?, generation: Int64, fd: Int32, timeoutMillis: Int64) -> String {
        // Go owns and closes the duplicated descriptor before this callback.
        // The callback remains synchronous so OS routes are removed before Go
        // can publish cleanup-complete IDLE.
        return releaseHandler(sessionID, generation, timeoutMillis)
    }

    func protectSocket(_ sessionID: String?, generation: Int64, fd: Int32) -> Bool {
        // BSD sockets opened by the packet tunnel provider are excluded from
        // its own tunnel by NetworkExtension. This callback remains in the
        // shared platform contract; iOS has no additional socket option.
        return true
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
    let tunnelId = UUID().uuidString

    // The containing app writes opaque configuration bytes to this shared
    // encrypted Keychain mailbox before asking NetworkExtension to start; the
    // provider is the only process that hands them to Go's configuration parser.

    var logs = IOSAppCompositionRoot.logsRepository
    private let secrets = SharedKeychainSecretStore.shared
    let commandQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.session-command", attributes: .concurrent)
    let settingsQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.settings")
    var callbackBridge: AnyObject?
    private func registerPlatform() throws {
        let bridge = IOSPlatformCallbacks(
            acquireHandler: { sessionID, generation in
                self.acquireTunnel(sessionID: sessionID, generation: generation)
            },
            releaseHandler: { sessionID, generation, timeoutMillis in
                self.releaseTunnel(sessionID: sessionID, generation: generation, timeoutMillis: timeoutMillis)
            },
            stateHandler: { sessionID, generation, state, failureCode in
                self.handleGoPublishedState(sessionID: sessionID, generation: generation, state: state, failureCode: failureCode)
            }
        )
        let failure = DobbyvpnRegisterSessionPlatform(bridge)
        guard failure.isEmpty else { throw sessionError(failure) }
        callbackBridge = bridge
    }
    lazy var settingsOwner = TunnelSettingsOwner { [weak self] level, message in
        self?.logs.writeLog(level: level, log: message)
    }

    var pathMonitor: Network.NWPathMonitor?
    var lastPathSignature: String?

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
            logs.writeLog(level: "WARN",
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
            logs.writeLog(level: "WARN",
                log: "[Interfaces] getifaddrs failed errno=\(code) error=\(String(cString: strerror(code)))"
            )
            return
        }
        var ptr = ifaddrPtr
        while ptr != nil {
            if let name = ptr?.pointee.ifa_name {
                let interfaceName = String(cString: name)
                if interfaceName.starts(with: "utun") {
                    logs.writeLog(log: "Active interface: \(interfaceName)")
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
            logs.writeLog(level: "WARN",
                log: "[Interfaces] getifaddrs failed errno=\(code) error=\(String(cString: strerror(code)))"
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

    override func startTunnel(options: [String: NSObject]?) async throws {
        let tid = UInt64(pthread_mach_thread_np(pthread_self()))
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        let optionKeys = options?.keys.sorted().joined(separator: ",") ?? "(none)"
        logSystemInfo(osVersionString: osVersionString)
        logs.writeLog(log: "[Interfaces] iOS version: \(osVersionString)")
        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logInterfacesDetailed(label: "BEFORE_VPN_TUNNEL")

        let path = IOSAppCompositionRoot.goLogFilePath().path
        logs.writeLog(log: "Starting Go tunnel logger using local storage")
        guard DobbyvpnInitLogger(path) else {
            logs.writeLog(level: "ERROR", log: "service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            throw sessionError("LOGGER_INITIALIZATION_FAILED")
        }
        // Initialize diagnostics before retaining OS callbacks. A failed
        // logger must not leave a callback cycle owning a rejected provider.
        try registerPlatform()
        logs.writeLog(log: "[tunnel:\(tunnelId)] control mode ready; waiting for session command")
        startPathLogging()
        logInitialNetworkPath(timeout: 1.0)
        logs.writeLog(log: "service_logger_init result=success state=ready")
        logs.writeLog(log: "[tunnel:\(tunnelId)] control-mode logger ready")
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
            await teardownForStop(
                reason: "stopTunnel(\(reason))",
                completionHandler: completionHandler
            )
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
            logs.writeLog(level: "ERROR", log: "[tunnel:\(tunnelId)] provider command decode failed: \(String(reflecting: error))")
            return VpnManagerImpl.transportFailure("INTERNAL", message: String(reflecting: error))
        }

        let includesSource = command.method == "Configure" || command.method == "StartWithSource"
        var rawConfiguration: Data?
        if includesSource {
            let key = SharedKeychainSecretStore.sessionConfigurationMailboxKey
            guard let bytes = secrets.data(for: key) else {
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] configuration mailbox is unavailable request_id=\(command.requestID)"
                )
                let failure = VpnManagerImpl.transportFailure(
                    "PLATFORM_FAILED",
                    message: "configuration mailbox is unavailable for this request"
                )
                return providerResponse(requestID: command.requestID, kind: .transport, goResponse: failure)
            }
            let mailbox: IOSConfigurationMailbox
            do {
                mailbox = try IOSConfigurationMailbox.decode(bytes)
            } catch {
                logs.writeLog(level: "ERROR",
                    log: "[tunnel:\(tunnelId)] configuration mailbox decode failed " +
                        "request_id=\(command.requestID):\n\(diagnosticErrorDescription(error))"
                )
                let failure = VpnManagerImpl.transportFailure(
                    "PLATFORM_FAILED",
                    message: "configuration mailbox is malformed"
                )
                return providerResponse(requestID: command.requestID, kind: .transport, goResponse: failure)
            }
            guard mailbox.requestID == command.requestID else {
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] configuration mailbox request mismatch " +
                        "command_id=\(command.requestID) mailbox_id=\(mailbox.requestID)"
                )
                let failure = VpnManagerImpl.transportFailure(
                    "PLATFORM_FAILED",
                    message: "configuration mailbox is unavailable for this request"
                )
                return providerResponse(requestID: command.requestID, kind: .transport, goResponse: failure)
            }
            rawConfiguration = mailbox.configuration
        }

        let response = GomobileProviderSessionClient.call(
            method: command.method == "StartWithSource" ? "Start" : command.method,
            params: command.params,
            rawConfiguration: rawConfiguration
        )
        return providerResponse(requestID: command.requestID, kind: .go, goResponse: response)
    }

    private func providerResponse(requestID: String, kind: IOSProviderResponseKind, goResponse: Data) -> Data {
        do {
            let envelope = try IOSProviderResponse(requestID: requestID, kind: kind, payload: goResponse)
            return try envelope.encoded()
        } catch {
            logs.writeLog(level: "ERROR", log: "[tunnel:\(tunnelId)] provider response encoding failed: \(String(reflecting: error))")
            return VpnManagerImpl.transportFailure("PLATFORM_FAILED", message: String(reflecting: error))
        }
    }
}
