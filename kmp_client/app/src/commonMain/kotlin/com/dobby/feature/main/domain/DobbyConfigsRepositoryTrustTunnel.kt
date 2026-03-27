package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryTrustTunnel {
    fun getTrustTunnelConfig(): String

    fun setTrustTunnelConfig(config: String)

    fun getIsTrustTunnelEnabled(): Boolean

    fun setIsTrustTunnelEnabled(isTrustTunnelEnabled: Boolean)
}