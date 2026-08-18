package com.dobby.feature.main.domain

import interop.session.SessionConfiguration as GrpcConfiguration
import interop.session.SessionEvent as GrpcEvent
import interop.session.SessionLibrary
import interop.session.SessionObservation as GrpcObservation
import interop.session.SessionResult as GrpcResult
import interop.session.SessionSnapshot as GrpcSnapshot
import interop.session.SessionStartTarget as GrpcStartTarget
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.map
import java.util.UUID

/** JVM transport adapter. Session and command IDs never escape this platform boundary. */
internal class GrpcSessionController(
    private val sessionLibrary: SessionLibrary,
    private val identityStore: SessionIdentityStore? = null,
) : SessionController {
    private val sessionMutex = Mutex()
    private var sessionId: String? = null

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> =
        sessionMutex.withLock {
            val id = ensureSession() ?: return@withLock SessionControllerResult.Failure("session creation failed")
            val result = sessionLibrary.configure(id, commandId(), rawConfig).toController { it.toDomain() }
            // A persisted CLI identity can outlive a desktop service restart. Only Configure may
            // replace it: starts/stops/status must never accidentally create a second session.
            if (result.isMissingSession()) {
                forgetSession()
                val replacement = ensureSession() ?: return@withLock SessionControllerResult.Failure("session creation failed")
                sessionLibrary.configure(replacement, commandId(), rawConfig).toController { it.toDomain() }
            } else {
                result
            }
        }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> =
        withExistingSession { id -> sessionLibrary.start(id, commandId(), target.toGrpc()).toController { it } }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> =
        withExistingSession { id -> sessionLibrary.stop(id, commandId(), generation).toController { it } }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
        sessionMutex.withLock {
            val id = existingSessionId() ?: recoverSession()
            if (id == null) {
                SessionControllerResult.Success(SessionSnapshot(0uL, SessionState.IDLE, configured = false, cleanupComplete = true))
            } else {
                val result = sessionLibrary.snapshot(id).toController { it.toDomain() }
                if (result.isMissingSession()) {
                    forgetSession()
                    SessionControllerResult.Success(SessionSnapshot(0uL, SessionState.IDLE, configured = false, cleanupComplete = true))
                } else result
            }
        }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> =
        sessionMutex.withLock {
            val id = existingSessionId() ?: recoverSession()
                ?: return@withLock SessionControllerResult.Success(SessionObservation(emptyList(), afterSequence))
            val result = sessionLibrary.observe(id, afterSequence).toController { it.toDomain() }
            if (result.isMissingSession()) {
                forgetSession()
                SessionControllerResult.Success(SessionObservation(emptyList(), afterSequence))
            } else result
        }

    override fun watch(afterSequence: ULong): Flow<SessionEvent> = kotlinx.coroutines.flow.flow {
        val id = sessionMutex.withLock { existingSessionId() ?: recoverSession() } ?: return@flow
        emitAll(sessionLibrary.watch(id, afterSequence).map(GrpcEvent::toDomain))
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = sessionMutex.withLock {
        val id = existingSessionId() ?: recoverSession() ?: return@withLock SessionControllerResult.Success(Unit)
        when (val result = sessionLibrary.destroySession(id).toController { Unit }) {
            is SessionControllerResult.Success -> {
                forgetSession()
                result
            }
            is SessionControllerResult.Failure -> if (result.isMissingSession()) {
                forgetSession()
                SessionControllerResult.Success(Unit)
            } else result
        }
    }

    private suspend fun <T> withExistingSession(
        operation: suspend (String) -> SessionControllerResult<T>,
    ): SessionControllerResult<T> = sessionMutex.withLock {
        val id = existingSessionId() ?: recoverSession()
            ?: return@withLock SessionControllerResult.Failure("session is not configured")
        val result = operation(id)
        if (result.isMissingSession()) forgetSession()
        result
    }

    private suspend fun ensureSession(): String? {
        existingSessionId()?.let { return it }
        recoverSession()?.let { return it }
        return when (val created = sessionLibrary.createSession().toController { it }) {
            is SessionControllerResult.Success -> created.value.also(::rememberSession)
            is SessionControllerResult.Failure -> null
        }
    }

    private suspend fun recoverSession(): String? = when (val recovered = sessionLibrary.recoverActiveSession().toController { it }) {
        is SessionControllerResult.Success -> recovered.value.also(::rememberSession)
        is SessionControllerResult.Failure -> null
    }

    private fun existingSessionId(): String? {
        sessionId?.let { return it }
        return identityStore?.load()?.also { sessionId = it }
    }

    private fun rememberSession(id: String) {
        sessionId = id
        identityStore?.save(id)
    }

    private fun forgetSession() {
        sessionId = null
        identityStore?.clear()
    }

    private fun commandId(): String = UUID.randomUUID().toString()
}

private fun SessionStartTarget.toGrpc(): GrpcStartTarget = when (this) {
    SessionStartTarget.AutoSelect -> GrpcStartTarget.AutoSelect
    is SessionStartTarget.ProfileIndex -> GrpcStartTarget.ProfileIndex(index)
}

private fun GrpcConfiguration.toDomain() = SessionConfiguration(
    digest = digest,
    profiles = profiles.map { SessionProfile(it.index, mapProtocol(it.protocol), it.description) },
    warnings = warnings.map { SessionWarning(it.code, it.message) },
)

private fun GrpcSnapshot.toDomain() = SessionSnapshot(
    generation = generation,
    state = state.toDomain(),
    configured = configured,
    cleanupComplete = cleanupComplete,
    lastFailureCode = lastFailure?.code?.name?.toSessionFailureCode(),
)

private fun GrpcObservation.toDomain() = SessionObservation(
    events = events.map(GrpcEvent::toDomain),
    nextSequence = nextSequence,
)

private fun GrpcEvent.toDomain() = SessionEvent(
    generation = generation,
    sequence = sequence,
    state = state.toDomain(),
    failureCode = failure?.code?.name?.toSessionFailureCode(),
    sessionId = sessionId,
)

private fun mapProtocol(protocol: interop.session.SessionProtocol) = when (protocol) {
    interop.session.SessionProtocol.UNSPECIFIED -> SessionProtocol.UNSPECIFIED
    interop.session.SessionProtocol.OUTLINE -> SessionProtocol.OUTLINE
    interop.session.SessionProtocol.XRAY -> SessionProtocol.XRAY
    interop.session.SessionProtocol.TRUST_TUNNEL -> SessionProtocol.TRUST_TUNNEL
    interop.session.SessionProtocol.UNKNOWN -> SessionProtocol.UNKNOWN
}

private fun interop.session.SessionState.toDomain() = when (this) {
    interop.session.SessionState.UNSPECIFIED -> SessionState.UNSPECIFIED
    interop.session.SessionState.IDLE -> SessionState.IDLE
    interop.session.SessionState.CONFIGURED -> SessionState.CONFIGURED
    interop.session.SessionState.PROBING -> SessionState.PROBING
    interop.session.SessionState.PREPARING -> SessionState.PREPARING
    interop.session.SessionState.CONNECTED -> SessionState.CONNECTED
    interop.session.SessionState.STOPPING -> SessionState.STOPPING
    interop.session.SessionState.FAILED -> SessionState.FAILED
    interop.session.SessionState.DESTROYED -> SessionState.DESTROYED
    interop.session.SessionState.UNKNOWN -> SessionState.UNKNOWN
}

private fun <T, R> GrpcResult<T>.toController(transform: (T) -> R): SessionControllerResult<R> = when (this) {
    is GrpcResult.Success -> SessionControllerResult.Success(transform(value))
    is GrpcResult.Failure -> SessionControllerResult.Failure(
        message = failure.message,
        code = failure.code.name.toSessionFailureCode(),
    )
}

private fun SessionControllerResult<*>.isMissingSession(): Boolean =
    this is SessionControllerResult.Failure && code == SessionFailureCode.NOT_FOUND

/** Optional platform-owned session identity storage. UI callers intentionally leave it null. */
internal interface SessionIdentityStore {
    fun load(): String?
    fun save(sessionId: String)
    fun clear()
}
