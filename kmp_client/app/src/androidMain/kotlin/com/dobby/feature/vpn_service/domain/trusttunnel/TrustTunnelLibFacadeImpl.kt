package com.dobby.feature.vpn_service.domain.trusttunnel

import android.util.Log
import com.dobby.feature.vpn_service.TrustTunnelLibFacade
import com.dobby.outline.OutlineGo

class TrustTunnelLibFacadeImpl : TrustTunnelLibFacade {

    private val TAG = "TrustTunnelLibFacade"

    override fun init(config: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with config length=${config.length}, tunFd=$tunFd")
        try {
            OutlineGo.newTrustTunnelClient(config, tunFd)
            Log.d(TAG, "Connecting TrustTunnel...")
            val result = OutlineGo.trustTunnelConnect()
            return if (result == 0) {
                Log.d(TAG, "TrustTunnelConnect finished successfully")
                true
            } else {
                Log.e(TAG, "TrustTunnelConnect FAILED")
                false
            }
        } catch (e: Exception) {
            Log.e(TAG, "TrustTunnel init failed", e)
            return false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        try {
            OutlineGo.trustTunnelDisconnect()
            Log.d(TAG, "disconnect() finished")
        } catch (e: Exception) {
            Log.e(TAG, "TrustTunnel disconnect failed", e)
        }
    }
}
