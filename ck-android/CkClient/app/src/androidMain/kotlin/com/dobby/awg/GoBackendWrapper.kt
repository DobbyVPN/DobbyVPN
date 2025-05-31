package com.dobby.awg

class GoBackendWrapper {
    companion object {
        private val backend = GoBackend()

        fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int = backend.awgTurnOn(ifname, tunFd, settings)

        fun awgTurnOff(handle: Int) = backend.awgTurnOff(handle)

        fun awgGetSocketV4(handle: Int): Int = backend.awgGetSocketV4(handle)

        fun awgGetSocketV6(handle: Int): Int = backend.awgGetSocketV6(handle)

        fun awgGetConfig(handle: Int): String = backend.awgGetConfig(handle)

        fun awgVersion(): String = backend.awgVersion()

        fun awgDumpLog(): String = backend.awgDumpLog()
    }
}