package com.dobby.feature.vpn_service.domain

import android.util.Log
import com.dobby.outline.OutlineGo
import com.dobby.feature.vpn_service.OutlineLibFacade

internal class OutlineLibFacadeImpl : OutlineLibFacade {

    private val TAG = "OutlineLibFacade"

    override fun init(apiKey: String) {
        Log.d(TAG, "init() called with apiKey=${apiKey.take(6)}...") // не логируем полный ключ!
        OutlineGo.newOutlineClient(apiKey).apply {
            Log.d(TAG, "Connecting Outline...")
            OutlineGo.connect()
            OutlineGo.couldStart()
            Log.d(TAG, "Connect finished")
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        OutlineGo.disconnect()
        Log.d(TAG, "disconnect() finished")
    }

    override fun writeData(data: ByteArray, length: Int) {
        Log.d(TAG, "writeData() called with length=$length")
        OutlineGo.write(data, length)
        Log.d(TAG, "writeData() finished")
    }

    override fun readData(data: ByteArray): Int {
        Log.d(TAG, "readData() called, buffer size=${data.size}")
        val bytesRead = OutlineGo.read(data, data.size)
        Log.d(TAG, "readData() finished, bytesRead=$bytesRead")
        return bytesRead
    }
}
