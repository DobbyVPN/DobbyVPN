package com.dobby.feature.vpn_service.domain.trusttunnel

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryTrustTunnel
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.TrustTunnelLibFacade

class TrustTunnelInteractor(
    private val trustTunnelLibFacade: TrustTunnelLibFacade,
    private val trustTunnelRepo: DobbyConfigsRepositoryTrustTunnel,
    private val logger: Logger
) {
    suspend fun startTrustTunnel(dobbyVpnService: DobbyVpnService?) {
        val config = trustTunnelRepo.getTrustTunnelConfig()
        if (config.isBlank()) {
            logger.log("[TrustTunnelInteractor] No config found, aborting.")
            return
        }

        // 1. Set up the Android TUN interface
        dobbyVpnService?.setupVpn()

        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        if (tunFd < 0) {
            logger.log("[svc:${dobbyVpnService?.serviceId}] TrustTunnel: failed to create VPN interface")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("[svc:${dobbyVpnService?.serviceId}] TrustTunnel: initializing with tunFd=$tunFd")

        // 2. Inject the Socket Protector so C++ doesn't cause an infinite routing loop
//        dobbyVpnService?.let {
//            val protector = AndroidSocketProtector(it)
//            OutlineGo.setSocketProtector(protector)
//        }

        // 3. Hand off to the Go/C++ Engine
        val connected = trustTunnelLibFacade.init(config, tunFd)
        if (!connected) {
            logger.log("TrustTunnel connection FAILED, stopping VPN service")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("trustTunnelLibFacade connected successfully")
        dobbyVpnService?.connectionState?.updateStatus(true)
        logger.log("[svc:${dobbyVpnService?.serviceId}] TrustTunnel started successfully")
    }

    fun stopTrustTunnel() {
        logger.log("[TrustTunnelInteractor] stopping TrustTunnel")
        trustTunnelLibFacade.disconnect()
    }
}
