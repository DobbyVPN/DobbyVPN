package com.dobby.feature.vpn_service.domain.xray

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService

class XrayInteractor(
    private val xrayLibFacade: com.dobby.feature.vpn_service.XrayLibFacade,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger
) {

    suspend fun startXray(dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startXray(): begin")

        val xrayConfig = dobbyConfigsRepository.getXrayConfig()
        if (xrayConfig.isEmpty()) {
            logger.log("[svc:$serviceId] startXray(): Xray config is empty, cannot start")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.stopSelf()
            return
        }

        dobbyVpnService?.setupVpn()

        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        if (tunFd < 0) {
            logger.log("[svc:$serviceId] startXray(): failed to create VPN interface")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

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

    suspend fun stopXray(dobbyVpnService: DobbyVpnService?) {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] stopXray(): disconnecting Xray")

        xrayLibFacade.disconnect()

        dobbyVpnService?.connectionState?.updateStatus(false)
    }
}
