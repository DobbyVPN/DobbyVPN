package com.dobby.feature.vpn_service

interface TrustTunnelLibFacade {
    companion object {
        const val TRUST_TUNNEL_PROTOCOL = "trusttunnel"
    }

    /**
     * Initialize and connect TrustTunnel client.
     * @param config TrustTunnel TOML config string
     * @param tunFd TUN interface file descriptor
     * @return true if connection successful, false otherwise
     */
    fun init(config: String, tunFd: Int): Boolean

    fun disconnect()
}
