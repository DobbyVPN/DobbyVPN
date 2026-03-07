package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.data.TcpPingResponse
import interop.data.UrlTestResponse
import interop.exceptions.VpnServiceStatusException
import interop.healthcheck.HealthCheckLibrary

class RestartableHealthCheckGrpcLibrary(private val logger: Logger) :
    HealthCheckLibrary {
    override fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.StartHealthCheck(period, sendMetrics)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }

    override fun StopHealthCheck() {
        try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.StopHealthCheck()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }

    override fun Status(): String {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.Status()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            ""
        }
    }

    override fun TcpPing(address: String): TcpPingResponse {

        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.TcpPing(address)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            TcpPingResponse(0, "")
        }
    }

    override fun UrlTest(url: String, standard: Int): UrlTestResponse {
        return try {
            GrpcVpnLibrary.healthCheckGrpcLibrary.UrlTest(url, standard)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")

            UrlTestResponse(0, "")
        }
    }

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
