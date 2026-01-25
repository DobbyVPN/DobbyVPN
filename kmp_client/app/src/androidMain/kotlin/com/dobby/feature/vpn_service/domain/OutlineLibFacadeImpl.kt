package com.dobby.feature.vpn_service.domain

import android.util.Log
import com.dobby.outline.OutlineGo
import com.dobby.feature.vpn_service.OutlineLibFacade

internal class OutlineLibFacadeImpl : OutlineLibFacade {

    private val TAG = "OutlineLibFacade"
    var lastError: String? = null
        private set

    override fun init(apiKey: String): Boolean {
        Log.d(TAG, "init() called with apiKey length=${apiKey.length}, starts with: ${apiKey.take(30)}...")
        OutlineGo.newOutlineClient(apiKey)
        Log.d(TAG, "Connecting Outline...")
        val result = OutlineGo.connect()
        return if (result == 0) {
            Log.d(TAG, "Connect finished successfully")
            lastError = null
            true
        } else {
            lastError = OutlineGo.getLastError()
            Log.e(TAG, "Connect FAILED: $lastError")
            false
        }
    }

    fun isConnected(): Boolean = lastError == null

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
