package com.dobby.feature.vpn_service.domain

import kotlin_exports.OutlineClient
import com.dobby.feature.vpn_service.OutlineLibFacade

internal class OutlineLibFacadeImpl: OutlineLibFacade {

    private var device: OutlineClient? = null

    override fun init(apiKey: String) {
        device = OutlineClient(apiKey).apply { connect() }
    }

    override fun disconnect() {
        device?.disconnect()
    }

    override fun writeData(data: ByteArray) {
        device?.write(data)
    }

    override fun readData(): ByteArray? {
        return device?.read()
    }
}
