package com.dobby.feature.vpn_service

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.clearVpnConfig
import interop.awg.AwgLibrary
import interop.cloak.CloakLibrary
import interop.georouting.GeoroutingLibrary
import interop.logger.LoggerLibrary
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
    private val awgLibrary: AwgLibrary,
    private val outlineLibrary: OutlineLibrary,
    private val xrayLibrary: XrayLibrary,
    private val cloakLibrary: CloakLibrary,
    private val loggerLibrary: LoggerLibrary,
    private val georoutingLibrary: GeoroutingLibrary
) {
    private val startStopLock = Any()

    fun enableTunnelLogging() {
        val logFilePath = provideLogFilePath()
        logger.log("Init tunnel logging to the path: $logFilePath")
        loggerLibrary.InitLogger(logFilePath.toString())
    }

    fun enableTunnelTelemetry() {
        val endpoint = dobbyConfigsRepository.getTelemetryEndpoint()
        logger.log("Init tunnel telemetry to the endpoint: $endpoint")
        if (endpoint.isNotBlank()) {
            loggerLibrary.InitTelemetry(endpoint)
            logger.log("Initialized tunnel telemetry")
        } else {
            logger.log("No telemetry endpoint provided")
        }
        logger.log("Setup telemetry attributes")
        val config = dobbyConfigsRepository.getTelemetryAttributes()
        if (config.isNotBlank()) {
            loggerLibrary.SetupTelemetryAttributes(config)
            logger.log("Setup tunnel telemetry attributes")
        } else {
            logger.log("No telemetry attributes provided")
        }
    }

    fun disableTunnelTelemetry() {
        logger.log("Stop tunnel telemetry")
        loggerLibrary.StopTelemetry()
    }

    /**
     * Starts VPN tunnel, that defined in the [dobbyConfigsRepository]
     *
     * @return true if VPN tunnel started successfully
     */
    fun startService(): Boolean {
        synchronized(startStopLock) {
            val runningInterface = dobbyConfigsRepository.getVpnInterface()

            enableTunnelLogging()
            enableTunnelTelemetry()

            georoutingLibrary.SetGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
            val started = when (runningInterface) {
                VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
                VpnInterface.AMNEZIA_WG -> startAwg()
                VpnInterface.XRAY -> startXray()
                VpnInterface.NONE -> startNone()
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
        disableTunnelTelemetry()
        stopCloakOutline()
        stopXray()
        stopAwg()
        stopNone()
        georoutingLibrary.ClearGeoRoutingConf()
        dobbyConfigsRepository.clearVpnConfig()
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

    private fun stopNone() {
        logger.log("[WARNING] There is no VPN, that should be stopped")
    }

}
