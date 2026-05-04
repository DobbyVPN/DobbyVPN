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

    func startXRay(
        tunnelFileDescriptor: Int32,
        mtu: Int
    ) throws {
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

        Cloak_outlineNewVpnClient(xrayConfig, "xray", Int(tunnelFileDescriptor), mtu, &err)
        if let error = err {
            xrayStarted = false
            throw error
        }

        Cloak_outlineVpnConnect(&err)
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
        var err: NSError?
        Cloak_outlineVpnDisconnect(&err)
        if let error = err {
            logs.writeLog(log: "Stop Xray get error \(error)")
        }
        xrayStarted = false
    }
}
