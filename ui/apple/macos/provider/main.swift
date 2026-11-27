import Darwin
import Foundation
import SystemConfiguration
import NetworkExtension
import os

private struct FlowSettings: Decodable {
    let IPv4: String
    let Mask: String
    let IPv6: String
    let IPv6PrefixLength: Int
    let DNS: [String]
    let MTU: Int
}

private func providerError(_ message: String) -> NSError {
    NSError(domain: "vpn.dobby.desktop.PacketTunnel", code: 1,
            userInfo: [NSLocalizedDescriptionKey: message])
}

private func goError(_ message: UnsafeMutablePointer<CChar>?) -> Error? {
    guard let message else { return nil }
    defer { free(message) }
    return providerError(String(cString: message))
}

private final class UplinkObservation {
    let changed: () -> Void
    init(_ changed: @escaping () -> Void) { self.changed = changed }
}

final class PacketTunnel: NEPacketTunnelProvider {
    private let lifecycle = DispatchQueue(label: "vpn.dobby.desktop.PacketTunnel.lifecycle")
    private let output = DispatchQueue(label: "vpn.dobby.desktop.PacketTunnel.output")
    private let logger = Logger(subsystem: "vpn.dobby.desktop.PacketTunnel", category: "lifecycle")
    private var lease: Int32 = -1
    private var generation: UInt64 = 0
    private var cleanupPending = false
    private var networkStore: SCDynamicStore?

    private func observeUplink(_ start: UInt64) throws {
        // Callbacks run on lifecycle. The weak capture and generation check
        // retire queued notifications when macOS replaces the tunnel.
        let handler = UplinkObservation { [weak self] in
            guard let self, self.generation == start else { return }
            if let error = goError(DobbyProviderCheckUplink()) {
                self.logger.error("Reacquiring the underlying network: \(error.localizedDescription, privacy: .public)")
                self.stopEngine()
                self.cancelTunnelWithError(error)
            }
        }
        var context = SCDynamicStoreContext(version: 0, info: Unmanaged.passUnretained(handler).toOpaque(),
            retain: { UnsafeRawPointer(Unmanaged<UplinkObservation>.fromOpaque($0).retain().toOpaque()) },
            release: { Unmanaged<UplinkObservation>.fromOpaque($0).release() }, copyDescription: nil)
        guard let store = withExtendedLifetime(handler, { SCDynamicStoreCreate(nil, "DobbyVPN uplink" as CFString, { _, _, info in
            guard let info else { return }
            Unmanaged<UplinkObservation>.fromOpaque(info).takeUnretainedValue().changed()
        }, &context) }) else { throw providerError("Cannot open network service store (\(SCError()))") }
        let keys = ["Setup:/Network/Global/IPv4"]
        let patterns = ["State:/Network/Service/[^/]+/(IPv4|IPv6|DNS)", "Setup:/Network/Service/[^/]+/DNS"]
        guard SCDynamicStoreSetNotificationKeys(store, keys as CFArray, patterns as CFArray),
              SCDynamicStoreSetDispatchQueue(store, lifecycle) else { throw providerError("Cannot observe underlying network changes") }
        networkStore = store
        if let error = goError(DobbyProviderCheckUplink()) { throw error }
    }

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        lifecycle.async {
            if self.cleanupPending, let error = self.stopEngine() {
                completionHandler(error)
                return
            }
            guard self.lease < 0,
                  let config = (self.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration,
                  let key = config["connection"] as? String, let session = config["session"] as? String,
                  let value = config["generation"] as? String, let networkGeneration = UInt64(value)
            else { completionHandler(providerError("Missing or overlapping desktop network generation")); return }
            self.lease = DobbyOpenProtectionLease(true)
            guard self.lease >= 0, DobbyReserveTunnelProcess(self.lease) else {
                let error = NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
                self.closeLease()
                completionHandler(error)
                return
            }
            self.generation &+= 1
            let start = self.generation
            do {
                if let error = key.withCString({ k in session.withCString { s in
                    goError(DobbyProviderStart(UnsafeMutablePointer(mutating: k), UnsafeMutablePointer(mutating: s), networkGeneration))
                } }) { throw error }
                guard let raw = DobbyProviderSettings() else { throw providerError("Missing packet flow settings") }
                defer { free(raw) }
                let spec = try JSONDecoder().decode(FlowSettings.self, from: Data(bytes: raw, count: strlen(raw)))
                let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
                let ipv4 = NEIPv4Settings(addresses: [spec.IPv4], subnetMasks: [spec.Mask])
                ipv4.includedRoutes = [.default()]
                let ipv6 = NEIPv6Settings(addresses: [spec.IPv6], networkPrefixLengths: [NSNumber(value: spec.IPv6PrefixLength)])
                ipv6.includedRoutes = [.default()]
                let dns = NEDNSSettings(servers: spec.DNS)
                dns.matchDomains = [""]
                settings.ipv4Settings = ipv4
                settings.ipv6Settings = ipv6
                settings.dnsSettings = dns
                settings.mtu = NSNumber(value: spec.MTU)
                self.setTunnelNetworkSettings(settings) { error in
                    self.lifecycle.async {
                        guard self.generation == start else { completionHandler(providerError("Tunnel start superseded")); return }
                        if let error { self.rollback(error, completionHandler); return }
                        self.awaitProtection(start, mtu: spec.MTU, completionHandler)
                    }
                }
            } catch {
                self.stopEngine()
                completionHandler(error)
            }
        }
    }

