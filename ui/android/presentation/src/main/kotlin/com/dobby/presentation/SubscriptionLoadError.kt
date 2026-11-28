package com.dobby.presentation

import java.net.URI
import java.net.URISyntaxException

/**
 * Builds the user-facing message for a failed subscription load without printing
 * the subscription link, its host, or its path and token. The error dialog's
 * Copy action copies this exact string, so redaction happens here.
 */
object SubscriptionLoadError {
    private const val LinkReference = "the subscription link"
    private const val HostReference = "the subscription server"

    fun compose(
        source: String,
        accepted: String,
        configured: Boolean,
        sameSource: Boolean,
        detail: String,
    ): String {
        val owner = if (configured && !sameSource && accepted.isNotEmpty()) {
            " Showing profiles from the previously accepted subscription."
        } else {
            ""
        }
        return "Could not load $LinkReference: ${scrub(detail, source, accepted)}.$owner"
    }

    private fun scrub(text: String, source: String, accepted: String): String {
        var scrubbed = text
        for (secret in listOf(source, accepted)) {
            if (secret.isEmpty()) continue
            scrubbed = scrubbed.replace(secret, LinkReference, ignoreCase = true)
        }
        for (secret in listOf(source, accepted)) {
            if (secret.isEmpty()) continue
            val host = hostOf(secret) ?: continue
            scrubbed = scrubbed.replace(host, HostReference, ignoreCase = true)
        }
        return scrubbed
    }

    private fun hostOf(secret: String): String? = try {
        URI(secret).host?.takeIf { it.isNotEmpty() }
    } catch (failure: URISyntaxException) {
        null
    }
}
