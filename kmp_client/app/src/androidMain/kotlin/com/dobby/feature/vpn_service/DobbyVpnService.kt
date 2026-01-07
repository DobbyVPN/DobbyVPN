package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import com.dobby.awg.TunnelManager
import com.dobby.awg.TunnelState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.ConnectResult
import com.dobby.feature.vpn_service.domain.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.IpFetcher
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.isActive
import org.koin.android.ext.android.inject
import java.io.BufferedReader
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.InputStreamReader
import java.nio.ByteBuffer
import kotlinx.coroutines.Job
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlin.coroutines.cancellation.CancellationException
import java.util.Base64
import android.os.Debug

private const val IS_FROM_UI = "isLaunchedFromUi"

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

class DobbyVpnService : VpnService() {

    companion object {
        @Volatile
        var instance: DobbyVpnService? = null

        fun createIntent(context: Context): Intent {
            return Intent(context, DobbyVpnService::class.java).apply {
                putExtra(IS_FROM_UI, true)
            }
        }
    }

    private var vpnInterface: ParcelFileDescriptor? = null

    private val logger: Logger by inject()
    private val ipFetcher: IpFetcher by inject()
    private val vpnInterfaceFactory: DobbyVpnInterfaceFactory by inject()
    private val cloakConnectInteractor: CloakConnectionInteractor by inject()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    private val outlineLibFacade: OutlineLibFacade by inject()
    private val connectionState: ConnectionStateRepository by inject()

