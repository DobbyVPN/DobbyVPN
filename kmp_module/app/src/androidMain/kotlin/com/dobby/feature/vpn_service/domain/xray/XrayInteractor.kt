package com.dobby.feature.vpn_service.domain.xray

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.domain.descriptor.FDManager

class XrayInteractor(
    private val xrayLibFacade: com.dobby.feature.vpn_service.XrayLibFacade,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val fdManager: FDManager,
) {
    private val interfaceFactory = XrayVpnInterfaceFactory(logger)

    fun startXray(dobbyVpnService: DobbyVpnService?): Boolean {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startXray(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val shouldTurnXrayOn = dobbyConfigsRepository.getIsXrayEnabled()
        logger.log("[svc:$serviceId] startXray(): shouldTurnXrayOn=$shouldTurnXrayOn")

        if (!shouldTurnXrayOn) {
            logger.log("Start disconnecting Xray")
            return false
        }

        val xrayConfig = dobbyConfigsRepository.getXrayConfig()
        if (xrayConfig.isEmpty()) {
            logger.log("[svc:$serviceId] startXray(): Xray config is empty, cannot start")
            return false
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
        if (tunFd < 0) return false

        logger.log("[svc:$serviceId] startXray(): initializing Xray with tunFd=$tunFd")

        val connected = xrayLibFacade.init(xrayConfig, tunFd)

        if (!connected) {
            logger.log("[svc:$serviceId] startXray(): connection FAILED, stopping VPN service")
            return false
        }

        logger.log("[svc:$serviceId] startXray(): connected successfully")
        return true
    }

    fun stopXray(dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] stopXray(): disconnecting Xray")

        xrayLibFacade.disconnect()
    }
}
