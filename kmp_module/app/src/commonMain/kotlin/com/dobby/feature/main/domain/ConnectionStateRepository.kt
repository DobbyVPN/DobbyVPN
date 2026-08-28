package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    // Android platform callbacks are an optional wake/stream equivalent of the
    // desktop SessionV2 stream. iOS fetches its Go-owned ledger through its
    // authenticated provider bridge instead of publishing local events here.
    private val _sessionEvents = MutableSharedFlow<SessionEvent>(replay = 64, extraBufferCapacity = 64)
    val sessionEvents = _sessionEvents.asSharedFlow()
    // Go sequences restart with a new SessionV2 identity. Keep one cursor per
    // identity so a late event from an older session cannot suppress sequence 1
    // of the current one, and a new session cannot rewind the old cursor.
    private val lastSessionSequences = mutableMapOf<String, ULong>()

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun tryUpdateStatus(connectionState: VpnConnectionState) {
        _statusFlow.tryEmit(connectionState)
    }

    /**
     * Publishes a safe generation-correlated Android platform event. Go
     * supplies the authoritative sequence; older or duplicate callbacks are
     * ignored.
     */
    fun tryPublishSessionEvent(
        sessionId: String,
        generation: Long,
        sequence: Long,
        state: String,
        failureCode: String,
    ) {
        if (sessionId.isBlank() || generation < 0L || sequence <= 0L) return
        val candidate = sequence.toULong()
        val previous = lastSessionSequences[sessionId] ?: 0uL
        if (candidate <= previous) return
        lastSessionSequences[sessionId] = candidate
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
