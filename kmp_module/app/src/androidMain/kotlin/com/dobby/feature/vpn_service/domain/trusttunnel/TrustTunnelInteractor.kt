package com.dobby.feature.vpn_service.domain.trusttunnel

import android.content.Intent
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.IS_FROM_UI
import com.dobby.feature.vpn_service.domain.descriptor.FDManager

class TrustTunnelInteractor(
    private val trustTunnelLibFacade: com.dobby.feature.vpn_service.TrustTunnelLibFacade,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val fdManager: FDManager,
) {
    private val interfaceFactory = TrustTunnelVpnInterfaceFactory(logger)

    fun startTrustTunnel(dobbyVpnService: DobbyVpnService?): Boolean {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startTrustTunnel(): begin vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        
        val shouldTurnOn = dobbyConfigsRepository.getIsTrustTunnelEnabled()
        if (!shouldTurnOn) {
            logger.log("[svc:$serviceId] startTrustTunnel(): TrustTunnel not enabled")
            return false
        }

        val config = dobbyConfigsRepository.getTrustTunnelConfig()
        if (config.isEmpty()) {
            logger.log("[svc:$serviceId] startTrustTunnel(): TrustTunnel config is empty, cannot start")
            return false
        }

        dobbyVpnService?.run {
            logger.log("[svc:$serviceId] startTrustTunnel(): establishing VPN interface")
            vpnInterface = runCatching {
                interfaceFactory.create(context = this, vpnService = this).establish()
            }.onFailure { e ->
                logger.log("[svc:$serviceId] startTrustTunnel(): establish FAILED: ${e.message}")
            }.getOrNull()
        }

        val tunFd = fdManager.GetTunFd(dobbyVpnService)
        if (tunFd < 0) {
            logger.log("[svc:$serviceId] startTrustTunnel(): failed to get tunFd")
            return false
        }

        logger.log("[svc:$serviceId] startTrustTunnel(): initializing TrustTunnel with tunFd=$tunFd")

        val connected = trustTunnelLibFacade.init(config, tunFd)

        if (!connected) {
            logger.log("[svc:$serviceId] startTrustTunnel(): connection FAILED, stopping VPN service")
            return false
        }

        logger.log("[svc:$serviceId] startTrustTunnel(): connected successfully")
        return true
    }

    fun stopTrustTunnel(dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] stopTrustTunnel(): disconnecting TrustTunnel")

        trustTunnelLibFacade.disconnect()
    }
}
