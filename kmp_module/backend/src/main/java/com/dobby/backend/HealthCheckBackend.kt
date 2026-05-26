package com.dobby.backend

class HealthCheckBackend {
    external fun getConnectionState(): Int

    external fun initHealthCheck(config: String): Unit

    external fun startHealthCheck(): Unit

    external fun stopHealthCheck(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
