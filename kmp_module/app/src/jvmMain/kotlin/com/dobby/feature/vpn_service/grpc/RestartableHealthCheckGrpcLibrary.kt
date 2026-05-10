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
            logger.log("[ERROR] Failed to check if we could start: $e")

            false
        }
    }

    override fun GetConnectionState(): Int {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.GetConnectionState()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to get vpn connection state: $e")
            -1
        }
    }

    override fun InitHealthCheck(config: String) {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.InitHealthCheck(config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to init health check: $e")
        }
    }

    override fun StartHealthCheck() {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.StartHealthCheck()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to start health check: $e")
        }
    }

    override fun StopHealthCheck() {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.StopHealthCheck()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to stop health check: $e")
        }
    }
}
