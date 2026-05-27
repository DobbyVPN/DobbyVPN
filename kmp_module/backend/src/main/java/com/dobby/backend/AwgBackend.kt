package com.dobby.backend

import com.dobby.gomobile.dobbyvpn.Dobbyvpn

class AwgBackend {
    fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int = Dobbyvpn.awgTurnOn(ifname, tunFd, settings)

    fun awgTurnOff() {
        Dobbyvpn.awgTurnOff()
    }

    fun awgGetSocketV4(): Int = Dobbyvpn.awgGetSocketV4()

    fun awgGetSocketV6(): Int = Dobbyvpn.awgGetSocketV6()
}
