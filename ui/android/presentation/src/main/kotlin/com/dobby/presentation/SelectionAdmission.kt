package com.dobby.presentation

/** Serializes source changes and close with the one-shot admission of a selection. */
class SelectionAdmission<T : Any> {
    private val lock = Any()
    private var closed = false
    private var revision = 0L
    private var pending: T? = null

    fun sourceChanged(change: () -> Long): Long = synchronized(lock) {
        if (closed) return@synchronized revision
        pending = null
        change().also { revision = it }
    }

    fun register(target: T, sourceRevision: Long): Boolean = synchronized(lock) {
        if (closed || revision != sourceRevision) return@synchronized false
        pending = target
        true
    }

    fun claim(target: T, sourceRevision: Long): Boolean = synchronized(lock) {
        if (closed || revision != sourceRevision || pending !== target) return@synchronized false
        pending = null
        true
    }

    fun invalidate(target: T? = null) = synchronized(lock) {
        if (target == null || pending === target) pending = null
    }

    fun close(invalidateLifetime: () -> Unit) = synchronized(lock) {
        closed = true
        pending = null
        invalidateLifetime()
    }
}
