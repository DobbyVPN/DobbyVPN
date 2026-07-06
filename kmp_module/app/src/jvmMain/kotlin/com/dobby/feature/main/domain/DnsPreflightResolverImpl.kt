package com.dobby.feature.main.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import com.dobby.feature.main.domain.config.isLocalOrIpLiteral
import interop.dnscache.DnsCacheLibrary
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.Inet4Address
import java.net.InetAddress
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class DnsPreflightResolverImpl(
    private val dnsCacheLibrary: DnsCacheLibrary,
    private val logger: Logger,
) : DnsPreflightResolver {
    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        dnsCacheLibrary.ClearDNSCache()

        val hosts = collectDnsPreflightHosts(profiles)
            .plus(ProtocolSelectionSettings.HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        if (hosts.isEmpty()) {
            logger.log("[DNSPreflight] No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        logger.log(
            "[DNSPreflight] Start hosts=${hosts.size} profiles=${profiles.size} " +
                "network=desktop/default " +
                "hostTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_HOST_TIMEOUT_MS} " +
                "totalTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS} " +
                "sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )

        val resolved = resolveHosts(hosts)

        val entries = resolved.joinToString("\n") { "${it.host}=${it.ip}" }
        val cachedCount = dnsCacheLibrary.SetDNSCacheEntries(entries, DNS_PREFLIGHT_SOURCE)
        val failedCount = hosts.size - resolved.size
        logger.log(
            "[DNSPreflight] Finished hosts=${hosts.size} resolved=${resolved.size} " +
                "cached=$cachedCount failed=$failedCount"
        )
    }

    private suspend fun resolveHosts(hosts: List<String>): List<ResolvedHost> = withContext(Dispatchers.IO) {
        val executor = Executors.newFixedThreadPool(hosts.size)
        try {
            val tasks = hosts.map { host ->
                Callable {
                    resolveHost(host)
                }
            }
            val futures = executor.invokeAll(
                tasks,
                ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS,
                TimeUnit.MILLISECONDS
            )
            futures.mapIndexedNotNull { index, future ->
                if (future.isCancelled) {
                    logger.log(
                        "[DNSPreflight] Batch timeout host=${maskStr(hosts[index])} " +
                            "timeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS}"
                    )
                    null
                } else {
                    runCatching { future.get() }
                        .getOrElse { error ->
                            logger.log("[DNSPreflight] Failed host=${maskStr(hosts[index])} error=${error.message}")
                            null
                        }
                }
            }
        } catch (error: InterruptedException) {
            Thread.currentThread().interrupt()
            logger.log("[DNSPreflight] Interrupted hosts=${hosts.size} error=${error.message}")
            emptyList()
        } finally {
            executor.shutdownNow()
        }
    }

    private fun resolveHost(host: String): ResolvedHost? {
        val addresses = runCatching {
            InetAddress.getAllByName(host).toList()
        }.getOrElse { error ->
            logger.log("[DNSPreflight] Failed host=${maskStr(host)} error=${error.message}")
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

    private data class ResolvedHost(val host: String, val ip: String)

    private companion object {
        const val DNS_PREFLIGHT_SOURCE = "desktop-preflight"
    }
}
