package com.dobby.feature.vpn_service

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * JVM-only service contract tests. Android framework services cannot be instantiated without an
 * emulator or Robolectric in this module, so this harness characterizes the intent/TUN ownership
 * boundary that DobbyVpnService must preserve. Device-level service verification remains a
 * release gate.
 */
class DobbyVpnServiceLifecycleContractTest {
    @Test
    fun foreground_promotion_precedes_tun_acquisition() {
        val service = AndroidServiceBoundaryModel()

        assertTrue(service.start(generation = 1, protocol = Protocol.OUTLINE))

        assertEquals(listOf("foreground", "tun:1", "start:outline:1"), service.events)
    }

    @Test
    fun protocol_transition_closes_old_descriptor_before_new_tun_is_acquired() {
        val service = AndroidServiceBoundaryModel()
        service.start(generation = 1, protocol = Protocol.TRUST_TUNNEL)

        assertTrue(service.start(generation = 2, protocol = Protocol.XRAY))

        assertEquals(
            listOf(
                "foreground", "tun:1", "start:trust_tunnel:1",
                "foreground", "stop:trust_tunnel:1", "close:1", "tun:2", "start:xray:2",
            ),
            service.events,
        )
        assertEquals(2L, service.openDescriptorGeneration)
    }

    @Test
    fun trust_tunnel_to_outline_also_uses_a_fresh_tun() {
        val service = AndroidServiceBoundaryModel()
        service.start(generation = 1, protocol = Protocol.TRUST_TUNNEL)

        assertTrue(service.start(generation = 2, protocol = Protocol.OUTLINE))

        assertEquals(
            listOf(
                "foreground", "tun:1", "start:trust_tunnel:1",
                "foreground", "stop:trust_tunnel:1", "close:1", "tun:2", "start:outline:2",
            ),
            service.events,
        )
    }

    @Test
    fun stale_intents_cannot_stop_or_replace_active_tun() {
        val service = AndroidServiceBoundaryModel()
        service.start(generation = 4, protocol = Protocol.OUTLINE)

        assertFalse(service.start(generation = 3, protocol = Protocol.XRAY))
        assertFalse(service.stop(generation = 3))

        assertEquals(4L, service.openDescriptorGeneration)
        assertEquals(Protocol.OUTLINE, service.activeProtocol)
    }

    @Test
    fun protection_failure_aborts_start_and_closes_descriptor() {
        val service = AndroidServiceBoundaryModel(protectionSucceeds = false)

        assertFalse(service.start(generation = 1, protocol = Protocol.TRUST_TUNNEL))

        assertEquals(listOf("foreground", "tun:1", "protect_failed:1", "close:1"), service.events)
        assertEquals(null, service.openDescriptorGeneration)
    }

    @Test
    fun process_recreation_does_not_reuse_descriptor_without_new_generation_intent() {
        val service = AndroidServiceBoundaryModel()
        service.restoreAfterProcessRecreation()

        assertEquals(null, service.openDescriptorGeneration)
        assertTrue(service.start(generation = 9, protocol = Protocol.OUTLINE))
        assertEquals(9L, service.openDescriptorGeneration)
    }
}

private enum class Protocol { OUTLINE, XRAY, TRUST_TUNNEL }

private class AndroidServiceBoundaryModel(
    private val protectionSucceeds: Boolean = true,
) {
    val events = mutableListOf<String>()
    var activeProtocol: Protocol? = null
        private set
    var openDescriptorGeneration: Long? = null
        private set
    private var activeGeneration = -1L

    fun start(generation: Long, protocol: Protocol): Boolean {
        if (generation < activeGeneration) return false
        events += "foreground"
        if (generation == activeGeneration && openDescriptorGeneration != null) return false
        if (openDescriptorGeneration != null) teardown()
        activeGeneration = generation
        openDescriptorGeneration = generation
        events += "tun:$generation"
        if (!protectionSucceeds) {
            events += "protect_failed:$generation"
            teardown()
            return false
        }
        activeProtocol = protocol
        events += "start:${protocol.name.lowercase()}:$generation"
        return true
    }

    fun stop(generation: Long): Boolean {
        if (generation < activeGeneration || openDescriptorGeneration == null) return false
        teardown()
        activeGeneration = -1L
        return true
    }

    fun restoreAfterProcessRecreation() {
        activeGeneration = -1L
        activeProtocol = null
        openDescriptorGeneration = null
    }

    private fun teardown() {
        activeProtocol?.let { events += "stop:${it.name.lowercase()}:$activeGeneration" }
        openDescriptorGeneration?.let { events += "close:$it" }
        activeProtocol = null
        openDescriptorGeneration = null
    }
}
