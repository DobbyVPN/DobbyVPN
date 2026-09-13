package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals

class ConnectionStateRepositoryEventTest {
    @Test
    fun platformCallbacksCoalesceToAContentFreeWakeHint() = runBlocking {
        val repository = ConnectionStateRepository()
        repository.publishSessionChanged()
        repository.publishSessionChanged()

        assertEquals(Unit, repository.sessionChanges.first())
        assertEquals(1, repository.sessionChanges.replayCache.size)
    }
}
