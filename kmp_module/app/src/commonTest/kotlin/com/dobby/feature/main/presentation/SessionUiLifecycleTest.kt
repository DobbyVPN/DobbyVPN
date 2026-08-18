package com.dobby.feature.main.presentation

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class SessionUiLifecycleTest {
    @Test
    fun double_connect_keeps_the_first_generation() {
        val lifecycle = SessionUiLifecycle()

        assertEquals(VpnConnectionState.CONNECTING, lifecycle.begin(1uL))
        assertNull(lifecycle.begin(2uL))
        assertEquals(1uL, lifecycle.activeGeneration)
    }

    @Test
    fun stop_while_probing_does_not_render_a_delayed_connection() {
        val lifecycle = SessionUiLifecycle()
        lifecycle.begin(4uL)

        assertEquals(VpnConnectionState.CONNECTING, lifecycle.render(event(4uL, 1uL, SessionState.PROBING)))
        assertEquals(VpnConnectionState.STOPPING, lifecycle.requestStop())
        assertNull(lifecycle.render(event(4uL, 2uL, SessionState.CONNECTED)))
        assertEquals(VpnConnectionState.STOPPING, lifecycle.render(event(4uL, 3uL, SessionState.STOPPING)))
        assertEquals(VpnConnectionState.DISCONNECTED, lifecycle.render(event(4uL, 4uL, SessionState.IDLE)))
    }

    @Test
    fun stale_generation_and_out_of_order_sequence_are_ignored() {
        val lifecycle = SessionUiLifecycle()
        lifecycle.begin(8uL)

        assertEquals(VpnConnectionState.CONNECTING, lifecycle.render(event(8uL, 4uL, SessionState.PREPARING)))
        assertNull(lifecycle.render(event(8uL, 3uL, SessionState.CONNECTED)))
        assertNull(lifecycle.render(event(7uL, 5uL, SessionState.CONNECTED)))
        assertEquals(VpnConnectionState.CONNECTED, lifecycle.render(event(8uL, 6uL, SessionState.CONNECTED)))
    }

    @Test
    fun go_failover_renders_the_next_generation_after_ordered_cleanup() {
        val lifecycle = SessionUiLifecycle()
        lifecycle.begin(1uL)

        assertEquals(VpnConnectionState.CONNECTED, lifecycle.render(event(1uL, 1uL, SessionState.CONNECTED)))
        assertEquals(VpnConnectionState.STOPPING, lifecycle.render(event(1uL, 2uL, SessionState.STOPPING)))
        assertEquals(VpnConnectionState.DISCONNECTED, lifecycle.render(event(1uL, 3uL, SessionState.IDLE)))
        assertEquals(VpnConnectionState.CONNECTING, lifecycle.render(event(2uL, 4uL, SessionState.PROBING)))
        assertEquals(VpnConnectionState.CONNECTED, lifecycle.render(event(2uL, 5uL, SessionState.CONNECTED)))
    }

    @Test
    fun snapshot_reconciliation_adopts_a_connected_generation_after_stream_loss() {
        val lifecycle = SessionUiLifecycle()

        assertEquals(
            VpnConnectionState.CONNECTED,
            lifecycle.reconcile(
                SessionSnapshot(
                    generation = 9uL,
                    state = SessionState.CONNECTED,
                    configured = true,
                    cleanupComplete = false,
                ),
            ),
        )
        assertEquals(9uL, lifecycle.activeGeneration)
    }

    @Test
    fun snapshot_reconciliation_clears_a_terminal_generation() {
        val lifecycle = SessionUiLifecycle()
        lifecycle.begin(9uL)

        assertEquals(
            VpnConnectionState.DISCONNECTED,
            lifecycle.reconcile(
                SessionSnapshot(
                    generation = 9uL,
                    state = SessionState.IDLE,
                    configured = true,
                    cleanupComplete = true,
                ),
            ),
        )
        assertNull(lifecycle.activeGeneration)
    }

    private fun event(generation: ULong, sequence: ULong, state: SessionState) =
        SessionEvent(generation = generation, sequence = sequence, state = state)
}
