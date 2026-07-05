package com.dobby.feature.main.domain

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withTimeoutOrNull
import java.net.Inet4Address
import java.net.InetAddress

class DnsPreflightResolverImpl(
    context: Context,
    private val logger: Logger,
) : DnsPreflightResolver {
    private val connectivityManager =
        context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        GoBackendWrapper.clearDNSCache()

        val hosts = collectDnsPreflightHosts(profiles)
            .plus(HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        if (hosts.isEmpty()) {
            logger.log("[DNSPreflight] No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        val network = selectPhysicalNetwork()
        logger.log(
            "[DNSPreflight] Start hosts=${hosts.size} profiles=${profiles.size} " +
                "network=${network?.toString() ?: "active/default"} sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )

        val resolved = coroutineScope {
            hosts.map { host ->
                async(Dispatchers.IO) {
                    resolveHost(network, host)
                }
            }.awaitAll().filterNotNull()
        }

        val entries = resolved.joinToString("\n") { "${it.host}=${it.ip}" }
        val cachedCount = GoBackendWrapper.setDNSCacheEntries(entries)
        val failedCount = hosts.size - resolved.size
        logger.log(
            "[DNSPreflight] Finished hosts=${hosts.size} resolved=${resolved.size} " +
                "cached=$cachedCount failed=$failedCount"
        )
    }

    private suspend fun resolveHost(network: Network?, host: String): ResolvedHost? {
        val addresses = withTimeoutOrNull(DNS_PREFLIGHT_HOST_TIMEOUT_MS) {
            runCatching {
                val raw = network?.getAllByName(host) ?: InetAddress.getAllByName(host)
                raw.toList()
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

    private fun selectPhysicalNetwork(): Network? {
        val physical = connectivityManager.allNetworks.firstOrNull { network ->
            val caps = connectivityManager.getNetworkCapabilities(network) ?: return@firstOrNull false
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
                !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
        }
        if (physical == null) {
            logger.log("[DNSPreflight] Physical network not found, using active/default resolver")
        }
        return physical ?: connectivityManager.activeNetwork
    }

    private fun isLocalOrIpLiteral(host: String): Boolean {
        if (host == "localhost" || host == "127.0.0.1" || host == "::1") return true
        if (ipv4Literal.matches(host)) return true
        return host.contains(":")
    }

    private data class ResolvedHost(val host: String, val ip: String)

    private companion object {
        const val DNS_PREFLIGHT_HOST_TIMEOUT_MS = 2_000L
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
