package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.awg.GoBackendWrapper
import com.dobby.feature.vpn_service.DobbyVpnService

class AwgManagerImpl(
    private val context: Context,
): AwgManager {

    override fun getAwgVersion() = GoBackendWrapper.awgVersion()
    override fun onAwgConnect() {
        DobbyVpnService
            .createIntent(context)
            .let(context::startService)
    }

    override fun onAwgDisconnect() {
        val vpnServiceIntent = DobbyVpnService.createIntent(context)
        context.startService(vpnServiceIntent)
        context.stopService(vpnServiceIntent)
    }
}
