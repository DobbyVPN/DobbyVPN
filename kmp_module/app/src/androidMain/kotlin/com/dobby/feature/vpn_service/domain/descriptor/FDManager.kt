package com.dobby.feature.vpn_service.domain.descriptor

import com.dobby.feature.vpn_service.DobbyVpnService

class FDManager {
    fun GetTunFd(dobbyVpnService: DobbyVpnService?) : Int {
        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        return if (tunFd < 0) -1 else tunFd
    }
}
