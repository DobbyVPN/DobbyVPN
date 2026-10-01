import Darwin
import DobbyVPNRuntime
import Foundation
import IOSIntegration
import Network
import NetworkExtension

extension PacketTunnelProvider {
    private func stopGoSession(reason: String) async {
        // Stop is sent by the app before the provider is
        // stopped. An unexpected provider stop has no safe generation to
        // invent, so it only tears down NetworkExtension state.
        logs.writeLog(log: "[tunnel:\(tunnelId)] provider stop reason=\(reason); Go owns any recorded cleanup")
    }

    private func markSettingsCleared() {
        settingsLock.lock()
        activeSettingsGeneration = nil
        settingsLock.unlock()
    }

    func handleGoPublishedState(
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
    func acquireTunnel(sessionID: String?, generation: Int64) -> Int32 {
        _ = sessionID
        return settingsQueue.sync {
            guard applyFixedTunnelSettings(generation: generation) else { return -1 }
            let rawDescriptor = DobbyvpnGetTunnelFileDescriptor()
            guard rawDescriptor >= 0, rawDescriptor <= Int(Int32.max) else {
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] AcquireTunnel received invalid descriptor=\(rawDescriptor) " +
                        "generation=\(generation)"
                )
                _ = clearSettingsOnCurrentQueue(reason: "TUN descriptor unavailable")
                return -1
            }
            let duplicated = dup(Int32(rawDescriptor))
            guard duplicated >= 0 else {
                let code = errno
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] TUN descriptor duplication failed errno=\(code) " +
                        "error=\(String(cString: strerror(code))) generation=\(generation)"
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
    func releaseTunnel(sessionID: String?, generation: Int64) -> Bool {
        _ = sessionID
        return settingsQueue.sync {
            settingsLock.lock()
            let activeGeneration = activeSettingsGeneration
            settingsLock.unlock()
            guard activeGeneration == nil || activeGeneration == generation else {
                logs.writeLog(
                    log: "[tunnel:\(tunnelId)] ReleaseTunnel generation mismatch requested=\(generation) " +
                        "active=\(activeGeneration ?? -1)"
                )
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
    func runSettingsOperation(_ operation: @escaping () async throws -> Void) -> Bool {
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
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] NetworkExtension settings operation timed out " +
                    "after \(Int(Self.settingsOperationTimeout))s"
            )
            return false
        }
        if let error = result.error {
            logs.writeLog(log: "[tunnel:\(tunnelId)] NetworkExtension settings operation failed: \(String(reflecting: error as NSError))")
        }
        return result.succeeded
    }

    func startPathLogging() {
        // Logs-only: helps correlate "Wi‑Fi off/on" with tunnel lifecycle and health-check decisions.
        let monitor = Network.NWPathMonitor()
        let pathQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.path.\(tunnelId)")
        pathMonitor = monitor

        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let status = path.status
            let ifaces = path.availableInterfaces.map { "\($0.name)[\(self.interfaceTypeKey($0.type))]" }.joined(separator: ",")
            let expensive = path.isExpensive
            let constrained = path.isConstrained
            let signature = "status=\(status) ifaces=[\(ifaces)] expensive=\(expensive) " +
                "constrained=\(constrained) supportsIPv4=\(path.supportsIPv4) " +
                "supportsIPv6=\(path.supportsIPv6)"
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
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] [Interfaces] INTERFACE name=\(iface.name) " +
                            "type=\(self.interfaceTypeKey(iface.type)) raw=\(iface.type)"
                    )
                }
            }
        }

        monitor.start(queue: pathQueue)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }

    func logInitialNetworkPath(timeout: TimeInterval) {
        let monitor = Network.NWPathMonitor()
        let startupPathQueue = DispatchQueue(label: "vpn.dobby.app.tunnel.startup-path.\(tunnelId)")
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
        monitor.start(queue: startupPathQueue)
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

    func teardownForStop(reason: String) async {
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
