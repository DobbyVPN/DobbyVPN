package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.logger.LoggerLibrary

class RestartableLoggerGrpcLibrary(private val logger: Logger) : LoggerLibrary {
    override fun InitLogger(path: String) {
        try {
            GrpcVpnLibrary.loggerGrpcLibrary.InitLogger(path)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }
}
