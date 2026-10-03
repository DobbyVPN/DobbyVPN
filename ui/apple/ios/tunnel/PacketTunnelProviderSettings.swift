import Darwin
import DobbyVPNRuntime
import Foundation
import IOSIntegration
import Network
import NetworkExtension

extension PacketTunnelProvider {
    func handleGoPublishedState(sessionID: String?, generation: Int64, state: String?, failureCode: String?) {
        // State is a wake hint only. ReleaseTunnel is the sole settings cleanup path.
        logs.writeLog(log: "Go state session=\(sessionID ?? "") generation=\(generation) state=\(state ?? "") failure=\(failureCode ?? "")")
    }

    private func tunnelResult(fd: Int32? = nil, error: Error? = nil, pending: Bool) -> String {
        var result: [String: Any] = ["cleanup_pending": pending]
        if let fd { result["fd"] = fd }
        if let error { result["error"] = diagnosticErrorDescription(error) }
        do {
            return String(decoding: try JSONSerialization.data(withJSONObject: result), as: UTF8.self)
        } catch {
            logs.writeLog(log: "encode tunnel result: \(String(reflecting: error))")
            return ""
        }
    }

    func acquireTunnel(sessionID: String?, generation: Int64) -> String {
        settingsQueue.sync {
            let error = settingsOwner.apply(session: sessionID ?? "", generation: generation) { completed in
                self.setTunnelNetworkSettings(self.fixedTunnelSettings(), completionHandler: completed)
            }
            if let error { return tunnelResult(error: error, pending: true) }
            let rawDescriptor = DobbyvpnGetTunnelFileDescriptor()
            guard rawDescriptor >= 0, rawDescriptor <= Int(Int32.max) else {
                let error = NSError(domain: "DobbyVPN.TUN", code: 1, userInfo: [NSLocalizedDescriptionKey: "TUN descriptor unavailable: \(rawDescriptor)"])
                return tunnelResult(error: error, pending: true)
            }
            let descriptor = dup(Int32(rawDescriptor))
            guard descriptor >= 0 else {
                return tunnelResult(error: NSError(domain: NSPOSIXErrorDomain, code: Int(errno)), pending: true)
            }
            return tunnelResult(fd: descriptor, pending: true)
        }
    }

    func releaseTunnel(sessionID: String?, generation: Int64, timeoutMillis: Int64) -> String {
        settingsQueue.sync {
            let error = settingsOwner.release(session: sessionID ?? "", generation: generation, timeout: Double(timeoutMillis) / 1000) { completed in
                self.setTunnelNetworkSettings(nil, completionHandler: completed)
            }
            return tunnelResult(error: error, pending: error != nil)
        }
    }

    private func fixedTunnelSettings() -> NEPacketTunnelNetworkSettings {
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
        return settings
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
        let failure = DobbyvpnStopSessionAndWait()
        if failure.isEmpty {
            callbackBridge = nil
        } else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] Go shutdown retains resources: \(failure)")
        }

        pathMonitor?.cancel()
        pathMonitor = nil
        lastPathSignature = nil

        logs.writeLog(log: "[tunnel:\(tunnelId)] [teardown] end (\(reason)) cleanup_complete=\(failure.isEmpty)")
    }
}
