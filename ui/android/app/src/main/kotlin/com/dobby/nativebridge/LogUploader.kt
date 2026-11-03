package com.dobby.nativebridge

import android.content.Context
import android.os.Build
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import javax.net.ssl.HttpsURLConnection
import org.json.JSONObject

/** One user-owned transfer; no redirects, retries, or provider lookup after preparation. */
internal data class LogUploadResult(val reference: String, val provider: String, val time: String, val persistenceError: String = "")

internal class LogUploader(
    private val context: Context,
    private val phase: (String) -> Unit = {},
    private val responseObserved: (Int) -> Unit = {},
    private val transferTimeoutMillis: Long = TimeUnit.MINUTES.toMillis(15),
) {
    companion object {
        private var startupCleaned = false
        @Synchronized fun cleanupStartup(context: Context) {
            if (startupCleaned) return
            val dir = File(context.cacheDir, "log-uploads")
            dir.listFiles()?.forEach { file ->
                val id = file.name.removeSuffix(".gz")
                if (file.name.endsWith(".gz") && runCatching { UUID.fromString(id).toString() == id }.getOrDefault(false)) {
                    if (!file.delete()) NativeVpnBridge.recordDiagnostic(context, "logs.upload_cleanup_failed", "Could not remove abandoned upload archive: ${file.absolutePath}", IOException("delete returned false"))
                }
            }
            startupCleaned = true
        }
    }
    private val cancelled = AtomicBoolean(false)
    @Volatile private var connection: HttpsURLConnection? = null
    fun cancel() {
        if (!cancelled.compareAndSet(false, true)) return
        val active = connection
        if (active != null) Thread({ active.disconnect() }, "dobby-log-upload-cancel").start()
    }
    private fun checkCancelled() { if (cancelled.get()) throw IOException("Log upload cancelled") }

    fun upload(sessionId: String, digest: String): LogUploadResult {
        checkCancelled()
        val response = JSONObject(NativeGoSession.prepareLogUpload(sessionId, digest))
        if (!response.optBoolean("ok")) throw IOException(response.optJSONObject("error")?.optString("message") ?: "Upload unavailable")
        val prepared = response.getJSONObject("result")
        val reference = prepared.getString("reference")
        val uuid = UUID.fromString(reference)
        require(uuid.version() == 7 && uuid.toString() == reference)
        val provider = prepared.getString("provider")
        val destination = prepared.getString("destination")
        val authorization = prepared.getString("authorization")
        val maxBytes = prepared.getLong("max_bytes")
        val dir = File(context.cacheDir, "log-uploads").also { if (!it.exists() && !it.mkdirs()) throw IOException("Cannot create upload directory") }
        val archive = File(dir, "$reference.gz")
        var originalFailure: Throwable? = null
        var ownsArchive = false
        var completed = false
        try {
            checkCancelled()
            if (!archive.createNewFile()) throw IOException("Upload archive already exists")
            ownsArchive = true
            val metadata = JSONObject().put("app_version", com.dobby.vpn.BuildConfig.VERSION_NAME)
                .put("source_commit", com.dobby.vpn.BuildConfig.PROJECT_REPOSITORY_COMMIT)
                .put("platform", "Android ${Build.VERSION.RELEASE}")
                .put("captured_at", Instant.now().toString())
                .put("native_diagnostics_unavailable", NativeVpnBridge.nativeDiagnosticsUnavailable())
            val paths = NativeVpnBridge.diagnosticPaths(context).lineSequence().filter(String::isNotBlank).map(::File).toList()
            archive.outputStream().use { writeDiagnosticArchive(it, paths, metadata, ::checkCancelled, requireComplete = true) }
            checkCancelled()
            if (archive.length() > maxBytes) throw IOException("Diagnostics archive exceeds upload size limit")
            phase("Uploading logs…")
            val http = URL(destination).openConnection() as? HttpsURLConnection ?: throw IOException("Upload destination is not HTTPS")
            connection = http
            val timer = Executors.newSingleThreadScheduledExecutor()
            val deadline = timer.schedule({ cancel() }, transferTimeoutMillis, TimeUnit.MILLISECONDS)
            try {
                checkCancelled()
                http.connectTimeout = transferTimeoutMillis.coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
                http.readTimeout = transferTimeoutMillis.coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
                http.instanceFollowRedirects = false
                http.requestMethod = "PUT"
                http.doOutput = true
                http.setFixedLengthStreamingMode(archive.length())
                http.setRequestProperty("Content-Type", "application/gzip")
                http.setRequestProperty("If-None-Match", "*")
                if (authorization.isNotEmpty()) http.setRequestProperty("Authorization", authorization)
                archive.inputStream().use { input -> http.outputStream.use { output ->
                    val buffer = ByteArray(65536)
                    while (true) { checkCancelled(); val count = input.read(buffer); if (count < 0) break; output.write(buffer, 0, count) }
                } }
                checkCancelled()
                val status = http.responseCode
                responseObserved(status)
                if (status != HttpURLConnection.HTTP_CREATED) throw IOException("Log upload returned HTTP $status")
                val time = Instant.now().toString()
                val saved = context.getSharedPreferences("log-upload", Context.MODE_PRIVATE).edit()
                    .putString("reference", reference).putString("provider", provider).putString("time", time).commit()
                completed = true
                return LogUploadResult(reference, provider, time, if (saved) "" else "Upload succeeded, but the reference could not be saved")
            } finally { deadline.cancel(false); timer.shutdownNow(); connection = null; http.disconnect() }
        } catch (failure: Throwable) {
            originalFailure = failure
            throw failure
        } finally {
            try {
                if (ownsArchive && archive.exists() && !archive.delete()) throw IOException("Could not remove upload archive: ${archive.absolutePath}")
            } catch (cleanup: Throwable) {
                NativeVpnBridge.recordDiagnostic(context, "logs.upload_cleanup_failed", "Upload archive cleanup failed", cleanup)
                if (originalFailure != null) originalFailure.addSuppressed(cleanup) else if (!completed) throw cleanup
            }
        }
    }
}
