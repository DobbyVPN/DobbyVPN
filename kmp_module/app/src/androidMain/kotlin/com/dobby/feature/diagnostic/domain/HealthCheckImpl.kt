package com.dobby.feature.diagnostic.domain

import android.os.SystemClock
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import com.dobby.backend.GoBackendWrapper
import com.dobby.backend.HealthCheckBackendWrapper
import kotlin.concurrent.thread

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
        HealthCheckBackendWrapper.initHealthCheck()
    }

    override fun StartHealthCheck() {
        HealthCheckBackendWrapper.startHealthCheck()
    }

    override fun StopHealthCheck() {
        HealthCheckBackendWrapper.stopHealthCheck()
    }
}
