package com.dobby.backend

object NetCheckBackendWrapper {
    private val backend = NetCheckBackend()

    val netCheck = backend::netCheck

    val cancelNetCheck = backend::cancelNetCheck
}
