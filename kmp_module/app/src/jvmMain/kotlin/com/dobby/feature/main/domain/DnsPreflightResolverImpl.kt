package com.dobby.feature.main.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import interop.dnscache.DnsCacheLibrary
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withTimeoutOrNull
import java.net.Inet4Address
import java.net.InetAddress

class DnsPreflightResolverImpl(
    private val dnsCacheLibrary: DnsCacheLibrary,
    private val logger: Logger,
) : DnsPreflightResolver {
    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        dnsCacheLibrary.ClearDNSCache()

        val hosts = collectDnsPreflightHosts(profiles)
            .plus(HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        if (hosts.isEmpty()) {
            logger.log("[DNSPreflight] No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        logger.log(
            "[DNSPreflight] Start hosts=${hosts.size} profiles=${profiles.size} " +
                "network=desktop/default sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )

        val resolved = coroutineScope {
            hosts.map { host ->
                async(Dispatchers.IO) {
                    resolveHost(host)
                }
            }.awaitAll().filterNotNull()
        }

        val entries = resolved.joinToString("\n") { "${it.host}=${it.ip}" }
        val cachedCount = dnsCacheLibrary.SetDNSCacheEntries(entries, DNS_PREFLIGHT_SOURCE)
        val failedCount = hosts.size - resolved.size
        logger.log(
            "[DNSPreflight] Finished hosts=${hosts.size} resolved=${resolved.size} " +
                "cached=$cachedCount failed=$failedCount"
        )
    }

    private suspend fun resolveHost(host: String): ResolvedHost? {
        val addresses = withTimeoutOrNull(DNS_PREFLIGHT_HOST_TIMEOUT_MS) {
            runCatching {
                InetAddress.getAllByName(host).toList()
            }.getOrElse { error ->
                logger.log("[DNSPreflight] Failed host=${maskStr(host)} error=${error.message}")
                return@withTimeoutOrNull emptyList()
            }
        } ?: run {
            logger.log("[DNSPreflight] Timeout host=${maskStr(host)} timeoutMs=$DNS_PREFLIGHT_HOST_TIMEOUT_MS")
            return null
        }

        val ipv4 = addresses.firstOrNull { it is Inet4Address }?.hostAddress
        if (ipv4 == null) {
            logger.log("[DNSPreflight] No IPv4 host=${maskStr(host)} addresses=${addresses.size}")
            return null
        }
        logger.log("[DNSPreflight] Resolved host=${maskStr(host)} ip=$ipv4")
        return ResolvedHost(host = host, ip = ipv4)
    }

    private fun isLocalOrIpLiteral(host: String): Boolean {
        if (host == "localhost" || host == "127.0.0.1" || host == "::1") return true
        if (ipv4Literal.matches(host)) return true
        return host.contains(":")
    }

    private data class ResolvedHost(val host: String, val ip: String)

    private companion object {
        const val DNS_PREFLIGHT_HOST_TIMEOUT_MS = 2_000L
        const val DNS_PREFLIGHT_SOURCE = "desktop-preflight"
        val ipv4Literal = Regex("""^\d{1,3}(\.\d{1,3}){3}$""")
        val HEALTHCHECK_PREWARM_HOSTS = listOf(
            "google.com",
            "one.one.one.one",
            "www.google.com",
            "www.cloudflare.com",
            "about.google",
            "api.ipify.org",
        )
    }
}
