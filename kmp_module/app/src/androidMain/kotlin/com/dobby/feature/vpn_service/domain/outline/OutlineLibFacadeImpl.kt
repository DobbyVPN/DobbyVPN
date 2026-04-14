package com.dobby.feature.vpn_service.domain.outline

import android.util.Log
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.protocol.ProtocolGo

internal class OutlineLibFacadeImpl : OutlineLibFacade {
    private val TAG = "OutlineLibFacade"

    override fun init(apiKey: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with apiKey length=${apiKey.length}, starts with: ${apiKey.take(30)}...")
        ProtocolGo.newOutlineClient(apiKey, tunFd)
        Log.d(TAG, "Connecting Outline...")
        val result = ProtocolGo.outlineConnect()
        return if (result == 0) {
            Log.d(TAG, "Connect finished successfully")
            true
        } else {
            val lastError = ProtocolGo.getLastError()
            Log.e(TAG, "Connect FAILED: $lastError")
            false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        ProtocolGo.outlineDisconnect()
        Log.d(TAG, "disconnect() finished")
    }
}
