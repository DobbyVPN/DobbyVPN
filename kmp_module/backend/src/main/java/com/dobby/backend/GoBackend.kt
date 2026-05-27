package com.dobby.backend

import android.net.VpnService
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import com.dobby.gomobile.dobbyvpn.SocketProtector

class GoBackend {
    fun startCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        Dobbyvpn.startCloakClient(localHost, localPort, config, udp)
    }

    fun stopCloakClient() {
        Dobbyvpn.stopCloakClient()
    }

    fun setGeoRoutingConf(cidrs: String) {
        Dobbyvpn.setGeoRoutingConf(cidrs)
    }

    fun clearGeoRoutingConf() {
        Dobbyvpn.clearGeoRoutingConf()
    }

    fun checkServerAlive(address: String, port: Int): Int = Dobbyvpn.checkServerAlive(address, port)

    fun initLogger(path: String) {
        Dobbyvpn.initLogger(path)
    }

    fun getLastError(): String? = Dobbyvpn.getVpnLastError()?.ifEmpty { null }

    fun newVpnClient(config: String, protocol: String, fd: Int) {
        Dobbyvpn.newVpnClient(config, protocol, fd)
    }

    fun vpnConnect(): Int = Dobbyvpn.vpnConnect()

    fun vpnDisconnect() {
        Dobbyvpn.vpnDisconnect()
    }

    fun registerVpnService(service: VpnService) {
        Dobbyvpn.registerSocketProtector(object : SocketProtector {
            override fun protect(fd: Int): Boolean = service.protect(fd)
        })
    }
}
