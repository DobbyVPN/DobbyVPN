package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.awg.GoBackendWrapper
import com.dobby.feature.vpn_service.MyVpnService

class AwgManagerImpl(
    private val context: Context,
): AwgManager {

    override fun getAwgVersion() = GoBackendWrapper.awgVersion()
    override fun onAwgConnect() {
        MyVpnService
            .createIntent(context)
            .let(context::startService)
    }

    override fun onAwgDisconnect() {
        val vpnServiceIntent = MyVpnService.createIntent(context)
        context.startService(vpnServiceIntent)
        context.stopService(vpnServiceIntent)
    }

}
