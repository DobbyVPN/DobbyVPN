package com.dobby.feature.vpn_service.domain.descriptor

import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService

class FDManager(
    private val logger: Logger
) {
    fun GetTunFd(serviceId: String, dobbyVpnService: DobbyVpnService?) : Int {
        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        if (tunFd < 0) {
            logger.log("[svc:$serviceId] startXray(): failed to create VPN interface")
            return -1
        }
        return tunFd
    }
}
