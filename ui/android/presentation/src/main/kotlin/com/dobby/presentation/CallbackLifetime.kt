package com.dobby.presentation

/** Drops both late completions and work already queued when the owner closes. */
class CallbackLifetime(private val enqueue: (() -> Unit) -> Unit) {
    @Volatile var isClosed = false
        private set

    fun post(action: () -> Unit) {
        if (!isClosed) enqueue { if (!isClosed) action() }
    }

    fun close() { isClosed = true }
}
