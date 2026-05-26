package com.dobby.backend

object VpnBackendWrapper {
    private val backend = VpnBackend()

    val getLastError = backend::getLastError

    val newVpnClient = backend::newVpnClient

    val vpnConnect = backend::vpnConnect

    val vpnDisconnect = backend::vpnDisconnect
}
