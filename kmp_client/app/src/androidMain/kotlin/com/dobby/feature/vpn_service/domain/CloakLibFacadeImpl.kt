package com.dobby.feature.vpn_service.domain

import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.outline.OutlineGo

class CloakLibFacadeImpl : CloakLibFacade {

    override fun startClient(localHost: String, localPort: String, config: String) {
        // Cloak start is a void JNI call; Go errors won't throw.
        // We opportunistically check the shared Go "last error" to surface failures.
        // Note: This is best-effort; some builds may not set last error for Cloak failures.
        runCatching { OutlineGo.getLastError() } // clear any previous error (if implemented that way)
        OutlineGo.startCloakClient(localHost, localPort, config, false)
        // Give Go a moment to validate config and set error string if it fails fast.
        try {
            Thread.sleep(50)
        } catch (_: InterruptedException) {
            // ignore
        }
        val err = runCatching { OutlineGo.getLastError() }.getOrNull()
        if (!err.isNullOrBlank()) {
            throw IllegalStateException("Cloak start failed: $err")
        }
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
