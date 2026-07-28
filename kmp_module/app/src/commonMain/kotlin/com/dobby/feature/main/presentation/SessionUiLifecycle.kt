package com.dobby.feature.main.presentation

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionState

/** Keeps UI rendering monotonic while the Go session owns connection policy. */
internal class SessionUiLifecycle {
    var activeGeneration: ULong? = null
        private set
    var lastSequence: ULong = 0u
        private set
    private var stopRequested = false

    fun begin(generation: ULong): VpnConnectionState? {
        if (activeGeneration != null) return null
        activeGeneration = generation
        stopRequested = false
        return VpnConnectionState.CONNECTING
    }

    fun requestStop(): VpnConnectionState? {
        if (activeGeneration == null) return null
        stopRequested = true
        return VpnConnectionState.STOPPING
    }

    fun render(event: SessionEvent): VpnConnectionState? {
        if (event.sequence <= lastSequence) return null
        lastSequence = event.sequence
        if (event.generation != activeGeneration) {
            // AUTO_SELECT can fail over inside Go. Only adopt an actual next-generation
            // start after the prior generation's ordered terminal event cleared it; never
            // treat generation-zero configuration notifications as a running tunnel.
            if (
                activeGeneration == null &&
                event.generation > 0uL &&
                event.state in setOf(SessionState.PROBING, SessionState.PREPARING)
            ) {
                activeGeneration = event.generation
                stopRequested = false
                return VpnConnectionState.CONNECTING
            }
            return null
        }
        if (stopRequested && event.state !in setOf(SessionState.STOPPING, SessionState.IDLE, SessionState.FAILED, SessionState.DESTROYED)) {
            return null
        }

        val state = event.state.toConnectionState()
        if (event.state in setOf(SessionState.IDLE, SessionState.CONFIGURED, SessionState.FAILED, SessionState.DESTROYED)) {
            activeGeneration = null
            stopRequested = false
        }
        return state
    }

    fun failStart() {
        activeGeneration = null
        stopRequested = false
    }
}

internal fun SessionState.toConnectionState(): VpnConnectionState = when (this) {
    SessionState.PROBING, SessionState.PREPARING -> VpnConnectionState.CONNECTING
    SessionState.CONNECTED -> VpnConnectionState.CONNECTED
    SessionState.STOPPING -> VpnConnectionState.STOPPING
    SessionState.IDLE,
    SessionState.CONFIGURED,
    SessionState.FAILED,
    SessionState.DESTROYED,
    SessionState.UNSPECIFIED,
    SessionState.UNKNOWN,
    -> VpnConnectionState.DISCONNECTED
}
