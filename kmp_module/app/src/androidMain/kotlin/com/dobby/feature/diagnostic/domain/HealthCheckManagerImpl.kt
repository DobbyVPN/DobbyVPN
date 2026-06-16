package com.dobby.feature.diagnostic.domain

import android.os.SystemClock
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import com.dobby.backend.GoBackendWrapper
import kotlin.concurrent.thread

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

    override fun start() {
        GoBackendWrapper.startHealthCheck()
    }

    override fun stop() {
        GoBackendWrapper.stopHealthCheck()
    }
}
