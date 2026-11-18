package com.dobby.presentation

/**
 * The connection a user asked for while the Android VPN consent dialog was open, with the
 * session and configuration it was requested against.
 */
data class PendingConsent(
    val index: Int?,
    val retry: Boolean,
    val sessionId: String,
    val digest: String,
    val generation: Long,
    /** Consent was allowed, but the connection has not been handed off yet. */
    val granted: Boolean = false,
) {
    /** True while the backend still shows the session and configuration the request was made for. */
    fun stillCurrent(sessionId: String, digest: String, generation: Long): Boolean =
        this.sessionId == sessionId && this.digest == digest && this.generation == generation
}

/**
 * Carries a consent intent across Activity recreation. An allowed result keeps the intent
 * (marked granted) until [handoffComplete], so a recreation in between still saves it and the
 * next Activity resumes it; [cancel]-style handoff completion also stops a resume already queued.
 *
 * [enqueue] runs the task later on the main thread, after a fresh backend snapshot.
 */
class ConsentRecovery(
    restored: PendingConsent?,
    private val enqueue: (Runnable) -> Unit,
    private val resume: (PendingConsent) -> Unit,
    private val refuse: () -> Unit,
) {
    // Written by the controller's worker and main threads, read by the state save.
    @Volatile private var restored: PendingConsent? = restored

    // The system consent dialog was launched and has not answered yet.
    @Volatile private var dialogOpen = false

    fun dialogLaunched() { dialogOpen = true }
    fun dialogClosed() { dialogOpen = false }

    /** A live request as saved: awaiting the open dialog, or granted (a restore re-runs the permission check). */
    fun save(intent: PendingConsent): PendingConsent = intent.copy(granted = !dialogOpen)

    /** The intent that must be saved again by the next state save. */
    val pending: PendingConsent? get() = restored

    /** Resumes an allowed result that a previous Activity received but did not finish handing off. */
    fun resumeInterrupted() {
        restored?.takeIf { it.granted }?.let(::schedule)
    }

    /** Consent result for a restored intent (an allowed one for an already granted intent changes nothing); a result without one belongs to the live request. */
    fun result(granted: Boolean) {
        if (granted) dialogOpen = false
        val intent = restored ?: return
        // A refusal counts even for an intent saved as granted: its dialog may have opened after the save.
        if (!granted) {
            restored = null
            refuse()
            return
        }
        if (intent.granted) return
        restored = intent.copy(granted = true)
        schedule(intent)
    }

    /** The user cancelled the request (source change, stop): forget it, allowed or not yet answered. */
    fun cancel() { restored = null }

    /** The live request got its consent and leaves the controller's target: keep it saved until the handoff. */
    fun hold(intent: PendingConsent) { restored = intent.copy(granted = true) }

    fun handoffComplete() {
        if (restored?.granted == true) restored = null
    }

    private fun schedule(intent: PendingConsent) {
        enqueue(Runnable { if (restored?.granted == true) resume(intent) })
    }
}
