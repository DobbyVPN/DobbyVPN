package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.netcheck.NetCheckLibrary

class RestartableNetCheckGrpcLibrary(private val logger: Logger) : NetCheckLibrary {
    override fun NetCheck(configPath: String): String {
        try {
            return GrpcVpnLibrary.netCheckGrpcLibrary.NetCheck(configPath)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to NetCheck: $e")

            return "GRPC error"
        }
    }

    override fun CancelNetCheck() {
        try {
            GrpcVpnLibrary.netCheckGrpcLibrary.CancelNetCheck()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to CancelNetCheck: $e")
        }
    }
}
