package com.dobby.feature.vpn_service.domain.outline

import android.util.Log
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.backend.GoBackendWrapper

internal class OutlineLibFacadeImpl : OutlineLibFacade {
    private val TAG = "OutlineLibFacade"

    override fun init(apiKey: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with apiKey length=${apiKey.length}, starts with: ${apiKey.take(30)}...")
        // MTU 1200 is the default used in iOS and matches core.NewClient default
        val mtu = 1200
        GoBackendWrapper.newVpnClient(apiKey, "outline", tunFd, mtu)
        Log.d(TAG, "Connecting Outline...")
        val result = GoBackendWrapper.vpnConnect()
        return if (result == 0) {
            Log.d(TAG, "Connect finished successfully")
            true
        } else {
            val lastError = GoBackendWrapper.getLastError()
            Log.e(TAG, "Connect FAILED: $lastError")
            false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        GoBackendWrapper.vpnDisconnect()
        Log.d(TAG, "disconnect() finished")
    }
}
