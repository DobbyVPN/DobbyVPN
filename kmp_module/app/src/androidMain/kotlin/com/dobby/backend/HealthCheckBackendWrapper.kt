package com.dobby.backend

object HealthCheckBackendWrapper {
    private val backend = HealthCheckBackend()

    val checkServerAlive = backend::checkServerAlive
}