    private func awaitProtection(_ start: UInt64, mtu: Int, _ complete: @escaping (Error?) -> Void) {
        guard generation == start else { complete(providerError("Tunnel start superseded")); return }
        guard DobbyFilterIsReady(lease) else {
            lifecycle.asyncAfter(deadline: .now() + .milliseconds(100)) {
                self.awaitProtection(start, mtu: mtu, complete)
            }
            return
        }
        if let error = goError(DobbyProviderRoutesReady()) {
            rollback(error, complete)
            return
        }
        do { try observeUplink(start) }
        catch { rollback(error, complete); return }
        guard DobbyAcquireTunnelLease(lease) else {
            rollback(NSError(domain: NSPOSIXErrorDomain, code: Int(errno)), complete)
            return
        }
        logger.info("Tunnel routes and independent physical protection are active")
        readPackets(start)
        output.async { self.writePackets(mtu: mtu) }
        complete(nil)
    }

    private func rollback(_ error: Error, _ complete: @escaping (Error?) -> Void) {
        let start = generation
        setTunnelNetworkSettings(nil) { cleanup in
            self.lifecycle.async {
                if self.generation == start { self.stopEngine() }
                if let cleanup { self.logger.error("Failed to remove tunnel settings: \(cleanup.localizedDescription, privacy: .public)") }
                complete(error)
            }
        }
    }

    private func readPackets(_ start: UInt64) {
        packetFlow.readPackets { packets, protocols in
            self.lifecycle.async {
                guard self.generation == start else { return }
                for (packet, family) in zip(packets, protocols) {
                    let version: UInt8
                    switch family.int32Value {
                    case AF_INET: version = 4
                    case AF_INET6: version = 6
                    default: continue
                    }
                    let accepted = packet.withUnsafeBytes { raw in
                        DobbyProviderReceive(version, UnsafeMutablePointer(mutating: raw.bindMemory(to: UInt8.self).baseAddress), raw.count)
                    }
                    if accepted == 0 { self.logger.error("Dropped invalid packet from the system") }
                }
                self.readPackets(start)
            }
        }
    }

    private func writePackets(mtu: Int) {
        var family: UInt8 = 0
        var bytes = [UInt8](repeating: 0, count: mtu)
        while true {
            let count = bytes.withUnsafeMutableBufferPointer { DobbyProviderRead($0.baseAddress, $0.count, &family) }
            guard count > 0 else { return }
            if !packetFlow.writePackets([Data(bytes.prefix(Int(count)))], withProtocols: [NSNumber(value: family == 4 ? AF_INET : AF_INET6)]) {
                logger.error("System rejected a packet from the tunnel")
            }
        }
    }

    private func closeLease() {
        if lease >= 0 { close(lease); lease = -1 }
    }

    @discardableResult
    private func stopEngine() -> Error? {
        generation &+= 1
        if let networkStore { SCDynamicStoreSetDispatchQueue(networkStore, nil) }
        networkStore = nil
        // Revoke physical egress first. Keep the separate process reservation
        // until Go has closed its socket and the final packet reader has left.
        if lease >= 0 && !DobbyReleaseTunnelLease(lease) {
            logger.error("Failed to revoke the tunnel lease, errno \(errno)")
            closeLease()
        }
        if let error = goError(DobbyProviderStop()) {
            logger.error("Packet runtime cleanup failed: \(error.localizedDescription, privacy: .public)")
            cleanupPending = true
            // Retain the process reservation while the old runtime owns resources.
            // A subsequent Start or Stop can retry even in the same extension process.
            return error
        }
        output.sync {}
        cleanupPending = false
        closeLease()
        return nil
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        lifecycle.async {
            self.logger.info("Stopping packet tunnel, system reason \(reason.rawValue)")
            self.finishStop(completionHandler)
        }
    }

    private func finishStop(_ complete: @escaping () -> Void) {
        if stopEngine() == nil {
            complete()
        } else {
            let stoppedGeneration = generation
            lifecycle.asyncAfter(deadline: .now() + .seconds(1)) {
                if self.generation == stoppedGeneration && self.cleanupPending {
                    self.finishStop(complete)
                } else {
                    complete()
                }
            }
        }
    }
}

NEProvider.startSystemExtensionMode()
dispatchMain()
