package com.dobby.feature.vpn_service

interface TrustTunnelLibFacade {

    /**
     * Initialize and connect TrustTunnel client
     * @param config TrustTunnel Toml config string
     * @param tunFd TUN interface file descriptor
     * @return true if connection successful, false otherwise
     */
    fun init(config: String, tunFd: Int): Boolean

    fun disconnect()
}