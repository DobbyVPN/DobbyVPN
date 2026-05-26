package com.dobby.backend

class LoggerBackend {
    external fun initLogger(path: String): Unit

    external fun initTelemetry(endpoint: String): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
