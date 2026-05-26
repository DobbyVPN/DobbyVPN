package com.dobby.backend

class NetCheckBackend {
    external fun netCheck(configPath: String): String?

    external fun cancelNetCheck(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
