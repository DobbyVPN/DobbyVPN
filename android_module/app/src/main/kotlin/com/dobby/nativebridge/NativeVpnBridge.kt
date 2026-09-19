package com.dobby.nativebridge

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.util.Log
import androidx.core.content.FileProvider
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.zip.Deflater
import java.util.zip.GZIPOutputStream

/**
 * Private JNI target used by the Go/Fyne activity. Kotlin contains only the
 * Android permission/service boundary; configuration and session policy stay
 * in the Go manager.
 */
object NativeVpnBridge {
    private const val REQUEST_VPN_PERMISSION = 4201
    private const val MAX_LOG_EXPORT_BYTES = 4 * 1024 * 1024

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

    /** Compress diagnostics and open Android's explicit share chooser. */
    @JvmStatic
    fun exportLogs(context: Context, rawLogs: ByteArray): Boolean {
        if (rawLogs.size > MAX_LOG_EXPORT_BYTES) {
            Log.e("DobbyVPN", "Log export failed: diagnostic export is too large")
            return false
        }
        val stamp = SimpleDateFormat("yyyy-MM-dd_HH-mm-ss", Locale.US).format(Date())
        // Keep concurrent exports independent. A timestamp-only name can
        // collide when two UI callbacks run in the same second and would
        // make one chooser point at the other export's contents.
        var archive: File? = null
        return try {
            val outputArchive = File.createTempFile(
                "DobbyVPN_logs_${stamp}_",
                ".jsonl.gz",
                context.cacheDir,
            )
            archive = outputArchive
            object : GZIPOutputStream(outputArchive.outputStream()) {
                init { def.setLevel(Deflater.BEST_COMPRESSION) }
            }.use { it.write(rawLogs) }
            val uri = FileProvider.getUriForFile(
                context,
                "${context.packageName}.fileprovider",
                outputArchive,
            )
            val share = Intent(Intent.ACTION_SEND)
                .setType("application/gzip")
                .putExtra(Intent.EXTRA_STREAM, uri)
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            context.startActivity(
                Intent.createChooser(share, "Export logs")
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
            true
        } catch (error: Exception) {
            // A failed chooser or FileProvider setup must not leave a private
            // archive in the cache on every attempted export. A successful
            // chooser keeps the file alive for the receiving application.
            archive?.delete()
            Log.e("DobbyVPN", "Log export failed", error)
            false
        }
    }
}
