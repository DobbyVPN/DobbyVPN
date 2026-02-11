package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import com.dobby.awg.TunnelManager
import com.dobby.awg.TunnelState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.runBlocking
import org.koin.android.ext.android.inject
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import android.os.Debug
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.system.Os
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import java.io.File
import java.io.FileInputStream
import java.util.UUID

const val IS_FROM_UI = "isLaunchedFromUi"

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

    var vpnInterface: ParcelFileDescriptor? = null
    var goTunFd: Int? = null
    val serviceId: String = UUID.randomUUID().toString().take(8)
    private var defaultNetworkCallback: ConnectivityManager.NetworkCallback? = null
    private val logger: Logger by inject()
    private val vpnInterfaceFactory: DobbyVpnInterfaceFactory by inject()
    private val cloakConnectInteractor: CloakConnectionInteractor by inject()
    private val outlineInteractor: OutlineInteractor by inject ()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    val connectionState: ConnectionStateRepository by inject()
    private val tunnelManager = TunnelManager(this, logger)

    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val startStopMutex = Mutex()

    override fun onCreate() {
        super.onCreate()
        instance = this
        logger.log("[svc:$serviceId] onCreate()")
        logger.log("Start go logger init with file = ${provideLogFilePath().toString()}")
        initLogger()
        logger.log("Finish go logger init")

        // Logs-only: track network transitions to correlate with crashes / restarts.
        runCatching {
            val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            val cb = object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) {
                    logger.log("[svc:$serviceId] net:onAvailable net=$network")
                }

                override fun onLost(network: Network) {
                    logger.log("[svc:$serviceId] net:onLost net=$network")
                }

                override fun onCapabilitiesChanged(network: Network, networkCapabilities: NetworkCapabilities) {
                    val hasInternet = networkCapabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    val validated = networkCapabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
                    val transports = buildList {
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) add("WIFI")
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) add("CELL")
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) add("ETH")
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) add("VPN")
                    }.joinToString("|")
                    logger.log("[svc:$serviceId] net:onCapabilitiesChanged net=$network transports=$transports internet=$hasInternet validated=$validated")
                }
            }
            defaultNetworkCallback = cb
            cm.registerDefaultNetworkCallback(cb)
            logger.log("[svc:$serviceId] net:registerDefaultNetworkCallback OK")
        }.onFailure { e ->
            logger.log("[svc:$serviceId] net:registerDefaultNetworkCallback FAILED: ${e.message}")
        }

        serviceScope.launch {
            connectionState.statusFlow.drop(1).collect { isConnected ->
                logger.log("[svc:$serviceId] statusFlow update: isConnected=$isConnected")
                if (!isConnected) {
                    startStopMutex.withLock {
                        logger.log("[svc:$serviceId] statusFlow requested stop → begin teardown")
                        stopCloakClient()
                        teardownVpn()
                        stopSelf()
                        logger.log("[svc:$serviceId] statusFlow requested stop → stopSelf() called")
                    }
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        logger.log("[svc:$serviceId] onStartCommand(startId=$startId flags=$flags intentFromUi=${intent?.getBooleanExtra(IS_FROM_UI, false)}) vpnInterface=${vpnInterface?.fd}")
        teardownVpn()
        when (dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline(intent)
            VpnInterface.AMNEZIA_WG -> startAwg()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        logger.log("[svc:$serviceId] onDestroy() begin vpnInterface=${vpnInterface?.fd}")
        teardownVpn()
        runCatching {
            val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            defaultNetworkCallback?.let { cb ->
                cm.unregisterNetworkCallback(cb)
                logger.log("[svc:$serviceId] net:unregisterNetworkCallback OK")
            }
        }.onFailure { e ->
            logger.log("[svc:$serviceId] net:unregisterNetworkCallback FAILED: ${e.message}")
        }
        serviceScope.cancel()
        tunnelManager.updateState(null, TunnelState.DOWN)
        instance = null
        super.onDestroy()
        logger.log("[svc:$serviceId] onDestroy() end")
    }

    fun getMemoryUsageMB(): Double {
        val memInfo = Debug.MemoryInfo()
        Debug.getMemoryInfo(memInfo)

        return memInfo.totalPss / 1024.0
    }

    private fun startCloakOutline(intent: Intent?) {
        serviceScope.launch {
            startStopMutex.withLock {
                if (dobbyConfigsRepository.getIsCloakEnabled()) {
                    cloakConnectInteractor.startCloak(instance)
                }
                outlineInteractor.startOutline(intent, instance)
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

    private suspend fun stopCloakClient() {
        runCatching {
            logger.log("Stopping Cloak client (if running)...")
            cloakConnectInteractor.disconnect()
        }.onFailure { e ->
            logger.log("Failed to stop Cloak client: ${e.message}")
        }
    }

    fun teardownVpn() {
        val fdBefore = runCatching { vpnInterface?.fd }.getOrNull()
        logger.log("[svc:$serviceId] teardownVpn(): begin fd=$fdBefore")
        runCatching {
            outlineInteractor.stopOutline();
        }.onFailure { e ->
            logger.log("[svc:$serviceId] onDestroy(): failed to disconnect Outline: ${e.message}")
        }
        runCatching {
            runBlocking {
                stopCloakClient()
            }
        }.onFailure { e ->
            logger.log("[svc:$serviceId] onDestroy(): failed to stop Cloak: ${e.message}")
        }
        goTunFd?.let { targetFd ->
            logger.log("[svc:$serviceId] teardownVpn(): safely terminating goTunFd=$targetFd")
            try {
                // Open /dev/null
                val devNull = FileInputStream(File("/dev/null"))
                val nullFd = devNull.fd

                // Overwrite the VPN FD (targetFd) with the Null FD.
                // This ATOMICALLY closes the VPN interface and replaces it with /dev/null.
                // Go still holds 'targetFd', but now it points to /dev/null.
                Os.dup2(nullFd, targetFd)

                // Close our handle to /dev/null
                devNull.close()

                logger.log("[svc:$serviceId] teardownVpn(): successfully redirected goTunFd to /dev/null")
            } catch (e: Exception) {
                // If this fails, it might mean Go already closed it. That's fine.
                logger.log("[svc:$serviceId] teardownVpn(): safe termination warning: ${e.message}")
            }
        }
        goTunFd = null
        runCatching {
            vpnInterface?.close()
        }
        vpnInterface = null
        logger.log("[svc:$serviceId] teardownVpn(): end fd=$fdBefore")
    }

    fun setupVpn() {
        teardownVpn()
        logger.log("[svc:$serviceId] setupVpn(): begin")
        vpnInterface = runCatching {
            vpnInterfaceFactory
                .create(context = this@DobbyVpnService, vpnService = this@DobbyVpnService)
                .establish()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] setupVpn(): establish FAILED: ${e.message}")
        }.getOrNull()
    }
}
