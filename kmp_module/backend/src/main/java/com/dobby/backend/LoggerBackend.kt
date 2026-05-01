package com.dobby.backend

class LoggerBackend {
    external fun initLogger(path: String): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
