package com.dobby.backend

class HealthCheckBackend {
    external fun checkServerAlive(address: String, port: Int): Int

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
