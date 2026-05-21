import NetworkExtension
import CommonDI
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class XRayInteractor {
    private var logs = NativeModuleHolder.logsRepository
    private var xrayStarted: Bool = false

    func startXRay() throws {
        if !configsRepository.getIsXrayEnabled() {
            logs.writeLog(log: "[Xray] startXRay requested but Xray is disabled")
            xrayStarted = false
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -5,
                userInfo: [NSLocalizedDescriptionKey: "Xray is disabled"]
            )
        }
        
        let xrayConfig = configsRepository.getXrayConfig()

        // Validate config early (prevents passing empty config into native layer).
        if xrayConfig.isEmpty {
            logs.writeLog(log: "[startTunnel] Empty Xray config → abort")
            xrayStarted = false
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Empty Xray configuration"]
            )
        }
        
        var err: NSError?

        logs.writeLog(log: "[Xray] NewVpnClient begin config.len=\(xrayConfig.count)")
        Cloak_outlineNewVpnClient(xrayConfig, "xray", &err)
        if let error = err {
            xrayStarted = false
            logs.writeLog(log: "[Xray] NewVpnClient failed: \(error.localizedDescription)")
            throw error
        }
        logs.writeLog(log: "[Xray] NewVpnClient success")

        logs.writeLog(log: "[Xray] VpnConnect begin")
        Cloak_outlineVpnConnect(&err)
        if let error = err {
            xrayStarted = false
            logs.writeLog(log: "[Xray] VpnConnect failed: \(error.localizedDescription)")
            throw error
        }
        xrayStarted = true
        logs.writeLog(log: "[Xray] VpnConnect success")
    }

    func stopXRay() {
        if !xrayStarted {
            return
        }
        var err: NSError?
        logs.writeLog(log: "[Xray] VpnDisconnect begin")
        Cloak_outlineVpnDisconnect(&err)
        if let error = err {
            logs.writeLog(log: "Stop Xray get error \(error)")
        }
        xrayStarted = false
        logs.writeLog(log: "[Xray] VpnDisconnect returned")
    }
}
