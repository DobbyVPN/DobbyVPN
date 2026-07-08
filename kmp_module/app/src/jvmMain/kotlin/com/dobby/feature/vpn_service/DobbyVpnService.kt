package com.dobby.feature.vpn_service

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.awg.AwgLibrary
import interop.cloak.CloakLibrary
import interop.georouting.GeoroutingLibrary
import interop.outline.OutlineLibrary
import interop.xray.XrayLibrary
import kotlinx.coroutines.runBlocking
import java.util.*

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

class DobbyVpnService(
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val logsRepository: LogsRepository,
    private val awgLibrary: AwgLibrary,
    private val outlineLibrary: OutlineLibrary,
    private val xrayLibrary: XrayLibrary,
    private val trustTunnelLibrary: interop.trusttunnel.TrustTunnelLibrary,
    private val cloakLibrary: CloakLibrary,
    private val georoutingLibrary: GeoroutingLibrary
) {
    private val startStopLock = Any()
    private var runtimeStarted: Boolean = false
    private var runtimeUsesCloak: Boolean = false

    /**
     * Starts VPN tunnel, that defined in the [dobbyConfigsRepository]
     *
     * @return true if VPN tunnel started successfully
     */
    fun startService(isProtocolProbe: Boolean): Boolean {
        synchronized(startStopLock) {
            val hadRuntimeStarted = runtimeStarted
            val hadRuntimeUsingCloak = runtimeUsesCloak
            val runningInterface = dobbyConfigsRepository.getVpnInterface()
            val startingUsesCloak = runningInterface == VpnInterface.CLOAK_OUTLINE &&
                dobbyConfigsRepository.getIsCloakEnabled()

            if (runtimeStarted && runtimeUsesCloak && startingUsesCloak) {
                logger.log(
                    "Stopping Cloak sidecar before protocol hot-switch " +
                        "configuredInterface=$runningInterface protocolProbe=$isProtocolProbe " +
                        "startingUsesCloak=$startingUsesCloak"
                )
                stopCloakSidecar()
                runtimeUsesCloak = false
            }

            if (runtimeStarted) {
                logger.log(
                    "Switching VPN protocol on existing runtime " +
                        "configuredInterface=$runningInterface protocolProbe=$isProtocolProbe"
                )
            } else {
                logger.log("Restarting VPN protocols before starting configured interface=$runningInterface")
                stopProtocols()
            }
            georoutingLibrary.SetGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
            val started = startConfiguredProtocol(runningInterface)
            runtimeStarted = started || hadRuntimeStarted
            if (started) {
                runtimeUsesCloak = startingUsesCloak
                if (hadRuntimeStarted && hadRuntimeUsingCloak && !startingUsesCloak) {
                    logger.log(
                        "Stopping previous Cloak sidecar after successful protocol hot-switch " +
                            "configuredInterface=$runningInterface protocolProbe=$isProtocolProbe"
                    )
                    stopCloakSidecar()
                }
            }

            return started
        }
    }

    fun stopService() {
        synchronized(startStopLock) {
            stopCurrentLocked()
        }
    }

    private fun stopCurrentLocked() {
        stopProtocols()
        georoutingLibrary.ClearGeoRoutingConf()
        runtimeStarted = false
        runtimeUsesCloak = false
        logger.log("VPN runtime stopped; saved connection profiles remain available")
    }

    private fun stopProtocols() {
        stopCloakOutline()
        stopXray()
        stopAwg()
        stopTrustTunnel()
        stopNone()
    }

    private fun stopCloakSidecar() {
        runBlocking {
            logger.log("StopCloak")
            cloakLibrary.StopCloakClient()
        }
    }

    private fun startConfiguredProtocol(runningInterface: VpnInterface): Boolean =
        when (runningInterface) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
            VpnInterface.AMNEZIA_WG -> startAwg()
            VpnInterface.XRAY -> startXray()
            VpnInterface.TRUST_TUNNEL -> startTrustTunnel()
            VpnInterface.NONE -> startNone()
        }

    private fun startCloakOutline(): Boolean {
        logger.log("Start startCloakOutline")
        val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
        val serverPort = dobbyConfigsRepository.getServerPort()
        val prefix = dobbyConfigsRepository.getPrefixOutline()
        val websocketEnabled = dobbyConfigsRepository.getIsWebsocketEnabled()
        val tcpPath = dobbyConfigsRepository.getTcpPathOutline()
        val udpPath = dobbyConfigsRepository.getUdpPathOutline()
        val localHost = "127.0.0.1"
        val localPort = dobbyConfigsRepository.getCloakLocalPort().toString()
        logger.log("startCloakOutline with key: methodPassword = ${maskStr(methodPassword)} serverPort = ${maskStr(serverPort)}")
        logger.log("Outline prefix: ${prefix.ifEmpty { "(none)" }}")
        logger.log("Outline websocket: $websocketEnabled, tcpPath: ${tcpPath.ifEmpty { "(none)" }}, udpPath: ${udpPath.ifEmpty { "(none)" }}")
        return runBlocking {
            logger.log("CloakIsEnable = " + dobbyConfigsRepository.getIsCloakEnabled())
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                cloakLibrary.StartCloakClient(localHost, localPort, dobbyConfigsRepository.getCloakConfig(), false)
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

            val connected = outlineLibrary.StartOutline(outlineUrl)
            if (connected == 0) {
                logger.log("Outline connection established successfully")
                true
            } else {
                logger.log("Outline connection FAILED: ${outlineLibrary.GetOutlineLastError()}")
                // Stop Cloak if it was started
                if (dobbyConfigsRepository.getIsCloakEnabled()) {
                    logger.log("Stopping Cloak due to Outline failure")
                    cloakLibrary.StopCloakClient()
                }
                false
            }
        }
    }

    /**
     * Starts Cloak+Outline tunnel
     *
     * @return true if VPN tunnel started successfully
     */
    private fun stopCloakOutline() {
        logger.log("StopOutline")
        runBlocking {
            outlineLibrary.StopOutline()
            logger.log("CloakIsEnable = " + dobbyConfigsRepository.getIsCloakEnabled())
            if (dobbyConfigsRepository.getIsCloakEnabled()) {
                logger.log("StopCloak")
                cloakLibrary.StopCloakClient()
            }
        }
    }

    /**
     * Starts AmneziaWG tunnel
     *
     * @return true if VPN tunnel started successfully
     */
    private fun startAwg(): Boolean {
        val apiKey = dobbyConfigsRepository.getAwgConfig()
        logger.log("startAwg with key.len=${apiKey.length}")
        val connected = awgLibrary.StartAwg(apiKey)
        return connected == 0
    }

    private fun stopAwg() {
        logger.log("stopAwg")
        awgLibrary.StopAwg()
    }

    /**
     * Starts XRAY tunnel
     *
     * @return true if VPN tunnel started successfully
     */
    private fun startXray(): Boolean {
        val config = dobbyConfigsRepository.getXrayConfig()
        logger.log("startXray with config length: ${config.length}")
        if (config.isEmpty()) {
            logger.log("Xray config is empty, cannot start")
            return false
        }
        return runBlocking {
            val result = xrayLibrary.StartXray(config)
            if (result == 0) {
                logger.log("Xray connection established successfully")
                true
            } else {
                logger.log("Xray connection FAILED: ${xrayLibrary.GetXrayLastError()}")
                false
            }
        }
    }

    private fun stopXray() {
        logger.log("stopXray")
        xrayLibrary.StopXray()
    }

    /**
     * Starts undefined tunnel. Mock function to warn user about invalid config/app usage.
     *
     * @return always false since we cannot start any VPN tunnel
     */
    private fun startNone(): Boolean {
        logger.log("[WARNING] There is no VPN, that can be started")
        return false
    }

    private fun startTrustTunnel(): Boolean {
        val config = dobbyConfigsRepository.getTrustTunnelConfig()
        logger.log("startTrustTunnel with config length: ${config.length}")
        if (config.isEmpty()) {
            logger.log("TrustTunnel config is empty, cannot start")
            return false
        }
        return runBlocking {
            val result = trustTunnelLibrary.StartTrustTunnel(config)
            if (result == 0) {
                logger.log("TrustTunnel connection established successfully")
                true
            } else {
                logger.log("TrustTunnel connection FAILED: ${trustTunnelLibrary.GetTrustTunnelLastError()}")
                false
            }
        }
    }

    private fun stopTrustTunnel() {
        logger.log("stopTrustTunnel")
        trustTunnelLibrary.StopTrustTunnel()
    }

    private fun stopNone() {
        logger.log("[WARNING] There is no VPN, that should be stopped")
    }

}
