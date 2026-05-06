package com.dobby.feature.vpn_service.domain.cloak

import android.util.Log
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.backend.GoBackendWrapper

class CloakLibFacadeImpl : CloakLibFacade {

    override fun startClient(localHost: String, localPort: String, config: String) {
        GoBackendWrapper.startCloakClient(localHost, localPort, config, false)
    }

    override fun stopClient() {
        GoBackendWrapper.stopCloakClient()
    }
}
