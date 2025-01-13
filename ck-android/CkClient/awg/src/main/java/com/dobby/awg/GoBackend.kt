package com.dobby.awg

class GoBackend {
    external fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int

    external fun awgTurnOff(handle: Int)

    external fun awgGetSocketV4(handle: Int): Int

    external fun awgGetSocketV6(handle: Int): Int

    external fun awgGetConfig(handle: Int): String

    external fun awgVersion(): String

    companion object {
        init {
            System.loadLibrary("wg-go")
            System.loadLibrary("wg")
            System.loadLibrary("wg-quick")
        }
    }
}