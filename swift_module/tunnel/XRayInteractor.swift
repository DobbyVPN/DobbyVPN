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
        let start = Date()
        logs.writeLog(log: "[XRay] startXRay begin fd=\(tunnelFileDescriptor) mtu=\(mtu)")
        if !configsRepository.getIsXrayEnabled() {
            logs.writeLog(log: "[XRay] startXRay skipped because Xray is disabled")
            return
        }
        
        let xrayConfig = configsRepository.getXrayConfig()
        logs.writeLog(log: "[XRay] config snapshot length=\(xrayConfig.count)")

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

        let newClientStart = Date()
        logs.writeLog(log: "[XRay] calling native NewVpnClient fd=\(tunnelFileDescriptor) mtu=\(mtu)")
        Cloak_outlineNewVpnClient(xrayConfig, "xray", Int(tunnelFileDescriptor), mtu, &err)
        if let error = err {
            xrayStarted = false
            logs.writeLog(
                log: "[XRay] NewVpnClient failed in \(elapsedMs(since: newClientStart))ms: " +
                    "\(error.localizedDescription)"
            )
            throw error
        }
        logs.writeLog(log: "[XRay] NewVpnClient succeeded in \(elapsedMs(since: newClientStart))ms")

        let connectStart = Date()
        logs.writeLog(log: "[XRay] calling native VpnConnect")
        Cloak_outlineVpnConnect(&err)
        if let error = err {
            xrayStarted = false
            logs.writeLog(
                log: "[XRay] VpnConnect failed in \(elapsedMs(since: connectStart))ms: " +
                    "\(error.localizedDescription)"
            )
            throw error
        }
        xrayStarted = true
        logs.writeLog(
            log: "[XRay] VpnConnect succeeded in \(elapsedMs(since: connectStart))ms " +
                "totalStartMs=\(elapsedMs(since: start))"
        )
    }

    func stopXRay() {
        if !xrayStarted {
            logs.writeLog(log: "[XRay] stopXRay skipped xrayStarted=false")
            return
        }
        var err: NSError?
        let start = Date()
        logs.writeLog(log: "[XRay] calling native VpnDisconnect")
        Cloak_outlineVpnDisconnect(&err)
        if let error = err {
            logs.writeLog(log: "[XRay] VpnDisconnect failed in \(elapsedMs(since: start))ms: \(error.localizedDescription)")
        } else {
            logs.writeLog(log: "[XRay] VpnDisconnect succeeded in \(elapsedMs(since: start))ms")
        }
        xrayStarted = false
    }

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }
}
