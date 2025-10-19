package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import com.dobby.awg.TunnelManager
import com.dobby.awg.TunnelState
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.IpFetcher
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.isActive
import org.koin.android.ext.android.inject
import java.io.BufferedReader
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.InputStreamReader
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.nio.ByteBuffer
import kotlin.coroutines.cancellation.CancellationException
import java.util.Base64

private const val IS_FROM_UI = "isLaunchedFromUi"

private fun buildOutlineUrl(
    methodPassword: String,
    serverPort: String
): String {
    val encoded = Base64.getEncoder().encodeToString(methodPassword.toByteArray())
    return "ss://$encoded@$serverPort"
}

class DobbyVpnService : VpnService() {

    companion object {

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

    override fun onCreate() {
        super.onCreate()
        serviceScope.launch {
            connectionState.flow.drop(1).collect { isConnected ->
                if (!isConnected) {
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
        connectionState.tryUpdate(isConnected = false)
        serviceScope.cancel()
        runCatching {
            inputStream?.close()
            outputStream?.close()
            vpnInterface?.close()
            disableCloakIfNeeded()
        }.onFailure { it.printStackTrace() }
        tunnelManager.updateState(null, TunnelState.DOWN)
        super.onDestroy()
    }

    private fun startCloakOutline(intent: Intent?) {
        logger.log("Tunnel: Start curl before connection")
        serviceScope.launch {
            val ipAddress = ipFetcher.fetchIp()
            withContext(Dispatchers.Main) {
                connectionState.update(isConnected = true)
                if (ipAddress != null) {
                    logger.log("Tunnel: response from curl: $ipAddress")
                    setupVpn()
                } else {
                    logger.log("Tunnel: Failed to fetch IP, cancelling VPN setup.")
                    stopSelf()
                }
            }
        }
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnOutlineOn = dobbyConfigsRepository.getIsOutlineEnabled()
        if (shouldTurnOutlineOn || !isServiceStartedFromUi) {
            val methodPassword = dobbyConfigsRepository.getMethodPasswordOutline()
            val serverPort = dobbyConfigsRepository.getServerPortOutline()
            if (methodPassword.isEmpty() || serverPort.isEmpty()) {
                logger.log("Previously used outline apiKey is empty")
                return
            }

            // Run Outline initialization asynchronously to prevent UI blocking
            // The init() call can take time and would freeze the interface if run on main thread
            serviceScope.launch {
                logger.log("!!! Start connecting Outline")
                outlineLibFacade.init(buildOutlineUrl(methodPassword, serverPort))
                logger.log("outlineLibFacade inited")
                enableCloakIfNeeded(force = !isServiceStartedFromUi)
            }
        } else {
            logger.log("!!! Start disconnecting Outline")
            vpnInterface?.close()
            stopSelf()
        }
    }

    private fun startAwg() {
        if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
            logger.log("!!! Starting AmneziaWG")
            val stringConfig = dobbyConfigsRepository.getAwgConfig()
            val state = if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
                TunnelState.UP
            } else {
                TunnelState.DOWN
            }
            tunnelManager.updateState(stringConfig, state)
        } else {
            logger.log("!!! Stopping AmneziaWG")
            tunnelManager.updateState(null, TunnelState.DOWN)
        }
    }

    private fun enableCloakIfNeeded(force: Boolean) {
        val shouldEnableCloak = dobbyConfigsRepository.getIsCloakEnabled() || force
        val cloakConfig = dobbyConfigsRepository.getCloakConfig().ifEmpty { return }
        if (shouldEnableCloak && cloakConfig.isNotEmpty()) {
            serviceScope.launch {
                logger.log("!!!Cloak: connect start")
                val result = cloakConnectInteractor.connect(config = cloakConfig)
                logger.log("!!!Cloak connection result is $result")
            }
        } else {
            logger.log("!!! Cloak is disabled. Config isEmpty == ${cloakConfig.isEmpty()}")
        }
    }

    private fun disableCloakIfNeeded() {
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            logger.log("!!! Disabling Cloak!")
            serviceScope.launch {
                cloakConnectInteractor.disconnect()
                dobbyConfigsRepository.setIsCloakEnabled(false)
            }
        }
    }

    private fun setupVpn() {
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

            serviceScope.launch {
                logger.log("Start function resolveAndLogDomain('google.com')")
                val ipAddress = resolveAndLogDomain("google.com")
                logger.log("Start function ping('1.1.1.1')")
                async { ping("1.1.1.1") }.await()
                ipAddress?.let(::checkServerAvailability)
                    ?: logger.log("MyVpnService: Unable to resolve IP for google.com")

                logger.log("Start curl after connection")
                val response = ipFetcher.fetchIp()
                logger.log("Response from curl: $response")
            }
        } else {
            logger.log("Tunnel: Failed to Create VPN Interface")
        }
    }

