package com.dobby.feature.vpn_service.domain

import com.dobby.outline.OutlineGo
import com.dobby.feature.vpn_service.OutlineLibFacade

internal class OutlineLibFacadeImpl : OutlineLibFacade {

    override fun init(apiKey: String) {
        OutlineGo.newOutlineClient(apiKey).apply { OutlineGo.connect() }
    }

    override fun disconnect() {
        OutlineGo.disconnect()
    }

    override fun writeData(data: ByteArray, length: Int) {
        OutlineGo.write(data, length)
    }

    override fun readData(data: ByteArray): Int {
        return OutlineGo.read(data, data.size)
    }
}
