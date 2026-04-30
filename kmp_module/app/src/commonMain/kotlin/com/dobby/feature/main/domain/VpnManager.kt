package com.dobby.feature.main.domain

interface VpnManager {
    /**
     * Platform dependent VPN start. Desktops: via gRPC. Mobile: via imported libraries.
     *
     * @return true if start succeeded, false otherwise
     */
    fun start(): Boolean

    fun stop()
}
