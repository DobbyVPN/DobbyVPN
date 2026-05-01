package com.dobby.backend

object GoBackendWrapper {
    private val backend = GoBackend()

    val registerVpnService = backend::registerVpnService
}
