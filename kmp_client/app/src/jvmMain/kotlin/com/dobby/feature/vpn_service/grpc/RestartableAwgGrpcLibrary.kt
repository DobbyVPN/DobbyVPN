package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.awg.AwgLibrary
import interop.exceptions.VpnServiceStatusException

class RestartableAwgGrpcLibrary(private val logger: Logger) : AwgLibrary {
    override fun StartAwg(key: String, config: String) {
        try {
            GrpcVpnLibrary.awgGrpcLibrary.StartAwg(key, config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }

    override fun StopAwg() {
        try {
            GrpcVpnLibrary.awgGrpcLibrary.StopAwg()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StopAwg: $e")
        }
    }
}
