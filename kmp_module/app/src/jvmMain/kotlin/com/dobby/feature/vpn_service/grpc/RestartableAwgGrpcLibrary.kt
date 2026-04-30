package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.awg.AwgLibrary
import interop.exceptions.VpnServiceStatusException

class RestartableAwgGrpcLibrary(private val logger: Logger) : AwgLibrary {
    override fun StartAwg(key: String, config: String): Int {
        try {
            return GrpcVpnLibrary.awgGrpcLibrary.StartAwg(key, config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to start AmneziaWG: $e")
            return -1
        }
    }

    override fun StopAwg() {
        try {
            GrpcVpnLibrary.awgGrpcLibrary.StopAwg()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to stop AmneziaWG: $e")
        }
    }
}
