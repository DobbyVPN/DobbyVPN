package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.regex.Pattern

/**
 * Device coverage for the real Android shell on an API 35 emulator. This suite accepts Android's
 * own consent dialog through UI Automator; it never grants VPN permission through shell commands
 * or hidden APIs. It then establishes the production [VpnService.Builder] TUN and observes real
 * IPv4 traffic routed into its descriptor. No profile, endpoint, or credential is required.
 */
@RunWith(AndroidJUnit4::class)
class DobbyVpnServiceInstrumentationTest {
    private val context: Context
        get() = InstrumentationRegistry.getInstrumentation().targetContext
    private var sessionId: String? = null
    private var acquiredTunnel: AcquiredTunnel? = null

    @After
    fun cleanup() {
        releaseAcquiredTunnel()
        sessionId?.let { context.stopService(DobbyVpnService.createStopIntent(context, 0, false)) }
        sessionId?.let { id ->
            PlatformServiceRegistry.current(id)?.let(PlatformServiceRegistry::clear)
        }
    }

    @Test
    fun foreground_promotion_precedes_platform_ready_callback(): Unit = runBlocking {
        val session = UUID.randomUUID().toString()
        sessionId = session
        resetInstrumentationLog()
        PlatformServiceRegistry.expect(session)

        context.startForegroundService(DobbyVpnService.createPrepareIntent(context, session))

        assertEquals(true, PlatformServiceRegistry.awaitReady(10_000))
        assertNotNull(PlatformServiceRegistry.current(session))
        val log = context.cacheDir.resolve(INSTRUMENTATION_LOG_FILE).readText()
        val foreground = log.indexOf("foreground promotion complete")
        val prepared = log.indexOf("platform preparation complete")
        assertTrue("service did not record foreground promotion", foreground >= 0)
        assertTrue("service did not record platform preparation", prepared >= 0)
        assertTrue("service became ready before foreground promotion", foreground < prepared)
    }

    @Test
    fun real_service_rejects_stale_session_and_invalid_socket_protection_without_tun(): Unit = runBlocking {
        val session = UUID.randomUUID().toString()
        sessionId = session
        PlatformServiceRegistry.expect(session)
        context.startForegroundService(DobbyVpnService.createPrepareIntent(context, session))
        assertEquals(true, PlatformServiceRegistry.awaitReady(10_000))
        val service = requireNotNull(PlatformServiceRegistry.current(session))

        assertEquals(-1, service.acquireTunnel("stale-$session", 1))
        assertFalse(service.protectProtocolSocket(session, 1, -1))
        assertFalse(service.releaseTunnel("stale-$session", 1, 42))
        assertFalse(service.releaseTunnel(session, 1, 42))
        assertNull(service.vpnInterface)
        assertNull(service.goTunFd)
    }

    @Test
    fun consented_service_establishes_routes_stable_traffic_and_cleans_up(): Unit = runBlocking {
        grantVpnConsentThroughSystemUi()

        val session = UUID.randomUUID().toString()
        sessionId = session
        PlatformServiceRegistry.expect(session)
        context.startForegroundService(DobbyVpnService.createPrepareIntent(context, session))
        assertEquals(true, PlatformServiceRegistry.awaitReady(10_000))
        val service = requireNotNull(PlatformServiceRegistry.current(session))

        val generation = 1L
        val fd = service.acquireTunnel(session, generation)
        assertTrue("Android refused to establish the consented VPN TUN", fd >= 0)
        // acquireTunnel hands the Go side a detached FD. Production Go closes it before it calls
        // releaseTunnel; this direct service test must faithfully take and close that ownership.
        acquiredTunnel = AcquiredTunnel(service, session, generation, fd, ParcelFileDescriptor.adoptFd(fd))

        val vpn = requireNotNull(connectivityManager.awaitVpnNetworkState(
            present = true,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS,
            pollIntervalMillis = POLL_INTERVAL_MILLIS
        ))
        val linkProperties = vpn.linkProperties
        assertTrue(!linkProperties.interfaceName.isNullOrBlank())
        assertTrue(linkProperties.linkAddresses.any { it.address.hostAddress == "10.7.0.2" })
        assertTrue(linkProperties.routes.any { it.destination.toString() == "0.0.0.0/0" })
        assertTrue(linkProperties.dnsServers.any { it.hostAddress == "1.1.1.1" })

        // Three independent packets make this a bounded stability check. The documentation-range
        // address is intentionally unreachable; seeing each packet on the TUN proves Android
        // routed it to the service rather than the physical emulator network.
        repeat(STABILITY_PACKET_COUNT) { index ->
            assertTrue("packet ${index + 1} did not reach the real TUN", sendAndObserveVpnPacket(service))
        }

        releaseAcquiredTunnel()
        assertNull(service.vpnInterface)
        assertNull(service.goTunFd)
        assertNull(awaitVpnNetwork(present = false))

        context.stopService(DobbyVpnService.createStopIntent(context, 0, false))
    }

