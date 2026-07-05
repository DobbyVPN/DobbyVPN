package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import com.dobby.backend.GoBackendWrapper

class HealthCheckManagerImpl(
    private val logger: Logger,
) : HealthCheckManager {
    override fun getConnectionState(): VpnConnectionState =
        when (GoBackendWrapper.getConnectionState()) {
            0 -> VpnConnectionState.DISCONNECTED
            1 -> VpnConnectionState.CONNECTING
            2 -> VpnConnectionState.CONNECTED
            else -> {
                logger.log("[WARN] Got invalid connection state")
                VpnConnectionState.DISCONNECTED
            }
        }

    override fun initHealthCheck() {
        GoBackendWrapper.initHealthCheck()
    }

    override fun startHealthCheck() {
        GoBackendWrapper.startHealthCheck()
    }

    override fun stopHealthCheck() {
        GoBackendWrapper.stopHealthCheck()
    }

    override fun measureTunnelProbeAverageLatencyMillis(timeoutMillis: Long): Long {
        return GoBackendWrapper.measureTunnelProbeAverageLatencyMillis(timeoutMillis)
    }
}
