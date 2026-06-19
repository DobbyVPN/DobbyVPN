package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import interop.healthcheck.HealthCheckLibrary

class HealthCheckManagerImpl(
    private val logger: Logger,
    private val healthCheckLibrary: HealthCheckLibrary,
) : HealthCheckManager {
    override fun getConnectionState(): VpnConnectionState =
        when (healthCheckLibrary.GetConnectionState()) {
            0 -> VpnConnectionState.DISCONNECTED
            1 -> VpnConnectionState.CONNECTING
            2 -> VpnConnectionState.CONNECTED
            else -> {
                logger.log("[WARN] Got invalid connection state")
                VpnConnectionState.DISCONNECTED
            }
        }

    override fun initHealthCheck() {
        healthCheckLibrary.InitHealthCheck()
    }

    override fun startHealthCheck() {
        healthCheckLibrary.StartHealthCheck()
    }

    override fun stopHealthCheck() {
        healthCheckLibrary.StopHealthCheck()
    }
}
