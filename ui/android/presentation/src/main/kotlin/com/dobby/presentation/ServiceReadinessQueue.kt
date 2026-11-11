package com.dobby.presentation

/** Main-thread queue waiting for one service owner to enter foreground. */
class ServiceReadinessQueue<T, S : Any>(
    private val ready: () -> Boolean,
    private val retiring: () -> Boolean,
    private val start: () -> Unit,
    private val complete: (T, Throwable?) -> Unit,
) {
    private val waiting = mutableListOf<T>()
    private var owner: S? = null
    private var startRequested = false

    fun submit(value: T) { waiting.add(value); advance() }
    fun cancel(value: T) { waiting.removeAll { it === value } }
    fun created(candidate: S): Boolean {
        if (owner != null && owner !== candidate) return false
        owner = candidate
        return true
    }
    fun ready(candidate: S) {
        if (owner !== candidate || !ready()) return
        startRequested = false
        advance()
    }
    fun failed(candidate: S, error: Throwable) {
        if (owner !== candidate) return
        startRequested = false
        failWaiting(error)
    }
    fun detached(candidate: S) {
        if (owner !== candidate) return
        owner = null
        startRequested = false
        advance()
    }
    private fun failWaiting(error: Throwable) {
        val failed = waiting.toList()
        waiting.clear()
        failed.forEach { complete(it, error) }
    }
    private fun advance() {
        if (waiting.isEmpty()) return
        if (ready()) {
            val completed = waiting.toList()
            waiting.clear()
            completed.forEach { complete(it, null) }
        } else if (!retiring() && !startRequested) {
            startRequested = true
            try { start() } catch (error: Throwable) {
                startRequested = false
                failWaiting(error)
            }
        }
    }
}
