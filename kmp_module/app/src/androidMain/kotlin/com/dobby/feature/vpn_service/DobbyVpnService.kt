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
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.georouting.GeoRouting
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import com.dobby.feature.vpn_service.domain.xray.XrayInteractor
import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.koin.android.ext.android.inject
import java.util.*
import com.dobby.feature.vpn_service.domain.trusttunnel.TrustTunnelInteractor

const val IS_FROM_UI = "isLaunchedFromUi"

class DobbyVpnService : VpnService() {
    companion object {
        private const val EXTRA_IS_PROTOCOL_PROBE = "com.dobby.vpn.extra.IS_PROTOCOL_PROBE"

        @Volatile
        var instance: DobbyVpnService? = null

        fun createIntent(context: Context, isProtocolProbe: Boolean): Intent {
            return Intent(context, DobbyVpnService::class.java)
                .putExtra(EXTRA_IS_PROTOCOL_PROBE, isProtocolProbe)
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
    private val xrayInteractor: XrayInteractor by inject()
    private val trustTunnelInteractor: TrustTunnelInteractor by inject()
    private val dobbyConfigsRepository: DobbyConfigsRepository by inject()
    private val connectionState: ConnectionStateRepository by inject()

    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val startStopMutex = Mutex()
    private var activeInterface: VpnInterface? = null

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
                    val hasVpnTransport = networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
                    val transports = buildList {
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) add("WIFI")
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) add("CELL")
                        if (networkCapabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) add("ETH")
                        if (hasVpnTransport) add("VPN")
                    }.joinToString("|")
                    logger.log(
                        "[svc:$serviceId] net:onCapabilitiesChanged " +
                            "net=$network transports=$transports " +
                            "internet=$hasInternet validated=$validated"
                    )
                    if (hasVpnTransport) {
                        connectionState.tryUpdateVpnNetworkReady(true)
                    }
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
        val isProtocolProbe = intent?.getBooleanExtra(EXTRA_IS_PROTOCOL_PROBE, false) == true
        logger.log(
            "[svc:$serviceId] onStartCommand(startId=$startId flags=$flags " +
                "isProtocolProbe=$isProtocolProbe) vpnInterface=${vpnInterface?.fd}"
        )

        startService(intent, isProtocolProbe)

