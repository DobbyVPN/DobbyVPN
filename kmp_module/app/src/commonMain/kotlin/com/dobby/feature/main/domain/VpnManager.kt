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
     * Starts a particular lifecycle generation.  Older platform implementations keep the
     * original entry point while mobile implementations use the generation to reject delayed
     * intents and callbacks from a previous tunnel.
     */
    fun start(isProtocolProbe: Boolean, generation: Long) {
        start(isProtocolProbe)
    }

    /**
     * Platform dependent VPN stop. Desktops: via gRPC. Mobile: via imported libraries.
     * Stops VPN service completely
     *
     * @param isUserInitiated true only when the stop was directly requested by the user.
     */
    fun stop(isUserInitiated: Boolean)

    /** Stops only the supplied generation when the platform can correlate it. */
    fun stop(isUserInitiated: Boolean, generation: Long) {
        stop(isUserInitiated)
    }
}
