package com.dobby.feature.diagnostic.domain

import com.dobby.backend.HealthCheckBackendWrapper

class HealthCheckImpl : HealthCheck {
    override fun GetConnectionState(): VpnConnectionState {
        return when (HealthCheckBackendWrapper.getConnectionState()) {
            0 -> VpnConnectionState.DISCONNECTED
            1 -> VpnConnectionState.CONNECTING
            2 -> VpnConnectionState.CONNECTED
            else -> VpnConnectionState.DISCONNECTED
        }
    }

    override fun InitHealthCheck() {
        HealthCheckBackendWrapper.initHealthCheck(config)
    }

    override fun StartHealthCheck() {
        HealthCheckBackendWrapper.startHealthCheck()
    }

    override fun StopHealthCheck() {
        HealthCheckBackendWrapper.stopHealthCheck()
    }
}
