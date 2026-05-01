package com.dobby.backend

object AwgBackendWrapper {
    private val awgBackend = AwgBackend()

    val awgTurnOn = awgBackend::awgTurnOn

    val awgTurnOff = awgBackend::awgTurnOff

    val awgGetSocketV4 = awgBackend::awgGetSocketV4

    val awgGetSocketV6 = awgBackend::awgGetSocketV6
}
