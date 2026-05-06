package com.dobby.backend

class VpnBackend {
    external fun getLastError(): String?

    external fun newVpnClient(config: String, protocol: String, fd: Int, mtu: Int): Unit

    external fun vpnConnect(): Int

    external fun vpnDisconnect(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
