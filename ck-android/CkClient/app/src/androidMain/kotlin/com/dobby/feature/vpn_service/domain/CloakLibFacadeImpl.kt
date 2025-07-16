package com.dobby.feature.vpn_service.domain

import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.outline.OutlineGo

class CloakLibFacadeImpl : CloakLibFacade {

    override fun startClient(localHost: String, localPort: String, config: String) {
        OutlineGo.startCloakClient(localHost, localPort, config, false)
    }

    override fun stopClient() {
        OutlineGo.stopCloakClient()
    }

    override fun restartClient() {
        android.util.Log.e(
            "DobbyTAG",
            "VpnService:  Cloak_outline.startAgain()\n\n\n"
        )
//        Cloak_outline.startAgain()
    }
}
