package com.dobby.feature.main.domain

import com.dobby.feature.vpn_service.DobbyVpnService

internal class VpnManagerImpl(
    private val connectionStateRepository: ConnectionStateRepository,
    private val dobbyVpnService: DobbyVpnService,
) : VpnManager {
    override val supportsVpnNetworkReadySignal: Boolean = false

    override fun start(isProtocolProbe: Boolean) {
        val isStarted = dobbyVpnService.startService(isProtocolProbe)
        connectionStateRepository.tryUpdateServiceStarted(isStarted)
    }

    override fun stop(isUserInitiated: Boolean) {
        dobbyVpnService.stopService()
    }
}
