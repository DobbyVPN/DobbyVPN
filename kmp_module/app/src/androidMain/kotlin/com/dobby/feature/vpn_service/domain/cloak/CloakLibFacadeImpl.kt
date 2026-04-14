package com.dobby.feature.vpn_service.domain.cloak

import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.protocol.ProtocolGo

class CloakLibFacadeImpl : CloakLibFacade {

    override fun startClient(localHost: String, localPort: String, config: String) {
        ProtocolGo.startCloakClient(localHost, localPort, config, false)
    }

    override fun stopClient() {
        ProtocolGo.stopCloakClient()
    }
}
