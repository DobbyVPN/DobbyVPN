package com.dobby.outline

import com.dobby.awg.GoBackendWrapper

object OutlineGo {
    fun newOutlineClient(config: String, fd: Int): Unit {}

    fun outlineConnect(): Int = -1

    fun getLastError(): String? = ""

    fun outlineDisconnect(): Unit {}

    fun startCloakClient(
        localHost: String,
        localPort: String,
        config: String,
        udp: Boolean
    ): Unit {}

    fun stopCloakClient(): Unit {}

    fun initLogger(path: String): Unit = GoBackendWrapper.InitLogger(path)

    fun checkServerAlive(address: String, port: Int): Int = 0

    fun registerVpnService(service: android.net.VpnService) {}

    fun setGeoRoutingConf(cidrsC: String) {}

    fun clearGeoRoutingConf() {}
}
