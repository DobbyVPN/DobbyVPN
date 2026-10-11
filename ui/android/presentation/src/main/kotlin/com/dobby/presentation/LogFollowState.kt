package com.dobby.presentation

class LogFollowState(initiallyFollowing: Boolean = true) {
    var followsLatest: Boolean = initiallyFollowing
        private set

    fun restore(following: Boolean) {
        followsLatest = following
    }

    /** Returns true when a rendered viewport at the bottom should catch up. */
    fun scrolled(canScrollDown: Boolean): Boolean {
        followsLatest = !canScrollDown
        return followsLatest
    }

    fun clear() {
        followsLatest = true
    }

    fun resume() {
        followsLatest = true
    }

    fun pause() {
        followsLatest = false
    }
}
