package com.dobby.backend

import android.net.VpnService
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import com.dobby.gomobile.dobbyvpn.PlatformCallbacks
import com.dobby.gomobile.dobbyvpn.SocketProtector

object GoBackendWrapper {
    fun stopSession(sessionId: String, commandId: String, generation: Long): String =
        Dobbyvpn.stopSession(sessionId, commandId, generation)

    fun startCloakClient(localHost: String, localPort: String, config: String, udp: Boolean): Int =
        Dobbyvpn.startCloakClient(localHost, localPort, config, udp)

    fun stopCloakClient() {
        Dobbyvpn.stopCloakClient()
    }

    fun setGeoRoutingConf(cidrs: String) {
        Dobbyvpn.setGeoRoutingConf(cidrs)
    }

    fun clearGeoRoutingConf() {
        Dobbyvpn.clearGeoRoutingConf()
    }

    fun clearDNSCache() {
        Dobbyvpn.clearDNSCache()
    }

    fun setDNSCacheEntries(entries: String): Int {
        return Dobbyvpn.setDNSCacheEntries(entries).toInt()
    }

    fun initLogger(path: String) {
        Dobbyvpn.initLogger(path)
    }

    fun initTelemetry(endpoint: String, token: String) {
        Dobbyvpn.initTelemetry(endpoint, token)
    }

    fun stopTelemetry() {
        Dobbyvpn.stopTelemetry()
    }

    fun setupTelemetryAttributes(config: String) {
        Dobbyvpn.setupTelemetryAttributes(config)
    }

    fun getConnectionState(): Int {
        return Dobbyvpn.getConnectionState()
    }

    fun initHealthCheck() {
        Dobbyvpn.initHealthCheck()
    }

    fun startHealthCheck() {
        Dobbyvpn.startHealthCheck()
    }

    fun stopHealthCheck() {
        Dobbyvpn.stopHealthCheck()
    }

    fun measureTunnelProbeAverageLatencyMillis(timeoutMillis: Long): Long {
        return Dobbyvpn.measureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
    }

    fun getLastError(): String? = Dobbyvpn.getVpnLastError()?.ifEmpty { null }

    fun newVpnClient(config: String, protocol: String, fd: Int) {
        Dobbyvpn.newVpnClient(config, protocol, fd)
    }

    fun vpnConnect(): Int = Dobbyvpn.vpnConnect()

    fun vpnDisconnect() {
        Dobbyvpn.vpnDisconnect()
    }

    fun registerVpnService(service: VpnService) {
        Dobbyvpn.registerSocketProtector(object : SocketProtector {
            override fun protect(fd: Int): Boolean = when (service) {
                is DobbyVpnService -> service.protectProtocolSocket(fd)
                else -> service.protect(fd)
            }
        })
    }

    /** Installs the v1 Android platform callback. Every native callback is correlated. */
    fun registerSessionPlatform(service: DobbyVpnService) {
        // Do not retain a destroyed Service in gomobile. The proxy resolves the current
        // prepared shell for every callback, so stale callbacks fail closed.
        Dobbyvpn.registerSessionPlatform(object : PlatformCallbacks {
            override fun acquireTunnel(sessionId: String, generation: Long): Int =
                PlatformServiceRegistry.current(sessionId)?.acquireTunnel(sessionId, generation) ?: -1

            override fun releaseTunnel(sessionId: String, generation: Long, fd: Int) {
                PlatformServiceRegistry.current(sessionId)?.releaseTunnel(sessionId, generation, fd)
            }

            override fun protectSocket(sessionId: String, generation: Long, fd: Int): Boolean =
                PlatformServiceRegistry.current(sessionId)?.protectProtocolSocket(sessionId, generation, fd) ?: false

            override fun publishState(
                sessionId: String,
                generation: Long,
                sequence: Long,
                state: String,
                profileIndex: Int,
                profileProtocol: String,
                failureCode: String,
            ) {
                PlatformServiceRegistry.current(sessionId)?.publishState(sessionId, generation, state, failureCode)
            }
        })
    }
}
