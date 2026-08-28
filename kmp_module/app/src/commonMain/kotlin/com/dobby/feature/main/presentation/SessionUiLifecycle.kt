package com.dobby.feature.main.presentation

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionState

/** Keeps UI rendering monotonic while the Go session owns connection policy. */
internal class SessionUiLifecycle {
    private var activeSessionId: String? = null
    var activeGeneration: ULong? = null
        private set
    var lastSequence: ULong = 0u
        private set
    private var stopRequested = false

    /** Starts a new UI session scope without inventing Go state or a sequence. */
    fun reset() {
        activeSessionId = null
        activeGeneration = null
        lastSequence = 0uL
        stopRequested = false
    }

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
        if (event.sessionId.isNotBlank()) {
            when (val knownSession = activeSessionId) {
                null -> activeSessionId = event.sessionId
                event.sessionId -> Unit
                else -> {
                    // Go starts its ordered ledger at sequence 1 for each
                    // session identity. Scope the UI cursor to that identity;
                    // never compare a new session's sequence with an old
                    // session's high-water mark.
                    reset()
                    activeSessionId = event.sessionId
                }
            }
        }
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

    /** Reconciles a retained process snapshot after a dropped event stream. */
    fun reconcile(snapshot: SessionSnapshot): VpnConnectionState? {
        if (snapshot.sessionId.isNotBlank()) {
            if (activeSessionId != null && activeSessionId != snapshot.sessionId) {
                reset()
            }
            activeSessionId = snapshot.sessionId
        }
        if (snapshot.generation > 0uL && snapshot.state in setOf(
                SessionState.PROBING,
                SessionState.PREPARING,
                SessionState.CONNECTED,
                SessionState.STOPPING,
            )) {
            if (activeGeneration == null || snapshot.generation > activeGeneration!!) {
                activeGeneration = snapshot.generation
                stopRequested = snapshot.state == SessionState.STOPPING
            }
            if (snapshot.generation != activeGeneration) return null
            return snapshot.state.toConnectionState()
        }
        if (snapshot.state in setOf(SessionState.IDLE, SessionState.CONFIGURED, SessionState.FAILED, SessionState.DESTROYED)) {
            activeGeneration = null
            stopRequested = false
            return snapshot.state.toConnectionState()
        }
        return null
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
