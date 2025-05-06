package com.dobby.feature.main.domain

import com.dobby.feature.vpn_service.DobbyVpnService

internal class VpnManagerImpl(
    private val dobbyVpnService: DobbyVpnService,
) : VpnManager {

    override fun start() {
        dobbyVpnService.startService()
    }

    override fun stop() {
        dobbyVpnService.stopService()
    }
}
