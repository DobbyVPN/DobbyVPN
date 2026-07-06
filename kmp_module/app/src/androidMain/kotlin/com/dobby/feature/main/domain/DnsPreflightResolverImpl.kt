package com.dobby.feature.main.domain

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import com.dobby.feature.main.domain.config.isLocalOrIpLiteral
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.Inet4Address
import java.net.InetAddress
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class DnsPreflightResolverImpl(
    context: Context,
    private val logger: Logger,
) : DnsPreflightResolver {
    private val connectivityManager =
        context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        GoBackendWrapper.clearDNSCache()

        val hosts = collectDnsPreflightHosts(profiles)
            .plus(ProtocolSelectionSettings.HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        if (hosts.isEmpty()) {
            logger.log("[DNSPreflight] No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        val network = selectPhysicalNetwork()
        logger.log(
            "[DNSPreflight] Start hosts=${hosts.size} profiles=${profiles.size} " +
                "network=${network?.toString() ?: "active/default"} " +
                "hostTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_HOST_TIMEOUT_MS} " +
                "totalTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS} " +
                "sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )

        val resolved = resolveHosts(network, hosts)

        val entries = resolved.joinToString("\n") { "${it.host}=${it.ip}" }
        val cachedCount = GoBackendWrapper.setDNSCacheEntries(entries)
        val failedCount = hosts.size - resolved.size
        logger.log(
            "[DNSPreflight] Finished hosts=${hosts.size} resolved=${resolved.size} " +
                "cached=$cachedCount failed=$failedCount"
        )
    }

    private suspend fun resolveHosts(network: Network?, hosts: List<String>): List<ResolvedHost> = withContext(Dispatchers.IO) {
        val executor = Executors.newFixedThreadPool(hosts.size)
        try {
            val tasks = hosts.map { host ->
                Callable {
                    resolveHost(network, host)
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

    private fun resolveHost(network: Network?, host: String): ResolvedHost? {
        val addresses = runCatching {
            val raw = network?.getAllByName(host) ?: InetAddress.getAllByName(host)
            raw.toList()
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

    private data class ResolvedHost(val host: String, val ip: String)
}
