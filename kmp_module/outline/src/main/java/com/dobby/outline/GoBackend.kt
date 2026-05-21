package com.dobby.outline

import android.util.Log

class OutlineGo {
    companion object {
        init {
            Log.d(TAG, "Start loading libraries")
            System.loadLibrary("outline_jni")
            System.loadLibrary("outline")
            Log.d(TAG, "Libraries loaded successfully")
        }

        private const val TAG = "OutlineGo"

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun newOutlineClient(config: String, fd: Int)

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun outlineConnect(): Int

        @JvmStatic
        external fun getLastError(): String?

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun outlineDisconnect()

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun startCloakClient(
            localHost: String,
            localPort: String,
            config: String,
            udp: Boolean,
        )

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun stopCloakClient()

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun initLogger(path: String)

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun checkServerAlive(address: String, port: Int): Int

        @JvmStatic
        external fun registerVpnService(service: android.net.VpnService)

        @JvmStatic
        external fun setGeoRoutingConf(cidrsC: String)

        @JvmStatic
        external fun clearGeoRoutingConf()
    }
}
