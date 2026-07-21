import NetworkExtension
import CommonDI
import MyLibrary
import os
import app
import Foundation

public final class TrustTunnelInteractor {
    private var logs = NativeModuleHolder.logsRepository
    private var trustTunnelStarted: Bool = false

    func startTrustTunnel() throws {
        if !configsRepository.getIsTrustTunnelEnabled() {
            logs.writeLog(log: "[TrustTunnel] startTrustTunnel requested but TrustTunnel is disabled")
            trustTunnelStarted = false
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -5,
                userInfo: [NSLocalizedDescriptionKey: "TrustTunnel is disabled"]
            )
        }

        let trustTunnelConfig = configsRepository.getTrustTunnelConfig()

        if trustTunnelConfig.isEmpty {
            logs.writeLog(log: "[TrustTunnel] startTrustTunnel: empty config → abort")
            trustTunnelStarted = false
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Empty TrustTunnel configuration"]
            )
        }

        var err: NSError?

        logs.writeLog(log: "[TrustTunnel] NewVpnClient begin config.len=\(trustTunnelConfig.count)")
        Cloak_outlineNewVpnClient(trustTunnelConfig, "trusttunnel", &err)
        if let error = err {
            trustTunnelStarted = false
            logs.writeLog(log: "[TrustTunnel] NewVpnClient failed: \(error.localizedDescription)")
            throw error
        }
        logs.writeLog(log: "[TrustTunnel] NewVpnClient success")

        logs.writeLog(log: "[TrustTunnel] VpnConnect begin")
        Cloak_outlineVpnConnect(&err)
        if let error = err {
            trustTunnelStarted = false
            logs.writeLog(log: "[TrustTunnel] VpnConnect failed: \(error.localizedDescription)")
            throw error
        }
        trustTunnelStarted = true
        logs.writeLog(log: "[TrustTunnel] VpnConnect success")
    }

    func stopTrustTunnel() {
        if !trustTunnelStarted {
            return
        }
        var err: NSError?
        logs.writeLog(log: "[TrustTunnel] VpnDisconnect begin")
        Cloak_outlineVpnDisconnect(&err)
        if let error = err {
            logs.writeLog(log: "[TrustTunnel] Stop TrustTunnel error: \(error)")
        }
        trustTunnelStarted = false
        logs.writeLog(log: "[TrustTunnel] VpnDisconnect returned")
    }
}
