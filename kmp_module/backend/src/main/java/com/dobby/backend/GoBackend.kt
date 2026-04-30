package com.dobby.backend

import android.net.VpnService

class GoBackend {
    external fun startCloakClient(localHost: String, localPort: String, config: String, udp: Boolean): Unit

    external fun stopCloakClient(): Unit

    external fun setGeoRoutingConf(cidrs: String): Unit

    external fun clearGeoRoutingConf(): Unit

    external fun checkServerAlive(address: String, port: Int): Int

    external fun initLogger(path: String): Unit

    external fun getLastError(): String?

    external fun newOutlineClient(config: String, fd: Int): Unit

    external fun outlineConnect(): Int

    external fun outlineDisconnect(): Unit

    external fun registerVpnService(service: VpnService): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
