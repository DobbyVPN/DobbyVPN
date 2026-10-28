package com.dobby.nativebridge

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject

/** Android-only VPN boundary for the Go-owned session manager. */
class DobbyVpnService : VpnService() {
    companion object {
        const val ACTION_PREPARE = "com.dobby.vpn.action.PREPARE"
        private const val CHANNEL = "dobby_vpn"
        private const val NOTIFICATION_ID = 101
        private const val TAG = "DobbyVpnService"
    }

    @Volatile private var shuttingDown = false
    private var goStopConfirmed = false
    @Volatile private var restorationRunning = false
    private var operationFailure: Throwable? = null

    private fun recordFailure(event: String, failure: Throwable) {
        operationFailure = failure
        NativeVpnBridge.recordNativeFailure(this, event, failure)
    }

    private var activeSession: String? = null
    private var activeGeneration: Long = -1
    private var vpnInterface: ParcelFileDescriptor? = null
    private var underlyingNetwork: UnderlyingNetworkOwner? = null
    private var goTunFd: Int = -1
    private var lastClosedSession: String? = null
    private var lastClosedGeneration: Long = -1
    private var lastClosedFd: Int = -1
    private var lastCloseSucceeded: Boolean? = null

    override fun onCreate() {
        super.onCreate()
        try {
            NativeGoSession.attach(this)
            NativeVpnBridge.attach(this)
        } catch (failure: Throwable) {
            shuttingDown = true
            recordFailure("vpn.service.attach_failed", failure)
            stopSelf()
            return
        }
        NativeVpnBridge.recordDiagnostic(this, "vpn.service.attached", "service=${System.identityHashCode(this)}")
        Log.i(TAG, "VPN service created")
    }

