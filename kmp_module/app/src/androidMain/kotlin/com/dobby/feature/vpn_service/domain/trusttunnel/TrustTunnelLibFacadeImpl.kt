package com.dobby.feature.vpn_service.domain.trusttunnel

import android.util.Log
import com.dobby.feature.vpn_service.TrustTunnelLibFacade
import com.dobby.backend.GoBackendWrapper

internal class TrustTunnelLibFacadeImpl : TrustTunnelLibFacade {

    private val TAG = "TrustTunnelLibFacade"

    override fun init(config: String, tunFd: Int): Boolean {
        Log.d(TAG, "init() called with config length=${config.length}, tunFd=$tunFd")
        try {
            GoBackendWrapper.newVpnClient(config, TrustTunnelLibFacade.TRUST_TUNNEL_PROTOCOL, tunFd)
            Log.d(TAG, "Connecting TrustTunnel...")
            val result = GoBackendWrapper.vpnConnect()
            if (result == 0) {
                Log.d(TAG, "TrustTunnelConnect finished successfully")
                return true
            } else {
                Log.e(TAG, "TrustTunnelConnect FAILED")
                GoBackendWrapper.vpnDisconnect()
                return false
            }
        } catch (e: Exception) {
            Log.e(TAG, "TrustTunnel init failed", e)
            GoBackendWrapper.vpnDisconnect()
            return false
        }
    }

    override fun disconnect() {
        Log.d(TAG, "disconnect() called")
        try {
            GoBackendWrapper.vpnDisconnect()
            Log.d(TAG, "disconnect() finished")
        } catch (e: Exception) {
            Log.e(TAG, "TrustTunnel disconnect failed", e)
        }
    }
}
