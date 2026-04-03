package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.georouting.GeoroutingLibrary

class RestartableGeoroutingGrpcLibrary(private val logger: Logger) : GeoroutingLibrary {
    override fun SetGeoRoutingConf(cidrs: String) {
        try {
            GrpcVpnLibrary.georoutingGrpcLibrary.SetGeoRoutingConf(cidrs)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to SetGeoRoutingConf: $e")
        }
    }

    override fun ClearGeoRoutingConf() {
        try {
            GrpcVpnLibrary.georoutingGrpcLibrary.ClearGeoRoutingConf()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to ClearGeoRoutingConf: $e")
        }
    }
}
