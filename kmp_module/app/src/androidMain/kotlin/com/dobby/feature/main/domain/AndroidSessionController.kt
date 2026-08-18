package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.UUID

/** Android gomobile transport for the versioned Go session API. */
internal class AndroidSessionController(
    private val context: Context,
    // Keep a self-contained default for owner-injected instrumentation callers;
    // production DI still supplies the shared callback repository.
    private val connectionState: ConnectionStateRepository = ConnectionStateRepository(),
) : SessionController {
    private val mutex = Mutex()
    private var sessionId: String? = null

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> =
        withSession { id ->
            SessionEnvelopeDecoder.decode(Dobbyvpn.configureSession(id, commandId(), rawConfig)) { it.toSessionConfiguration() }
        }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> = withSession { id ->
        DobbyVpnService.requestShell(context, id)
        if (!PlatformServiceRegistry.awaitReady(5_000)) {
            return@withSession SessionControllerResult.Failure(
                message = "ANDROID_PLATFORM_UNAVAILABLE",
                code = SessionFailureCode.PLATFORM_FAILED,
            )
        }
        val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
        SessionEnvelopeDecoder.decode(Dobbyvpn.startSession(id, commandId(), mode, index)) { it.sessionLong("generation").toULong() }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> = withSession { id ->
        SessionEnvelopeDecoder.decode(Dobbyvpn.stopSession(id, commandId(), generation.toLong())) { it.sessionLong("generation").toULong() }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = withSession { id ->
        SessionEnvelopeDecoder.decode(Dobbyvpn.snapshotSession(id)) { it.toSessionSnapshot() }
    }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> = withSession { id ->
        SessionEnvelopeDecoder.decode(Dobbyvpn.observeSession(id, afterSequence.toLong())) { it.toSessionObservation() }
    }

    /**
     * Replays the Go-owned ledger first, then follows the generation-correlated
     * Android service callback stream. The sequence filter closes the race
     * between the snapshot/ledger read and subscription without polling.
     */
    override fun watch(afterSequence: ULong): Flow<SessionEvent> = flow {
        val observed = observe(afterSequence)
        var cursor = afterSequence
        if (observed is SessionControllerResult.Success) {
            observed.value.events.forEach { event ->
                emit(event)
                if (event.sequence > cursor) cursor = event.sequence
            }
            if (observed.value.nextSequence > cursor) cursor = observed.value.nextSequence
        }
        val observedSession = mutex.withLock { sessionId }
        emitAll(
            connectionState.sessionEvents.filter { event ->
                event.sequence > cursor &&
                    (observedSession.isNullOrBlank() || event.sessionId.isBlank() || event.sessionId == observedSession)
            },
        )
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = mutex.withLock {
        val id = sessionId ?: return@withLock SessionControllerResult.Success(Unit)
        when (val result = SessionEnvelopeDecoder.decode(Dobbyvpn.destroySession(id)) { Unit }) {
            is SessionControllerResult.Success -> { sessionId = null; result }
            is SessionControllerResult.Failure -> result
        }
    }

    private suspend fun <T> withSession(operation: suspend (String) -> SessionControllerResult<T>): SessionControllerResult<T> = mutex.withLock {
        val id = sessionId ?: recoverOrCreate()
            ?: return@withLock SessionControllerResult.Failure("session creation failed")
        operation(id)
    }

    private fun recoverOrCreate(): String? {
        when (val recovered = SessionEnvelopeDecoder.decode(Dobbyvpn.recoverActiveSession()) { it.sessionString("session_id") }) {
            is SessionControllerResult.Success -> return recovered.value.also { sessionId = it }
            is SessionControllerResult.Failure -> if (recovered.code != SessionFailureCode.NOT_FOUND) return null
        }
        return when (val created = SessionEnvelopeDecoder.decode(Dobbyvpn.createSession()) { it.sessionString("session_id") }) {
            is SessionControllerResult.Success -> created.value.also { sessionId = it }
            is SessionControllerResult.Failure -> null
        }
    }

    private fun commandId(): String = UUID.randomUUID().toString()
}
