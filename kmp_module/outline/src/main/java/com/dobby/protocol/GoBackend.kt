package com.dobby.protocol

import android.util.Log

class ProtocolGo {
    companion object {
        init {
            Log.d(TAG, "Start loading libraries")
            System.loadLibrary("outline_jni")
            System.loadLibrary("outline")
            Log.d(TAG, "Libraries loaded successfully")
        }

        private const val TAG = "ProtocolGo"

        /**
         * Returns the last error from Go code.
         * @return error string or null if there is no error
         */
        @JvmStatic
        external fun getLastError(): String?

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

        /**
         * Creates a new Vpn client with the provided config and TUN file descriptor.
         * The protocol is chosen by provided protocol string (xray, outline)
         * @throws IllegalStateException if libraries are not loaded
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun newVpnClient(config: String, protocol: String, tunFd: Int): Unit

        /**
         * Connects the Vpn client.
         * @return 0 on success, -1 on error
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun vpnConnect(): Int

        /**
         * Disconnects the Vpn client.
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun vpnDisconnect(): Unit
    }
}
