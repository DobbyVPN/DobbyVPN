package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import kotlin.time.TimeSource

internal expect object ProfileDnsResolver {
    fun resolveIpv4(host: String): String?
}

internal class ProfileServerResolver(
    private val logger: Logger,
) {
    private val cache = mutableMapOf<String, String>()

    fun resolveIpv4(host: String, context: String): String? {
        val trimmed = host.trim()
        if (trimmed.isEmpty()) return null
        if (trimmed.isIpv4Literal()) return trimmed

        cache[trimmed]?.let { return it }

        logger.log("[Profiles] Pre-resolving DNS for $context host=${maskStr(trimmed)}")
        val startedAt = TimeSource.Monotonic.markNow()
        val ip = runCatching { ProfileDnsResolver.resolveIpv4(trimmed) }
            .onFailure { e ->
                logger.log("[Profiles] DNS pre-resolve failed for $context host=${maskStr(trimmed)} error=${maskStr(e.message ?: "unknown")}")
            }
            .getOrNull()

        if (ip.isNullOrBlank()) {
            logger.log("[Profiles] DNS pre-resolve returned no IPv4 for $context host=${maskStr(trimmed)}")
            return null
        }

        val elapsedMs = startedAt.elapsedNow().inWholeMilliseconds
        logger.log("[Profiles] DNS pre-resolved $context host=${maskStr(trimmed)} -> ${maskStr(ip)} elapsedMs=$elapsedMs")
        cache[trimmed] = ip
        return ip
    }
}

internal fun String.isIpv4Literal(): Boolean {
    val parts = split(".")
    return parts.size == 4 && parts.all { part ->
        val octet = part.toIntOrNull()
        part.isNotEmpty() && part.all { it.isDigit() } && octet != null && octet in 0..255
    }
}
