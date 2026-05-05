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

// C function to convert interface name to index (for Go socket binding)
@_silgen_name("if_nametoindex")
func if_nametoindex(_: UnsafePointer<CChar>) -> CUnsignedInt



class PacketTunnelProvider: NEPacketTunnelProvider {
    private let launchId = UUID().uuidString
    private let tunnelId = String(UUID().uuidString.prefix(8))
    private let tunnelMTU = 1200

    private let xrayInteractor: XRayInteractor = XRayInteractor()
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
        let message = mach_error_string(result).map { String(cString: $0) } ?? "unknown"
        logs.writeLog(log: "[Memory] unable to get info result=\(result) error=\(message)")
        return 0.0
    }

    func logInterfaces() {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        let rc = getifaddrs(&ifaddrPtr)
        guard rc == 0, ifaddrPtr != nil else {
            let getErrno = errno
            logs.writeLog(
                log: "[DEBUG][Interfaces] getifaddrs failed rc=\(rc) errno=\(getErrno) " +
                    "\(String(cString: strerror(getErrno)))"
            )
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
                    } else {
                        let ntopErrno = errno
                        values.append("ipv4_ntop_error=\(ntopErrno):\(String(cString: strerror(ntopErrno)))")
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
        func enterStage(_ stage: String) {
            startupStage = stage
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage begin: \(stage)")
        }

        // iOS 26 research: Log iOS version
        let osVersion = ProcessInfo.processInfo.operatingSystemVersion
        let osVersionString = "\(osVersion.majorVersion).\(osVersion.minorVersion).\(osVersion.patchVersion)"
        logs.writeLog(log: "[iOS26-RESEARCH] iOS version: \(osVersionString)")

        logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel tid=\(tid) launchId=\(launchId) optionKeys=\(optionKeys)")
        logs.writeLog(log: "Sentry is running in PacketTunnelProvider")

        // iOS 26 research: Log network interfaces BEFORE tunnel starts
        logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: BEFORE_VPN_TUNNEL ==========")
        logInterfaces()
        logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: END_BEFORE_VPN_TUNNEL ==========")

        do {
            enterStage("go logger init")
            let path = LogsRepository_iosKt.provideLogFilePath().normalized().description()
            logs.writeLog(log: "Start go logger init path = \(path)")
            Cloak_outlineInitLogger(path)
            logs.writeLog(log: "Finish go logger init")

            // Defensive: if the system retries start without a proper stop, ensure we teardown previous state.
            enterStage("pre-start cleanup")
            await teardownForStop(reason: "pre-start cleanup")
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: pre-start cleanup")

            enterStage("path monitor")
            startPathLogging()
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: path monitor")
            
            // iOS 26+: set the physical interface index before any protected sockets are opened.
            enterStage("initial interface detection")
            setInitialDefaultInterfaceIndexForStartup(timeout: 1.0)
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: initial interface detection")
            enterStage("geo routing config")
            let geoRoutingConf = configsRepository.getGeoRoutingConf()
            logs.writeLog(log: "[DEBUG][Routing] applying geo routing config length=\(geoRoutingConf.count)")
            Cloak_outlineSetGeoRoutingConf(geoRoutingConf)
            logs.writeLog(log: "[DEBUG][Routing] geo routing config applied")

            enterStage("route exclusions")
            let cloakConfig = configsRepository.getCloakConfig()
            // Excluding the remote server route helps avoid routing loops (especially with WSS/domain hosts).
            // DNS resolution at tunnel start can hang in offline/captive-portal cases, so we do it with a hard timeout.
            var excludedRoutes: [NEIPv4Route] = []
            if let hostOrIp = extractIP(from: configsRepository.getServerPort()) {
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
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: route exclusions count=\(excludedRoutes.count)")

            // Determine which protocol to use early - needed for Cloak startup
            let vpnInterface = configsRepository.getVpnInterface()
            logs.writeLog(log: "[tunnel:\(tunnelId)] selected vpnInterface=\(vpnInterface)")

            // Cloak must bind/connect before the full-tunnel route is installed; otherwise
            // iOS can route the upstream bootstrap traffic into a tunnel that is not ready yet.
            // Only start Cloak for cloakOutline protocol.
            if vpnInterface == .cloakOutline {
                enterStage("cloak startup")
                logs.writeLog(log: "[tunnel:\(tunnelId)] starting Cloak phase")
                try cloakInteractor.startCloak(outlineServerPort: configsRepository.getServerPort())
                logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: cloak startup")
            }

            enterStage("network settings build")
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
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: network settings build")

            logNetworkSettings(
                localAddress: localAddress,
                remoteAddress: remoteAddress,
                subnetMask: subnetMask,
                mtu: tunnelMTU,
                dnsServers: dnsServers,
                excludedRoutes: excludedRoutes
            )
            enterStage("setTunnelNetworkSettings")
            let settingsStart = Date()
            logs.writeLog(log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings begin")
            try await self.setTunnelNetworkSettings(settings)
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] setTunnelNetworkSettings applied in \(elapsedMs(since: settingsStart))ms"
            )

            // iOS 26 research: Log network interfaces AFTER tunnel starts
            logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: AFTER_VPN_TUNNEL ==========")
            logInterfaces()
            logs.writeLog(log: "[iOS26-RESEARCH] ========== INTERFACES: END_AFTER_VPN_TUNNEL ==========")

            enterStage("packetFlow bridge")
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
            logs.writeLog(log: "[tunnel:\(tunnelId)] startTunnel stage complete: packetFlow bridge fd=\(bridge.tunnelFileDescriptor)")

            enterStage("protocol startup")
            logs.writeLog(log: "[tunnel:\(tunnelId)] starting VPN protocol phase tunnelFD=\(bridge.tunnelFileDescriptor) mtu=\(tunnelMTU)")

            switch vpnInterface {
            case .cloakOutline:
                try outlineInteractor.startOutline(
                    tunnelFileDescriptor: bridge.tunnelFileDescriptor,
                    mtu: tunnelMTU,
                    nativeClientCreated: {
                        bridgeLogs.writeLog(log: "[tunnel:\(bridgeTunnelId)] native Outline client created; releasing bridge fd to Go")
                        bridge.releaseTunnelFileDescriptor()
                    }
                )
            case .xray:
                try xrayInteractor.startXRay(
                    tunnelFileDescriptor: bridge.tunnelFileDescriptor,
                    mtu: tunnelMTU
                )
            case .amneziaWg:
                logs.writeLog(log: "[tunnel:\(tunnelId)] AmneziaWG not yet implemented on iOS")
                throw NSError(
                    domain: "PacketTunnelProvider",
                    code: -4,
                    userInfo: [NSLocalizedDescriptionKey: "AmneziaWG not yet implemented on iOS"]
                )
            default:
                logs.writeLog(log: "[tunnel:\(tunnelId)] No VPN interface selected")
                throw NSError(
                    domain: "PacketTunnelProvider",
                    code: -4,
                    userInfo: [NSLocalizedDescriptionKey: "No VPN interface selected"]
                )
            }

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
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel clearing geo routing config")
        Cloak_outlineClearGeoRoutingConf()
        logs.writeLog(log: "[tunnel:\(tunnelId)] stopTunnel geo routing clear returned")

        // Stop the active protocol based on the VPN interface
        let vpnInterface = configsRepository.getVpnInterface()
        switch vpnInterface {
        case .cloakOutline:
            do {
                try outlineInteractor.stopOutline()
            } catch {
                logs.writeLog(log: "[tunnel:\(tunnelId)] stopOutline error: \(error.localizedDescription)")
            }
            cloakInteractor.stopCloak()
        case .xray:
            xrayInteractor.stopXRay()
        case .amneziaWg:
            logs.writeLog(log: "[tunnel:\(tunnelId)] AmneziaWG stop not yet implemented on iOS")
        default:
            logs.writeLog(log: "[tunnel:\(tunnelId)] No VPN interface to stop")
        }

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
        if let monitor = pathMonitor {
            updateDefaultInterfaceIndex(for: monitor.currentPath)
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        logs.writeLog(
            log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage begin bytes=\(messageData.count) " +
                "hasCompletion=\(completionHandler != nil)"
        )
        if let msg = String(data: messageData, encoding: .utf8), msg == "getMemory" {
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory")
            let response = "Memory:\(reportMemoryUsageMB())".data(using: .utf8)
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getMemory responseBytes=\(response?.count ?? -1)")
            completionHandler?(response)
        } else if let msg = String(data: messageData, encoding: .utf8), msg == "getOutlineStatus" {
            let status = outlineInteractor.outlineStatus()
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getOutlineStatus \(status)")
            let response = "OutlineStatus:\(status)".data(using: .utf8)
            logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage getOutlineStatus responseBytes=\(response?.count ?? -1)")
            completionHandler?(response)
        } else {
            if String(data: messageData, encoding: .utf8) == nil {
                logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage nonUtf8 echo bytes=\(messageData.count)")
            } else {
                logs.writeLog(log: "[DEBUG][tunnel:\(tunnelId)] handleAppMessage unknown echo bytes=\(messageData.count)")
            }
            completionHandler?(messageData)
        }
    }

    /// Get the default (primary) interface index for socket binding.
    /// Returns 0 if no valid interface is found.
    private func getDefaultInterfaceIndex(from path: Network.NWPath) -> Int {
        // Find the non-VPN, non-loopback interface.
        let physicalInterfaces = path.availableInterfaces.filter { $0.type != .other && $0.type != .loopback }
        let candidates = path.availableInterfaces
            .map { "\($0.name):\(interfaceTypeKey($0.type))" }
            .joined(separator: ",")
        logs.writeLog(
            log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] selecting default interface " +
                "status=\(path.status) candidates=[\(candidates)] physicalCount=\(physicalInterfaces.count)"
        )
        
        // Prefer WiFi, then cellular, then any other physical interface
        let preferredInterface = physicalInterfaces.first { $0.type == .wifi }
            ?? physicalInterfaces.first { $0.type == .cellular }
            ?? physicalInterfaces.first { $0.type == .wiredEthernet }
            ?? physicalInterfaces.first
        
        guard let iface = preferredInterface else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] no physical interface candidate found")
            return 0
        }
        
        // Convert interface name to index
        let index = iface.name.withCString { Int(if_nametoindex($0)) }
        if index == 0 {
            let indexErrno = errno
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] if_nametoindex failed " +
                    "name=\(iface.name) type=\(iface.type) errno=\(indexErrno) \(String(cString: strerror(indexErrno)))"
            )
        } else {
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] selected default interface " +
                    "name=\(iface.name) type=\(iface.type) index=\(index)"
            )
        }
        return index
    }
    
    /// Update the default interface index in Go for socket protection.
    /// Call this whenever the network path changes.
    private func updateDefaultInterfaceIndex(for path: Network.NWPath) {
        let index = getDefaultInterfaceIndex(from: path)
        
        if index > 0 {
            // Get interface name for logging
            let physicalInterfaces = path.availableInterfaces.filter { $0.type != .other && $0.type != .loopback }
            let ifaceName = physicalInterfaces.first { $0.type == .wifi }?.name
                ?? physicalInterfaces.first { $0.type == .cellular }?.name
                ?? physicalInterfaces.first { $0.type == .wiredEthernet }?.name
                ?? physicalInterfaces.first?.name
                ?? "unknown"
            
            let ifaceType = physicalInterfaces.first { $0.type == .wifi } != nil ? "WiFi"
                : physicalInterfaces.first { $0.type == .cellular } != nil ? "Cellular"
                : physicalInterfaces.first { $0.type == .wiredEthernet } != nil ? "Ethernet"
                : "Other"
            
            // Call Go function to set the interface index
            Cloak_outlineSetDefaultInterfaceIndex(index)
            
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] Set default interface index: \(index) (\(ifaceName)/\(ifaceType))"
            )
        } else {
            logs.writeLog(
                log: "[tunnel:\(tunnelId)] [iOS26-RESEARCH] WARNING: Could not determine default interface index! " +
                    "pathStatus=\(path.status) ifaces=\(path.availableInterfaces.map { "\($0.name):\($0.type)" }.joined(separator: ","))"
            )
        }
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

    private func startPathLogging() {
        // Logs-only: helps correlate "Wi‑Fi off/on" with tunnel lifecycle and health-check decisions.
        let monitor = Network.NWPathMonitor()
        let q = DispatchQueue(label: "vpn.dobby.app.tunnel.path.\(tunnelId)")
        pathMonitor = monitor

        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            // iOS 26+: Update the interface index in Go for socket protection
            self.updateDefaultInterfaceIndex(for: path)
       
            // iOS 26+ diagnostic: capture all path properties
            let supportsIPV4 = path.supportsIPv4
            let supportsIPV6 = path.supportsIPv6

            // iOS 26 research: Log detailed interface info with interface name and type
            let ifaces = path.availableInterfaces.map { iface -> String in
                let interfaceType: String
                switch iface.type {
                case .wifi:
                    interfaceType = "WiFi"
                case .cellular:
                    interfaceType = "Cellular"
                case .wiredEthernet:
                    interfaceType = "Ethernet"
                case .loopback:
                    interfaceType = "Loopback"
                case .other:
                    interfaceType = "OTHER (VPN_TUNNEL)"
                @unknown default:
                    interfaceType = "Unknown"
                }
                return "\(iface.name)[\(interfaceType)]"
            }.joined(separator: ", ")

            let expensive = path.isExpensive
            let constrained = path.isConstrained
            let status = path.status

            // iOS 26+ detection of problematic scenarios - now with FULL detail
            // Format: [TIMESTAMP] PHASE: interfaces=... expensive=... constrained=...
            let timestamp = ISO8601DateFormatter().string(from: Date())
            var pathDesc = "[\(timestamp)] PHASE=DETECT status=\(status) ifaces=[\(ifaces)]"
            pathDesc += " expensive=\(expensive) constrained=\(constrained)"
            pathDesc += " supportsIPv4=\(supportsIPV4) supportsIPv6=\(supportsIPV6)"

            if #available(iOS 26.0, *) {
                if expensive {
                    pathDesc += " [EXPENSIVE_NETWORK]"
                }
                if constrained {
                    pathDesc += " [CONSTRAINED]"
                }
                switch status {
                case .satisfied:
                    pathDesc += " [CONNECTED]"
                case .unsatisfied:
                    pathDesc += " [DISCONNECTED]"
                case .requiresConnection:
                    pathDesc += " [NEEDS_CONNECTION]"
                @unknown default:
                    pathDesc += " [UNKNOWN_STATUS]"
                }
            }

            // iOS 26 research: Create fingerprint with more detail (include interface names)
            let interfaceDescriptors = path.availableInterfaces
                .map { "\($0.name):\(self.interfaceTypeKey($0.type))" }
                .sorted()
                .joined(separator: "|")
            let fingerprint = "status=\(path.status)|ifaces=\(interfaceDescriptors)|expensive=\(expensive)|constrained=\(constrained)"
            
            if self.lastPathFingerprint != fingerprint {
                let previousFingerprint = self.lastPathFingerprint ?? "(none)"
                self.lastPathFingerprint = fingerprint

                // iOS 26 research: Log with clear transition marker
                if previousFingerprint != "(none)" {
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] NETWORK_CHANGED: \(previousFingerprint) -> \(fingerprint)"
                    )
                }

                self.logs.writeLog(
                    log: "[tunnel:\(self.tunnelId)] PATH_UPDATE: " + pathDesc
                )

                // iOS 26: Log warning for problematic network conditions
                if constrained && expensive {
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] WARNING: Network is BOTH expensive AND constrained - expect connection issues!"
                    )
                }

                // Log each interface with full details
                for iface in path.availableInterfaces {
                    let interfaceType: String
                    switch iface.type {
                    case .wifi:
                        interfaceType = "WiFi"
                    case .cellular:
                        interfaceType = "Cellular"
                    case .wiredEthernet:
                        interfaceType = "Ethernet"
                    case .loopback:
                        interfaceType = "Loopback"
                    case .other:
                        interfaceType = "OTHER_VPN_TUNNEL"
                    @unknown default:
                        interfaceType = "Unknown"
                    }

                    // iOS 26 research: This is the KEY log - shows exactly which interface is which
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] INTERFACE: name=\(iface.name) type=\(interfaceType) (raw=\(iface.type))"
                    )
                }

                // iOS 26 research: Detect when "other" interface appears
                let hasOtherInterface = path.availableInterfaces.contains { $0.type == .other }
                if hasOtherInterface {
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] *** VPN TUNNEL INTERFACE DETECTED *** This is the utunX interface created by the VPN!"
                    )
                }

                // iOS 26: Log active connection counts - if UDP connections drop, that's a problem
                self.logs.writeLog(
                    log: "[tunnel:\(self.tunnelId)] [iOS26-RESEARCH] ROUTE_CHECK: Active interfaces: \(path.availableInterfaces.map { $0.name }.joined(separator: ", "))"
                )

                // CRITICAL: Log when WiFi->Cellular transition happens (this is when tunnel issues start)
                let currentTypes = Set(path.availableInterfaces.map { self.interfaceTypeKey($0.type) })
                let wasUsingWiFi = previousFingerprint.contains(":wifi")
                let isNowUsingWiFi = currentTypes.contains("wifi")
                let isNowUsingCellular = currentTypes.contains("cellular")
                
                if wasUsingWiFi && isNowUsingCellular && !isNowUsingWiFi {
                    self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] CRITICAL: Network transitioned WiFi -> Cellular! Tunnel instability expected!"
                    )
                    // iOS 26: Signal the need for a potential health check or engine refresh
                    // We don't restart automatically here to avoid flapping, but we log it for the app to see.
                } else if !wasUsingWiFi && isNowUsingWiFi {
                     self.logs.writeLog(
                        log: "[tunnel:\(self.tunnelId)] INFO: Network transitioned back to WiFi."
                    )
                }
            }
        }

        monitor.start(queue: q)
        logs.writeLog(log: "[tunnel:\(tunnelId)] NWPathMonitor started")
    }
    
    /// Capture startup path and synchronously publish the physical interface index to Go.
    private func setInitialDefaultInterfaceIndexForStartup(timeout: TimeInterval) {
        let monitor = Network.NWPathMonitor()
        let q = DispatchQueue(label: "vpn.dobby.app.tunnel.startup-path")
        let semaphore = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var captured = false

        monitor.pathUpdateHandler = { path in
            lock.lock()
            if captured {
                lock.unlock()
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP_NETWORK: duplicate path update ignored")
                return
            }
            captured = true
            lock.unlock()

            // Capture interface details
            var ifaceDetails: [String] = []
            for iface in path.availableInterfaces {
                let detail = "\(iface.name):\(iface.type) cellular=\(iface.type == .cellular) wifi=\(iface.type == .wifi)"
                ifaceDetails.append(detail)
            }
            let ifacesStr = ifaceDetails.joined(separator: ", ")

            let expensive = path.isExpensive
            let constrained = path.isConstrained
            let status = path.status
            let supportsIPv4 = path.supportsIPv4
            let supportsIPv6 = path.supportsIPv6

            // Log all network state
            let log = "[tunnel:\(self.tunnelId)] STARTUP_NETWORK: status=\(status) ifaces=[\(ifacesStr)] " +
                      "expensive=\(expensive) constrained=\(constrained) " +
                      "ipv4=\(supportsIPv4) ipv6=\(supportsIPv6)"

            self.logs.writeLog(log: log)

            // iOS 26+: Set initial interface index from startup path
            let ifaceIndex = self.getDefaultInterfaceIndex(from: path)
            if ifaceIndex > 0 {
                Cloak_outlineSetDefaultInterfaceIndex(ifaceIndex)
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP: Set initial interface index returned: \(ifaceIndex)")
            } else {
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP_WARNING: Could not determine initial interface index")
            }

            // iOS 26-specific warnings
            if expensive && constrained {
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP_WARNING: Cellular with BOTH expensive AND constrained=true - expect instability!")
            }
            if path.status == .unsatisfied {
                self.logs.writeLog(log: "[tunnel:\(self.tunnelId)] STARTUP_ERROR: Network path unsatisfied at tunnel start!")
            }

            semaphore.signal()
        }

        logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: starting temporary NWPathMonitor timeoutMs=\(Int(timeout * 1000))")
        monitor.start(queue: q)

        if semaphore.wait(timeout: .now() + timeout) == .timedOut {
            logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_WARNING: Timed out waiting for initial network path")
        } else {
            logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: initial path captured")
        }

        monitor.cancel()
        logs.writeLog(log: "[tunnel:\(tunnelId)] STARTUP_NETWORK: temporary NWPathMonitor cancelled")
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
