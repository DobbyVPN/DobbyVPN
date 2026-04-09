package com.dobby.awg

import com.dobby.outline.OutlineGo

class GoBackendWrapper {
    companion object {
        private val backend = OutlineGo

        fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int = backend.awgTurnOn(ifname, tunFd, settings)

        fun awgTurnOff() = backend.awgTurnOff()

        fun awgGetSocketV4(): Int = backend.awgGetSocketV4()

        fun awgGetSocketV6(): Int = backend.awgGetSocketV6()

        fun awgGetConfig(): String? = backend.awgGetConfig()

        fun awgVersion(): String? = backend.awgVersion()
    }
}
