package com.dobby.backend

class AwgBackend {
    external fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int

    external fun awgTurnOff()

    external fun awgGetSocketV4(): Int

    external fun awgGetSocketV6(): Int

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
