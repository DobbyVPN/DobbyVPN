package com.dobby.feature.diagnostic.domain


interface HealthCheckManager {
    /**
     * Get vpn connection state from health check thread.
     */
    fun getConnectionState(): VpnConnectionState

    /**
     * Platform dependent health check initiation. It should be run before VPN connection setup.
     */
    fun initHealthCheck(): Unit

    /**
     * Platform dependent health check start. It should be run after VPN connection setup.
     */
    fun startHealthCheck(): Unit

    /**
     * Platform dependent health check stop
     */
    fun stopHealthCheck(): Unit

    /**
     * Measures protocol-selection latency using the native tunnel probe.
     * Returns a negative value when the probe failed.
     */
    fun measureTunnelProbeAverageLatencyMillis(): Long
}
