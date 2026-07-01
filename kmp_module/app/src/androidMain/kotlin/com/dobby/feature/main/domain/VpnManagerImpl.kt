package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.feature.vpn_service.DobbyVpnService

class VpnManagerImpl(
    private val context: Context,
): VpnManager {
    override val supportsVpnNetworkReadySignal: Boolean = true

    override fun start(isProtocolProbe: Boolean) {
        DobbyVpnService
            .createIntent(context, isProtocolProbe)
            .let(context::startService)
    }

    override fun stop() {
        DobbyVpnService
            .instance
            ?.stopService()
    }
}