    @Synchronized
    internal fun isReadyForTunnel(): Boolean = !shuttingDown

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        try {
            ensureForeground()
        } catch (failure: Throwable) {
            recordFailure("vpn.service.foreground_failed", failure)
            throw failure
        }
        if (!isReadyForTunnel()) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf(startId)
            return START_NOT_STICKY
        }
        // PREPARE belongs to an app command; Go already has that selection.
        // Android's always-on start has the VpnService action. A null intent
        // restores a killed sticky service only if the user had left it on.
        if ((intent == null || intent.action == VpnService.SERVICE_INTERFACE) && !restorationRunning) {
            restorationRunning = true
            val onlyPreviouslyEnabled = intent == null
            Thread({
                try {
                    val reply = JSONObject(NativeGoSession.restore(onlyPreviouslyEnabled))
                    check(reply.optBoolean("ok")) { reply.toString() }
                    NativeVpnBridge.recordDiagnostic(this, "vpn.service.restored", reply.toString())
                    if (reply.getJSONObject("result").optLong("generation") == 0L) {
                        stopForeground(STOP_FOREGROUND_REMOVE)
                        stopSelf(startId)
                    }
                } catch (failure: Throwable) {
                    recordFailure("vpn.service.restore_failed", failure)
                    synchronized(this) {
                        if (vpnInterface == null && underlyingNetwork == null) {
                            stopForeground(STOP_FOREGROUND_REMOVE)
                            stopSelf(startId)
                        }
                    }
                } finally {
                    synchronized(this) { restorationRunning = false }
                }
            }, "dobby-vpn-restore").start()
        }
        return START_STICKY
    }

    @Synchronized
    fun acquireTunnel(sessionID: String, generation: Long, dnsServersJSON: String): String {
        operationFailure = null
        val fd = if (isReadyForTunnel()) acquireDescriptor(sessionID, generation, dnsServersJSON) else -1
        return JSONObject().put("fd", fd).put("cleanup_pending", vpnInterface != null || underlyingNetwork != null)
            .apply { if (fd < 0) put("error", operationFailure?.stackTraceToString() ?: "VPN service is stopping") }
            .toString()
    }

    private fun acquireDescriptor(sessionID: String, generation: Long, dnsServersJSON: String): Int {
        if (activeSession != null && activeSession != sessionID) {
            recordFailure(
                "vpn.service.acquire_rejected",
                IllegalStateException("VPN tunnel is owned by a different session"),
            )
            return -1
        }
        if (activeGeneration > generation || vpnInterface != null || underlyingNetwork != null) {
            recordFailure(
                "vpn.service.acquire_rejected",
                IllegalStateException("VPN tunnel is already active or generation is stale"),
            )
            return -1
        }
        goStopConfirmed = false
        try {
            ensureForeground()
        } catch (failure: Throwable) {
            recordFailure("vpn.service.foreground_failed", failure)
            return -1
        }
        val established = try {
            val dnsServers = parseDnsServers(dnsServersJSON)
            activeSession = sessionID
            activeGeneration = generation
            val owner = UnderlyingNetworkOwner(this) {
                NativeVpnBridge.recordDiagnostic(this, "vpn.underlying_network", it)
            }
            underlyingNetwork = owner
            val physical = owner.start()
            val builder = Builder()
                .setSession("Dobby VPN")
                .setMtu(1280) // IPv6 minimum; matches the shared packet stack.
                .addAddress("10.7.0.2", 32)
                .addAddress("fd00:dbb::2", 128)
                .setUnderlyingNetworks(arrayOf(physical))
                .addRoute("0.0.0.0", 0)
                .addRoute("::", 0)
            dnsServers.forEach { builder.addDnsServer(it) }
            builder.establish()
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN interface establish failed", failure)
            recordFailure("vpn.service.establish_failed", failure)
            return -1
        } ?: run {
            recordFailure(
                "vpn.service.establish_failed",
                IllegalStateException("VpnService.Builder.establish returned null"),
            )
            return -1
        }

        activeSession = sessionID
        activeGeneration = generation
        vpnInterface = established
        goTunFd = -1
        try {
            checkNotNull(underlyingNetwork) { "Underlying network owner is unavailable" }.established()
        } catch (failure: Throwable) {
            recordFailure("vpn.service.underlying_bind_failed", failure)
            return -1
        }
        val duplicate = try {
            ParcelFileDescriptor.dup(established.fileDescriptor)
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN interface duplication failed", failure)
            recordFailure("vpn.service.duplicate_failed", failure)
            return -1
        }
        val detached = try {
            duplicate.detachFd()
        } catch (failure: Throwable) {
            try { duplicate.close() } catch (cleanupFailure: Throwable) {
                failure.addSuppressed(cleanupFailure)
            }
            Log.e(TAG, "VPN descriptor transfer failed", failure)
            recordFailure("vpn.service.detach_failed", failure)
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

    private fun parseDnsServers(dnsServersJSON: String): List<String> {
        val servers = JSONArray(dnsServersJSON)
        if (servers.length() == 0) throw IllegalArgumentException("VPN DNS server list is empty")
        return (0 until servers.length()).map { index ->
            val server = servers.get(index) as? String
                ?: throw IllegalArgumentException("VPN DNS server at index $index is not a string")
            if (server.isBlank()) throw IllegalArgumentException("VPN DNS server at index $index is empty")
            server
        }
    }

    @Synchronized
    fun releaseTunnel(sessionID: String, generation: Long, fd: Int, timeoutMillis: Long): String {
        operationFailure = null
        val released = timeoutMillis > 0 && releaseDescriptor(sessionID, generation, fd)
        return JSONObject().put("cleanup_pending", !released)
            .apply { if (!released) put("error", operationFailure?.stackTraceToString() ?: "VPN release remains pending") }
            .toString()
    }

    private fun releaseDescriptor(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID == lastClosedSession && generation == lastClosedGeneration && fd == lastClosedFd) {
            return lastCloseSucceeded ?: false
        }
        if (sessionID != activeSession || generation != activeGeneration || fd != goTunFd) {
            recordFailure(
                "vpn.service.release_rejected",
                IllegalStateException("VPN release did not match the active session, generation, and descriptor"),
            )
            return false
        }
        return closeTunnel()
    }

    @Synchronized
    fun protectProtocolSocket(sessionID: String, generation: Long, fd: Int): Boolean {
        if (sessionID != activeSession || generation != activeGeneration || fd < 0 || vpnInterface == null) {
            recordFailure(
                "vpn.service.protect_rejected",
                IllegalStateException("VPN socket protect request did not match the active session, generation, and descriptor"),
            )
            return false
        }
        return try {
            checkNotNull(underlyingNetwork) { "Underlying network owner is unavailable" }.protect(fd)
        } catch (failure: Throwable) {
            recordFailure("vpn.service.protect_failed", failure)
            throw failure
        }
    }

    private fun networkOwner(sessionID: String, generation: Long): UnderlyingNetworkOwner = synchronized(this) {
        check(sessionID == activeSession && generation == activeGeneration && !shuttingDown) {
            "Underlying network request does not match the active owner"
        }
        checkNotNull(underlyingNetwork) { "Underlying network owner is unavailable" }
    }

    private fun dnsReply(operation: () -> String): String = try { operation() } catch (failure: Throwable) {
        NativeVpnBridge.recordNativeFailure(this, "vpn.underlying_dns_failed", failure)
        JSONObject().put("error", failure.stackTraceToString()).toString()
    }

    fun underlyingNetwork(sessionID: String, generation: Long): String = dnsReply {
        networkOwner(sessionID, generation).snapshot()
    }

    fun resolveUnderlying(sessionID: String, generation: Long, revision: String, queryID: Long, host: String, timeoutMillis: Long): String = dnsReply {
        networkOwner(sessionID, generation).resolve(revision, queryID, host, timeoutMillis)
    }

    fun cancelUnderlying(queryID: Long) { synchronized(this) { underlyingNetwork }?.cancel(queryID) }
    fun finishUnderlying(queryID: Long) { synchronized(this) { underlyingNetwork }?.finish(queryID) }

    @Synchronized
    fun publishState(sessionID: String, generation: Long, state: String, failureCode: String) {
        val active = sessionID == activeSession && generation == activeGeneration
        val released = sessionID == lastClosedSession && generation == lastClosedGeneration && lastCloseSucceeded == true
        if ((!active && !released) || (vpnInterface != null && !active)) return
        val message = "Go state=$state generation=$generation failure=$failureCode"
        Log.i(TAG, message)
        NativeVpnBridge.recordDiagnostic(this, "vpn.service.state", message)
        if (vpnInterface != null) {
            try {
                ensureForeground()
            } catch (failure: Throwable) {
                recordFailure("vpn.service.foreground_failed", failure)
            }
        } else if (state == "IDLE" || state == "FAILED") {
            goStopConfirmed = released && underlyingNetwork == null
            // Explicit Stop releases the network, not this reusable Android
            // service instance. Queuing stopSelf here races an immediate Start
            // and can destroy the service after it accepts the new network.
            // Android still owns termination of the now-background service.
            stopForeground(STOP_FOREGROUND_REMOVE)
        }
    }

    override fun onRevoke() {
        shutdownOwner()
        super.onRevoke()
    }

    override fun onDestroy() {
        shutdownOwner()
        super.onDestroy()
    }

    private fun shutdownOwner() {
        val alreadyReleased = synchronized(this) {
            if (shuttingDown) return
            shuttingDown = true
            goStopConfirmed && vpnInterface == null && underlyingNetwork == null
        }
        val owner = "service=${System.identityHashCode(this)}"
        if (alreadyReleased) {
            // Go published its terminal state after releasing this network.
            // Retire synchronously before Android can create another service;
            // a delayed global Stop would otherwise stop that replacement.
            NativeVpnBridge.detach(this)
            NativeVpnBridge.recordDiagnostic(this, "vpn.service.detached", "$owner mode=confirmed_stop")
            return
        }
        // OS callbacks run on the main thread. Keep this service reachable by
        // Go's release callback until the one owner confirms cleanup.
        NativeVpnBridge.recordDiagnostic(this, "vpn.service.shutdown", "$owner mode=stop_and_wait")
        Thread({
            val failure = NativeGoSession.stopAndWait()
            if (failure.isEmpty()) {
                stopForeground(STOP_FOREGROUND_REMOVE)
                NativeVpnBridge.detach(this)
                NativeVpnBridge.recordDiagnostic(this, "vpn.service.detached", "$owner mode=stop_and_wait")
            } else {
                recordFailure("vpn.service.shutdown_failed", IllegalStateException(failure))
            }
        }, "dobby-vpn-shutdown").start()
    }

    private fun closeTunnel(): Boolean {
        if (vpnInterface == null && underlyingNetwork == null) return lastCloseSucceeded ?: true
        val closingSession = activeSession
        val closingGeneration = activeGeneration
        val closingFd = goTunFd
        val closed = try {
            vpnInterface?.close()
            underlyingNetwork?.stop()
            true
        } catch (failure: Throwable) {
            Log.e(TAG, "VPN close failed", failure)
            recordFailure("vpn.service.close_failed", failure)
            false
        }
        if (!closed) return false
        vpnInterface = null
        underlyingNetwork = null
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
            .setContentText(if (vpnInterface != null) "VPN protection is active" else "Starting VPN")
            .setOngoing(true)
            .build()
        startForeground(NOTIFICATION_ID, notification)
    }
}
