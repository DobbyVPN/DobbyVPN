package com.dobby.feature.main.presentation

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Black-box lifecycle contract for the platform coordinator.  The production coordinator is
 * currently embedded in MainViewModel, so this deterministic model keeps the externally visible
 * rules stable while orchestration moves into the Go session API.
 */
class LifecycleCoordinatorCharacterizationTest {
    @Test
    fun double_start_has_one_generation_and_one_start_operation() {
        val coordinator = LifecycleCoordinatorModel()

        val first = coordinator.start()!!
        val second = coordinator.start()
        coordinator.completeProbe(first)

        assertEquals(1L, first)
        assertEquals(null, second)
        assertEquals(LifecycleState.CONNECTED, coordinator.state)
        assertEquals(1, coordinator.startOperations)
    }

    @Test
    fun stop_during_probe_prevents_delayed_probe_from_connecting() {
        val coordinator = LifecycleCoordinatorModel()
        val generation = coordinator.start()!!

        coordinator.stop(generation)
        coordinator.completeProbe(generation)

        assertEquals(LifecycleState.STOPPING, coordinator.state)
        assertFalse(coordinator.connected)
        coordinator.completeCleanup(generation)
        assertEquals(LifecycleState.IDLE, coordinator.state)
    }

    @Test
    fun delayed_callback_for_old_generation_is_ignored() {
        val coordinator = LifecycleCoordinatorModel()
        val oldGeneration = coordinator.start()!!
        coordinator.stop(oldGeneration)
        coordinator.completeCleanup(oldGeneration)
        val activeGeneration = coordinator.start()!!

        coordinator.completeProbe(oldGeneration)

        assertEquals(LifecycleState.PROBING, coordinator.state)
        assertFalse(coordinator.connected)
        coordinator.completeProbe(activeGeneration)
        assertTrue(coordinator.connected)
    }

    @Test
    fun failover_waits_for_cleanup_before_starting_next_generation() {
        val coordinator = LifecycleCoordinatorModel()
        val first = coordinator.start()!!
        coordinator.completeProbe(first)

        assertTrue(coordinator.requestFailover())
        assertFalse(coordinator.requestFailover())
        assertEquals(LifecycleState.STOPPING, coordinator.state)
        assertEquals(null, coordinator.start())

        coordinator.completeCleanup(first)

        assertEquals(2L, coordinator.activeGeneration)
        assertEquals(LifecycleState.PROBING, coordinator.state)
        assertEquals(2, coordinator.startOperations)
    }

    @Test
    fun start_timeout_enters_cleanup_and_only_then_allows_restart() {
        val coordinator = LifecycleCoordinatorModel()
        val first = coordinator.start()!!

        coordinator.timeout(first)

        assertEquals(LifecycleState.STOPPING, coordinator.state)
        assertEquals(null, coordinator.start())
        coordinator.completeCleanup(first)
        assertEquals(2L, coordinator.start())
    }
}

private enum class LifecycleState { IDLE, PROBING, CONNECTED, STOPPING }

private class LifecycleCoordinatorModel {
    var state: LifecycleState = LifecycleState.IDLE
        private set
    var activeGeneration: Long = 0
        private set
    var startOperations: Int = 0
        private set
    var connected: Boolean = false
        private set
    private var failoverPending = false

    fun start(): Long? {
        if (state != LifecycleState.IDLE) return null
        activeGeneration += 1
        startOperations += 1
        state = LifecycleState.PROBING
        connected = false
        return activeGeneration
    }

    fun completeProbe(generation: Long) {
        if (generation != activeGeneration || state != LifecycleState.PROBING) return
        state = LifecycleState.CONNECTED
        connected = true
    }

    fun stop(generation: Long) {
        if (generation != activeGeneration || state == LifecycleState.IDLE) return
        state = LifecycleState.STOPPING
        connected = false
    }

    fun timeout(generation: Long) = stop(generation)

    fun requestFailover(): Boolean {
        if (state != LifecycleState.CONNECTED || failoverPending) return false
        failoverPending = true
        stop(activeGeneration)
        return true
    }

    fun completeCleanup(generation: Long) {
        if (generation != activeGeneration || state != LifecycleState.STOPPING) return
        state = LifecycleState.IDLE
        connected = false
        if (failoverPending) {
            failoverPending = false
            check(start() != null)
        }
    }
}
