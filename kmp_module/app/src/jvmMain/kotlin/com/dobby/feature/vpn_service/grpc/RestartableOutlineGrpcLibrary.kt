package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.outline.OutlineLibrary

class RestartableOutlineGrpcLibrary(private val logger: Logger) : OutlineLibrary {
    override fun GetOutlineLastError(): String {
        try {
            return GrpcVpnLibrary.outlineGrpcLibrary.GetOutlineLastError()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            return ""
        }
    }

    override fun StartOutline(key: String): Int {
        try {
            return GrpcVpnLibrary.outlineGrpcLibrary.StartOutline(key)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            return -1
        }
    }

    override fun StopOutline() {
        try {
            GrpcVpnLibrary.outlineGrpcLibrary.StopOutline()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }
}