    private val connectivityManager: ConnectivityManager
        get() = requireNotNull(context.getSystemService(ConnectivityManager::class.java))

    private fun resetInstrumentationLog() {
        context.cacheDir.resolve(INSTRUMENTATION_LOG_FILE).writeText("")
    }

    private fun grantVpnConsentThroughSystemUi() {
        if (VpnService.prepare(context) == null) return
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        instrumentation.startActivitySync(
            Intent(context, VpnConsentTestActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        val device = UiDevice.getInstance(instrumentation)
        val approval = device.wait(
            Until.findObject(By.res(Pattern.compile(".+:id/button1"))),
            CONSENT_TIMEOUT_MILLIS,
        )
        assertNotNull("Android VPN consent dialog did not expose its approval button", approval)
        requireNotNull(approval).click()

        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(CONSENT_TIMEOUT_MILLIS)
        while (System.nanoTime() < deadline) {
            if (VpnService.prepare(context) == null) return
            Thread.sleep(POLL_INTERVAL_MILLIS)
        }
        assertNull("Android did not grant VPN consent after the system dialog was approved", VpnService.prepare(context))
    }

    private fun awaitVpnNetwork(present: Boolean): Network? {
        return connectivityManager.awaitVpnNetwork(
            present = present,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS,
            pollIntervalMillis = POLL_INTERVAL_MILLIS
        )
    }

    private fun sendAndObserveVpnPacket(service: DobbyVpnService): Boolean {
        val descriptor = requireNotNull(service.vpnInterface)
        // The caller owns this duplicate so it can forcibly unblock a read on timeout. Do not
        // create it inside the worker: a timed-out worker would then retain an unread descriptor.
        val readerDescriptor = ParcelFileDescriptor.dup(descriptor.fileDescriptor)
        val input = ParcelFileDescriptor.AutoCloseInputStream(readerDescriptor)
        val executor = Executors.newSingleThreadExecutor()
        val packet = executor.submit<Boolean> {
            val buffer = ByteArray(MAX_PACKET_SIZE)
            var found = false
            while (!found) {
                val count = input.read(buffer)
                found = count >= IPV4_DESTINATION_END && packetTargetsDocumentationAddress(buffer, count)
            }
            found
        }
        try {
            val instrumentation = InstrumentationRegistry.getInstrumentation()
            val trafficComponent =
                "${instrumentation.context.packageName}/${VpnTrafficTestActivity::class.java.name}"
            UiDevice.getInstance(instrumentation).executeShellCommand(
                "am start -W -n $trafficComponent",
            )
            return try {
                packet.get(PACKET_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
            } catch (_: TimeoutException) {
                false
            }
        } finally {
            // Closing the owner unblocks AutoCloseInputStream.read before the worker is interrupted.
            runCatching { input.close() }
            packet.cancel(true)
            executor.shutdownNow()
            executor.awaitTermination(READER_SHUTDOWN_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        }
    }

    private fun releaseAcquiredTunnel() {
        val tunnel = acquiredTunnel ?: return
        // Clear first so an assertion failure or @After re-entry cannot close or release twice.
        acquiredTunnel = null
        runCatching { tunnel.goOwnedDescriptor.close() }
        check(tunnel.service.releaseTunnel(tunnel.sessionId, tunnel.generation, tunnel.fd)) {
            "Android service failed to release the Go-owned generation"
        }
    }

    private fun packetTargetsDocumentationAddress(packet: ByteArray, count: Int): Boolean =
        (packet[0].toInt() ushr 4) == 4 &&
            count >= IPV4_DESTINATION_END &&
            packet.copyOfRange(IPV4_DESTINATION_OFFSET, IPV4_DESTINATION_END)
                .contentEquals(byteArrayOf(192.toByte(), 0, 2, 1))

    private data class AcquiredTunnel(
        val service: DobbyVpnService,
        val sessionId: String,
        val generation: Long,
        val fd: Int,
        val goOwnedDescriptor: ParcelFileDescriptor,
    )

    private companion object {
        const val CONSENT_TIMEOUT_MILLIS = 10_000L
        const val NETWORK_TIMEOUT_MILLIS = 10_000L
        const val PACKET_TIMEOUT_MILLIS = 10_000L
        const val READER_SHUTDOWN_TIMEOUT_MILLIS = 1_000L
        const val POLL_INTERVAL_MILLIS = 100L
        const val STABILITY_PACKET_COUNT = 3
        const val MAX_PACKET_SIZE = 32_767
        const val DOCUMENTATION_ROUTE_ADDRESS = "192.0.2.1"
        const val IPV4_DESTINATION_OFFSET = 16
        const val IPV4_DESTINATION_END = 20
        const val INSTRUMENTATION_LOG_FILE = "instrumentation.log"
    }
}
