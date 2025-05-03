package com.dobby.feature.vpn_service.domain

import com.dobby.feature.vpn_service.CloakLibFacade
import kotlin_exports.Kotlin_exports.startCloakClient
import kotlin_exports.Kotlin_exports.stopCloakClient

class CloakLibFacadeImpl : CloakLibFacade {

    override fun startClient(localHost: String, localPort: String, config: String) {
        startCloakClient(localHost, localPort, config, false)
    }

    override fun stopClient() {
        stopCloakClient()
    }

    override fun restartClient() {
//        Cloak_outline.startAgain()
    }
}
