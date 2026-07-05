package com.dobby.feature.main.domain

import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import platform.Foundation.NSLog
import platform.Foundation.NSUserDefaults

class DnsPreflightResolverImpl : DnsPreflightResolver {
    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        val hosts = collectDnsPreflightHosts(profiles)
            .plus(HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        val store = NSUserDefaults(suiteName = APP_GROUP_IDENTIFIER)
        store?.removeObjectForKey(DNS_PREFLIGHT_ENTRIES_KEY)
        store?.setObject(hosts.joinToString("\n"), forKey = DNS_PREFLIGHT_HOSTS_KEY)
        store?.synchronize()

        if (hosts.isEmpty()) {
            log("No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        log(
            "Prepared hosts=${hosts.size} profiles=${profiles.size} " +
                "target=packet-tunnel sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )
    }

    private fun isLocalOrIpLiteral(host: String): Boolean {
        if (host == "localhost" || host == "127.0.0.1" || host == "::1") return true
        if (ipv4Literal.matches(host)) return true
        return host.contains(":")
    }

    private fun log(message: String) {
        NSLog("[DNSPreflight] $message")
    }

    private companion object {
        const val APP_GROUP_IDENTIFIER = "group.vpn.dobby.app"
        const val DNS_PREFLIGHT_HOSTS_KEY = "dnsPreflightHosts"
        const val DNS_PREFLIGHT_ENTRIES_KEY = "dnsPreflightEntries"
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
