package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ConnectionStateRepositoryEventTest {
    @Test
    fun platform_callbacks_are_replayed_and_duplicate_sequences_are_ignored() = runBlocking {
        val repository = ConnectionStateRepository()
        repository.tryPublishSessionEvent("android-session", 4, 12, "CONNECTED", "")
        repository.tryPublishSessionEvent("android-session", 4, 12, "FAILED", "RUNTIME_FAILED")

        val event = repository.sessionEvents.first()
        assertEquals("android-session", event.sessionId)
        assertEquals(4uL, event.generation)
        assertEquals(12uL, event.sequence)
        assertEquals(SessionState.CONNECTED, event.state)
    }

    @Test
    fun zero_sequence_callbacks_are_rejected_without_synthesis() = runBlocking {
        val repository = ConnectionStateRepository()
        repository.tryPublishSessionEvent("ios", 1, 0, "PREPARING", "")
        repository.tryPublishSessionEvent("ios", 1, 0, "CONNECTED", "")

        assertTrue(repository.sessionEvents.replayCache.isEmpty())
    }

    @Test
    fun sequence_cursors_are_scoped_to_the_go_session_identity() = runBlocking {
        val repository = ConnectionStateRepository()
        repository.tryPublishSessionEvent("old-session", 4, 12, "CONNECTED", "")
        repository.tryPublishSessionEvent("new-session", 1, 1, "PROBING", "")
        repository.tryPublishSessionEvent("old-session", 4, 11, "FAILED", "")

        assertEquals(listOf("old-session", "new-session"), repository.sessionEvents.replayCache.map { it.sessionId })
        assertEquals(1uL, repository.sessionEvents.replayCache.last().sequence)
    }
}
