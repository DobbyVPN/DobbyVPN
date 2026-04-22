package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import interop.healthcheck.HealthCheckLibrary
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread
import kotlin.system.measureTimeMillis

class HealthCheckImpl(
    private val logger: Logger,
    private val healthCheckLibrary: HealthCheckLibrary,
) : HealthCheck {
    override fun GetConnectionState(): VpnConnectionState =
        when (healthCheckLibrary.GetConnectionState()) {
            0 -> VpnConnectionState.DISCONNECTED
            1 -> VpnConnectionState.CONNECTING
            2 -> VpnConnectionState.CONNECTED
            else -> {
                logger.log("[WARN] Got invalid connection state")
                VpnConnectionState.DISCONNECTED
            }
        }

    override fun StartHealthCheck() {
        healthCheckLibrary.StartHealthCheck()
    }

    override fun StopHealthCheck() {
        healthCheckLibrary.StopHealthCheck()
    }
}
