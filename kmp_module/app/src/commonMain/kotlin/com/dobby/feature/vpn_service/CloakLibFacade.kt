package com.dobby.feature.vpn_service

interface CloakLibFacade {

    fun startClient(localHost: String, localPort: String, config: String)

    fun stopClient()
}
