package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    // Platform callbacks are the mobile equivalent of the desktop SessionV2
    // stream. Keep a bounded replay so a UI process can reattach after it was
    // recreated without starting a polling loop or losing a terminal event.
    private val _sessionEvents = MutableSharedFlow<SessionEvent>(replay = 64, extraBufferCapacity = 64)
    val sessionEvents = _sessionEvents.asSharedFlow()
    private var lastSessionSequence = 0uL

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun tryUpdateStatus(connectionState: VpnConnectionState) {
        _statusFlow.tryEmit(connectionState)
    }

    /**
     * Publishes a safe generation-correlated platform event. Go supplies its
     * authoritative sequence; NetworkExtension uses sequence 0 and receives a
     * local monotonic sequence. Older or duplicate callbacks are ignored.
     */
    fun tryPublishSessionEvent(
        sessionId: String,
        generation: Long,
        sequence: Long,
        state: String,
        failureCode: String,
    ) {
        if (generation < 0L) return
        val candidate = if (sequence > 0L) sequence.toULong() else lastSessionSequence + 1uL
        if (candidate <= lastSessionSequence) return
        lastSessionSequence = candidate
        _sessionEvents.tryEmit(
            SessionEvent(
                generation = generation.toULong(),
                sequence = candidate,
                state = state.toSessionState(),
                failureCode = failureCode.takeIf(String::isNotBlank)?.toSessionFailureCode(),
                sessionId = sessionId,
            ),
        )
    }
}
