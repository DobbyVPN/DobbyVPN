package com.dobby.feature.vpn_service

import android.content.Context
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import java.util.UUID

/**
 * Device coverage for the real Android shell. TUN creation itself deliberately remains outside
 * this suite because Android requires an explicit user VPN-consent flow; that is a physical
 * release gate, not something the test is allowed to bypass.
 */
@RunWith(AndroidJUnit4::class)
class DobbyVpnServiceInstrumentationTest {
    private val context: Context
        get() = InstrumentationRegistry.getInstrumentation().targetContext
    private var sessionId: String? = null

    @After
    fun cleanup() {
        sessionId?.let { context.stopService(DobbyVpnService.createStopIntent(context, 0, false)) }
        sessionId?.let { id ->
            PlatformServiceRegistry.current(id)?.let(PlatformServiceRegistry::clear)
        }
        DobbyVpnServiceTestEvents.endCapture()
    }

    @Test
    fun foreground_promotion_precedes_platform_ready_callback() = runBlocking {
        val session = UUID.randomUUID().toString()
        sessionId = session
        DobbyVpnServiceTestEvents.beginCapture()
        PlatformServiceRegistry.expect(session)

        context.startForegroundService(DobbyVpnService.createPrepareIntent(context, session))

        assertEquals(true, PlatformServiceRegistry.awaitReady(10_000))
        assertNotNull(PlatformServiceRegistry.current(session))
        assertEquals(listOf("foreground", "prepared"), DobbyVpnServiceTestEvents.snapshot())
    }

    @Test
    fun real_service_rejects_stale_session_and_invalid_socket_protection_without_tun() = runBlocking {
        val session = UUID.randomUUID().toString()
        sessionId = session
        DobbyVpnServiceTestEvents.beginCapture()
        PlatformServiceRegistry.expect(session)
        context.startForegroundService(DobbyVpnService.createPrepareIntent(context, session))
        assertEquals(true, PlatformServiceRegistry.awaitReady(10_000))
        val service = requireNotNull(PlatformServiceRegistry.current(session))

        assertEquals(-1, service.acquireTunnel("stale-$session", 1))
        assertFalse(service.protectProtocolSocket(session, 1, -1))
        service.releaseTunnel("stale-$session", 1, 42)
        service.releaseTunnel(session, 1, 42)
        assertNull(service.vpnInterface)
        assertNull(service.goTunFd)
    }
}
