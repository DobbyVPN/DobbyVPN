package com.dobby.backend

object CloakBackendWrapper {
    private val backend = CloakBackend()

    val startCloakClient = backend::startCloakClient

    val stopCloakClient = backend::stopCloakClient
}
