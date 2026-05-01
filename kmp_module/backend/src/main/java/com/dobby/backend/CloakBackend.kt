package com.dobby.backend

class CloakBackend {
    external fun startCloakClient(localHost: String, localPort: String, config: String, udp: Boolean): Unit

    external fun stopCloakClient(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
