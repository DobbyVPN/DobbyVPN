package com.dobby.feature.vpn_service

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.VPNLibraryLoader
import kotlinx.coroutines.runBlocking
import java.util.Base64

private fun buildOutlineUrl(
    methodPassword: String,
    serverPort: String
): String {
    val encoded = Base64.getEncoder().encodeToString(methodPassword.toByteArray())
    return "ss://$encoded@$serverPort/?outline=1"
}

internal class DobbyVpnService(
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val vpnLibrary: VPNLibraryLoader,
    private val connectionState: ConnectionStateRepository
) {

    fun startService() {
        when(dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
            VpnInterface.AMNEZIA_WG -> startAwg()
        }
    }

    fun stopService() {
        when(dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> stopCloakOutline()
            VpnInterface.AMNEZIA_WG -> stopAwg()
        }
    }


    private fun startCloakOutline() {
        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPortOutline()
        val localHost = "127.0.0.1"
        val localPort = "1984"
        logger.log("startCloakOutline with key: $methodPassword $serverPort")
        runBlocking {
            connectionState.update(isConnected = true)
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                vpnLibrary.startCloak(localHost, localPort, dobbyConfigsRepository.getCloakConfig(), false)
            }
            vpnLibrary.startOutline(buildOutlineUrl(methodPassword, serverPort))
        }
    }


    private fun stopCloakOutline() {
        logger.log("StopOutline")
        runBlocking {
            vpnLibrary.stopOutline()
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                vpnLibrary.stopCloak()
            }
            connectionState.update(isConnected = false)
        }
    }

    private fun startAwg() {
        val apiKey = dobbyConfigsRepository.getAwgConfig()
        logger.log("startAwg with key: $apiKey")
        vpnLibrary.startAwg(apiKey)
    }

    private fun stopAwg() {
        logger.log("stopAwg")
        vpnLibrary.stopAwg()
    }

}