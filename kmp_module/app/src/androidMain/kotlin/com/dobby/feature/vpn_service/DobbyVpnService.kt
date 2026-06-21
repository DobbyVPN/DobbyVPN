package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.ParcelFileDescriptor
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.awg.AmneziaWGInteractor
import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.georouting.GeoRouting
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import com.dobby.feature.vpn_service.domain.xray.XrayInteractor
import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.koin.android.ext.android.inject
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
    private val logsRepository: LogsRepository by inject()
    private val geoRouting: GeoRouting by inject()
    private val cloakConnectInteractor: CloakConnectionInteractor by inject()
    private val outlineInteractor: OutlineInteractor by inject()
    private val awgInteractor: AmneziaWGInteractor by inject()
    private val xrayInteractor: XrayInteractor by inject()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    private val connectionState: ConnectionStateRepository by inject()

    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val startStopMutex = Mutex()

    override fun onCreate() {
        super.onCreate()
        instance = this
        logger.log("[svc:$serviceId] onCreate()")

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

        serviceScope.launch {
            startStopMutex.withLock {
                val hasActiveTunnel = vpnInterface != null || goTunFd != null

                if (hasActiveTunnel) {
                    logger.log("[svc:$serviceId] onStartCommand(): existing tunnel detected → teardown before start")
                    teardownVpn()
                }

                geoRouting.setGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
                startConfiguredProtocol()
            }
        }
    }

    fun switchProtocol(): Boolean {
        logger.log("[svc:$serviceId] switchProtocol() requested vpnInterface=${vpnInterface?.fd} goTunFd=$goTunFd")
        return runBlocking {
            startStopMutex.withLock {
                val hasActiveTunnel = vpnInterface != null || goTunFd != null
                if (!hasActiveTunnel) {
                    logger.log("[svc:$serviceId] switchProtocol(): no active tunnel")
                    return@withLock false
                }

                stopProtocolsForSwitch()
                geoRouting.setGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
                val switched = startConfiguredProtocol()
                logger.log("[svc:$serviceId] switchProtocol(): result=$switched")
                switched
            }
        }
    }

    override fun onDestroy() {
        logger.log("[svc:$serviceId] onDestroy() begin")
        teardownVpn()
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

    private suspend fun startConfiguredProtocol(): Boolean =
        when (dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
            VpnInterface.AMNEZIA_WG -> startAwg()
            VpnInterface.XRAY -> startXray()
            VpnInterface.NONE -> startNone()
        }

    private suspend fun startCloakOutline(): Boolean {
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            if (!cloakConnectInteractor.startCloak()) {
                connectionState.updateServiceStarted(false)
                teardownVpn()
                stopSelf()
                return false
            }
        }
        if (!outlineInteractor.startOutline(instance)) {
            connectionState.updateServiceStarted(false)
            teardownVpn()
            stopSelf()
            return false
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    private suspend fun startAwg(): Boolean {
        if (!awgInteractor.startAwg(instance)) {
            connectionState.updateServiceStarted(false)
            teardownVpn()
            stopSelf()
            return false
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    private suspend fun startXray(): Boolean {
        if (!dobbyConfigsRepository.getIsXrayEnabled()) {
            connectionState.updateServiceStarted(false)
            stopSelf()
            return false
        }

        if (!xrayInteractor.startXray(instance)) {
            connectionState.updateServiceStarted(false)
            teardownVpn()
            stopSelf()
            return false
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    private fun startNone(): Boolean {
        connectionState.tryUpdateServiceStarted(false)
        return false
    }

    fun stopService() {
        logger.log("[svc:$serviceId] stopService() vpnInterface=${vpnInterface?.fd}")
        runBlocking {
            startStopMutex.withLock {
                teardownVpn()
            }
        }
    }

    private suspend fun stopProtocolsForSwitch() {
        logger.log("[svc:$serviceId] stopProtocolsForSwitch(): begin configuredInterface=${dobbyConfigsRepository.getVpnInterface()}")
        runCatching {
            outlineInteractor.stopOutline()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocolsForSwitch(): outline disconnect warning: ${e.message}")
        }
        runCatching {
            xrayInteractor.stopXray(instance)
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocolsForSwitch(): xray disconnect warning: ${e.message}")
        }
        runCatching {
            awgInteractor.stopAwg()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocolsForSwitch(): awg disconnect warning: ${e.message}")
        }
        runCatching {
            logger.log("Stopping Cloak client for protocol switch (if running)...")
            cloakConnectInteractor.disconnect()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocolsForSwitch(): failed to stop Cloak: ${e.message}")
        }
        logger.log("[svc:$serviceId] stopProtocolsForSwitch(): end")
    }

    @Synchronized
    fun teardownVpn() {
        val fdBefore = runCatching { vpnInterface?.fd }.getOrNull()
        logger.log("[svc:$serviceId] teardownVpn(): begin fd=$fdBefore configuredInterface=${dobbyConfigsRepository.getVpnInterface()}")

        runBlocking {
            stopProtocolsForSwitch()
        }
        goTunFd?.let { targetFd ->
            logger.log("[svc:$serviceId] teardownVpn(): goTunFd=$targetFd released to Go disconnect path")
        }
        goTunFd = null
        runCatching {
            vpnInterface?.close()
        }
        vpnInterface = null
        logger.log("[svc:$serviceId] teardownVpn(): end fd=$fdBefore")
    }
}
