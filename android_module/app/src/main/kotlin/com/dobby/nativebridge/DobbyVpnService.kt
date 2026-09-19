package com.dobby.nativebridge

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log

/** Android-only VPN boundary for the Go-owned session manager. */
class DobbyVpnService : VpnService() {
    companion object {
        const val ACTION_PREPARE = "com.dobby.vpn.action.PREPARE"
        private const val CHANNEL = "dobby_vpn"
        private const val NOTIFICATION_ID = 101
        private const val TAG = "DobbyVpnService"
    }

    private var activeSession: String? = null
    private var activeGeneration: Long = -1
    private var vpnInterface: ParcelFileDescriptor? = null
    private var goTunFd: Int = -1

    override fun onCreate() {
        super.onCreate()
        NativeVpnBridge.attach(this)
        Log.i(TAG, "VPN service created")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_PREPARE || intent?.action == null) {
            ensureForeground()
        }
        return START_NOT_STICKY
    }

    @Synchronized
    fun acquireTunnel(sessionID: String, generation: Long): Int {
        if (activeSession != null && activeSession != sessionID) return -1
        if (activeGeneration > generation || vpnInterface != null) return -1
        ensureForeground()
        val established = try {
            Builder()
                .setSession("Dobby VPN")
                .setMtu(1500)
                .addAddress("10.7.0.2", 32)
                .addRoute("0.0.0.0", 0)
                .addRoute("::", 0)
                .addDnsServer("1.1.1.1")
                .addDnsServer("2606:4700:4700::1111")
                .establish()
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN interface establish failed", failure)
            return -1
        } ?: return -1

        val duplicate = try {
            ParcelFileDescriptor.dup(established.fileDescriptor)
        } catch (failure: Throwable) {
            established.close()
            Log.e(TAG, "VPN interface duplication failed", failure)
            return -1
        }
        val detached = try {
            duplicate.detachFd()
        } catch (failure: Throwable) {
            duplicate.close()
            established.close()
            Log.e(TAG, "VPN descriptor transfer failed", failure)
            return -1
        }
        activeSession = sessionID
        activeGeneration = generation
        vpnInterface = established
        goTunFd = detached
        return detached
    }

    @Synchronized
    fun releaseTunnel(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID != activeSession || generation != activeGeneration || fd != goTunFd) return false
        closeTunnel()
        return true
    }

    @Synchronized
    fun protectProtocolSocket(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID != activeSession || generation != activeGeneration || fd < 0) return false
        return protect(fd)
    }

    @Synchronized
    fun publishState(sessionID: String, generation: Long, state: String, failureCode: String) {
        if (sessionID != activeSession || generation < activeGeneration) return
        Log.i(TAG, "Go state=$state generation=$generation failure=$failureCode")
        if (state == "IDLE" || state == "FAILED") {
            stopForeground(STOP_FOREGROUND_REMOVE)
        }
    }

    override fun onDestroy() {
        synchronized(this) { closeTunnel() }
        NativeVpnBridge.detach(this)
        super.onDestroy()
    }

    private fun closeTunnel() {
        try { vpnInterface?.close() } catch (failure: Throwable) { Log.e(TAG, "VPN close failed", failure) }
        vpnInterface = null
        goTunFd = -1
        activeGeneration = -1
        activeSession = null
    }

    private fun ensureForeground() {
        val manager = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            manager.createNotificationChannel(NotificationChannel(CHANNEL, "Dobby VPN", NotificationManager.IMPORTANCE_LOW))
        }
        val notification = Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setContentTitle("Dobby VPN")
            .setContentText("VPN connection is active")
            .setOngoing(true)
            .build()
        startForeground(NOTIFICATION_ID, notification)
    }
}
