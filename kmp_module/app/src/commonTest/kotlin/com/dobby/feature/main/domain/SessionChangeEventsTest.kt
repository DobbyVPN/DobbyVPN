package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals

class SessionChangeEventsTest {
    @Test
    fun platformCallbacksCoalesceToAContentFreeWakeHint() = runBlocking {
        val events = SessionChangeEvents()
        events.publishSessionChanged()
        events.publishSessionChanged()

        assertEquals(Unit, events.sessionChanges.first())
        assertEquals(1, events.sessionChanges.replayCache.size)
    }
}
