package com.dobby.feature.vpn_service.domain.awg

import com.dobby.backend.AwgBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import kotlinx.serialization.decodeFromString
import net.peanuuutz.tomlkt.Toml

class AmneziaWGInteractor(
    private val logger: Logger,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
) {
    fun startAwg(dobbyVpnService: DobbyVpnService?): Boolean {
        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val shouldTurnOn = dobbyConfigsRepository.getIsAmneziaWGEnabled()
        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): shouldTurnOutlineOn=$shouldTurnOn")

        if (!shouldTurnOn) {
            logger.log("Start disconnecting AmneziaWG")
            return false
        }

        val tomlConfigString = dobbyConfigsRepository.getAwgTomlConfig()
        val tomlConfig = if (tomlConfigString.isBlank()) {
            logger.log("AmneziaWG config is empty")
            return false
        } else {
            try {
                Toml.decodeFromString<AmneziaWGConfig>(tomlConfigString)
            } catch (e: Exception) {
                logger.log("AmneziaWG config parse failed: $e")
                return false
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
            return false
        }

        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): initializing AmneziaWG with tunFd=$tunFd")

        val connected = AwgBackendWrapper.awgTurnOn("awg0", tunFd, tomlConfig.toAwgQuick())
        if (connected != 0) {
            logger.log("AmneziaWG connection FAILED, stopping VPN service")
            return false
        }
        logger.log("AmneziaWG connected successfully")

        dobbyVpnService?.protect(AwgBackendWrapper.awgGetSocketV4())
        dobbyVpnService?.protect(AwgBackendWrapper.awgGetSocketV6())
        logger.log("AmneziaWG peers protect")

        logger.log("[svc:${dobbyVpnService?.serviceId}] startAwg(): completed (status=true) vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        return true
    }

    fun stopAwg() {
        logger.log("[svc:...] stopAwg()")
        AwgBackendWrapper.awgTurnOff()
        logger.log("[svc:...] stopAwg(): completed")
    }
}
