package com.dobby.presentation

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SubscriptionLoadCoordinatorTest {
    @Test
    fun typedEditStartsAt400Milliseconds() {
        val clock = TestClock()
        val started = mutableListOf<SubscriptionRequest>()
        val loads = clock.coordinator(started)

        loads.change("https://one.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(399)
        assertTrue(started.isEmpty())

        clock.advanceBy(1)
        assertEquals(listOf("https://one.example"), started.map { it.source })
    }

    @Test
    fun laterTypedEditResetsThe400MillisecondDeadline() {
        val clock = TestClock()
        val started = mutableListOf<SubscriptionRequest>()
        val loads = clock.coordinator(started)

        loads.change("https://old.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(250)
        loads.change("https://new.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(399)
        assertTrue(started.isEmpty())

        clock.advanceBy(1)
        assertEquals(listOf("https://new.example"), started.map { it.source })
    }

    @Test
    fun pasteImportAndRetryAreScheduledWithoutDebounce() {
        val clock = TestClock()
        val started = mutableListOf<SubscriptionRequest>()
        val loads = clock.coordinator(started)

        for ((source, trigger) in listOf(
            "https://paste.example" to LoadTrigger.PASTE,
            "https://import.example" to LoadTrigger.IMPORT,
            "https://retry.example" to LoadTrigger.RETRY,
        )) {
            loads.change(source, trigger, valid = true)
            clock.runCurrent()
            assertEquals(source, started.last().source)
            loads.complete(started.last()) { assertTrue(it) }
        }
        assertEquals(3, started.size)
    }

    @Test
    fun heldLoadStartsOnlyNewestQueuedEditWhenItCompletes() {
        val clock = TestClock()
        val started = mutableListOf<SubscriptionRequest>()
        val loads = clock.coordinator(started)

        loads.change("https://first.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(400)
        val first = started.single()
        loads.change("https://middle.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(400)
        loads.change("https://newest.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(400)
        assertEquals(listOf("https://first.example"), started.map { it.source })

        var firstWasCurrent: Boolean? = null
        loads.complete(first) { firstWasCurrent = it }

        assertEquals(false, firstWasCurrent)
        assertEquals(
            listOf("https://first.example", "https://newest.example"),
            started.map { it.source },
        )
        loads.complete(started.last()) { assertTrue(it) }
    }

    @Test
    fun pendingRequestWaitsForSessionAndAcceptedSourceInvalidatesOldTimer() {
        val clock = TestClock()
        val started = mutableListOf<SubscriptionRequest>()
        var sessionReady = false
        val loads = clock.coordinator(started) { sessionReady }

        loads.change("https://not-ready.example", LoadTrigger.TYPED, valid = true)
        clock.advanceBy(400)
        assertTrue(started.isEmpty())
        sessionReady = true
        loads.sessionBecameReady()
        assertEquals("https://not-ready.example", started.single().source)
        loads.complete(started.single()) { assertTrue(it) }

        loads.change("https://stale.example", LoadTrigger.TYPED, valid = true)
        loads.change(
            "https://accepted.example",
            LoadTrigger.TYPED,
            valid = true,
            alreadyAccepted = true,
        )
        clock.advanceBy(400)
        assertEquals(listOf("https://not-ready.example"), started.map { it.source })
        assertFalse(loads.hasWork)
    }

    private fun TestClock.coordinator(
        started: MutableList<SubscriptionRequest>,
        ready: () -> Boolean = { true },
    ) = SubscriptionLoadCoordinator(
        schedule = ::schedule,
        sessionReady = ready,
        start = started::add,
    )

    private class TestClock {
        private data class Task(
            val at: Long,
            val order: Long,
            val run: () -> Unit,
            var cancelled: Boolean = false,
        )

        private val tasks = mutableListOf<Task>()
        private var order = 0L
        var now = 0L
            private set

        fun schedule(delayMillis: Long, run: () -> Unit): () -> Unit {
            val task = Task(now + delayMillis, order++, run)
            tasks += task
            return { task.cancelled = true }
        }

        fun advanceBy(millis: Long) {
            val target = now + millis
            while (true) {
                val next = tasks.filter { !it.cancelled && it.at <= target }
                    .minWithOrNull(compareBy<Task> { it.at }.thenBy { it.order }) ?: break
                tasks.remove(next)
                now = next.at
                if (!next.cancelled) next.run()
            }
            now = target
        }

        fun runCurrent() = advanceBy(0)
    }
}
