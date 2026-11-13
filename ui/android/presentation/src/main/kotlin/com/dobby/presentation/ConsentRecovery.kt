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
) {
    fun encode(): String = listOf(
        if (retry) "retry" else "start",
        index?.toString() ?: "auto",
        generation.toString(),
        URLEncoder.encode(sessionId, "UTF-8"),
        URLEncoder.encode(digest, "UTF-8"),
    ).joinToString(":")

    /** True while the backend still shows the session and configuration the request was made for. */
    fun stillCurrent(sessionId: String, digest: String, generation: Long): Boolean =
        this.sessionId == sessionId && this.digest == digest && this.generation == generation

    companion object {
        fun decode(encoded: String?): PendingConsent? {
            val parts = encoded?.split(':') ?: return null
            if (parts.size != 5) return null
            val retry = when (parts[0]) {
                "retry" -> true
                "start" -> false
                else -> return null
            }
            val index = if (parts[1] == "auto") null else parts[1].toIntOrNull() ?: return null
            val generation = parts[2].toLongOrNull() ?: return null
            return try {
                PendingConsent(
                    index, retry,
                    URLDecoder.decode(parts[3], "UTF-8"),
                    URLDecoder.decode(parts[4], "UTF-8"),
                    generation,
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

/** Holds a restored consent intent until the first consent result reaches the recreated Activity. */
class ConsentRecovery(private var restored: PendingConsent?) {
    /** The restored intent that no consent result has consumed yet; it must be saved again. */
    val pending: PendingConsent? get() = restored

    fun result(granted: Boolean): ConsentRecoveryOutcome {
        val intent = restored ?: return ConsentRecoveryOutcome.NotRestored
        restored = null
        return if (granted) ConsentRecoveryOutcome.Resume(intent) else ConsentRecoveryOutcome.Refused
    }
}
