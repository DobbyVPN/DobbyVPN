package com.dobby.outline

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class OutlineGo {
    companion object {
        init {
            Log.d(TAG, "Start loading libraries")
            System.loadLibrary("outline_jni")
            System.loadLibrary("outline")
            Log.d(TAG, "Libraries loaded successfully")
        }

        private const val TAG = "OutlineGo"

        /**
         * Initializes the device with the provided Shadowsocks config.
         * @throws IllegalStateException if libraries are not loaded
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun newOutlineClient(config: String, fd: Int): Unit

        /**
         * Connects to the Outline server.
         * @return 0 on success, -1 on error (use getLastError() for details)
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun outlineConnect(): Int

        /**
         * Returns the last error from Go code.
         * @return error string or null if there is no error
         */
        @JvmStatic
        external fun getLastError(): String?

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun outlineDisconnect(): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun startCloakClient(
            localHost: String,
            localPort: String,
            config: String,
            udp: Boolean
        ): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun stopCloakClient(): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun initLogger(path: String): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun checkServerAlive(address: String, port: Int): Int

        @JvmStatic
        external fun registerVpnService(service: android.net.VpnService)

        @JvmStatic
        external fun setGeoRoutingConf(cidrsC: String)

        @JvmStatic
        external fun clearGeoRoutingConf()

        @JvmStatic
        external fun netCheck(configPath: String): String?

        @JvmStatic
        external fun cancelNetCheck()
    }
}
