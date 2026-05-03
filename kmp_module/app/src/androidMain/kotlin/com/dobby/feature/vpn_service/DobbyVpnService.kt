package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.system.Os
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.initTelemetry
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.awg.AmneziaWGInteractor
import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.cloak.DisconnectResult
import com.dobby.feature.vpn_service.domain.georouting.GeoRouting
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.koin.android.ext.android.inject
import java.io.File
import java.io.FileInputStream
import java.util.*

class DobbyVpnService : VpnService() {
    companion object {
        @Volatile
        var instance: DobbyVpnService? = null

        fun createIntent(context: Context): Intent {
            return Intent(context, DobbyVpnService::class.java)
        }
    }

    var vpnInterface: ParcelFileDescriptor? = null
    var goTunFd: Int? = null
    val serviceId: String = UUID.randomUUID().toString().take(8)
    private var defaultNetworkCallback: ConnectivityManager.NetworkCallback? = null
    private val logger: Logger by inject()
    private val geoRouting: GeoRouting by inject()
    private val cloakConnectInteractor: CloakConnectionInteractor by inject()
    private val outlineInteractor: OutlineInteractor by inject()
    private val awgInteractor: AmneziaWGInteractor by inject()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    private val connectionState: ConnectionStateRepository by inject()

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
                    logger.log(
                        "[svc:$serviceId] net:onCapabilitiesChanged " +
                            "net=$network transports=$transports " +
                            "internet=$hasInternet validated=$validated"
                    )
                }
            }
            defaultNetworkCallback = cb
            cm.registerDefaultNetworkCallback(cb)
            logger.log("[svc:$serviceId] net:registerDefaultNetworkCallback OK")
        }.onFailure { e ->
            logger.log("[svc:$serviceId] net:registerDefaultNetworkCallback FAILED: ${e.message}")
        }

        GoBackendWrapper.registerVpnService(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        logger.log(
            "[svc:$serviceId] onStartCommand(startId=$startId flags=$flags) vpnInterface=${vpnInterface?.fd}"
        )

        startService()

        return START_NOT_STICKY
    }

    fun startService() {
        logger.log(
            "[svc:$serviceId] startService() vpnInterface=${vpnInterface?.fd}"
        )

        if (dobbyConfigsRepository.getTelemetryEndpoint().isNotBlank()) {
            initTelemetry(dobbyConfigsRepository.getTelemetryEndpoint())
        }

        serviceScope.launch {
            startStopMutex.withLock {
                val hasActiveTunnel = vpnInterface != null || goTunFd != null

                if (hasActiveTunnel) {
                    logger.log("[svc:$serviceId] onStartCommand(): existing tunnel detected → teardown before start")
                    teardownVpn()
                }

                geoRouting.setGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
                when (dobbyConfigsRepository.getVpnInterface()) {
                    VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
                    VpnInterface.AMNEZIA_WG -> startAwg()
                    VpnInterface.NONE -> startNone()
                }
            }
        }
    }

    fun stopService() {
        logger.log(
            "[svc:$serviceId] stopService() vpnInterface=${vpnInterface?.fd}"
        )
        teardownVpn()
    }

    override fun onDestroy() {
        logger.log("[svc:$serviceId] onDestroy() begin vpnInterface=${vpnInterface?.fd}")
        // Cancel the scope first so any coroutine currently holding startStopMutex
        // is interrupted at its next suspension point and releases the lock.
        serviceScope.cancel()
        runBlocking {
            startStopMutex.withLock {
                teardownVpn()
            }
        }
        geoRouting.clearGeoRoutingConf()
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
        instance = null
        super.onDestroy()
        logger.log("[svc:$serviceId] onDestroy() end")
    }

    private fun startCloakOutline() {
        serviceScope.launch {
            startStopMutex.withLock {
                if (dobbyConfigsRepository.getIsCloakEnabled()) {
                    if (!cloakConnectInteractor.startCloak()) {
                        connectionState.updateServiceStarted(false)
                        teardownVpn()
                        stopSelf()
                        return@launch
                    }
                }
                if (!outlineInteractor.startOutline(instance)) {
                    connectionState.updateServiceStarted(false)
                    teardownVpn()
                    stopSelf()
                    return@launch
                }

                connectionState.updateServiceStarted(true)
            }
        }
    }

    private fun startAwg() {
        serviceScope.launch {
            startStopMutex.withLock {
                if (!awgInteractor.startAwg(instance)) {
                    connectionState.updateServiceStarted(false)
                    teardownVpn()
                    stopSelf()
                    return@launch
                }

                connectionState.updateServiceStarted(true)
            }
        }
    }

    private fun startNone() {
        connectionState.tryUpdateServiceStarted(false)
    }

    fun teardownVpn() {
        val interfaceToClose = vpnInterface
        val targetGoTunFd = goTunFd
        val fdBefore = runCatching { interfaceToClose?.fd }.getOrNull()
        logger.log("[svc:$serviceId] teardownVpn(): begin fd=$fdBefore")
        runCatching {
            awgInteractor.stopAwg()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] onDestroy(): failed to disconnect AmneziaWG: ${e.message}")
        }
        runCatching {
            outlineInteractor.stopOutline()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] onDestroy(): failed to disconnect Outline: ${e.message}")
        }
        runCatching {
            runBlocking {
                this@runCatching.runCatching {
                    logger.log("Stopping Cloak client (if running)...")
                    cloakConnectInteractor.disconnect()
                }.onFailure<DisconnectResult> { e ->
                    logger.log("Failed to stop Cloak client: ${e.message}")
                }
            }
        }.onFailure { e ->
            logger.log("[svc:$serviceId] onDestroy(): failed to stop Cloak: ${e.message}")
        }
        targetGoTunFd?.let { targetFd ->
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
        runCatching {
            interfaceToClose?.close()
        }
        vpnInterface = null
        goTunFd = null
        logger.log("[svc:$serviceId] teardownVpn(): end fd=$fdBefore")
    }
}
