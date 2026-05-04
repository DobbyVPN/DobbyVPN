package com.dobby.backend

object LoggerBackendWrapper {
    private val backend = LoggerBackend()

    val initLogger = backend::initLogger
}
