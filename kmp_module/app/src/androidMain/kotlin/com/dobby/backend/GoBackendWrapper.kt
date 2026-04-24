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

    val newOutlineClient = backend::newOutlineClient

    val outlineConnect = backend::outlineConnect

    val outlineDisconnect = backend::outlineDisconnect

    val registerVpnService = backend::registerVpnService
}
