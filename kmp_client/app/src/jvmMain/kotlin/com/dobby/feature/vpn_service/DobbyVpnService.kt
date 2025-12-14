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
    serverPort: String,
    prefix: String,
    dataPrefix: String
): String {
    val encoded = Base64.getEncoder().encodeToString(methodPassword.toByteArray())
    
    // Build ss:// URL with optional data prefix as query parameter
    // Use & if serverPort already contains query params (e.g. ?outline=1)
    val dataPrefixParam = if (dataPrefix.isNotEmpty()) {
        val encodedDataPrefix = java.net.URLEncoder.encode(dataPrefix, "UTF-8")
        val separator = if (serverPort.contains("?")) "&" else "?"
        "${separator}prefix=$encodedDataPrefix"
    } else ""
    
    val ssUrl = "ss://$encoded@$serverPort$dataPrefixParam"
    
    // Wrap with transport prefix if provided
    val cleanedPrefix = prefix.trim().trim('|')
    return if (cleanedPrefix.isEmpty()) ssUrl else "$cleanedPrefix|$ssUrl"
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
        logger.log("Start startCloakOutline")
        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPortOutline()
        val prefix = dobbyConfigsRepository.getPrefixOutline()
        val dataPrefix = dobbyConfigsRepository.getDataPrefixOutline()
        val localHost = "127.0.0.1"
        val localPort = "1984"
        runBlocking {
            connectionState.update(isConnected = true)
            logger.log("CloakIsEnable = " + dobbyConfigsRepository.getIsCloakEnabled())
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                vpnLibrary.startCloak(localHost, localPort, dobbyConfigsRepository.getCloakConfig(), false)
            }
            vpnLibrary.startOutline(buildOutlineUrl(methodPassword, serverPort, prefix, dataPrefix))
        }
    }


    private fun stopCloakOutline() {
        logger.log("StopOutline")
        runBlocking {
            vpnLibrary.stopOutline()
            logger.log("CloakIsEnable = " + dobbyConfigsRepository.getIsCloakEnabled())
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                logger.log("StopCloak")
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