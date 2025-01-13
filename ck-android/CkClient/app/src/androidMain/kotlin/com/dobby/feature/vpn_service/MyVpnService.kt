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
import cloak_outline.OutlineDevice
import cloak_outline.Cloak_outline
import com.dobby.awg.TunnelManager
import com.dobby.awg.TunnelState
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.vpn_service.domain.CloakConnectionInteractor
import com.dobby.util.Logger
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

    private lateinit var dobbyConfigsRepository: DobbyConfigsRepository

    private val tunnelManager = TunnelManager(this)
    private var vpnInterface: ParcelFileDescriptor? = null
    private var device: OutlineDevice? = null
    private val ipFetcher: IpFetcher = IpFetcher()
    private val vpnInterfaceFactory: DobbyVpnInterfaceFactory = DobbyVpnInterfaceFactory()

    private val cloakConnectInteractor: CloakConnectionInteractor by inject()

    private val bufferSize = 65536
    private var inputStream: FileInputStream? = null
    private var outputStream: FileOutputStream? = null
    private var check = true

    override fun onCreate() {
        super.onCreate()

        dobbyConfigsRepository = DobbyConfigsRepositoryImpl(
            prefs = getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )

        Logger.init(this)
        Logger.log("MyVpnService: Start curl before connection")
        CoroutineScope(Dispatchers.IO).launch {
            val ipAddress = ipFetcher.fetchIp()
            withContext(Dispatchers.Main) {
                if (ipAddress != null) {
                    Logger.log( "MyVpnService: response from curl: $ipAddress")
                    setupVpn()
                    //checkServerAvailability(iqAddress)

                } else {
                    Logger.log("MyVpnService: Failed to fetch IP, cancelling VPN setup.")
                    stopSelf()
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
            Logger.log("!!! Starting AmneziaWG")
            val stringConfig = dobbyConfigsRepository.getAwgConfig()
            val state = if (dobbyConfigsRepository.getIsAmneziaWGEnabled()) {
                TunnelState.UP
            } else {
                TunnelState.DOWN
            }
            tunnelManager.updateState(stringConfig, state)

            return START_STICKY
        }

        val connectionFlag = dobbyConfigsRepository.getIsOutlineEnabled()
        check = connectionFlag
        if (connectionFlag) {
            val apiKey = dobbyConfigsRepository.getOutlineKey()
            Logger.log("!!! Starting connecting Outline")
            device = Cloak_outline.newOutlineDevice(apiKey)
            enableCloakIfNeeded()
            ConnectionStateRepository.update(isConnected = true) // todo move somewhere
        } else {
            Logger.log("!!! Starting disconnecting Outline")
            vpnInterface?.close()
            ConnectionStateRepository.update(isConnected = false) // todo move somewhere
            stopSelf()
        }
        return START_STICKY
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
                Logger.log("!!!Cloak start connecting...")
                val result = cloakConnectInteractor.connect(config = cloakConfig)
                Logger.log("!!!Cloak connection result is $result")
            }
        } else {
            Logger.log("!!! You chose not to use Cloak")
        }
    }

    private fun disableCloakIfNeeded() {
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            Logger.log("!!! Disabling Cloak!")
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

            Logger.log("MyVpnService: VPN Interface Created Successfully")

            CoroutineScope(Dispatchers.IO).launch {
                setupTunnel()
                Logger.log("MyVpnService: Start function startReadingPackets()")
                startReadingPackets()
                Logger.log("MyVpnService: Start function startWritingPackets()")
                startWritingPackets()

                logRoutingTable()

                Logger.log("MyVpnService: Start function resolveAndLogDomain(\"google.com\")")
                val ipAddress = resolveAndLogDomain("google.com")
                Logger.log("MyVpnService: Start function ping(\"1.1.1.1\")")
                ping("1.1.1.1").await()
                if (ipAddress != null) {
                    checkServerAvailability(ipAddress)

                } else {
                    Logger.log("MyVpnService: Unable to resolve IP for google.com")
                }

                Logger.log("MyVpnService: Start curl after connection")
                val response = ipFetcher.fetchIp()
                Logger.log("MyVpnService: response from curl: $response")
            }
        } else {
            Logger.log("MyVpnService: Failed to Create VPN Interface")
        }
    }

    private suspend fun setupTunnel() {
        withContext(Dispatchers.IO) {
            try {
                Logger.log("MyVpnService: Start function setupTunnel()")
                Logger.log("MyVpnService: End of function setupTunnel")
            } catch (e: Exception) {
                Logger.log("MyVpnService: Failed to setup tunnel: ${e.message}")
            }
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

                Logger.log("MyVpnService: Routing Table:\n$output")

            } catch (e: Exception) {
                Logger.log("MyVpnService: Failed to retrieve routing table: ${e.message}")
            }
        }
    }

    private suspend fun resolveAndLogDomain(domain: String): String? {
        return withContext(Dispatchers.IO) {
            try {
                withTimeout(5000L) {
                    val address = InetAddress.getByName(domain)
                    val ipAddress = address.hostAddress
                    Logger.log("MyVpnService: Domain resolved successfully. Domain: $domain, IP: $ipAddress")
                    ipAddress
                }
            } catch (e: TimeoutCancellationException) {
                Logger.log("MyVpnService: Domain resolution timed out. Domain: $domain")
                null
            } catch (e: UnknownHostException) {
                Logger.log("MyVpnService: Failed to resolve domain. Domain: $domain: ${e.message}")
                null
            } catch (e: Exception) {
                Logger.log("MyVpnService: Exception during domain resolution. Domain: $domain, Error: ${e.message}")
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
                Logger.log("MyVpnService: Ping output:\n$output")
                deferred.complete(Unit)
            } catch (e: Exception) {
                Logger.log("MyVpnService: Failed to execute ping command: ${e.message}")
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
                    if (check) {
                        try {
                            val length = inputStream?.read(buffer.array()) ?: 0
                            if (length > 0) {
                                val packetData: ByteArray = buffer.array().copyOfRange(0, length)
                                device?.write(packetData)
                                // val hexString = packetData.joinToString(separator = " ") { byte -> "%02x".format(byte) }
                                // Logger.log("MyVpnService: Packet Data Written (Hex): $hexString")
                            }
                        } catch (e: Exception) {
                            Logger.log(
                                "MyVpnService: Failed to write packet to Outline: ${e.message}"
                            )
                        }
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

                    Logger.log("MyVpnService: Successfully reached $host on port 443 via TCP")
                    //Logger.log("MyVpnService: Response from server:\n$response")

//                    writer.close()
//                    reader.close()
                    socket.close()
                } else {
                    Logger.log("MyVpnService: Failed to reach $host on port 443 via TCP")
                }
            } catch (e: SocketTimeoutException) {
                Logger.log("MyVpnService: Timeout error when connecting to $host on port 443 via TCP: ${e.message}")
            } catch (e: Exception) {
                Logger.log("MyVpnService: Error when connecting to $host on port 443 via TCP: ${e.message}")
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
                        if (check) {
                            val packetData: ByteArray? = device?.read()

                            packetData?.let {
                                outputStream?.write(it)
                                //val hexString = it.joinToString(separator = " ") { byte -> "%02x".format(byte) }
                                //Logger.log("MyVpnService: Packet Data Read (Hex): $hexString")
                            } ?: Unit // Logger.log("No data read from Outline") TODO remove comment
                        }
                    } catch (e: Exception) {
                        Logger.log("MyVpnService: Failed to read packet from tunnel: ${e.message}")
                    }
                    buffer.clear()
                }
            }
        }
    }
}
