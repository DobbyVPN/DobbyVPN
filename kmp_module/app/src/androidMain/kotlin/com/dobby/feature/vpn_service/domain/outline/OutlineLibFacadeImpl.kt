package com.dobby.feature.vpn_service.domain.outline

import android.util.Log
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.backend.GoBackendWrapper
import com.dobby.backend.OutlineBackendWrapper

internal class OutlineLibFacadeImpl : OutlineLibFacade {
    private val TAG = "OutlineLibFacade"

    override fun init(apiKey: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with apiKey length=${apiKey.length}, starts with: ${apiKey.take(30)}...")
        OutlineBackendWrapper.newOutlineClient(apiKey, tunFd)
        Log.d(TAG, "Connecting Outline...")
        val result = OutlineBackendWrapper.outlineConnect()
        return if (result == 0) {
            Log.d(TAG, "Connect finished successfully")
            true
        } else {
            val lastError = OutlineBackendWrapper.getLastError()
            Log.e(TAG, "Connect FAILED: $lastError")
            false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        OutlineBackendWrapper.outlineDisconnect()
        Log.d(TAG, "disconnect() finished")
    }
}