    private val bufferSize = 65536
    private var inputStream: FileInputStream? = null
    private var outputStream: FileOutputStream? = null
    private val tunnelManager = TunnelManager(this, logger)

    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val startStopMutex = Mutex()
    private var readJob: Job? = null
    private var writeJob: Job? = null
    private var routingJob: Job? = null
    private var postConnectCurlJob: Job? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        logger.log("Start go logger init with file = ${provideLogFilePath().toString()}")
        initLogger()
        logger.log("Finish go logger init")
        serviceScope.launch {
            connectionState.statusFlow.drop(1).collect { isConnected ->
                if (!isConnected) {
                    stopCloakClient()
                    vpnInterface?.close()
                    vpnInterface = null
                    stopSelf()
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline(intent)
            VpnInterface.AMNEZIA_WG -> startAwg()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        connectionState.tryUpdateVpnStarted(isStarted = false)
        serviceScope.cancel()
        runCatching {
            teardownVpn()
            outlineLibFacade.disconnect()
            stopCloakClient()
        }.onFailure { it.printStackTrace() }
        tunnelManager.updateState(null, TunnelState.DOWN)
        instance = null
        super.onDestroy()
    }

    fun getMemoryUsageMB(): Double {
        val memInfo = Debug.MemoryInfo()
        Debug.getMemoryInfo(memInfo)

        return memInfo.totalPss / 1024.0
    }

    private fun startCloakOutline(intent: Intent?) {
        serviceScope.launch {
            startStopMutex.withLock {
                val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
                val shouldTurnOutlineOn = dobbyConfigsRepository.getIsOutlineEnabled()

                if (!shouldTurnOutlineOn && isServiceStartedFromUi) {
                    logger.log("Start disconnecting Outline")
                    teardownVpn()
                    outlineLibFacade.disconnect()
                    stopCloakClient()
                    stopSelf()
                    return@withLock
                }

                logger.log("Tunnel: Start curl before connection")
                val ipAddress = runCatching { ipFetcher.fetchIp() }.getOrNull()
                if (ipAddress != null) {
                    logger.log("Tunnel: response from curl: $ipAddress")
                } else {
                    logger.log("Tunnel: Failed to fetch IP, continuing anyway.")
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
                    connectionState.tryUpdateStatus(false)
                    teardownVpn()
                    outlineLibFacade.disconnect()
                    stopCloakClient()
                    stopSelf()
                    return@withLock
                }

                // If Cloak is enabled, start it BEFORE Outline tries to connect to 127.0.0.1:LocalPort.
                val shouldEnableCloak = dobbyConfigsRepository.getIsCloakEnabled()
                if (shouldEnableCloak) {
                    val cloakConfig = dobbyConfigsRepository.getCloakConfig()
                    val localPort = dobbyConfigsRepository.getCloakLocalPort().toString()
                    if (cloakConfig.isNotEmpty()) {
                        logger.log("Cloak: connect start")
                        val cloakResult = cloakConnectInteractor.connect(
                            config = cloakConfig,
                            localHost = "127.0.0.1",
                            localPort = localPort
                        )
                        logger.log("Cloak connection result is $cloakResult")
                        if (cloakResult is ConnectResult.Error || cloakResult is ConnectResult.ValidationError) {
                            logger.log("Cloak failed to start, stopping VPN service")
                            connectionState.tryUpdateStatus(false)
                            teardownVpn()
                            outlineLibFacade.disconnect()
                            stopCloakClient()
                            stopSelf()
                            return@withLock
                        }
                    } else {
                        val outlineHost = extractHostFromHostPort(serverPort).lowercase()
                        val cloakRequired = outlineHost == "127.0.0.1" || outlineHost == "localhost"

                        logger.log("Cloak enabled but config is empty")
                        if (cloakRequired) {
                            logger.log("Cloak is required for Outline host=$outlineHost, stopping VPN service")
                            connectionState.tryUpdateStatus(false)
                            teardownVpn()
                            outlineLibFacade.disconnect()
                            stopCloakClient()
                            stopSelf()
                            return@withLock
                        } else {
                            logger.log("Cloak config empty → continuing without Cloak (Outline host=$outlineHost)")
                        }
                    }
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

                teardownVpn()
                outlineLibFacade.disconnect()

                val connected = outlineLibFacade.init(outlineUrl)
                if (!connected) {
                    logger.log("Outline connection FAILED, stopping VPN service")
                    connectionState.tryUpdateStatus(false)
                    outlineLibFacade.disconnect()
                    stopCloakClient()
                    stopSelf()
                    return@withLock
                }
                logger.log("outlineLibFacade connected successfully")
                if (websocketEnabled) {
                    logger.log("WebSocket transport connected successfully")
                }

                setupVpn()
                connectionState.updateStatus(true)
            }
        }
    }

    private fun startAwg() {
        if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
            logger.log("Starting AmneziaWG")
            val stringConfig = dobbyConfigsRepository.getAwgConfig()
            val state = if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
                TunnelState.UP
            } else {
                TunnelState.DOWN
            }
            tunnelManager.updateState(stringConfig, state)
        } else {
            logger.log("Stopping AmneziaWG")
            tunnelManager.updateState(null, TunnelState.DOWN)
        }
    }

    private fun enableCloakIfNeeded(force: Boolean) {
        val shouldEnableCloak = dobbyConfigsRepository.getIsCloakEnabled() || force
        val cloakConfig = dobbyConfigsRepository.getCloakConfig().ifEmpty { return }
        if (shouldEnableCloak && cloakConfig.isNotEmpty()) {
            val localPort = dobbyConfigsRepository.getCloakLocalPort().toString()
            serviceScope.launch {
                logger.log("Cloak: connect start")
                val result = cloakConnectInteractor.connect(
                    config = cloakConfig,
                    localHost = "127.0.0.1",
                    localPort = localPort
                )
                logger.log("Cloak connection result is $result")
            }
        } else {
            logger.log("Cloak is disabled. Config isEmpty == ${cloakConfig.isEmpty()}")
        }
    }

    private fun stopCloakClient() {
        runCatching {
            logger.log("Stopping Cloak client (if running)...")
            serviceScope.launch { cloakConnectInteractor.disconnect() }
        }.onFailure { e ->
            logger.log("Failed to stop Cloak client: ${e.message}")
        }
    }

    private fun teardownVpn() {
        runCatching { readJob?.cancel() }
        runCatching { writeJob?.cancel() }
        runCatching { routingJob?.cancel() }
        runCatching { postConnectCurlJob?.cancel() }
        readJob = null
        writeJob = null
        routingJob = null
        postConnectCurlJob = null

        runCatching { inputStream?.close() }
        runCatching { outputStream?.close() }
        runCatching { vpnInterface?.close() }
        inputStream = null
        outputStream = null
        vpnInterface = null
    }

    private fun setupVpn() {
        teardownVpn()

        vpnInterface = vpnInterfaceFactory
            .create(context = this@DobbyVpnService, vpnService = this@DobbyVpnService)
            .establish()

        if (vpnInterface != null) {
            inputStream = FileInputStream(vpnInterface?.fileDescriptor)
            outputStream = FileOutputStream(vpnInterface?.fileDescriptor)

            logger.log("Start reading packets")
            startReadingPackets()

            logger.log("Start writing packets")
            startWritingPackets()

            logRoutingTable()

            postConnectCurlJob = serviceScope.launch {
                logger.log("Start curl after connection")
                val response = ipFetcher.fetchIp()
                logger.log("Response from curl: $response")
            }
        } else {
            logger.log("Tunnel: Failed to Create VPN Interface")
        }
    }

    private fun logRoutingTable() {
        routingJob = serviceScope.launch {
            try {
                val processBuilder = ProcessBuilder("ip", "route")
                processBuilder.redirectErrorStream(true)
                val process = processBuilder.start()

                val reader = BufferedReader(InputStreamReader(process.inputStream))
                val output = StringBuilder()
                var line: String?
                while (reader.readLine().also { line = it } != null) {
                    output.append(line).append("\n")
                }

                process.waitFor()

                logger.log("Routing Table:\n$output")

            } catch (e: Exception) {
                logger.log("Failed to retrieve routing table: ${e.message}")
            }
        }
    }

    private fun startReadingPackets() {
        readJob = serviceScope.launch {
            vpnInterface?.let { vpn ->
                val buffer = ByteBuffer.allocate(bufferSize)

                while (isActive) {
                    try {
                        val length = inputStream?.read(buffer.array()) ?: 0
                        if (length > 0) {
                            outlineLibFacade.writeData(buffer.array(), length)
                            // val hexString = packetData.joinToString(separator = " ") { byte -> "%02x".format(byte) }
                            // Logger.log("MyVpnService: Packet Data Written (Hex): $hexString")
                        }
                    } catch (e: CancellationException) {
                        logger.log("VpnService: Packet reading coroutine was cancelled.")
                        break
                    } catch (e: Exception) {
                        android.util.Log.e(
                            "DobbyTAG",
                            "VpnService: Failed to write packet to Outline: ${e.message}"
                        )
                    }
                    buffer.clear()
                }
            }
        }
    }

    private fun startWritingPackets() {
        writeJob = serviceScope.launch {
            vpnInterface?.let {
                val buffer = ByteArray(bufferSize)

                while (isActive) {
                    try {
                        val length: Int = outlineLibFacade.readData(buffer)
                        if (length > 0) {
                            outputStream?.write(buffer, 0, length)
                        }
                    } catch (e: CancellationException) {
                        logger.log("VpnService: Packet writing coroutine was cancelled.")
                        break
                    } catch (e: Exception) {
                        logger.log("VpnService: Failed to read packet from tunnel: ${e.message}")
                    }
                }
            }
        }
    }
}
