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
<<<<<<< HEAD
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
=======
>>>>>>> 327dcb95 (unify vpn export for xray and outline)
         * Returns the last error from Go code.
         * @return error string or null if there is no error
         */
        @JvmStatic
        external fun getLastError(): String?

        @JvmStatic
<<<<<<< HEAD
        external fun outlineDisconnect()

        @JvmStatic
=======
        @Throws(IllegalStateException::class)
>>>>>>> 327dcb95 (unify vpn export for xray and outline)
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

<<<<<<< HEAD:kmp_module/outline/src/main/java/com/dobby/outline/GoBackend.kt
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
=======
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
>>>>>>> ad8d9c92 (make core module in go_module that unifies work with protocols, fix HeathCheck on desktop):kmp_module/outline/src/main/java/com/dobby/protocol/GoBackend.kt
    }
}
