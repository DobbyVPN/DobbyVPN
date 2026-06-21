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
        try {
            GrpcVpnLibrary.loggerGrpcLibrary.InitTelemetry(endpoint, token)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to init telemetry with endpoint $endpoint: $e, token.len: ${token.length}")
        }
    }

    override fun StopTelemetry() {
        try {
            GrpcVpnLibrary.loggerGrpcLibrary.StopTelemetry()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to stop telemetry: $e")
        }
    }

    override fun SetupTelemetryAttributes(config: String) {
        try {
            GrpcVpnLibrary.loggerGrpcLibrary.SetupTelemetryAttributes(config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to setup telemetry attributes with config.len=${config.length}: $e")
        }
    }
}
