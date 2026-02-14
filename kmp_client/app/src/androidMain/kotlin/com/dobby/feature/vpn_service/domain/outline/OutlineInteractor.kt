package com.dobby.feature.vpn_service.domain.outline;

import android.content.Intent
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.IS_FROM_UI
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.feature.vpn_service.domain.cloak.ConnectResult

import java.util.Base64

private fun extractHostFromHostPort(hostPortMaybeWithQuery: String): String {
    val hostPort = hostPortMaybeWithQuery.substringBefore("?").trim()
    if (hostPort.startsWith("[")) {
        // IPv6 in brackets: [2001:db8::1]:443
        return hostPort.substringAfter("[").substringBefore("]")
    }
    // host:port (best-effort)
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
    val result = if (websocketEnabled) {
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

    return result
}

class OutlineInteractor(
    private val logger: Logger,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val outlineLibFacade: OutlineLibFacade
) {

    suspend fun startOutline(intent: Intent?, dobbyVpnService: DobbyVpnService?) {
        logger.log("[svc:${dobbyVpnService?.serviceId}] startCloakOutline(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnOutlineOn = dobbyConfigsRepository.getIsOutlineEnabled()
        logger.log("[svc:${dobbyVpnService?.serviceId}] startCloakOutline(): fromUi=$isServiceStartedFromUi shouldTurnOutlineOn=$shouldTurnOutlineOn")

        if (!shouldTurnOutlineOn && isServiceStartedFromUi) {
            logger.log("Start disconnecting Outline")
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPortOutline()
        val prefix = dobbyConfigsRepository.getPrefixOutline()
        val websocketEnabled = dobbyConfigsRepository.getIsWebsocketEnabled()
        val tcpPath = dobbyConfigsRepository.getTcpPathOutline()
        val udpPath = dobbyConfigsRepository.getUdpPathOutline()
        logger.log("DEBUG: tcpPath='$tcpPath', udpPath='$udpPath'")

        if (methodPassword.isEmpty() || serverPort.isEmpty()) {
            logger.log("Previously used outline apiKey is empty")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("Start connecting Outline")
        val outlineUrl = buildOutlineUrl(
            methodPassword = methodPassword,
            serverPort = serverPort,
            prefix = prefix,
            websocketEnabled = websocketEnabled,
            tcpPath = tcpPath,
            udpPath = udpPath
        )
        logger.log("Outline URL built (prefix=${prefix.isNotEmpty()}, ws=$websocketEnabled, tcpPath=${tcpPath.isNotEmpty()}, udpPath=${udpPath.isNotEmpty()})")
        logger.log("Outline URL: $outlineUrl")

        dobbyVpnService?.setupVpn()

        val dupPfd = dobbyVpnService?.vpnInterface?.dup()
        val tunFd = dupPfd?.detachFd() ?: -1
        dobbyVpnService?.goTunFd = if (tunFd != -1) tunFd else null

        if (tunFd < 0) {
            logger.log("[svc:${dobbyVpnService?.serviceId}] startCloakOutline(): failed to create VPN interface")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }

        logger.log("[svc:${dobbyVpnService?.serviceId}] startCloakOutline(): initializing Xray with tunFd=$tunFd")

        val connected = outlineLibFacade.init(outlineUrl, tunFd)
        if (!connected) {
            logger.log("Outline connection FAILED, stopping VPN service")
            dobbyVpnService?.connectionState?.tryUpdateStatus(false)
            dobbyVpnService?.teardownVpn()
            dobbyVpnService?.stopSelf()
            return
        }
        logger.log("outlineLibFacade connected successfully")
        if (websocketEnabled) {
            logger.log("WebSocket transport connected successfully")
        }
        dobbyVpnService?.connectionState?.updateStatus(true)
        logger.log("[svc:${dobbyVpnService?.serviceId}] startCloakOutline(): completed (status=true) vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
    }

    fun stopOutline() {
        outlineLibFacade.disconnect()
    }
}
