package com.dobby.nativebridge

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.system.Os
import android.util.Log
import androidx.core.content.FileProvider
import java.io.File
import java.io.IOException
import java.io.OutputStreamWriter
import java.io.FileOutputStream
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.zip.Deflater
import java.util.zip.GZIPOutputStream
import org.json.JSONObject

/**
 * Private JNI target used by the Compose Activity and Go backend. Kotlin contains only the
 * Android permission/service boundary; configuration and session policy stay
 * in the Go manager.
 */
object NativeVpnBridge {
    internal const val VPN_PERMISSION_REQUEST = 4201
    private const val CONSENT_LAUNCH_NOT_REQUESTED = "NOT_REQUESTED"
    private const val CONSENT_LAUNCH_QUEUED = "QUEUED"
    private const val CONSENT_LAUNCH_STARTED = "STARTED"
    private const val CONSENT_LAUNCH_RETURNED = "RETURNED"
    private const val CONSENT_LAUNCH_FAILED = "FAILED"
    private const val DIAGNOSTIC_DIRECTORY = "diagnostics"
    private const val UI_DIAGNOSTIC_FILE = "ui_diagnostics.jsonl"
    private const val NATIVE_DIAGNOSTIC_FILE = "native_logs.jsonl"
    private const val SAVED_SOURCE_DIRECTORY = "configs"
    private const val SAVED_SOURCE_FILE = "connection-url.txt"
    private const val GO_DIAGNOSTIC_FILE = "go_app_logs.jsonl"
    private val diagnosticLock = Any()

    @Volatile
    private var service: DobbyVpnService? = null
    private val serviceLock = Object()

    // This state is intentionally a fixed vocabulary. It is read only by the
    // Android hosted test companion after a consent timeout; production Go
    // code receives only prepare's existing integer result.
    @Volatile
    private var consentLaunchState = CONSENT_LAUNCH_NOT_REQUESTED

    @JvmStatic
    fun consentLaunchStateForTest(): String = consentLaunchState

    @JvmStatic
    fun prepare(context: Context): Int {
        consentLaunchState = CONSENT_LAUNCH_NOT_REQUESTED
        recordDiagnostic(context, "vpn.prepare", "Android VPN preparation requested")
        val permission = VpnService.prepare(context)
        if (permission != null) {
            // JNI may call this bridge from a Go-attached thread. Android's
            // consent activity must be launched on the main thread or newer
            // releases can leave the request queued without showing a dialog.
            val launchConsent = Runnable {
                if (context is Activity) {
                    // Keep the consent activity in the caller's task. Adding
                    // FLAG_ACTIVITY_NEW_TASK to startActivityForResult can
                    // detach the system dialog from the visible Activity.
                    context.startActivityForResult(permission, VPN_PERMISSION_REQUEST)
                } else {
                    // Instrumentation and recovery callers may only have the
                    // application context. The VPN consent is process-global,
                    // so a normal task launch is sufficient; the next Connect
                    // call re-checks VpnService.prepare before starting the
                    // service.
                    context.startActivity(permission.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                }
            }
            val launched = launchConsentOnMainThread(launchConsent)
            if (!launched) {
                recordDiagnostic(context, "vpn.prepare_failed", "Android VPN consent could not be scheduled")
                return -1
            }
            recordDiagnostic(context, "vpn.permission", "Android VPN consent is required")
            return 0
        }
        val intent = Intent(context, DobbyVpnService::class.java)
            .setAction(DobbyVpnService.ACTION_PREPARE)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(intent)
        } else {
            context.startService(intent)
        }
        val ready = awaitService()
        if (!ready) {
            recordDiagnostic(context, "vpn.prepare_failed", "Android VPN service did not become ready")
        }
        return if (ready) 1 else -1
    }

