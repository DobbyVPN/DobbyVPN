package com.dobby.feature.vpn_service

import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.VPNLibraryLoader
import kotlinx.coroutines.runBlocking
import java.util.Base64

private fun extractHostFromHostPort(hostPortMaybeWithQuery: String): String {
    val hostPort = hostPortMaybeWithQuery.substringBefore("?").trim()
    if (hostPort.startsWith("[")) {
        return hostPort.substringAfter("[").substringBefore("]")
    }
    val lastColon = hostPort.lastIndexOf(':')
    return if (lastColon > 0 && hostPort.count { it == ':' } == 1) {
        hostPort.substring(0, lastColon)
    } else {
        hostPort
    }
}

private fun buildOutlineUrl(
    methodPassword: String,
    serverPort: String,
    prefix: String = "",
    websocketEnabled: Boolean = false,
    tcpPath: String = "",
    udpPath: String = ""
): String {
    val encoded = Base64.getEncoder().encodeToString(methodPassword.toByteArray())
    val baseUrl = "ss://$encoded@$serverPort"

    // Add prefix parameter if present (URL-encoded)
    val ssUrl = if (prefix.isNotEmpty()) {
        val separator = if (serverPort.contains("?")) "&" else "?"
        val encodedPrefix = java.net.URLEncoder.encode(prefix, "UTF-8")
        "$baseUrl${separator}prefix=$encodedPrefix"
    } else {
        baseUrl
    }

    // Wrap with WebSocket over TLS transport if enabled (wss://)
    return if (websocketEnabled) {
        val effectiveHost = extractHostFromHostPort(serverPort).trim()
        val wsParams = buildList {
            if (tcpPath.isNotEmpty()) add("tcp_path=$tcpPath")
            if (udpPath.isNotEmpty()) add("udp_path=$udpPath")
        }.joinToString("&")
        
        // Use tls:sni|ws: for WebSocket over TLS (wss://) with SNI
        val tlsPrefix = "tls:sni=$effectiveHost"
        if (wsParams.isNotEmpty()) {
            "$tlsPrefix|ws:$wsParams|$ssUrl"
        } else {
            "$tlsPrefix|ws:|$ssUrl"
        }
    } else {
        ssUrl
    }
}

internal class DobbyVpnService(
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val vpnLibrary: VPNLibraryLoader,
    private val connectionState: ConnectionStateRepository
) {
    private val startStopLock = Any()
    private var runningInterface: VpnInterface? = null

    fun startService() {
        synchronized(startStopLock) {
            if (runningInterface != null) {
                stopCurrentLocked()
            }

            val iface = dobbyConfigsRepository.getVpnInterface()
            when (iface) {
                VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
                VpnInterface.AMNEZIA_WG -> startAwg()
            }
            runningInterface = iface
        }
    }

    fun stopService() {
        synchronized(startStopLock) {
            stopCurrentLocked()
        }
    }

    private fun stopCurrentLocked() {
        when (runningInterface) {
            VpnInterface.CLOAK_OUTLINE -> stopCloakOutline()
            VpnInterface.AMNEZIA_WG -> stopAwg()
            null -> return
        }
        runningInterface = null
    }

    private fun startCloakOutline() {
        logger.log("Start startCloakOutline")
        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPortOutline()
        val prefix = dobbyConfigsRepository.getPrefixOutline()
        val websocketEnabled = dobbyConfigsRepository.getIsWebsocketEnabled()
        val tcpPath = dobbyConfigsRepository.getTcpPathOutline()
        val udpPath = dobbyConfigsRepository.getUdpPathOutline()
        val localHost = "127.0.0.1"
        val localPort = dobbyConfigsRepository.getCloakLocalPort().toString()
        logger.log("startCloakOutline with key: methodPassword = ${maskStr(methodPassword)} serverPort = ${maskStr(serverPort)}")
        logger.log("Outline prefix: ${prefix.ifEmpty { "(none)" }}")
        logger.log("Outline websocket: $websocketEnabled, tcpPath: ${tcpPath.ifEmpty { "(none)" }}, udpPath: ${udpPath.ifEmpty { "(none)" }}")
        runBlocking {
            connectionState.updateVpnStarted(isStarted = true)
            logger.log("CloakIsEnable = " + dobbyConfigsRepository.getIsCloakEnabled())
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                vpnLibrary.startCloak(localHost, localPort, dobbyConfigsRepository.getCloakConfig(), false)
            }
            val outlineUrl = buildOutlineUrl(
                methodPassword = methodPassword,
                serverPort = serverPort,
                prefix = prefix,
                websocketEnabled = websocketEnabled,
                tcpPath = tcpPath,
                udpPath = udpPath
            )
            logger.log("Outline URL built (prefix=${prefix.isNotEmpty()}, ws=$websocketEnabled, tcpPath=${tcpPath.isNotEmpty()}, udpPath=${udpPath.isNotEmpty()})")
            if (websocketEnabled) {
                logger.log("WebSocket transport requested (will connect if server supports it)")
            }
            vpnLibrary.startOutline(outlineUrl)
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
            connectionState.updateVpnStarted(isStarted = false)
        }
    }

    private fun startAwg() {
        val apiKey = dobbyConfigsRepository.getAwgConfig()
        logger.log("startAwg with key: $apiKey")
        runBlocking { connectionState.updateVpnStarted(isStarted = true) }
        vpnLibrary.startAwg(apiKey)
    }

    private fun stopAwg() {
        logger.log("stopAwg")
        vpnLibrary.stopAwg()
        runBlocking { connectionState.updateVpnStarted(isStarted = false) }
    }

}
