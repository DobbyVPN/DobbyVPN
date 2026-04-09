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

        /**
         * Initializes the device with the provided Shadowsocks config.
         */
        @JvmStatic
        external fun newOutlineClient(config: String, fd: Int)

        /**
         * Connects to the Outline server.
         * @return 0 on success, -1 on error (use getLastError() for details)
         */
        @JvmStatic
        external fun outlineConnect(): Int

        /**
         * Returns the last error from Go code.
         * @return error string or null if there is no error
         */
        @JvmStatic
        external fun getLastError(): String?

        @JvmStatic
        external fun outlineDisconnect()

        @JvmStatic
        external fun startCloakClient(
            localHost: String,
            localPort: String,
            config: String,
            udp: Boolean
        )

        @JvmStatic
        external fun stopCloakClient()

        @JvmStatic
        external fun initLogger(path: String)

        @JvmStatic
        external fun checkServerAlive(address: String, port: Int): Int

        @JvmStatic
        external fun registerVpnService(service: android.net.VpnService)

        @JvmStatic
        external fun setGeoRoutingConf(cidrsC: String)

        @JvmStatic
        external fun clearGeoRoutingConf()

        @JvmStatic
        external fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int

        @JvmStatic
        external fun awgTurnOff()

        @JvmStatic
        external fun awgGetSocketV4(): Int

        @JvmStatic
        external fun awgGetSocketV6(): Int

        @JvmStatic
        external fun awgGetConfig(): String?

        @JvmStatic
        external fun awgVersion(): String?
    }
}
