package com.dobby.awg

class GoBackendWrapper {
    companion object {
        private val backend = GoBackend()

        fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int = backend.awgTurnOn(ifname, tunFd, settings)

        fun awgTurnOff() = backend.awgTurnOff()

        fun awgGetSocketV4(): Int = backend.awgGetSocketV4()

        fun awgGetSocketV6(): Int = backend.awgGetSocketV6()

        fun InitLogger(path: String) = backend.initLogger(path)
    }
}
