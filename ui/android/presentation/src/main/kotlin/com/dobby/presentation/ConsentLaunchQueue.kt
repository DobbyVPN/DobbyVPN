package com.dobby.presentation

/** Main-thread owner of the single Android activity-result consent slot. */
class ConsentLaunchQueue<T>(
    private val launchOrComplete: (T) -> Boolean,
    private val returned: (T, Boolean) -> Unit,
    private val failed: (T, Throwable) -> Unit,
    private val discarded: (T, Boolean) -> Unit = { _, _ -> },
) {
    private data class Entry<T>(val value: T, var canceled: Boolean = false)
    private val waiting = ArrayDeque<Entry<T>>()
    private var active: Entry<T>? = null
    private var draining = false

    fun submit(value: T) {
        waiting.addLast(Entry(value))
        drain()
    }

    fun cancel(value: T) {
        waiting.removeAll { it.value === value }
        active?.takeIf { it.value === value }?.canceled = true
    }

    fun result(granted: Boolean) {
        val entry = active ?: return
        active = null
        if (!entry.canceled) returned(entry.value, granted) else discarded(entry.value, granted)
        drain()
    }

    private fun drain() {
        if (draining) return
        draining = true
        try {
            while (active == null && waiting.isNotEmpty()) {
                val entry = waiting.removeFirst()
                active = entry
                try {
                    if (!launchOrComplete(entry.value)) active = null
                } catch (error: Throwable) {
                    active = null
                    if (!entry.canceled) failed(entry.value, error)
                }
            }
        } finally {
            draining = false
        }
    }
}
