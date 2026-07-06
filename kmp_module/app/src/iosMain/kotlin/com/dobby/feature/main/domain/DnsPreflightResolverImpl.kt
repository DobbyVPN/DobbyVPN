package com.dobby.feature.main.domain

import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.config.collectDnsPreflightHosts
import com.dobby.feature.main.domain.config.isLocalOrIpLiteral
import platform.Foundation.NSLog
import platform.Foundation.NSUserDefaults

class DnsPreflightResolverImpl : DnsPreflightResolver {
    override suspend fun prewarm(profiles: List<ConnectionProfile>) {
        val hosts = collectDnsPreflightHosts(profiles)
            .plus(ProtocolSelectionSettings.HEALTHCHECK_PREWARM_HOSTS)
            .filterNot(::isLocalOrIpLiteral)
            .distinct()

        val store = NSUserDefaults(suiteName = APP_GROUP_IDENTIFIER)
        store?.removeObjectForKey(ProtocolSelectionSettings.IOS_DNS_PREFLIGHT_ENTRIES_KEY)
        store?.setObject(hosts.joinToString("\n"), forKey = ProtocolSelectionSettings.IOS_DNS_PREFLIGHT_HOSTS_KEY)
        store?.setObject(
            ProtocolSelectionSettings.DNS_PREFLIGHT_HOST_TIMEOUT_MS.toString(),
            forKey = ProtocolSelectionSettings.IOS_DNS_PREFLIGHT_HOST_TIMEOUT_MS_KEY
        )
        store?.setObject(
            ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS.toString(),
            forKey = ProtocolSelectionSettings.IOS_DNS_PREFLIGHT_TOTAL_TIMEOUT_MS_KEY
        )
        store?.synchronize()

        if (hosts.isEmpty()) {
            log("No DNS hosts to pre-resolve for profiles=${profiles.size}")
            return
        }

        log(
            "Prepared hosts=${hosts.size} profiles=${profiles.size} " +
                "target=packet-tunnel " +
                "hostTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_HOST_TIMEOUT_MS} " +
                "totalTimeoutMs=${ProtocolSelectionSettings.DNS_PREFLIGHT_TOTAL_TIMEOUT_MS} " +
                "sample=${hosts.take(3).joinToString { maskStr(it) }}"
        )
    }

    private fun log(message: String) {
        NSLog("[DNSPreflight] $message")
    }

    private companion object {
        const val APP_GROUP_IDENTIFIER = "group.vpn.dobby.app"
    }
}
