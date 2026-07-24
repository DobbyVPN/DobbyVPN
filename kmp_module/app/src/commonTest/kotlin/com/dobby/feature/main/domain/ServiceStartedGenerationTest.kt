package com.dobby.feature.main.domain

import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class ServiceStartedGenerationTest {
    @Test
    fun stale_result_is_ignored_until_active_generation_completes() = runBlocking {
        val started = ServiceStarted()
        started.prepare(generation = 2)

        started.emit(started = true, generation = 1)
        started.emit(started = false, generation = 2)

        assertFalse(started.awaitResult(timeoutMs = 100, generation = 2))
    }

    @Test
    fun prepare_discards_delayed_result_from_previous_attempt() = runBlocking {
        val started = ServiceStarted()
        started.prepare(generation = 1)
        started.emit(started = true, generation = 1)

        started.prepare(generation = 2)

        assertFalse(started.awaitResult(timeoutMs = 10, generation = 2))
    }

    @Test
    fun active_generation_result_is_delivered() = runBlocking {
        val started = ServiceStarted()
        started.prepare(generation = 3)
        started.emit(started = true, generation = 3)

        assertTrue(started.awaitResult(timeoutMs = 100, generation = 3))
    }
}
