package com.dobby.backend

import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import com.dobby.gomobile.dobbyvpn.PlatformCallbacks
import com.dobby.gomobile.dobbyvpn.SocketProtector

object GoBackendWrapper {
    fun stopSession(sessionId: String, commandId: String, generation: Long): String =
        Dobbyvpn.stopSession(sessionId, commandId, generation)

    fun initLogger(path: String): Boolean = Dobbyvpn.initLogger(path)

    fun initTelemetry(endpoint: String, token: String) {
        Dobbyvpn.initTelemetry(endpoint, token)
    }

    fun stopTelemetry() {
        Dobbyvpn.stopTelemetry()
    }

    fun setupTelemetryAttributes(config: String) {
        Dobbyvpn.setupTelemetryAttributes(config)
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
