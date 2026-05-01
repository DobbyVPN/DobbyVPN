package com.dobby.backend

object HealthCheckBackendWrapper {
    private val backend = HealthCheckBackend()

    val getConnectionState = backend::getConnectionState

    val initHealthCheck = backend::initHealthCheck

    val startHealthCheck = backend::startHealthCheck

    val stopHealthCheck = backend::stopHealthCheck
}
