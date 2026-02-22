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
        logs.writeLog(log: "startCloakOutline: entering")
        
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
            logs.writeLog(log: "startCloakOutline: starting cloak")
            Cloak_outlineStartCloakClient("127.0.0.1", localPort, cloakConfig, false)
            cloakStarted = true
            logs.writeLog(log: "startCloakOutline: started")
        } else {
            logs.writeLog(log: "startCloakOutline: cloak disabled")
        }
    }

    func stopCloak() throws {
        if cloakStarted {
            var err: NSError?
            Cloak_outlineOutlineDisconnect(&err)
            if let error = err {
                throw error
            }
        }
    }
}