    private fun logRoutingTable() {
        serviceScope.launch {
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

    private suspend fun resolveAndLogDomain(domain: String): String? {
        return withContext(Dispatchers.IO) {
            try {
                withTimeout(5000L) {
                    val address = InetAddress.getByName(domain)
                    val ipAddress = address.hostAddress
                    logger.log("VpnService: Domain resolved successfully. Domain: $domain, IP: $ipAddress")
                    ipAddress
                }
            } catch (e: TimeoutCancellationException) {
                logger.log("VpnService: Domain resolution timed out. Domain: $domain")
                null
            } catch (e: UnknownHostException) {
                logger.log("VpnService: Failed to resolve domain. Domain: $domain: ${e.message}")
                null
            } catch (e: Exception) {
                logger.log("VpnService: Exception during domain resolution. Domain: $domain, Error: ${e.message}")
                null
            }
        }
    }

    private fun ping(host: String) {
        serviceScope.launch {
            try {
                val processBuilder = ProcessBuilder("ping", "-c", "4", host)
                processBuilder.redirectErrorStream(true)
                val process = processBuilder.start()

                val reader = BufferedReader(InputStreamReader(process.inputStream))
                val output = StringBuilder()
                var line: String?
                while (reader.readLine().also { line = it } != null) {
                    output.append(line).append("\n")
                }

                process.waitFor()
                logger.log("VpnService: Ping output:\n$output")
            } catch (e: Exception) {
                logger.log("MyVpnService: Failed to execute ping command: ${e.message}")
            }
        }
    }

    private fun startReadingPackets() {
        serviceScope.launch {
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

    private fun checkServerAvailability(host: String) {
        serviceScope.launch {
            try {
                val socket = Socket()
                val socketAddress = InetSocketAddress(host, 443)

                socket.connect(socketAddress, 5000)

                if (socket.isConnected) {
//                    val writer = OutputStreamWriter(socket.getOutputStream(), "UTF-8")
//                    val reader = BufferedReader(InputStreamReader(socket.getInputStream(), "UTF-8"))
//
//                    val request = "GET / HTTP/1.1\r\nHost: $host\r\nConnection: close\r\n\r\n"
//                    writer.write(request)
//                    writer.flush()
//
//                    val response = StringBuilder()
//                    var line: String?
//                    while (reader.readLine().also { line = it } != null) {
//                        response.append(line).append("\n")
//                    }

                    logger.log("VpnService: Successfully reached $host on port 443 via TCP")
                    //Logger.log("MyVpnService: Response from server:\n$response")

//                    writer.close()
//                    reader.close()
                    socket.close()
                } else {
                    logger.log("VpnService: Failed to reach $host on port 443 via TCP")
                }
            } catch (e: SocketTimeoutException) {
                logger.log("VpnService: Timeout error when connecting to $host on port 443 via TCP: ${e.message}")
            } catch (e: Exception) {
                logger.log("VpnService: Error when connecting to $host on port 443 via TCP: ${e.message}")
            }
        }
    }

    private fun startWritingPackets() {
        serviceScope.launch {
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
