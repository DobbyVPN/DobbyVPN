package com.dobby.feature.vpn_service.domain.outline

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.feature.vpn_service.domain.descriptor.FDManager
import java.util.*

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
    private val outlineLibFacade: OutlineLibFacade,
    private val fdManager: FDManager,
) {
    private val interfaceFactory = OutlineVpnInterfaceFactory(logger)

    fun startOutline(dobbyVpnService: DobbyVpnService?): Boolean {
        val serviceId = dobbyVpnService?.serviceId ?: "unknown"
        logger.log("[svc:$serviceId] startCloakOutline(): lock acquired vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        val shouldTurnOutlineOn = dobbyConfigsRepository.getIsOutlineEnabled()
        logger.log("[svc:$serviceId] startCloakOutline(): shouldTurnOutlineOn=$shouldTurnOutlineOn")

        if (!shouldTurnOutlineOn) {
            logger.log("Start disconnecting Outline")
            return false
        }

        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPort()
        val prefix = dobbyConfigsRepository.getPrefixOutline()
        val websocketEnabled = dobbyConfigsRepository.getIsWebsocketEnabled()
        val tcpPath = dobbyConfigsRepository.getTcpPathOutline()
        val udpPath = dobbyConfigsRepository.getUdpPathOutline()
        logger.log("DEBUG: tcpPath='$tcpPath', udpPath='$udpPath'")

        if (methodPassword.isEmpty() || serverPort.isEmpty()) {
            logger.log("Previously used outline apiKey is empty")
            return false
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

        dobbyVpnService?.run {
            logger.log("[svc:$${serviceId}] setupVpn(): begin")
            vpnInterface = runCatching {
                interfaceFactory.create(context=this, vpnService=this).establish()
            }.onFailure { e ->
                logger.log("[svc:$${serviceId}] setupVpn(): establish FAILED: ${e.message}")
            }.getOrNull()

        }

        val tunFd = fdManager.GetTunFd(serviceId, dobbyVpnService)
        if (tunFd < 0) return false

        logger.log("[svc:$serviceId] startCloakOutline(): initializing Outline with tunFd=$tunFd")

        val connected = outlineLibFacade.init(outlineUrl, tunFd)
        if (!connected) {
            logger.log("Outline connection FAILED, stopping VPN service")
            return false
        }
        logger.log("outlineLibFacade connected successfully")
        if (websocketEnabled) {
            logger.log("WebSocket transport connected successfully")
        }
        logger.log("[svc:$serviceId] startCloakOutline(): completed (status=true) vpnInterface=${dobbyVpnService?.vpnInterface?.fd}")
        return true
    }

    fun stopOutline() {
        outlineLibFacade.disconnect()
    }
}
