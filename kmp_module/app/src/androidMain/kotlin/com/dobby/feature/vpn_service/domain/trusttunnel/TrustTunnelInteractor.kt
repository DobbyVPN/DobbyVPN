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

    suspend fun startTrustTunnel(intent: Intent?, dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startTrustTunnel(): begin vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnOn = dobbyConfigsRepository.getIsTrustTunnelEnabled()
        logger.log("[svc:$serviceId] startTrustTunnel(): fromUi=$isServiceStartedFromUi shouldTurnOn=$shouldTurnOn")

        if (!shouldTurnOn && isServiceStartedFromUi) {
            logger.log("[svc:$serviceId] startTrustTunnel(): disconnecting TrustTunnel (not enabled)")
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        val config = dobbyConfigsRepository.getTrustTunnelConfig()
        if (config.isEmpty()) {
            logger.log("[svc:$serviceId] startTrustTunnel(): TrustTunnel config is empty, cannot start")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.stopSelf()
            return
        }

        dobbyVpnService?.run {
            logger.log("[svc:$serviceId] startTrustTunnel(): establishing VPN interface")
            vpnInterface = runCatching {
                interfaceFactory.create(context = this, vpnService = this).establish()
            }.onFailure { e ->
                logger.log("[svc:$serviceId] startTrustTunnel(): establish FAILED: ${e.message}")
            }.getOrNull()
        }

        val tunFd = fdManager.GetTunFd(serviceId, dobbyVpnService)
        if (tunFd < 0) return

        logger.log("[svc:$serviceId] startTrustTunnel(): initializing TrustTunnel with tunFd=$tunFd")

        val connected = trustTunnelLibFacade.init(config, tunFd)

        if (!connected) {
            logger.log("[svc:$serviceId] startTrustTunnel(): connection FAILED, stopping VPN service")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("[svc:$serviceId] startTrustTunnel(): connected successfully")
        dobbyVpnService?.connectionState?.updateStatus(true)
    }

    suspend fun stopTrustTunnel(dobbyVpnService: DobbyVpnService?, updateState: Boolean = true) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] stopTrustTunnel(): disconnecting TrustTunnel updateState=$updateState")

        trustTunnelLibFacade.disconnect()

        if (updateState) {
            dobbyVpnService?.connectionState?.updateStatus(false)
        }
    }
}
