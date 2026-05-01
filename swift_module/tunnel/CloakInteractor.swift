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
        let localPort = String(configsRepository.getCloakLocalPort())
        logs.writeLog(log: "startCloakOutline: entering, localPort=\(localPort)")

        if configsRepository.getIsCloakEnabled() {
            let cloakConfig = configsRepository.getCloakConfig()
            if cloakConfig.isEmpty {
                let host = OutlineInteractor.extractHost(from: outlineServerPort).lowercased()
                let cloakRequired = (host == "127.0.0.1" || host == "localhost")
                logs.writeLog(log: "startCloakOutline: enabled but config empty (required=\(cloakRequired), host=\(host))")
                if cloakRequired {
                    throw NSError(
                        domain: "PacketTunnelProvider",
                        code: -3,
                        userInfo: [NSLocalizedDescriptionKey: "Cloak enabled but config is empty"]
                    )
                }
                return
            }
            logs.writeLog(log: "startCloakOutline: starting cloak, config.length=\(cloakConfig.count)")
            var err: NSError?
            Cloak_outlineStartCloakClient("127.0.0.1", localPort, cloakConfig, false, &err)
            if let error = err {
                logs.writeLog(log: "startCloakOutline: failed to start cloak: \(error.localizedDescription)")
                throw error
            }
            cloakStarted = true
            logs.writeLog(log: "startCloakOutline: Cloak_outlineStartCloakClient returned (cloakStarted=true)")
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled")
        }
    }

    func stopCloak() {
        if cloakStarted {
            Cloak_outlineStopCloakClient()
            cloakStarted = false
        }
    }
}
