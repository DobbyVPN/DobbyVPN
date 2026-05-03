package com.dobby.feature.vpn_service.domain.xray

import android.util.Log
import com.dobby.feature.vpn_service.XrayLibFacade
import com.dobby.backend.GoBackendWrapper

internal class XrayLibFacadeImpl : XrayLibFacade {

    private val TAG = "XrayLibFacade"

    override fun init(config: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with config length=${config.length}, tunFd=$tunFd")
        try {
            // MTU 1200 is the default used in iOS and matches core.NewClient default
            val mtu = 1200
            GoBackendWrapper.newVpnClient(config, "xray", tunFd, mtu)
            Log.d(TAG, "Connecting Xray...")
            val result = GoBackendWrapper.vpnConnect()
            return if (result == 0) {
                Log.d(TAG, "XrayConnect finished successfully")
                true
            } else {
                Log.e(TAG, "XrayConnect FAILED")
                false
            }
        } catch (e: Exception) {
            Log.e(TAG, "Xray init failed", e)
            return false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        try {
            GoBackendWrapper.vpnDisconnect()
            Log.d(TAG, "disconnect() finished")
        } catch (e: Exception) {
            Log.e(TAG, "Xray disconnect failed", e)
        }
    }
}
