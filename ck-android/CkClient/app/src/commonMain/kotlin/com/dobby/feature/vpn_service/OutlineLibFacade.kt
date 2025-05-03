package com.dobby.feature.vpn_service

interface OutlineLibFacade {

    fun init(apiKey: String)

    fun disconnect()

    fun writeData(data: ByteArray)

    fun readData(): ByteArray?
}
