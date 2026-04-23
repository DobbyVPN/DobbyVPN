import NetworkExtension
import CommonDI
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class CloakInteractor {
    private var logs = NativeModuleHolder.logsRepository
    private var cloakStarted: Bool = false

    func startCloak(outlineServerPort: String) throws {
        let start = Date()
        let localPort = String(configsRepository.getCloakLocalPort())
        logs.writeLog(log: "startCloakOutline: entering localPort=\(localPort) outlineServerPort.len=\(outlineServerPort.count)")
        
        if configsRepository.getIsCloakEnabled() {
            let cloakConfig = configsRepository.getCloakConfig()
            if cloakConfig.isEmpty {
                let host = OutlineInteractor.extractHost(from: outlineServerPort).lowercased()
                let cloakRequired = (host == "127.0.0.1" || host == "localhost")
                logs.writeLog(
                    log: "startCloakOutline: enabled but config empty " +
                        "(required=\(cloakRequired), host=\(maskStr(value: host)))"
                )
                if cloakRequired {
                    throw NSError(
                        domain: "PacketTunnelProvider",
                        code: -3,
                        userInfo: [NSLocalizedDescriptionKey: "Cloak enabled but config is empty"]
                    )
                }
                logs.writeLog(log: "startCloakOutline: config empty but not required elapsedMs=\(elapsedMs(since: start))")
                return
            }
            logs.writeLog(log: "startCloakOutline: starting cloak config.len=\(cloakConfig.count)")
            let nativeStart = Date()
            Cloak_outlineStartCloakClient("127.0.0.1", localPort, cloakConfig, false)
            cloakStarted = true
            logs.writeLog(
                log: "startCloakOutline: Cloak_outlineStartCloakClient returned " +
                    "nativeMs=\(elapsedMs(since: nativeStart)) totalMs=\(elapsedMs(since: start))"
            )
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled elapsedMs=\(elapsedMs(since: start))")
        }
    }

    func stopCloak() {
        if cloakStarted {
            let start = Date()
            logs.writeLog(log: "stopCloak: stopping Cloak client")
            Cloak_outlineStopCloakClient()
            cloakStarted = false
            logs.writeLog(log: "stopCloak: Cloak client stopped elapsedMs=\(elapsedMs(since: start))")
        } else {
            logs.writeLog(log: "[DEBUG] stopCloak: skipped cloakStarted=false")
        }
        cloakStarted = false
    }

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }
}
