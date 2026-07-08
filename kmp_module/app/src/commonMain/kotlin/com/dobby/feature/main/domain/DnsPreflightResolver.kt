package com.dobby.feature.main.domain

interface DnsPreflightResolver {
    suspend fun prewarm(profiles: List<ConnectionProfile>)
}