    /**
     * Queue consent on Android's main thread without waiting for it.
     *
     * Go may call this bridge on an attached worker thread. Schedule the
     * permission UI on Android's main thread without waiting for the Activity
     * result; the foreground screen handles that result and retries start.
     */
    private fun launchConsentOnMainThread(launch: Runnable): Boolean {
        if (Looper.myLooper() == Looper.getMainLooper()) {
            consentLaunchState = CONSENT_LAUNCH_STARTED
            return try {
                launch.run()
                consentLaunchState = CONSENT_LAUNCH_RETURNED
                true
            } catch (error: RuntimeException) {
                consentLaunchState = CONSENT_LAUNCH_FAILED
                Log.e("DobbyVPN", "Android VPN consent launch failed", error)
                false
            }
        }

        val handler = Handler(Looper.getMainLooper())
        consentLaunchState = CONSENT_LAUNCH_QUEUED
        val posted = handler.post {
            consentLaunchState = CONSENT_LAUNCH_STARTED
            try {
                launch.run()
                consentLaunchState = CONSENT_LAUNCH_RETURNED
            } catch (error: RuntimeException) {
                consentLaunchState = CONSENT_LAUNCH_FAILED
                Log.e("DobbyVPN", "Android VPN consent launch failed", error)
            }
        }
        if (!posted) consentLaunchState = CONSENT_LAUNCH_FAILED
        return posted
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
            recordDiagnostic(context, "logs.export_started", "Diagnostic export share chooser opened")
            true
        } catch (error: Exception) {
            // A failed chooser or FileProvider setup must not leave a private
            // archive in the cache on every attempted export. A successful
            // chooser keeps the file alive for the receiving application.
            val cleanupError = try {
                val createdArchive = archive
                if (createdArchive != null && createdArchive.exists() && !createdArchive.delete()) {
                    IOException("partial diagnostic archive could not be deleted: ${createdArchive.absolutePath}")
                } else {
                    null
                }
            } catch (cleanup: Exception) {
                cleanup
            }
            recordDiagnostic(context, "logs.export_failed", "Diagnostic export chooser failed")
            Log.e("DobbyVPN", "Log export failed", error)
            if (cleanupError != null) {
                recordDiagnostic(
                    context,
                    "logs.export_cleanup_failed",
                    "Partial diagnostic archive cleanup failed",
                )
                Log.e("DobbyVPN", "Partial diagnostic archive cleanup failed", cleanupError)
            }
            false
        }
    }

    /**
     * Returns the fixed files owned by this application. The native UI reads
     * these files directly and leaves their contents intact.
     */
    @JvmStatic
    fun diagnosticPaths(context: Context): String {
        val directory = diagnosticsDirectory(context)
        recordDiagnostic(context, "startup.diagnostic_store_ready", "Android diagnostic store resolved")
        return listOf(
            File(directory, UI_DIAGNOSTIC_FILE),
            File(directory, NATIVE_DIAGNOSTIC_FILE),
            File(directory, GO_DIAGNOSTIC_FILE),
        ).joinToString("\n") { it.absolutePath }
    }

    @JvmStatic
    fun loadSourceURL(context: Context): String = try {
        File(File(context.filesDir, SAVED_SOURCE_DIRECTORY), SAVED_SOURCE_FILE)
            .takeIf { it.isFile }
            ?.readText(Charsets.UTF_8)
            .orEmpty()
    } catch (error: IOException) {
        Log.e("DobbyVPN", "Saved configuration URL could not be read", error)
        ""
    }

    @JvmStatic
    fun saveSourceURL(context: Context, value: String): Boolean {
        if (value.isBlank()) return false
        val directory = File(context.filesDir, SAVED_SOURCE_DIRECTORY)
        if (!directory.exists() && !directory.mkdirs()) return false
        val destination = File(directory, SAVED_SOURCE_FILE)
        val temporary = try {
            File.createTempFile("dobby-source-", ".tmp", directory)
        } catch (error: IOException) {
            Log.e("DobbyVPN", "Saved configuration URL temporary file could not be created", error)
            return false
        }
        return try {
            FileOutputStream(temporary).use { output ->
                output.write(value.toByteArray(Charsets.UTF_8))
                output.fd.sync()
            }
            Os.rename(temporary.absolutePath, destination.absolutePath)
            true
        } catch (error: Exception) {
            Log.e("DobbyVPN", "Saved configuration URL could not be written", error)
            false
        } finally {
            if (temporary.exists()) temporary.delete()
        }
    }

    @JvmStatic
    fun clearSourceURL(context: Context): Boolean = try {
        val file = File(File(context.filesDir, SAVED_SOURCE_DIRECTORY), SAVED_SOURCE_FILE)
        !file.exists() || file.delete()
    } catch (error: SecurityException) {
        Log.e("DobbyVPN", "Saved configuration URL could not be cleared", error)
        false
    }

    private fun diagnosticsDirectory(context: Context): File =
        File(context.applicationContext.filesDir, DIAGNOSTIC_DIRECTORY).also { it.mkdirs() }

    private fun recordDiagnostic(context: Context, event: String, message: String) {
        val record = JSONObject()
            .put("schema", "dobby.log/v1")
            .put("timestamp", isoTimestamp())
            .put("level", "INFO")
            .put("source", "android-native")
            .put("event", event)
            .put("message", message)
            .toString()
        synchronized(diagnosticLock) {
            val file = File(diagnosticsDirectory(context), NATIVE_DIAGNOSTIC_FILE)
            try {
                OutputStreamWriter(FileOutputStream(file, true), Charsets.UTF_8).use { writer ->
                    writer.append(record).append('\n')
                }
            } catch (error: Exception) {
                Log.e("DobbyVPN", "Native diagnostic write failed", error)
            }
        }
    }

    private fun isoTimestamp(): String =
        SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSXXX", Locale.US).apply {
            timeZone = java.util.TimeZone.getTimeZone("UTC")
        }.format(Date())
}
