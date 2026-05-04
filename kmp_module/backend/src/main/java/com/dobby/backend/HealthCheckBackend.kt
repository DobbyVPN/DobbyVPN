package com.dobby.backend

class HealthCheckBackend {
    external fun checkServerAlive(address: String, port: Int): Int

    external fun getConnectionState(): Int

    external fun initHealthCheck(): Unit

    external fun startHealthCheck(): Unit

    external fun stopHealthCheck(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
