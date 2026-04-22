package com.dobby.feature.diagnostic.domain

interface HealthCheck {
    fun GetConnectionState(): VpnConnectionState
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
}
