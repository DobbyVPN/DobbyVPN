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

        // 1. Получаем конфигурацию Xray
        val xrayConfig = dobbyConfigsRepository.getXrayConfig()
        if (xrayConfig.isEmpty()) {
            logger.log("[svc:$serviceId] startXray(): Xray config is empty, cannot start")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.stopSelf()
            return
        }

        // 2. Поднимаем VPN интерфейс (Android VpnService)
        dobbyVpnService?.setupVpn()

        // 3. Дублируем файловый дескриптор, чтобы передать его в Go (tun2socks / Xray)
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

        // 4. Передаем конфиг и дескриптор в XrayLibFacade
        val connected = xrayLibFacade.init(xrayConfig, tunFd)

        // 5. Обрабатываем результат
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

        // Обновляем состояние приложения
        dobbyVpnService?.connectionState?.updateStatus(false)
    }
}
