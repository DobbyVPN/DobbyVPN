package com.dobby.feature.diagnostic.domain

interface HealthCheck {
    fun GetConnectionState(): VpnConnectionState
    fun InitHealthCheck(config: String): Unit
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
}
