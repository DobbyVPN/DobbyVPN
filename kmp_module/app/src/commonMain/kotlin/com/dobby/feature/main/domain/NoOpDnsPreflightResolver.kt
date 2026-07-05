package com.dobby.feature.main.domain

class NoOpDnsPreflightResolver : DnsPreflightResolver {
    override suspend fun prewarm(profiles: List<ConnectionProfile>) = Unit
}
