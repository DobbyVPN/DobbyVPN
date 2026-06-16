package com.dobby.feature.diagnostic.domain


interface HealthCheckManager {
    /**
     * Get vpn connection state from health check thread.
     */
    fun getConnectionState(): VpnConnectionState

    /**
     * Platform dependent health check initiation. It should be run before VPN connection setup.
     */
    fun init(): Unit

    /**
     * Platform dependent health check start. It should be run after VPN connection setup.
     */
    fun start(): Unit

    /**
     * Platform dependent health check stop
     */
    fun stop(): Unit
}
