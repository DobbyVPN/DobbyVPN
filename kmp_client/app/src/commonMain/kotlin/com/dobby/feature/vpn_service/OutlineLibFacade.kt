package com.dobby.feature.vpn_service

interface OutlineLibFacade {

    /**
     * Initialize and connect to Outline server
     * @return true if connection successful, false otherwise
     */
    fun init(apiKey: String): Boolean

    fun disconnect()

    fun writeData(data: ByteArray, length: Int)

    fun readData(data: ByteArray): Int
}
