import NetworkExtension
import CommonDI
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class AwgInteractor {
    private var logs = NativeModuleHolder.logsRepository

    func startAwg(
        tunnelFileDescriptor: Int32,
        nativeClientCreated: () -> Void
    ) throws {
        let config = configsRepository.getAwgConfig()
        logs.writeLog(
            log: "Config snapshot: config.len=\(config.count)"
        )

        // Validate config early (prevents passing empty config into native layer).
        if config.isEmpty {
            logs.writeLog(log: "[startTunnel] Empty AmneziaWG config → abort")
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Empty AmneziaWG configuration"]
            )
        }

        var err: NSError?

        nativeClientCreated()

        logs.writeLog(log: "[DEBUG][AmneziaWG] calling native AmneziaWGConnect")
        Cloak_outlineAwgTurnOn("utun0", Int(tunnelFileDescriptor), config, &err)
        if let error = err {
            logs.writeLog(log: "[AmneziaWG] AmneziaWGConnect failed: \(error.localizedDescription)")
            throw error
        }
        logs.writeLog(log: "[AmneziaWG] AmneziaWGConnect succeeded")
    }

    func stopAmneziaWG() throws {
        logs.writeLog(log: "[DEBUG][AmneziaWG] calling native AmneziaWGDisconnect")
        Cloak_outlineAwgTurnOff()
        logs.writeLog(log: "[DEBUG][AmneziaWG] AmneziaWGDisconnect returned")
    }
}
