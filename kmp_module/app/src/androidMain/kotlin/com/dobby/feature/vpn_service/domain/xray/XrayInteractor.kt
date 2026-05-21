package com.dobby.feature.vpn_service.domain.xray

import android.content.Intent
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.IS_FROM_UI
import com.dobby.feature.vpn_service.domain.descriptor.FDManager

class XrayInteractor(
    private val xrayLibFacade: com.dobby.feature.vpn_service.XrayLibFacade,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val fdManager: FDManager,
) {
    private val interfaceFactory = XrayVpnInterfaceFactory(logger)
    suspend fun startXray(intent: Intent?, dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startXray(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnXrayOn = dobbyConfigsRepository.getIsXrayEnabled()
        logger.log("[svc:$serviceId] startXray(): fromUi=$isServiceStartedFromUi shouldTurnXrayOn=$shouldTurnXrayOn")

        if (!shouldTurnXrayOn && isServiceStartedFromUi) {
            logger.log("Start disconnecting Xray")
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        val xrayConfig = dobbyConfigsRepository.getXrayConfig()
        if (xrayConfig.isEmpty()) {
            logger.log("[svc:$serviceId] startXray(): Xray config is empty, cannot start")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.stopSelf()
            return
        }

        dobbyVpnService?.run {
            logger.log("[svc:$serviceId] setupVpn(): begin")
            vpnInterface = runCatching {
                interfaceFactory.create(context = this, vpnService = this).establish()
            }.onFailure { e ->
                logger.log("[svc:$serviceId] setupVpn(): establish FAILED: ${e.message}")
            }.getOrNull()
        }

        val tunFd = fdManager.GetTunFd(serviceId, dobbyVpnService)
        if (tunFd < 0) return

        logger.log("[svc:$serviceId] startXray(): initializing Xray with tunFd=$tunFd")

        val connected = xrayLibFacade.init(xrayConfig, tunFd)

        if (!connected) {
            logger.log("[svc:$serviceId] startXray(): connection FAILED, stopping VPN service")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("[svc:$serviceId] startXray(): connected successfully")
        dobbyVpnService?.connectionState?.updateStatus(true)
    }

    suspend fun stopXray(dobbyVpnService: DobbyVpnService?, updateState: Boolean = true) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] stopXray(): disconnecting Xray updateState=$updateState")

        xrayLibFacade.disconnect()

        if (updateState) {
            dobbyVpnService?.connectionState?.updateStatus(false)
        }
    }
}
