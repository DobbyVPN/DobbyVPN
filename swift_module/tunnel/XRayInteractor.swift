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
            return
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

        Cloak_outlineNewXrayClient(xrayConfig)
        if let error = err {
            xrayStarted = false
            throw error
        }

        Cloak_outlineXrayConnect(&err)
        if let error = err {
            xrayStarted = false
            throw error
        }
        xrayStarted = true
    }

    func stopXRay() {
        if !xrayStarted {
            return
        }
        Cloak_outlineXrayDisconnect()
        xrayStarted = false
    }
}
