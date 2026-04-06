package com.dobby.feature.main.domain

import com.dobby.feature.vpn_service.DobbyVpnService

internal class AwgManagerImpl(
    private val dobbyVpnService: DobbyVpnService,
): AwgManager {

    override fun getAwgVersion(): String { return "AwgVersion" }
    override fun onAwgConnect() {
        dobbyVpnService.startService()
    }

    override fun onAwgDisconnect() {
        dobbyVpnService.stopService()
    }

}
