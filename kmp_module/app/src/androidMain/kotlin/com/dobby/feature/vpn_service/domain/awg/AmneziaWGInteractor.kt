package com.dobby.feature.vpn_service.domain.awg

import android.content.Intent
import com.dobby.awg.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.IS_FROM_UI
import kotlinx.serialization.decodeFromString
import net.peanuuutz.tomlkt.Toml

class AmneziaWGInteractor(
    private val logger: Logger,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
) {

    private fun startupFailure(dobbyVpnService: DobbyVpnService?) {
        dobbyVpnService?.connectionState?.tryUpdateStatus(false)
        dobbyVpnService?.teardownVpn()
        dobbyVpnService?.stopSelf()
    }

    suspend fun startAwg(intent: Intent?, dobbyVpnService: DobbyVpnService?) {
        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnOn = dobbyConfigsRepository.getIsAmneziaWGEnabled()
        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): fromUi=$isServiceStartedFromUi shouldTurnOutlineOn=$shouldTurnOn")

        if (!shouldTurnOn && isServiceStartedFromUi) {
            logger.log("Start disconnecting AmneziaWG")
            return startupFailure(dobbyVpnService)
        }

        val tomlConfigString = dobbyConfigsRepository.getAwgTomlConfig()
        val tomlConfig = if (tomlConfigString.isBlank()) {
            logger.log("AmneziaWG config is empty")
            return startupFailure(dobbyVpnService)
        } else {
            try {
                Toml.decodeFromString<AmneziaWGConfig>(tomlConfigString)
            } catch (e: Exception) {
                logger.log("AmneziaWG config parse failed: $e")
                return startupFailure(dobbyVpnService)
            }
        }

        val interfaceFactory = AmneziaWGVpnInterfaceFactory(logger, tomlConfig)
        dobbyVpnService?.run {
            logger.log("[svc:$${serviceId}] setupVpn(): begin")
            vpnInterface = runCatching {
                interfaceFactory.create(context=this, vpnService=this).establish()
            }.onFailure { e ->
                logger.log("[svc:$${serviceId}] setupVpn(): establish FAILED: ${e.message}")
            }.getOrNull()
        }

        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        if (tunFd < 0) {
            logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): failed to create VPN interface")
            return startupFailure(dobbyVpnService)
        }

        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): initializing AmneziaWG with tunFd=$tunFd")

        val connected = GoBackendWrapper.awgTurnOn("awg0", tunFd, tomlConfig.toAwgQuick())
        if (connected != 0) {
            logger.log("AmneziaWG connection FAILED, stopping VPN service")
            return startupFailure(dobbyVpnService)
        }
        logger.log("AmneziaWG connected successfully")

        dobbyVpnService?.protect(GoBackendWrapper.awgGetSocketV4())
        dobbyVpnService?.protect(GoBackendWrapper.awgGetSocketV6())
        logger.log("AmneziaWG peers protect")

        dobbyVpnService?.connectionState?.updateStatus(true)
        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): completed (status=true) vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
    }

    fun stopAwg() {
        logger.log("[svc:...] stopAwg()")
        GoBackendWrapper.awgTurnOff()
        logger.log("[svc:...] stopAwg(): completed")
    }
}
