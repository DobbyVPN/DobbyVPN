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

        assertTrue(service.start(generation = 1))

        assertEquals(listOf("foreground", "tun:1", "start:1"), service.events)
    }

    @Test
    fun generation_replacement_closes_old_descriptor_before_new_tun_is_acquired() {
        val service = AndroidServiceBoundaryModel()
        service.start(generation = 1)

        assertTrue(service.start(generation = 2))

        assertEquals(
            listOf(
                "foreground", "tun:1", "start:1",
                "foreground", "stop:1", "close:1", "tun:2", "start:2",
            ),
            service.events,
        )
        assertEquals(2L, service.openDescriptorGeneration)
    }

    @Test
    fun stale_intents_cannot_stop_or_replace_active_tun() {
        val service = AndroidServiceBoundaryModel()
        service.start(generation = 4)

        assertFalse(service.start(generation = 3))
        assertFalse(service.stop(generation = 3))

        assertEquals(4L, service.openDescriptorGeneration)
    }

    @Test
    fun protection_failure_aborts_start_and_closes_descriptor() {
        val service = AndroidServiceBoundaryModel(protectionSucceeds = false)

        assertFalse(service.start(generation = 1))

        assertEquals(listOf("foreground", "tun:1", "protect_failed:1", "close:1"), service.events)
        assertEquals(null, service.openDescriptorGeneration)
    }

    @Test
    fun process_recreation_does_not_reuse_descriptor_without_new_generation_intent() {
        val service = AndroidServiceBoundaryModel()
        service.restoreAfterProcessRecreation()

        assertEquals(null, service.openDescriptorGeneration)
        assertTrue(service.start(generation = 9))
        assertEquals(9L, service.openDescriptorGeneration)
    }
}

private class AndroidServiceBoundaryModel(
    private val protectionSucceeds: Boolean = true,
) {
    val events = mutableListOf<String>()
    var openDescriptorGeneration: Long? = null
        private set
    private var activeGeneration = -1L
    private var started = false

    fun start(generation: Long): Boolean {
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
        started = true
        events += "start:$generation"
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
        started = false
        openDescriptorGeneration = null
    }

    private fun teardown() {
        if (started) events += "stop:$activeGeneration"
        openDescriptorGeneration?.let { events += "close:$it" }
        started = false
        openDescriptorGeneration = null
    }
}
