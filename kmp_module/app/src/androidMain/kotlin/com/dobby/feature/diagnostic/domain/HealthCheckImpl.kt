package com.dobby.feature.diagnostic.domain

import android.os.SystemClock
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import com.dobby.outline.OutlineGo
import kotlin.concurrent.thread
import kotlin.math.log

class HealthCheckImpl : HealthCheck {
    override fun GetConnectionState(): VpnConnectionState {
        return when (OutlineGo.getConnectionState()) {
            0 -> VpnConnectionState.DISCONNECTED
            1 -> VpnConnectionState.CONNECTING
            2 -> VpnConnectionState.CONNECTED
            else -> VpnConnectionState.DISCONNECTED
        }
    }

    override fun InitHealthCheck() {
        OutlineGo.initHealthCheck()
    }

    override fun StartHealthCheck() {
        OutlineGo.startHealthCheck()
    }

    override fun StopHealthCheck() {
        OutlineGo.stopHealthCheck()
    }
}
