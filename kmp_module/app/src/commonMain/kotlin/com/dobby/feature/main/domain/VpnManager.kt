package com.dobby.feature.main.domain

interface VpnManager {
    val supportsVpnNetworkReadySignal: Boolean

    /**
     * Platform dependent VPN start. Desktops: via gRPC. Mobile: via imported libraries.
     * Starts VPN service and sends VPN start result via [ConnectionStateRepository.serviceStartedFlow]
     *
     * @see ServiceStarted
     */
    fun start(isProtocolProbe: Boolean)

    /**
     * Platform dependent VPN stop. Desktops: via gRPC. Mobile: via imported libraries.
     * Stops VPN service completely
     */
    fun stop()
}
