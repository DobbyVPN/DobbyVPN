package com.dobby.presentation

/** The connection a user asked for while the Android VPN consent dialog was open. */
data class PendingConsent(val index: Int?, val retry: Boolean) {
    fun encode(): String = "${if (retry) "retry" else "start"}:${index ?: "auto"}"

    companion object {
        fun decode(encoded: String?): PendingConsent? {
            val parts = encoded?.split(':') ?: return null
            if (parts.size != 2) return null
            val retry = when (parts[0]) {
                "retry" -> true
                "start" -> false
                else -> return null
            }
            if (parts[1] == "auto") return PendingConsent(null, retry)
            return parts[1].toIntOrNull()?.let { PendingConsent(it, retry) }
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
