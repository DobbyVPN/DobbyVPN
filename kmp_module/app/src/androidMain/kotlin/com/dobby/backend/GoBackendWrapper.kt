package com.dobby.backend

import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import com.dobby.gomobile.dobbyvpn.PlatformCallbacks

object GoBackendWrapper {
    fun stopSession(sessionId: String, generation: Long): String =
        Dobbyvpn.stopSession(sessionId, generation)

    fun initLogger(path: String): Boolean = Dobbyvpn.initLogger(path)

    /** Installs the Android platform callback. Every native callback is correlated. */
    fun registerSessionPlatform(service: DobbyVpnService) {
        // Do not retain a destroyed Service in gomobile. The proxy resolves the current
        // prepared shell for every callback, so stale callbacks fail closed.
        Dobbyvpn.registerSessionPlatform(object : PlatformCallbacks {
            override fun acquireTunnel(sessionId: String, generation: Long): Int =
                PlatformServiceRegistry.current(sessionId)?.acquireTunnel(sessionId, generation)
                    ?: missingPlatform("acquireTunnel", generation, -1)

            override fun releaseTunnel(sessionId: String, generation: Long, fd: Int): Boolean =
                PlatformServiceRegistry.current(sessionId)?.releaseTunnel(sessionId, generation, fd)
                    ?: missingPlatform("releaseTunnel", generation, false)

            override fun protectSocket(sessionId: String, generation: Long, fd: Int): Boolean =
                PlatformServiceRegistry.current(sessionId)?.protectProtocolSocket(sessionId, generation, fd)
                    ?: missingPlatform("protectSocket", generation, false)

            override fun publishState(
                sessionId: String,
                generation: Long,
                state: String,
                failureCode: String,
            ) {
                val platform = PlatformServiceRegistry.current(sessionId)
                if (platform == null) {
                    missingPlatform("publishState", generation, Unit)
                } else {
                    platform.publishState(sessionId, generation, state, failureCode)
                }
            }
        })
    }

    private fun <T> missingPlatform(operation: String, generation: Long, result: T): T {
        IllegalStateException(
            "$operation has no prepared Android platform for generation=$generation",
        ).printStackTrace()
        return result
    }
}
