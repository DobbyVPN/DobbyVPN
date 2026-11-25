import AppKit
import NetworkExtension
import SystemExtensions

private let tunnelID = "vpn.dobby.desktop.PacketTunnel"
private let protectionID = "vpn.dobby.desktop.Protection"

private func hostError(_ message: String) -> NSError {
    NSError(domain: "vpn.dobby.desktop", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}

@MainActor
private final class ExtensionRequest: NSObject, OSSystemExtensionRequestDelegate {
    private var completion: CheckedContinuation<Void, Error>?

    func submit(_ id: String, remove: Bool = false) async throws {
        let request = remove
            ? OSSystemExtensionRequest.deactivationRequest(forExtensionWithIdentifier: id, queue: .main)
            : OSSystemExtensionRequest.activationRequest(forExtensionWithIdentifier: id, queue: .main)
        request.delegate = self
        try await withCheckedThrowingContinuation { continuation in
            completion = continuation
            OSSystemExtensionManager.shared.submitRequest(request)
        }
    }
    nonisolated func requestNeedsUserApproval(_ request: OSSystemExtensionRequest) {
        NSLog("DobbyVPN needs System Settings approval for %@", request.identifier)
    }
    nonisolated func request(_ request: OSSystemExtensionRequest, actionForReplacingExtension existing: OSSystemExtensionProperties,
                             withExtension replacement: OSSystemExtensionProperties) -> OSSystemExtensionRequest.ReplacementAction { .replace }
    nonisolated func request(_ request: OSSystemExtensionRequest, didFinishWithResult result: OSSystemExtensionRequest.Result) {
        Task { @MainActor in
            if result == .completed { completion?.resume() }
            else { completion?.resume(throwing: hostError("Restart the Mac to finish the system extension request")) }
            completion = nil
        }
    }
    nonisolated func request(_ request: OSSystemExtensionRequest, didFailWithError error: Error) {
        Task { @MainActor in completion?.resume(throwing: error); completion = nil }
    }
}

@MainActor
enum MacNetworkExtension {
    static func run(_ args: [String]) -> Never {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        Task { @MainActor in
            do {
                try await perform(args)
                exit(EXIT_SUCCESS)
            } catch {
                let error = error as NSError
                FileHandle.standardError.write(Data("\(error.domain) \(error.code) \(error.localizedDescription)\n".utf8))
                exit(EXIT_FAILURE)
            }
        }
        app.run()
        exit(EXIT_FAILURE)
    }

    private static func ownedVPN() async throws -> NETunnelProviderManager? {
        try await NETunnelProviderManager.loadAllFromPreferences().first {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == tunnelID
        }
    }

    private static func filter() async throws -> NEFilterManager {
        let filter = NEFilterManager.shared()
        try await filter.loadFromPreferences()
        return filter
    }

    private static func owned(_ filter: NEFilterManager) -> Bool {
        filter.providerConfiguration?.filterPacketProviderBundleIdentifier == protectionID
    }

    // Register before asking the OS to change state. Read status on the main
    // actor after each notification, including those queued during the request.
    private static func transition(_ connection: NEVPNConnection, start: Bool) async throws {
        let (updates, continuation) = AsyncStream<Void>.makeStream()
        let token = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange,
            object: connection, queue: .main) { _ in continuation.yield(()) }
        defer {
            NotificationCenter.default.removeObserver(token)
            continuation.finish()
        }
        let terminal: [NEVPNStatus] = [.disconnected, .invalid]
        if start {
            if connection.status == .connected { return }
            try connection.startVPNTunnel()
        } else {
            if terminal.contains(connection.status) { return }
            if connection.status != .disconnecting { connection.stopVPNTunnel() }
        }
        if start && connection.status == .connected || !start && terminal.contains(connection.status) { return }
        for await _ in updates {
            let status = connection.status
            if start && status == .connected || !start && terminal.contains(status) { return }
            if start && terminal.contains(status) { throw hostError("Packet tunnel stopped before activation") }
        }
        throw hostError("Packet tunnel status observation ended")
    }

    private static func arm() async throws {
        let guardManager = try await filter()
        if let current = guardManager.providerConfiguration, current.filterPacketProviderBundleIdentifier != protectionID {
            throw hostError("A different content filter owns these preferences")
        }
        if !owned(guardManager) || !guardManager.isEnabled {
            let config = NEFilterProviderConfiguration()
            config.filterPackets = true
            config.filterSockets = false
            config.filterPacketProviderBundleIdentifier = protectionID
            guardManager.localizedDescription = "DobbyVPN traffic protection"
            guardManager.providerConfiguration = config
            guardManager.isEnabled = true
            try await guardManager.saveToPreferences()
        }
    }

    private static func perform(_ args: [String]) async throws {
        switch args.first {
        case "status":
            let manager = try await filter()
            print(owned(manager) && manager.isEnabled ? "armed" : "idle")
        case "arm":
            try await ExtensionRequest().submit(protectionID)
            try await arm()
        case "disarm":
            let manager = try await filter()
            if owned(manager) && manager.isEnabled {
                manager.isEnabled = false
                try await manager.saveToPreferences()
            }
        case "stop":
            guard args.count == 2 else { throw hostError("Missing connection identity") }
            if let vpn = try await ownedVPN(),
               let config = (vpn.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration,
               config["connection"] as? String == args[1] {
                try await transition(vpn.connection, start: false)
            }
        case "uninstall":
            if let vpn = try await ownedVPN() {
                try await transition(vpn.connection, start: false)
                try await vpn.removeFromPreferences()
            }
            let manager = try await filter()
            if owned(manager) { try await manager.removeFromPreferences() }
            for id in [protectionID, tunnelID] {
                do { try await ExtensionRequest().submit(id, remove: true) }
                catch let error as NSError where error.domain == OSSystemExtensionErrorDomain &&
                    error.code == OSSystemExtensionError.Code.extensionNotFound.rawValue { continue }
            }
        case "connect":
            guard args.count == 4, args[1].count == 32, args[1].allSatisfy(\.isHexDigit),
                  !args[2].isEmpty, UInt64(args[3]) != nil else { throw hostError("Invalid network generation") }
            let request = ExtensionRequest()
            for id in [protectionID, tunnelID] { try await request.submit(id) }
            let vpn = try await ownedVPN() ?? NETunnelProviderManager()
            try await transition(vpn.connection, start: false)
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = tunnelID
            proto.serverAddress = "DobbyVPN"
            proto.includeAllNetworks = true
            proto.excludeLocalNetworks = false
            if #available(macOS 13.3, *) {
                proto.excludeAPNs = false
                proto.excludeCellularServices = false
            }
            if #available(macOS 14.4, *) { proto.excludeDeviceCommunication = false }
            proto.providerConfiguration = ["connection": args[1], "session": args[2], "generation": args[3]]
            vpn.protocolConfiguration = proto
            vpn.localizedDescription = "DobbyVPN"
            vpn.isEnabled = true
            try await vpn.saveToPreferences()
            try await vpn.loadFromPreferences()
            // Both preference approvals precede traffic interruption. The
            // packet provider waits for the independently running filter.
            try await arm()
            try await transition(vpn.connection, start: true)
        case "reconnect":
            guard args.count == 2, let vpn = try await ownedVPN(),
                  (vpn.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration?["connection"] as? String == args[1]
            else { throw hostError("Desktop network generation has retired") }
            let guardManager = try await filter()
            guard owned(guardManager), guardManager.isEnabled else { throw hostError("Independent traffic protection is unavailable") }
            // The control service confirmed this provider's socket disappeared.
            // macOS can still report connected or disconnecting for that process.
            try await transition(vpn.connection, start: false)
            try await transition(vpn.connection, start: true)
        default: throw hostError("Unknown Network Extension operation")
        }
    }
}
