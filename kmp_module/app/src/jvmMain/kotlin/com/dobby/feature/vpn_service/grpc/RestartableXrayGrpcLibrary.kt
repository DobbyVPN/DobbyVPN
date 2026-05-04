package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.xray.XrayLibrary

class RestartableXrayGrpcLibrary(private val logger: Logger) : XrayLibrary {
    override fun GetXrayLastError(): String {
        return try {
            GrpcVpnLibrary.xrayGrpcLibrary.GetXrayLastError()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to get last Xray error: $e")
            ""
        }
    }

    override fun StartXray(config: String): Int {
        return try {
            GrpcVpnLibrary.xrayGrpcLibrary.StartXray(config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to start Xray: $e")
            -1
        }
    }

    override fun StopXray() {
        try {
            GrpcVpnLibrary.xrayGrpcLibrary.StopXray()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to stop Xray: $e")
        }
    }
}

