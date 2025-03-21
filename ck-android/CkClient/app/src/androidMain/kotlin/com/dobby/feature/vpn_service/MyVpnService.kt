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
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Deferred
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

class MyVpnService : VpnService() {

    companion object {

        fun createIntent(context: Context): Intent {
            return Intent(context, MyVpnService::class.java)
        }
    }

    private var vpnInterface: ParcelFileDescriptor? = null

    private val logger: Logger by inject()
    private val ipFetcher: IpFetcher by inject()
    private val vpnInterfaceFactory: DobbyVpnInterfaceFactory by inject()
    private val cloakConnectInteractor: CloakConnectionInteractor by inject()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    private val outlineLibFacade: OutlineLibFacade by inject()

    private val bufferSize = 65536
    private var inputStream: FileInputStream? = null
    private var outputStream: FileOutputStream? = null
    private val tunnelManager = TunnelManager(this, logger)

    override fun onCreate() {
        super.onCreate()
        logger.log("Tunnel: Start curl before connection")
        CoroutineScope(Dispatchers.IO).launch {
            val ipAddress = ipFetcher.fetchIp()
            withContext(Dispatchers.Main) {
                ConnectionStateRepository.update(isConnected = true)
                if (ipAddress != null) {
                    logger.log("Tunnel: response from curl: $ipAddress")
                    setupVpn()
                    //checkServerAvailability(iqAddress)

                } else {
                    logger.log("Tunnel: Failed to fetch IP, cancelling VPN setup.")
                    stopSelf()
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> {

                // should make sense, TODO test when turning on VPN via settings (not from ui)
                if (dobbyConfigsRepository.getIsOutlineEnabled()) {
                    val apiKey = dobbyConfigsRepository.getOutlineKey()
                    logger.log("!!! Starting connecting Outline")
                    outlineLibFacade.init(apiKey)
                    enableCloakIfNeeded()
                } else {
                    logger.log("!!! Starting disconnecting Outline")
                    vpnInterface?.close()
                    ConnectionStateRepository.update(isConnected = false) // todo move somewhere
                    stopSelf()

                }
                return START_STICKY
            }

            VpnInterface.AMNEZIA_WG -> {
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

                return START_STICKY
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        ConnectionStateRepository.update(isConnected = false)
        runCatching {
            inputStream?.close()
            outputStream?.close()
            vpnInterface?.close()
            disableCloakIfNeeded()
        }.onFailure { it.printStackTrace() }
        tunnelManager.updateState(null, TunnelState.DOWN)
    }

    private fun enableCloakIfNeeded() {
        val shouldEnableCloak = dobbyConfigsRepository.getIsCloakEnabled()
        val cloakConfig = dobbyConfigsRepository.getCloakConfig()
        if (shouldEnableCloak) {
            CoroutineScope(Dispatchers.IO).launch {
                logger.log("!!!Cloak start connecting...")
                val result = cloakConnectInteractor.connect(config = cloakConfig)
                logger.log("!!!Cloak connection result is $result")
            }
        } else {
            logger.log("!!! You chose not to use Cloak")
        }
    }

    private fun disableCloakIfNeeded() {
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            logger.log("!!! Disabling Cloak!")
            CoroutineScope(Dispatchers.IO).launch {
                cloakConnectInteractor.disconnect()
                dobbyConfigsRepository.setIsCloakEnabled(false)
            }
        }
    }

    private fun setupVpn() {
        vpnInterface = vpnInterfaceFactory
            .create(context = this@MyVpnService, vpnService = this@MyVpnService)
            .establish()

        if (vpnInterface != null) {
            inputStream = FileInputStream(vpnInterface?.fileDescriptor)
            outputStream = FileOutputStream(vpnInterface?.fileDescriptor)

            logger.log("VPN Interface Created Successfully")

            CoroutineScope(Dispatchers.IO).launch {
                logger.log("Start reading packets")
                startReadingPackets()
                logger.log("Start writing packets")
                startWritingPackets()

                logRoutingTable()

                logger.log("Start function resolveAndLogDomain('google.com')")
                val ipAddress = resolveAndLogDomain("google.com")
                logger.log("Start function ping('1.1.1.1')")
                ping("1.1.1.1").await()
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
        CoroutineScope(Dispatchers.IO).launch {
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

    private fun ping(host: String): Deferred<Unit> {
        val deferred = CompletableDeferred<Unit>()
        CoroutineScope(Dispatchers.IO).launch {
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
                deferred.complete(Unit)
            } catch (e: Exception) {
                logger.log("MyVpnService: Failed to execute ping command: ${e.message}")
                deferred.completeExceptionally(e)
            }
        }
        return deferred
    }

    private fun startReadingPackets() {
        CoroutineScope(Dispatchers.IO).launch {
            vpnInterface?.let { vpn ->
                val buffer = ByteBuffer.allocate(bufferSize)

                while (true) {
                    try {
                        val length = inputStream?.read(buffer.array()) ?: 0

                        if (length > 0) {
                            val packetData: ByteArray = buffer.array().copyOfRange(0, length)
                            outlineLibFacade.writeData(packetData)
                            // val hexString = packetData.joinToString(separator = " ") { byte -> "%02x".format(byte) }
                            // Logger.log("MyVpnService: Packet Data Written (Hex): $hexString")
                        }
                    } catch (e: Exception) {
                        logger.log("VpnService: Failed to write packet to Outline: ${e.message}")
                    }
                    buffer.clear()
                }
            }
        }
    }

    private fun checkServerAvailability(host: String) {
        CoroutineScope(Dispatchers.IO).launch {
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
        CoroutineScope(Dispatchers.IO).launch {
            vpnInterface?.let {
                val buffer = ByteBuffer.allocate(bufferSize)

                while (true) {
                    try {
                        //val length = tunnel.read(buffer)
                        //if (length > 0) {
                        //    outputStream.write(buffer.array(), 0, length)
                        //}
                        //buffer.clear()
                        //Logger.log("MyVpnService: read packet from tunnel")
                        val packetData: ByteArray? = outlineLibFacade.readData()

                        packetData?.let {
                            outputStream?.write(it)
                            //val hexString = it.joinToString(separator = " ") { byte -> "%02x".format(byte) }
                            //Logger.log("MyVpnService: Packet Data Read (Hex): $hexString")
                        } ?: Unit // Logger.log("No data read from Outline") TODO remove comment
                    } catch (e: Exception) {
                        logger.log("VpnService: Failed to read packet from tunnel: ${e.message}")
                    }
                    buffer.clear()
                }
            }
        }
    }
}
