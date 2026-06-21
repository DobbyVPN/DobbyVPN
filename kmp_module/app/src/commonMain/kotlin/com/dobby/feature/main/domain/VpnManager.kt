package com.dobby.feature.main.domain

interface VpnManager {
    /**
     * Platform dependent VPN start. Desktops: via gRPC. Mobile: via imported libraries.
     * Starts VPN service and sends VPN start result via [ConnectionStateRepository.serviceStartedFlow]
     *
     * @see ServiceStarted
     */
    fun start()

    /**
     * Platform dependent VPN stop. Desktops: via gRPC. Mobile: via imported libraries.
     * Stops VPN service completely
     */
    fun stop()

    /**
     * Switches only the active protocol inside an already running VPN session.
     * Returns false when hot switching is unavailable or failed, so callers can fall back to stop/start.
     */
    fun switchProtocol(): Boolean
}
