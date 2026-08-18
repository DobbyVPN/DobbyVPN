package com.dobby.feature.main.domain

import interop.session.SessionCapabilities as TransportCapabilities
import interop.session.SessionConfiguration as TransportConfiguration
import interop.session.SessionFailure
import interop.session.SessionFailureCode as TransportFailureCode
import interop.session.SessionEvent as TransportEvent
import interop.session.SessionLibrary
import interop.session.SessionObservation as TransportObservation
import interop.session.SessionProfile as TransportProfile
import interop.session.SessionProtocol as TransportProtocol
import interop.session.SessionResult
import interop.session.SessionSnapshot as TransportSnapshot
import interop.session.SessionStartTarget as TransportStartTarget
import interop.session.SessionState as TransportState
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

class GrpcSessionControllerIdentityTest {
    @Test
    fun aSecondControllerUsesThePersistedCliSession() = runBlocking {
        val store = MemoryStore()
        val library = RecordingSessionLibrary()
        val first = GrpcSessionController(library, store)

        assertIs<SessionControllerResult.Success<SessionConfiguration>>(first.configure(byteArrayOf(7)))
        assertEquals("session-1", store.value)

        val second = GrpcSessionController(library, store)
        assertIs<SessionControllerResult.Success<SessionSnapshot>>(second.snapshot())
        assertEquals("session-1", library.snapshotSessionId)
        assertIs<SessionControllerResult.Success<ULong>>(second.stop(7uL))
        assertEquals("session-1", library.stopSessionId)
        assertEquals(1, library.createCalls)
    }

    @Test
    fun configureReplacesAStalePersistedIdentity() = runBlocking {
        val store = MemoryStore("stale")
        val library = RecordingSessionLibrary()

        assertIs<SessionControllerResult.Success<SessionConfiguration>>(
            GrpcSessionController(library, store).configure(byteArrayOf(1)),
        )

        assertEquals("session-1", store.value)
        assertEquals(1, library.createCalls)
    }

    @Test
    fun snapshotAndEventsPreserveTypedFailureCodesOnly() = runBlocking {
        val library = RecordingSessionLibrary().apply {
            terminalFailure = SessionFailure(TransportFailureCode.RUNTIME_FAILED, "credential-value")
        }
        val controller = GrpcSessionController(library, MemoryStore())
        assertIs<SessionControllerResult.Success<SessionConfiguration>>(controller.configure(byteArrayOf(1)))

        val snapshot = assertIs<SessionControllerResult.Success<SessionSnapshot>>(controller.snapshot()).value
        assertEquals(SessionState.FAILED, snapshot.state)
        assertEquals(SessionFailureCode.RUNTIME_FAILED, snapshot.lastFailureCode)

        val event = assertIs<SessionControllerResult.Success<SessionObservation>>(controller.observe(0uL))
            .value.events.single()
        assertEquals(SessionState.FAILED, event.state)
        assertEquals(SessionFailureCode.RUNTIME_FAILED, event.failureCode)
    }
}

private class MemoryStore(var value: String? = null) : SessionIdentityStore {
    override fun load(): String? = value
    override fun save(sessionId: String) { value = sessionId }
    override fun clear() { value = null }
}

private class RecordingSessionLibrary : SessionLibrary {
    var createCalls = 0
    var snapshotSessionId: String? = null
    var stopSessionId: String? = null
    var terminalFailure: SessionFailure? = null
    private val sessions = mutableSetOf<String>()

    override suspend fun getCapabilities(): SessionResult<TransportCapabilities> =
        SessionResult.Success(TransportCapabilities("sessionapi/v2", emptyList(), emptyList()))

    override suspend fun createSession(): SessionResult<String> {
        createCalls += 1
        return SessionResult.Success("session-$createCalls".also(sessions::add))
    }

    override suspend fun configure(sessionId: String, commandId: String, rawConfig: ByteArray): SessionResult<TransportConfiguration> =
        if (sessionId !in sessions) missing() else SessionResult.Success(
            TransportConfiguration("digest", listOf(TransportProfile(0, TransportProtocol.OUTLINE, "")), emptyList()),
        )

    override suspend fun start(sessionId: String, commandId: String, target: TransportStartTarget): SessionResult<ULong> =
        if (sessionId !in sessions) missing() else SessionResult.Success(7uL)

    override suspend fun stop(sessionId: String, commandId: String, generation: ULong): SessionResult<ULong> {
        stopSessionId = sessionId
        return if (sessionId !in sessions) missing() else SessionResult.Success(generation)
    }

    override suspend fun snapshot(sessionId: String): SessionResult<TransportSnapshot> {
        snapshotSessionId = sessionId
        return if (sessionId !in sessions) missing() else SessionResult.Success(
            TransportSnapshot(
                sessionId,
                7uL,
                if (terminalFailure == null) TransportState.CONNECTED else TransportState.FAILED,
                true,
                null,
                terminalFailure,
                false,
            ),
        )
    }

    override suspend fun observe(sessionId: String, afterSequence: ULong): SessionResult<TransportObservation> =
        if (sessionId !in sessions) {
            missing()
        } else {
            SessionResult.Success(
                TransportObservation(
                    events = terminalFailure?.let {
                        listOf(TransportEvent(sessionId, 7uL, 1uL, TransportState.FAILED, null, it, null))
                    }.orEmpty(),
                    nextSequence = if (terminalFailure == null) afterSequence else 1uL,
                ),
            )
        }

    override suspend fun destroySession(sessionId: String): SessionResult<Unit> =
        if (sessionId !in sessions) missing() else SessionResult.Success(Unit)

    private fun <T> missing(): SessionResult<T> = SessionResult.Failure(
        SessionFailure(TransportFailureCode.NOT_FOUND, "session does not exist"),
    )
}
