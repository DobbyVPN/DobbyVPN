package com.dobby.feature.main.domain

import android.content.Context
import androidx.core.content.ContextCompat
import com.dobby.feature.vpn_service.DobbyVpnService

class VpnManagerImpl(
    private val context: Context,
): VpnManager {
    override val supportsVpnNetworkReadySignal: Boolean = true

    override fun start(isProtocolProbe: Boolean) {
        start(isProtocolProbe, generation = 0L)
    }

    override fun start(isProtocolProbe: Boolean, generation: Long) {
        DobbyVpnService
            .createStartIntent(context, isProtocolProbe, generation)
            .let { ContextCompat.startForegroundService(context, it) }
    }

    override fun stop(isUserInitiated: Boolean) {
        stop(isUserInitiated, generation = Long.MAX_VALUE)
    }

    override fun stop(isUserInitiated: Boolean, generation: Long) {
        DobbyVpnService
            .createStopIntent(context, generation, isUserInitiated)
            .let(context::startService)
    }
}
