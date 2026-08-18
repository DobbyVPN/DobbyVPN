package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals

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
    fun zero_sequence_callbacks_get_a_local_monotonic_cursor() = runBlocking {
        val repository = ConnectionStateRepository()
        repository.tryPublishSessionEvent("ios", 1, 0, "PREPARING", "")
        repository.tryPublishSessionEvent("ios", 1, 0, "CONNECTED", "")

        val first = repository.sessionEvents.first()
        assertEquals(1uL, first.sequence)
        val second = repository.sessionEvents.first { it.sequence == 2uL }
        assertEquals(SessionState.CONNECTED, second.state)
    }
}
