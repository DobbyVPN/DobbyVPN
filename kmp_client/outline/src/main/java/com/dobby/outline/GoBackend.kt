package com.dobby.outline

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class OutlineGo {
    companion object {
        init {
            Log.d(TAG, "Start loading libraries")
            System.loadLibrary("outline")
            System.loadLibrary("outline_jni")
            Log.d(TAG, "Libraries loaded successfully")
        }

        private const val TAG = "OutlineGo"

        @Volatile
        private var isLibrariesLoaded = false
        private val loadingLock = Object()

        /**
         * Loads native libraries asynchronously.
         * @return true if libraries were loaded successfully
         */
        suspend fun loadLibraries(): Boolean = withContext(Dispatchers.IO) {
            synchronized(loadingLock) {
                Log.d(TAG, "Start loading libraries")
                if (isLibrariesLoaded) {
                    return@withContext true
                }

                try {
                    // Load the Go library first, then our JNI wrapper.
                    System.loadLibrary("outline")
                    System.loadLibrary("outline_jni")
                    isLibrariesLoaded = true
                    Log.d(TAG, "Libraries loaded successfully")
                    true
                } catch (e: UnsatisfiedLinkError) {
                    Log.e(TAG, "Failed to load libraries", e)
                    false
                }
            }
        }

        /**
         * Checks if the libraries are loaded
         * @throws IllegalStateException if libraries are not loaded
         */
        @Throws(IllegalStateException::class)
        fun ensureLibrariesLoaded() {
            if (!isLibrariesLoaded) {
                throw IllegalStateException("Libraries not loaded. Call loadLibraries() first")
            }
        }

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
        external fun startCloakClient(localHost: String,
                                      localPort: String,
                                      config: String,
                                      udp: Boolean): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun stopCloakClient(): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun initLogger(path: String): Unit

        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun checkServerAlive(address: String, port: Int): Int

        /**
         * Safe call to newOutlineClient with a library-loaded check.
         */
        suspend fun safeNewOutlineClient(config: String, fd: Int): Boolean = withContext(Dispatchers.IO) {
            Log.d(TAG, "Start safeNewOutlineClient")
            try {
                if (!isLibrariesLoaded && !loadLibraries()) {
                    return@withContext false
                }
                newOutlineClient(config, fd)
                true
            } catch (e: Exception) {
                Log.e(TAG, "Failed to create outline device", e)
                false
            }
        }

        suspend fun safeConnect(): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                val result = outlineConnect()
                if (result != 0) {
                    val error = getLastError()
                    Log.e(TAG, "Connect failed: $error")
                }
                result
            } catch (e: Exception) {
                Log.e(TAG, "Connect failed with exception", e)
                -1
            }
        }

        suspend fun safeDisconnect(): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                outlineDisconnect()
                1
            } catch (e: Exception) {
                Log.e(TAG, "Read failed", e)
                -1
            }
        }

        suspend fun safeStartCloakClient(localHost: String,
                                         localPort: String,
                                         config: String,
                                         udp: Boolean): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                startCloakClient(localHost, localPort, config, udp)
                1
            } catch (e: Exception) {
                Log.e(TAG, "StartCloakClient failed", e)
                -1
            }
        }

        suspend fun safeStopCloakClient(): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                stopCloakClient()
                1
            } catch (e: Exception) {
                Log.e(TAG, "StopCloakClient failed", e)
                -1
            }
        }

        suspend fun safeInitLogger(path: String): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                initLogger(path)
                1
            } catch (e: Exception) {
                Log.e(TAG, "InitLogger failed", e)
                -1
            }
        }
        suspend fun safeCheckServerAlive(address: String, port: Int): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                checkServerAlive(address, port)
            } catch (e: Exception) {
                Log.e(TAG, "Read failed", e)
                -1
            }
        }
    }
}