        return START_NOT_STICKY
    }

    fun startService(intent: Intent? = null, isProtocolProbe: Boolean) {
        logger.log(
            "[svc:$serviceId] startService(isProtocolProbe=$isProtocolProbe) vpnInterface=${vpnInterface?.fd}"
        )

        serviceScope.launch {
            startStopMutex.withLock {
                val hasActiveTunnel = vpnInterface != null || goTunFd != null
                var canPreserveActiveTunnelOnProbeFailure = hasActiveTunnel
                val requestedInterface = dobbyConfigsRepository.getVpnInterface()

                if (hasActiveTunnel) {
                    if (requiresInterfaceTeardown(activeInterface, requestedInterface)) {
                        canPreserveActiveTunnelOnProbeFailure = false
                        logger.log(
                            "[svc:$serviceId] startService(): existing VPN interface is incompatible with " +
                                "requestedInterface=$requestedInterface activeInterface=$activeInterface; tearing down interface"
                        )
                        teardownVpn()
                    } else {
                        logger.log("[svc:$serviceId] startService(): existing VPN interface detected; switching protocol without closing interface")
                        connectionState.tryUpdateVpnNetworkReady(true)
                        stopCloakSidecarForProtocolRestart()
                        goTunFd = null
                    }
                }

                geoRouting.setGeoRoutingConf(dobbyConfigsRepository.getGeoRoutingConf())
                startConfiguredProtocol(
                    intent = intent,
                    isProtocolProbe = isProtocolProbe,
                    preserveActiveTunnelOnProbeFailure = canPreserveActiveTunnelOnProbeFailure
                )
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

    private suspend fun startConfiguredProtocol(
        intent: Intent?,
        isProtocolProbe: Boolean,
        preserveActiveTunnelOnProbeFailure: Boolean,
    ): Boolean {
        val requestedInterface = dobbyConfigsRepository.getVpnInterface()
        val started = when (requestedInterface) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline(
                isProtocolProbe = isProtocolProbe,
                preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
            )
            VpnInterface.XRAY -> startXray(
                isProtocolProbe = isProtocolProbe,
                preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
            )
            VpnInterface.TRUST_TUNNEL -> startTrustTunnel(intent)
            VpnInterface.NONE -> startNone()
        }
        activeInterface = if (started) {
            requestedInterface
        } else if (!preserveActiveTunnelOnProbeFailure || vpnInterface == null && goTunFd == null) {
            null
        } else {
            activeInterface
        }
        return started
    }

    private fun requiresInterfaceTeardown(
        currentInterface: VpnInterface?,
        requestedInterface: VpnInterface,
    ): Boolean {
        if (currentInterface == null) return true
        return false
    }

    private suspend fun startCloakOutline(
        isProtocolProbe: Boolean,
        preserveActiveTunnelOnProbeFailure: Boolean,
    ): Boolean {
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            if (!cloakConnectInteractor.startCloak()) {
                return handleProtocolStartFailure(
                    protocolName = "Cloak",
                    isProtocolProbe = isProtocolProbe,
                    preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
                )
            }
        }
        if (!outlineInteractor.startOutline(instance)) {
            return handleProtocolStartFailure(
                protocolName = "Outline",
                isProtocolProbe = isProtocolProbe,
                preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
            )
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    private suspend fun startXray(
        isProtocolProbe: Boolean,
        preserveActiveTunnelOnProbeFailure: Boolean,
    ): Boolean {
        if (!dobbyConfigsRepository.getIsXrayEnabled()) {
            return handleProtocolStartFailure(
                protocolName = "Xray",
                isProtocolProbe = isProtocolProbe,
                preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
            )
        }

        if (!xrayInteractor.startXray(instance)) {
            return handleProtocolStartFailure(
                protocolName = "Xray",
                isProtocolProbe = isProtocolProbe,
                preserveActiveTunnelOnProbeFailure = preserveActiveTunnelOnProbeFailure,
            )
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    private suspend fun handleProtocolStartFailure(
        protocolName: String,
        isProtocolProbe: Boolean,
        preserveActiveTunnelOnProbeFailure: Boolean,
    ): Boolean {
        connectionState.updateServiceStarted(false)
        if (isProtocolProbe && preserveActiveTunnelOnProbeFailure) {
            logger.log(
                "[svc:$serviceId] $protocolName probe failed; preserving existing VPN interface for further protocol probes"
            )
            stopProtocols()
            return false
        }

        teardownVpn()
        stopSelf()
        return false
    }

    private fun startNone(): Boolean {
        connectionState.tryUpdateServiceStarted(false)
        return false
    }

    private suspend fun startTrustTunnel(intent: Intent?): Boolean {
        val isServiceStartedFromUi = intent?.getBooleanExtra(IS_FROM_UI, false) ?: false
        val shouldTurnOn = dobbyConfigsRepository.getIsTrustTunnelEnabled()

        if (!shouldTurnOn && isServiceStartedFromUi) {
            connectionState.updateServiceStarted(false)
            teardownVpn()
            stopSelf()
            return false
        }

        if (!trustTunnelInteractor.startTrustTunnel(instance)) {
            connectionState.updateServiceStarted(false)
            teardownVpn()
            stopSelf()
            return false
        }

        connectionState.updateServiceStarted(true)
        return true
    }

    fun stopService() {
        logger.log("[svc:$serviceId] stopService() vpnInterface=${vpnInterface?.fd}")
        runBlocking {
            startStopMutex.withLock {
                teardownVpn()
            }
        }
    }

    private suspend fun stopCloakSidecarForProtocolRestart() {
        runCatching {
            logger.log("[svc:$serviceId] stopCloak before protocol restart")
            cloakConnectInteractor.disconnect()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopCloak before protocol restart failed: ${e.message}")
        }
    }

    private suspend fun stopProtocols() {
        logger.log("[svc:$serviceId] stopProtocols(): begin configuredInterface=${dobbyConfigsRepository.getVpnInterface()}")
        runCatching {
            outlineInteractor.stopOutline()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocols(): outline disconnect warning: ${e.message}")
        }
        runCatching {
            xrayInteractor.stopXray(instance)
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocols(): xray disconnect warning: ${e.message}")
        }
        runCatching {
            trustTunnelInteractor.stopTrustTunnel(instance)
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocols(): trusttunnel disconnect warning: ${e.message}")
        }
        runCatching {
            logger.log("Stopping Cloak client (if running)...")
            cloakConnectInteractor.disconnect()
        }.onFailure { e ->
            logger.log("[svc:$serviceId] stopProtocols(): failed to stop Cloak: ${e.message}")
        }
        logger.log("[svc:$serviceId] stopProtocols(): end")
    }

    @Synchronized
    fun teardownVpn() {
        val fdBefore = runCatching { vpnInterface?.fd }.getOrNull()
        logger.log("[svc:$serviceId] teardownVpn(): begin fd=$fdBefore configuredInterface=${dobbyConfigsRepository.getVpnInterface()}")

        runBlocking {
            stopProtocols()
        }
        goTunFd?.let { targetFd ->
            logger.log("[svc:$serviceId] teardownVpn(): goTunFd=$targetFd released to Go disconnect path")
        }
        goTunFd = null
        runCatching {
            vpnInterface?.close()
        }
        vpnInterface = null
        activeInterface = null
        logger.log("[svc:$serviceId] teardownVpn(): end fd=$fdBefore")
    }
}
