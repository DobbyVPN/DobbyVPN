package com.dobby.backend

object OutlineBackendWrapper {
    private val backend = OutlineBackend()

    val getLastError = backend::getLastError

    val newOutlineClient = backend::newOutlineClient

    val outlineConnect = backend::outlineConnect

    val outlineDisconnect = backend::outlineDisconnect
}
