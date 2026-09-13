package com.dobby.feature.main.presentation

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SessionRevisionGateTest {
    @Test
    fun rejectsWatchSnapshotsOlderThanAcceptedCommandRevision() {
        val gate = SessionRevisionGate()

        assertTrue(gate.accept("session-a", 4uL))
        gate.advance("session-a", 6uL)

        assertFalse(gate.accept("session-a", 5uL))
        assertTrue(gate.accept("session-a", 6uL))
        assertTrue(gate.accept("session-a", 7uL))
    }

    @Test
    fun aNewSessionStartsItsRevisionSequenceAgain() {
        val gate = SessionRevisionGate()
        gate.advance("session-a", 12uL)

        assertTrue(gate.accept("session-b", 1uL))
        assertFalse(gate.accept("session-b", 0uL))
    }
}
