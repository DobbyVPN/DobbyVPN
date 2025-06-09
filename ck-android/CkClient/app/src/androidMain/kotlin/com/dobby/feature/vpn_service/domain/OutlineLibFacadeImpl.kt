package com.dobby.feature.vpn_service.domain

import com.dobby.outline.OutlineGo
import com.dobby.feature.vpn_service.OutlineLibFacade
import java.nio.ByteBuffer

internal class OutlineLibFacadeImpl: OutlineLibFacade {

    override fun init(apiKey: String) {
        OutlineGo.newOutlineDevice(apiKey)
    }

    override fun disconnect() {
//        device?.disconnect()
    }

    override fun writeData(data: ByteArray) {
        OutlineGo.write(data, data.size)
    }

    override fun readData(): ByteArray? {
        // 1) Allocate a Kotlin ByteArray
        val buffer = ByteArray(65536)
        // 2) Pass it into your native read method
        val n = OutlineGo.read(buffer, buffer.size)
        // 3) If nothing was read, return null
        if (n <= 0) return null
        // 4) Otherwise return exactly the bytes you got
        return buffer.copyOf(n)
    }
}
