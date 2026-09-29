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
    private var lastClosedSession: String? = null
    private var lastClosedGeneration: Long = -1
    private var lastClosedFd: Int = -1
    private var lastCloseSucceeded: Boolean? = null

    override fun onCreate() {
        super.onCreate()
        try {
            NativeVpnBridge.attach(this)
        } catch (failure: Throwable) {
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.attach_failed", failure)
            throw failure
        }
        Log.i(TAG, "VPN service created")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_PREPARE || intent?.action == null) {
            try {
                ensureForeground()
            } catch (failure: Throwable) {
                NativeVpnBridge.recordNativeFailure(this, "vpn.service.foreground_failed", failure)
                throw failure
            }
        }
        return START_NOT_STICKY
    }

    @Synchronized
    fun acquireTunnel(sessionID: String, generation: Long): Int {
        if (activeSession != null && activeSession != sessionID) {
            NativeVpnBridge.recordNativeFailure(
                this, "vpn.service.acquire_rejected",
                IllegalStateException("VPN tunnel is owned by a different session"),
            )
            return -1
        }
        if (activeGeneration > generation || vpnInterface != null) {
            NativeVpnBridge.recordNativeFailure(
                this, "vpn.service.acquire_rejected",
                IllegalStateException("VPN tunnel is already active or generation is stale"),
            )
            return -1
        }
        try {
            ensureForeground()
        } catch (failure: Throwable) {
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.foreground_failed", failure)
            throw failure
        }
        val established = try {
            Builder()
                .setSession("Dobby VPN")
                .setMtu(1500)
                .addAddress("10.7.0.2", 32)
                .addRoute("0.0.0.0", 0)
                // Keep IPv6 fail-closed: the shared tunnel router blocks IPv6
                // destinations until the protocol engines can carry them.
                .addRoute("::", 0)
                // The tunnel router currently blocks IPv6 destinations, so
                // advertise only a resolver it can reach through the tunnel.
                .addDnsServer("1.1.1.1")
                .establish()
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN interface establish failed", failure)
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.establish_failed", failure)
            return -1
        } ?: run {
            NativeVpnBridge.recordNativeFailure(
                this, "vpn.service.establish_failed",
                IllegalStateException("VpnService.Builder.establish returned null"),
            )
            return -1
        }

        val duplicate = try {
            ParcelFileDescriptor.dup(established.fileDescriptor)
        } catch (failure: Throwable) {
            try { established.close() } catch (cleanupFailure: Throwable) {
                failure.addSuppressed(cleanupFailure)
            }
            Log.e(TAG, "VPN interface duplication failed", failure)
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.duplicate_failed", failure)
            return -1
        }
        val detached = try {
            duplicate.detachFd()
        } catch (failure: Throwable) {
            try { duplicate.close() } catch (cleanupFailure: Throwable) {
                failure.addSuppressed(cleanupFailure)
            }
            try { established.close() } catch (cleanupFailure: Throwable) {
                failure.addSuppressed(cleanupFailure)
            }
            Log.e(TAG, "VPN descriptor transfer failed", failure)
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.detach_failed", failure)
            return -1
        }
        activeSession = sessionID
        activeGeneration = generation
        vpnInterface = established
        goTunFd = detached
        lastClosedSession = null
        lastClosedGeneration = -1
        lastClosedFd = -1
        lastCloseSucceeded = null
        return detached
    }

    @Synchronized
    fun releaseTunnel(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID == lastClosedSession && generation == lastClosedGeneration && fd == lastClosedFd) {
            return lastCloseSucceeded ?: false
        }
        if (sessionID != activeSession || generation != activeGeneration || fd != goTunFd) {
            NativeVpnBridge.recordNativeFailure(
                this, "vpn.service.release_rejected",
                IllegalStateException("VPN release did not match the active session, generation, and descriptor"),
            )
            return false
        }
        return closeTunnel()
    }

    @Synchronized
    fun protectProtocolSocket(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID != activeSession || generation != activeGeneration || fd < 0) {
            NativeVpnBridge.recordNativeFailure(
                this, "vpn.service.protect_rejected",
                IllegalStateException("VPN socket protect request did not match the active session, generation, and descriptor"),
            )
            return false
        }
        return try {
            protect(fd).also { protected ->
                if (!protected) NativeVpnBridge.recordNativeFailure(
                    this, "vpn.service.protect_failed",
                    IllegalStateException("VpnService.protect returned false"),
                )
            }
        } catch (failure: Throwable) {
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.protect_failed", failure)
            throw failure
        }
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

    private fun closeTunnel(): Boolean {
        if (vpnInterface == null) return lastCloseSucceeded ?: true
        val closingSession = activeSession
        val closingGeneration = activeGeneration
        val closingFd = goTunFd
        val closed = try {
            vpnInterface?.close()
            true
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN close failed", failure)
            NativeVpnBridge.recordNativeFailure(this, "vpn.service.close_failed", failure)
            false
        }
        vpnInterface = null
        goTunFd = -1
        activeGeneration = -1
        activeSession = null
        lastClosedSession = closingSession
        lastClosedGeneration = closingGeneration
        lastClosedFd = closingFd
        lastCloseSucceeded = closed
        return closed
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
