package com.dobby.feature.vpn_service

interface XrayLibFacade {

    /**
     * Initialize and connect Xray client
     * @param config Xray JSON config string
     * @param tunFd TUN interface file descriptor
     * @return true if connection successful, false otherwise
     */
    fun init(config: String, tunFd: Int): Boolean

    fun disconnect()
}
