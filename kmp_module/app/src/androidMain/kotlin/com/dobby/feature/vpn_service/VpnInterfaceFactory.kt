package com.dobby.feature.vpn_service

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService.Builder
import android.os.Build
import android.util.Log
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.common.reservedBypassSubnets

interface VpnInterfaceFactory {
    fun create(context: Context, vpnService: DobbyVpnService): Builder
}
