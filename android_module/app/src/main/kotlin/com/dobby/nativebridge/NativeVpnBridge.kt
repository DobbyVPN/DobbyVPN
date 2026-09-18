package com.dobby.nativebridge

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build

/**
 * Private JNI target used by the Go/Fyne activity. Kotlin contains only the
 * Android permission/service boundary; configuration and session policy stay
 * in the Go manager.
 */
object NativeVpnBridge {
    private const val REQUEST_VPN_PERMISSION = 4201

    @Volatile
    private var service: DobbyVpnService? = null
    private val serviceLock = Object()

    @JvmStatic
    fun prepare(context: Context): Int {
        val permission = VpnService.prepare(context)
        if (permission != null) {
            val launch = permission.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (context is Activity) {
                context.startActivityForResult(launch, REQUEST_VPN_PERMISSION)
            } else {
                // Instrumentation and recovery callers may only have the
                // application context. The VPN consent is process-global, so
                // a normal task launch is sufficient; the next Connect call
                // re-checks VpnService.prepare before starting the service.
                context.startActivity(launch)
            }
            return 0
        }
        val intent = Intent(context, DobbyVpnService::class.java)
            .setAction(DobbyVpnService.ACTION_PREPARE)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(intent)
        } else {
            context.startService(intent)
        }
        return if (awaitService()) 1 else -1
    }

    internal fun attach(candidate: DobbyVpnService) {
        synchronized(serviceLock) {
            service = candidate
            serviceLock.notifyAll()
        }
    }
    internal fun detach(candidate: DobbyVpnService) {
        synchronized(serviceLock) {
            if (service === candidate) service = null
            serviceLock.notifyAll()
        }
    }

    private fun awaitService(): Boolean {
        synchronized(serviceLock) {
            if (service != null) return true
            val deadline = System.nanoTime() + 2_000_000_000L
            while (service == null) {
                val remaining = deadline - System.nanoTime()
                if (remaining <= 0) return false
                val millis = remaining / 1_000_000L
                val nanos = (remaining % 1_000_000L).toInt()
                try {
                    serviceLock.wait(millis, nanos)
                } catch (_: InterruptedException) {
                    Thread.currentThread().interrupt()
                    return false
                }
            }
            return true
        }
    }

    @JvmStatic
    fun acquireTunnel(sessionID: String, generation: Long): Int =
        service?.acquireTunnel(sessionID, generation) ?: -1

    @JvmStatic
    fun releaseTunnel(sessionID: String, generation: Long, fd: Int): Boolean =
        service?.releaseTunnel(sessionID, generation, fd) ?: false

    @JvmStatic
    fun protectSocket(sessionID: String, generation: Long, fd: Int): Boolean =
        service?.protectProtocolSocket(sessionID, generation, fd) ?: false

    @JvmStatic
    fun publishState(sessionID: String, generation: Long, state: String, failureCode: String) {
        service?.publishState(sessionID, generation, state, failureCode)
    }
}
