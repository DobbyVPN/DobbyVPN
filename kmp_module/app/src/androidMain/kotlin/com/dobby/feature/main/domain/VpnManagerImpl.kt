package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.feature.vpn_service.DobbyVpnService
import org.koin.android.scope.destroyServiceScope

class VpnManagerImpl(
    private val context: Context,
): VpnManager {

    override fun start() {
        val service = DobbyVpnService.instance
        if (service != null) {
            service.startService()
        } else {
            DobbyVpnService
                .createIntent(context)
                .let(context::startService)
        }
    }

    override fun stop() {
        DobbyVpnService.instance?.stopService()
    }
}
