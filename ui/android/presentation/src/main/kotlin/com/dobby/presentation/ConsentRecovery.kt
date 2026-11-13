package com.dobby.presentation

import java.net.URLDecoder
import java.net.URLEncoder

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
    fun encode(): String = listOf(
        if (retry) "retry" else "start",
        index?.toString() ?: "auto",
        generation.toString(),
        URLEncoder.encode(sessionId, "UTF-8"),
        URLEncoder.encode(digest, "UTF-8"),
        if (granted) "granted" else "waiting",
    ).joinToString(":")

    /** True while the backend still shows the session and configuration the request was made for. */
    fun stillCurrent(sessionId: String, digest: String, generation: Long): Boolean =
        this.sessionId == sessionId && this.digest == digest && this.generation == generation

    companion object {
        fun decode(encoded: String?): PendingConsent? {
            val parts = encoded?.split(':') ?: return null
            if (parts.size != 6) return null
            val retry = when (parts[0]) {
                "retry" -> true
                "start" -> false
                else -> return null
            }
            val index = if (parts[1] == "auto") null else parts[1].toIntOrNull() ?: return null
            val generation = parts[2].toLongOrNull() ?: return null
            val granted = when (parts[5]) {
                "granted" -> true
                "waiting" -> false
                else -> return null
            }
            return try {
                PendingConsent(
                    index, retry,
                    URLDecoder.decode(parts[3], "UTF-8"),
                    URLDecoder.decode(parts[4], "UTF-8"),
                    generation,
                    granted,
                )
            } catch (_: IllegalArgumentException) {
                null
            }
        }
    }
}

/** Consent outcome for an intent that was saved by a previous instance of the Activity. */
sealed class ConsentRecoveryOutcome {
    object NotRestored : ConsentRecoveryOutcome()
    data class Resume(val intent: PendingConsent) : ConsentRecoveryOutcome()
    object Refused : ConsentRecoveryOutcome()
}

/**
 * Holds a restored consent intent until its connection has been handed off. An allowed result
 * keeps the intent (marked granted) until [handoffComplete], so a recreation in between still
 * saves it and the next Activity resumes it.
 */
class ConsentRecovery(private var restored: PendingConsent?) {
    /** The intent that must be saved again by the next state save. */
    val pending: PendingConsent? get() = restored

    /** An allowed result that a previous Activity received but did not finish handing off. */
    fun interruptedHandoff(): PendingConsent? = restored?.takeIf { it.granted }

    fun result(granted: Boolean): ConsentRecoveryOutcome {
        val intent = restored?.takeIf { !it.granted } ?: return ConsentRecoveryOutcome.NotRestored
        if (!granted) {
            restored = null
            return ConsentRecoveryOutcome.Refused
        }
        restored = intent.copy(granted = true)
        return ConsentRecoveryOutcome.Resume(intent)
    }

    fun handoffComplete() {
        if (restored?.granted == true) restored = null
    }
}
