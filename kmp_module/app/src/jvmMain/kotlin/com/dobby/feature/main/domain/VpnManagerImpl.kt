package com.dobby.feature.main.domain

import com.dobby.feature.vpn_service.DobbyVpnService

internal class VpnManagerImpl(
    private val connectionStateRepository: ConnectionStateRepository,
    private val dobbyVpnService: DobbyVpnService,
) : VpnManager {

    override fun start() {
        val isStarted = dobbyVpnService.startService()
        connectionStateRepository.tryUpdateServiceStarted(isStarted)
    }

    override fun stop() {
        dobbyVpnService.stopService()
    }
}
