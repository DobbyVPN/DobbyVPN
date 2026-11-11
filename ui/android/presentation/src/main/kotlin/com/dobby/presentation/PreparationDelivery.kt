package com.dobby.presentation

/** Cancellation is observable once, and late terminal/progress results are ignored. */
class PreparationDelivery<T>(private val canceledResult: T, private val callback: (T) -> Unit) {
    @Volatile var canceled = false
        private set
    private var terminal = false

    fun markCanceled() { canceled = true }
    fun progress(result: T) { if (!canceled && !terminal) callback(result) }
    fun finish(result: T) {
        if (terminal || canceled) return
        terminal = true
        callback(result)
    }
    fun finishCancellation() {
        if (terminal) return
        terminal = true
        callback(canceledResult)
    }
}
