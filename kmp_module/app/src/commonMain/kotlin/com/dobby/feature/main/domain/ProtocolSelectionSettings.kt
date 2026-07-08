package com.dobby.feature.main.domain

object ProtocolSelectionSettings {
    const val HEALTH_CHECK_START_GRACE_MS = 15_000L
    const val SERVICE_START_TIMEOUT_MS = 90_000L

    const val TUNNEL_PROBE_FAST_TIMEOUT_MS = 2_000L
    const val TUNNEL_PROBE_RETRY_TIMEOUT_MS = 3_000L
    const val TUNNEL_PROBE_SLOW_RETRY_THRESHOLD_MS = 1_000L
    const val TUNNEL_PROBE_ROUTE_READY_TIMEOUT_MS = 1_500L

    const val DNS_PREFLIGHT_HOST_TIMEOUT_MS = 2_000L
    const val DNS_PREFLIGHT_TOTAL_TIMEOUT_MS = 2_000L

    const val IOS_DNS_PREFLIGHT_HOSTS_KEY = "dnsPreflightHosts"
    const val IOS_DNS_PREFLIGHT_ENTRIES_KEY = "dnsPreflightEntries"
    const val IOS_DNS_PREFLIGHT_HOST_TIMEOUT_MS_KEY = "dnsPreflightHostTimeoutMs"
    const val IOS_DNS_PREFLIGHT_TOTAL_TIMEOUT_MS_KEY = "dnsPreflightTotalTimeoutMs"

    val HEALTHCHECK_PREWARM_HOSTS = listOf(
        "google.com",
        "one.one.one.one",
        "www.google.com",
        "www.cloudflare.com",
        "about.google",
        "api.ipify.org",
    )
}
