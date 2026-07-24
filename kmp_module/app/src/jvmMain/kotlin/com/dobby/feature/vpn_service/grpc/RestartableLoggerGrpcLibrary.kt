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
            logger.log("[ERROR] Failed to init service logger: $e")
        }
    }

    override fun InitTelemetry(endpoint: String, token: String) {
        logger.log("Remote telemetry request ignored; local logging only")
    }

    override fun StopTelemetry() {
        logger.log("Remote telemetry is disabled; no exporter to stop")
    }

    override fun SetupTelemetryAttributes(config: String) {
        logger.log("Telemetry attributes ignored; remote telemetry is disabled")
    }
}
