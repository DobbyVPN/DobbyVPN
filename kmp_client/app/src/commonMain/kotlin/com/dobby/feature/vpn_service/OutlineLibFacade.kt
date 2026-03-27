package com.dobby.feature.vpn_service

interface OutlineLibFacade {

    /**
     * Initialize and connect to Outline server
     * @return true if connection successful, false otherwise
     */
    fun init(apiKey: String, tunFd: Int): Boolean

    fun disconnect()
}
