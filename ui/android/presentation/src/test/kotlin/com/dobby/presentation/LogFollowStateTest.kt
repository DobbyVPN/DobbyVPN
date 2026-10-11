package com.dobby.presentation

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LogFollowStateTest {
    @Test
    fun scrollAwayPausesFollowingAndReturningToBottomResumesIt() {
        val state = LogFollowState()
        assertTrue(state.followsLatest)
        assertFalse(state.scrolled(canScrollDown = true))
        assertFalse(state.followsLatest)
        assertTrue(state.scrolled(canScrollDown = false))
        assertTrue(state.followsLatest)
    }

    @Test
    fun clearAndRestoreKeepViewportFollowingDecisionConsistent() {
        val state = LogFollowState()
        state.pause()
        state.clear()
        assertTrue(state.followsLatest)
        state.restore(following = false)
        assertFalse(state.followsLatest)
        state.resume()
        assertTrue(state.followsLatest)
    }
}
