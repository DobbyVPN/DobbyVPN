package com.dobby.backend

object GoBackendWrapper {
    private val backend = GoBackend()

    val startCloakClient = backend::startCloakClient

    val stopCloakClient = backend::stopCloakClient

    val setGeoRoutingConf = backend::setGeoRoutingConf

    val clearGeoRoutingConf = backend::clearGeoRoutingConf

    val checkServerAlive = backend::checkServerAlive

    val initLogger = backend::initLogger

    val getLastError = backend::getLastError

    val newVpnClient = backend::newVpnClient

    val vpnConnect = backend::vpnConnect

    val vpnDisconnect = backend::vpnDisconnect

    val registerVpnService = backend::registerVpnService
}
