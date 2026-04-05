package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryTrustTunnel {
    fun getTrustTunnelConfig(): String

    fun setTrustTunnelConfig(config: String)

    fun getIsTrustTunnelEnabled(): Boolean

    fun setIsTrustTunnelEnabled(isTrustTunnelEnabled: Boolean)

    // For now left endpoint name as Outline to match existing method
    // TODO(rename to setServerPort and use for all protocols)
    fun setServerPortOutline(newConfig: String)

    fun getServerPortOutline(): String
}
