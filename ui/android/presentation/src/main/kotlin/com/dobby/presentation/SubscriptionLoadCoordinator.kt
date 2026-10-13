package com.dobby.presentation

enum class LoadTrigger(val delayMillis: Long) {
    TYPED(400),
    PASTE(0),
    IMPORT(0),
    RETRY(0),
    RESTORE(0),
}

data class SubscriptionRequest(val source: String, val revision: Long)

/**
 * Owns the UI's debounce and serial latest-request queue. The platform supplies
 * its timer and starts the returned request through the existing Go boundary.
 */
class SubscriptionLoadCoordinator(
    private val schedule: (Long, () -> Unit) -> (() -> Unit),
    private val sessionReady: () -> Boolean,
    private val start: (SubscriptionRequest) -> Unit,
) {
    private data class Timer(val request: SubscriptionRequest, var cancel: () -> Unit = {})

    @Volatile private var revisionValue = 0L
    private var timer: Timer? = null
    private var queued: SubscriptionRequest? = null
    private var active: SubscriptionRequest? = null
    private var acceptedSource: String? = null

    val revision: Long get() = revisionValue
    val inFlight: Boolean get() = active != null
    val hasWork: Boolean get() = timer != null || queued != null || active != null

    fun change(
        source: String,
        trigger: LoadTrigger,
        valid: Boolean,
        alreadyAccepted: Boolean = false,
    ): Long {
        revisionValue++
        timer?.cancel?.invoke()
        timer = null
        queued = null
        if (valid && !alreadyAccepted) {
            val request = SubscriptionRequest(source, revisionValue)
            val nextTimer = Timer(request)
            timer = nextTimer
            nextTimer.cancel = schedule(trigger.delayMillis) { timerElapsed(nextTimer) }
        }
        return revisionValue
    }

    fun hasOutstanding(source: String): Boolean =
        timer?.request?.source == source || queued?.source == source || active?.source == source

    /** A current successful configure can be newer than the next backend snapshot. */
    fun isAccepted(source: String): Boolean = acceptedSource == source

    /** Clear the short-lived success record once a current backend snapshot is observed. */
    fun snapshotObserved() {
        acceptedSource = null
    }

    /** Start a request left queued while the backend session was unavailable. */
    fun sessionBecameReady() = startQueued()

    fun close() {
        timer?.cancel?.invoke()
        timer = null
        queued = null
    }

    private fun timerElapsed(expected: Timer) {
        if (timer !== expected) return
        timer = null
        if (expected.request.revision == revisionValue) queued = expected.request
        startQueued()
    }

    private fun startQueued() {
        if (active != null || !sessionReady()) return
        val next = queued ?: return
        queued = null
        active = next
        start(next)
    }

    fun complete(
        request: SubscriptionRequest,
        succeeded: Boolean = false,
        completed: (current: Boolean) -> Unit,
    ) {
        check(active == request) { "Completed request does not own the active subscription load" }
        active = null
        val current = request.revision == revisionValue
        if (current && succeeded) acceptedSource = request.source
        completed(current)
        startQueued()
    }
}
