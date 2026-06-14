package com.dobby.feature.diagnostic.domain


interface HealthCheck {
    fun GetConnectionState(): VpnConnectionState
    fun InitHealthCheck(): Unit
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
}
