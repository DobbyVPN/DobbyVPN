package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.healthcheck.HealthCheckLibrary

class RestartableHealthCheckGrpcLibrary(private val logger: Logger) : HealthCheckLibrary {
    override fun CouldStart(): Boolean {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.CouldStart()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            false
        }
    }

    override fun CheckServerAlive(address: String, port: Int): Int {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.CheckServerAlive(address, port)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            -1
        }
    }
}
