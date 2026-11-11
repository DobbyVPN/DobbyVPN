package com.dobby.nativebridge

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Base64
import android.util.Log
import com.dobby.ui.MainActivity
import com.dobby.presentation.ConsentLaunchQueue
import com.dobby.presentation.ServiceReadinessQueue
import com.dobby.presentation.PreparationDelivery
import java.io.File
import java.io.IOException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import org.json.JSONObject

/**
 * Private JNI target used by the Compose Activity and Go backend. Kotlin contains only the
 * Android permission/service boundary; configuration and session policy stay
 * in the Go manager.
 */
object NativeVpnBridge {
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
    private const val LOGCAT_FALLBACK_CHUNK_BYTES = 1024
    private val diagnosticLock = Any()
    private val diagnosticRun = java.util.UUID.randomUUID().toString()
    private val diagnosticSequence = java.util.concurrent.atomic.AtomicLong()
    private val preparationSequence = java.util.concurrent.atomic.AtomicLong()

    @Volatile
    private var nativeDiagnosticWriteFailed = false

    @Volatile
    private var service: DobbyVpnService? = null
    private val serviceLock = Object()

    // This tracks consent launch state for the Android hosted test companion.
    @Volatile
    private var consentLaunchState = CONSENT_LAUNCH_NOT_REQUESTED

    @JvmStatic
    fun consentLaunchStateForTest(): String = consentLaunchState

    sealed class PreparationResult {
        object Ready : PreparationResult()
        object PermissionRequired : PreparationResult()
        object PermissionDenied : PreparationResult()
        object Cancelled : PreparationResult()
        data class Failed(val error: Throwable) : PreparationResult()
    }

    class Preparation internal constructor(
        internal val context: Context,
        internal val callback: (PreparationResult) -> Unit,
        internal val identity: String,
        internal val details: String,
    ) {
        internal val delivery = PreparationDelivery(PreparationResult.Cancelled as PreparationResult, callback)
        internal val canceled: Boolean get() = delivery.canceled
        fun cancel() {
            tracePreparation(this, "cancel_requested")
            delivery.markCanceled()
            mainHandler.post {
                serviceQueue.cancel(this)
                consentQueue.cancel(this)
                tracePreparation(this, "cancel_completed")
                delivery.finishCancellation()
            }
        }
    }

    // All preparation state and callbacks are confined to the main thread.
    private val mainHandler = Handler(Looper.getMainLooper())
    private val serviceQueue = ServiceReadinessQueue<Preparation, DobbyVpnService>(
        ready = { service?.isReadyForTunnel() == true },
        retiring = { service?.isRetiring() == true },
        start = {
            val context = checkNotNull(preparationContext)
            val intent = Intent(context, DobbyVpnService::class.java).setAction(DobbyVpnService.ACTION_PREPARE)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) context.startForegroundService(intent)
            else context.startService(intent)
        },
        complete = { request, error ->
            completePreparation(request, if (error == null) PreparationResult.Ready else PreparationResult.Failed(error))
        },
    )
    private var preparationContext: Context? = null
    private val consentQueue = ConsentLaunchQueue<Preparation>(
        launchOrComplete = { request ->
            if (request.canceled) false else {
                tracePreparation(request, "permission_check_begin")
                val permission = VpnService.prepare(request.context)
                if (permission == null) {
                    tracePreparation(request, "service_wait_begin")
                    preparationContext = request.context.applicationContext
                    serviceQueue.submit(request)
                    false
                } else {
                    consentLaunchState = CONSENT_LAUNCH_STARTED
                    val activity = request.context as? MainActivity
                        ?: throw IllegalStateException("VPN consent requires the product Activity result handler")
                    tracePreparation(request, "consent_launch_begin")
                    activity.launchVpnConsent(permission)
                    tracePreparation(request, "consent_launch_end")
                    consentLaunchState = CONSENT_LAUNCH_RETURNED
                    request.delivery.progress(PreparationResult.PermissionRequired)
                    true
                }
            }
        },
        returned = { request, granted ->
            tracePreparation(request, if (request.canceled) "consent_result_ignored granted=$granted" else "consent_result granted=$granted")
            if (!request.canceled) {
                if (granted) retryPreparation(request)
                else completePreparation(request, PreparationResult.PermissionDenied)
            }
        },
        discarded = { request, granted -> tracePreparation(request, "consent_result_ignored granted=$granted") },
        failed = { request, error ->
            tracePreparation(request, "failure", error)
            consentLaunchState = CONSENT_LAUNCH_FAILED
            recordServiceDiagnostic(request.context, "vpn.prepare_failed", "Android VPN preparation failed", error)
            completePreparation(request, PreparationResult.Failed(error))
        },
    )

    private fun retryPreparation(request: Preparation) { consentQueue.submit(request) }

    @JvmStatic
    fun prepare(context: Context, completed: (PreparationResult) -> Unit): Preparation =
        prepareForSelection(context, "", completed)

    fun prepareForSelection(context: Context, details: String, completed: (PreparationResult) -> Unit): Preparation {
        val request = Preparation(context, completed, "$diagnosticRun-${preparationSequence.incrementAndGet()}", details)
        tracePreparation(request, "submitted")
        val posted = mainHandler.post {
            if (!request.canceled) {
                consentLaunchState = CONSENT_LAUNCH_NOT_REQUESTED
                consentQueue.submit(request)
            }
        }
        if (!posted) {
            val error = IllegalStateException("main thread rejected VPN preparation")
            tracePreparation(request, "failure", error)
            request.delivery.finish(PreparationResult.Failed(error))
        }
        return request
    }

    @JvmStatic
    fun consentResult(granted: Boolean) { consentQueue.result(granted) }

    private fun completePreparation(request: Preparation, result: PreparationResult) {
        tracePreparation(request, "completed result=${result.javaClass.simpleName} canceled=${request.canceled}", (result as? PreparationResult.Failed)?.error)
        request.delivery.finish(result)
    }

    private fun tracePreparation(request: Preparation, phase: String, failure: Throwable? = null) {
        val owner = service?.let { System.identityHashCode(it).toString() } ?: "none"
        recordDiagnostic(request.context.applicationContext, "vpn.preparation", "operation_id=${request.identity} phase=$phase service=$owner ${request.details}", failure)
    }

    @Volatile private var lifecycleOwner: DobbyVpnService? = null

    // Called by Android on the main thread, before initialization may fail.
    internal fun serviceCreated(candidate: DobbyVpnService): Boolean {
        synchronized(serviceLock) {
            if (lifecycleOwner != null && lifecycleOwner !== candidate) return false
            if (!serviceQueue.created(candidate)) return false
            lifecycleOwner = candidate
        }
        return true
    }

    internal fun attach(candidate: DobbyVpnService) {
        synchronized(serviceLock) {
            check(lifecycleOwner === candidate) { "Another VPN service owns the lifecycle" }
            check(service == null || service === candidate) { "Previous VPN service still owns cleanup" }
            val failure = NativeGoSession.resume()
            check(failure.isEmpty()) { failure }
            service = candidate
        }
    }

    internal fun serviceReady(candidate: DobbyVpnService) {
        if (lifecycleOwner !== candidate) return
        mainHandler.post {
            if (lifecycleOwner === candidate && service === candidate) serviceQueue.ready(candidate)
        }
    }

    internal fun serviceFailed(candidate: DobbyVpnService, failure: Throwable) {
        if (lifecycleOwner !== candidate) return
        mainHandler.post {
            if (lifecycleOwner !== candidate) return@post
            serviceQueue.failed(candidate, failure)
            if (service !== candidate) {
                synchronized(serviceLock) {
                    if (lifecycleOwner === candidate) lifecycleOwner = null
                }
                serviceQueue.detached(candidate)
            }
        }
    }

    internal fun detach(candidate: DobbyVpnService) {
        val owned = synchronized(serviceLock) {
            if (lifecycleOwner !== candidate) false else {
                if (service === candidate) service = null
                true
            }
        }
        if (!owned) return
        mainHandler.post {
            if (lifecycleOwner !== candidate) return@post
            synchronized(serviceLock) {
                if (lifecycleOwner === candidate) lifecycleOwner = null
            }
            serviceQueue.detached(candidate)
        }
    }

    @JvmStatic
    fun acquireTunnel(sessionID: String, generation: Long, dnsServersJSON: String): String =
        service?.acquireTunnel(sessionID, generation, dnsServersJSON) ?: """{"error":"VPN service unavailable","cleanup_pending":false}"""

    @JvmStatic
    fun releaseTunnel(sessionID: String, generation: Long, fd: Int, timeoutMillis: Long): String =
        service?.releaseTunnel(sessionID, generation, fd, timeoutMillis) ?: """{"error":"VPN service unavailable during release","cleanup_pending":true}"""

    @JvmStatic
    fun protectSocket(sessionID: String, generation: Long, fd: Int): Boolean =
        service?.protectProtocolSocket(sessionID, generation, fd) ?: false

    @JvmStatic
    fun underlyingNetwork(sessionID: String, generation: Long): String =
        service?.underlyingNetwork(sessionID, generation) ?: """{"error":"VPN service unavailable"}"""

    @JvmStatic
    fun resolveUnderlying(sessionID: String, generation: Long, revision: String, queryID: Long, host: String, timeoutMillis: Long): String =
        service?.resolveUnderlying(sessionID, generation, revision, queryID, host, timeoutMillis)
            ?: """{"error":"VPN service unavailable during DNS query"}"""

    @JvmStatic
    fun cancelUnderlying(queryID: Long) { service?.cancelUnderlying(queryID) }

    @JvmStatic
    fun finishUnderlying(queryID: Long) { service?.finishUnderlying(queryID) }

    @JvmStatic
    fun publishState(sessionID: String, generation: Long, state: String, failureCode: String) {
        service?.publishState(sessionID, generation, state, failureCode)
    }

    @JvmStatic
    fun recordNativeFailure(context: Context, event: String, failure: Throwable) {
        try {
            recordDiagnostic(context, event, "Android native bridge operation failed", failure)
        } catch (diagnosticFailure: Throwable) {
            if (diagnosticFailure !== failure) failure.addSuppressed(diagnosticFailure)
            try {
                Log.e("DobbyVPN", "Native failure could not be stored: $event", failure)
            } catch (loggingFailure: Throwable) {
                if (loggingFailure !== failure) failure.addSuppressed(loggingFailure)
                System.err.println("Native failure could not be stored: $event\n${failure.stackTraceToString()}")
            }
        }
    }

    /** Prepare one private archive; the Activity owns its chooser and lifetime. */
    @JvmStatic
    fun prepareLogExport(context: Context): File {
        val stamp = SimpleDateFormat("yyyy-MM-dd_HH-mm-ss", Locale.US).format(Date())
        val archive = File.createTempFile("DobbyVPN_logs_${stamp}_", ".jsonl.gz", context.cacheDir)
        try {
            val metadata = JSONObject().put("app_version", com.dobby.vpn.BuildConfig.VERSION_NAME)
                .put("source_commit", com.dobby.vpn.BuildConfig.PROJECT_REPOSITORY_COMMIT)
                .put("platform", "Android ${Build.VERSION.RELEASE}")
                .put("captured_at", java.time.Instant.now().toString())
                .put("native_diagnostics_unavailable", nativeDiagnosticsUnavailable())
            val paths = diagnosticPaths(context).lineSequence().filter(String::isNotBlank).map(::File).toList()
            archive.outputStream().use { writeDiagnosticArchive(it, paths, metadata) }
            return archive
        } catch (failure: Throwable) {
            try { deleteLogExport(context, archive) } catch (cleanup: Throwable) { failure.addSuppressed(cleanup) }
            throw failure
        }
    }

    @JvmStatic
    fun ownedLogExport(context: Context, name: String?): File? {
        if (name == null || !name.startsWith("DobbyVPN_logs_") || !name.endsWith(".jsonl.gz") ||
            name != File(name).name) return null
        val cache = context.cacheDir.canonicalFile
        val file = File(cache, name)
        return if (file.canonicalFile.parentFile == cache) file else null
    }

    @JvmStatic
    fun deleteLogExport(context: Context, file: File) {
        val owned = ownedLogExport(context, file.name)
        require(owned != null && owned == file.canonicalFile) { "Unrecognized diagnostic archive" }
        if (owned.exists() && !owned.delete()) throw IOException("Diagnostic archive could not be deleted: ${owned.absolutePath}")
    }

    @JvmStatic
    fun cleanupLogExports(context: Context, preservedName: String?) {
        val files = context.cacheDir.listFiles() ?: throw IOException("Could not list diagnostic archive cache")
        for (file in files) {
            if (file.name == preservedName) continue
            val owned = ownedLogExport(context, file.name) ?: continue
            try { deleteLogExport(context, owned) } catch (failure: Exception) {
                recordDiagnostic(context, "logs.export_cleanup_failed", "Abandoned diagnostic archive cleanup failed", failure)
            }
        }
    }

    /**
     * Returns the fixed files owned by this application. The native UI reads
     * these files directly and leaves their contents intact.
     */
    @JvmStatic
    fun diagnosticPaths(context: Context): String {
        val directory = diagnosticsDirectory(context)
        return listOf(
            File(directory, UI_DIAGNOSTIC_FILE),
            File(directory, GO_DIAGNOSTIC_FILE),
        ).joinToString("\n") { it.absolutePath }
    }

    @JvmStatic
    fun goDiagnosticPath(context: Context): String =
        File(diagnosticsDirectory(context), GO_DIAGNOSTIC_FILE).absolutePath

    @JvmStatic
    fun nativeDiagnosticsUnavailable(): Boolean = nativeDiagnosticWriteFailed

    @JvmStatic
    fun sourceURLPath(context: Context): String =
        File(File(context.filesDir.canonicalFile, SAVED_SOURCE_DIRECTORY), SAVED_SOURCE_FILE).absolutePath

    private fun diagnosticsDirectory(context: Context): File =
        File(context.applicationContext.filesDir, DIAGNOSTIC_DIRECTORY).also { it.mkdirs() }

    internal fun recordAppStarted(context: Context) {
        val version = com.dobby.vpn.BuildConfig.VERSION_NAME
        val commit = com.dobby.vpn.BuildConfig.PROJECT_REPOSITORY_COMMIT.take(8)
        recordDiagnostic(context, "app.started", "app started · doBBYVPN $version · $commit")
    }

    internal fun recordServiceDiagnostic(context: Context, event: String, message: String, failure: Throwable? = null, level: String = "INFO") =
        recordDiagnostic(context, event, message, failure, level, serviceRecord = true)

    internal fun recordDiagnostic(
        context: Context,
        event: String,
        message: String,
        failure: Throwable? = null,
        level: String = "INFO",
        serviceRecord: Boolean = false,
    ) {
        synchronized(diagnosticLock) {
            val record = JSONObject()
                .put("schema", "dobby.log/v1")
                .put("timestamp", isoTimestamp())
                .put("level", if (failure == null) level else "ERROR")
                .put("source", "android-native")
                .put("process_id", android.os.Process.myPid())
                .put("run_id", diagnosticRun)
                .put("process_sequence", diagnosticSequence.incrementAndGet())
                .put("app_version", com.dobby.vpn.BuildConfig.VERSION_NAME)
                .put("source_commit", com.dobby.vpn.BuildConfig.PROJECT_REPOSITORY_COMMIT)
                .put("build_configuration", com.dobby.vpn.BuildConfig.BUILD_TYPE)
                .put("event", event)
                .put("message", message)
                .apply {
                    if (failure != null) put("error_detail", stackTrace(failure))
                }
                .toString()
            if (serviceRecord) {
                try {
                    val failure = NativeGoSession.appendServiceDiagnostic(goDiagnosticPath(context), record + "\n")
                    if (failure.isNotEmpty()) reportNativeDiagnosticWriteFailure(record, IOException(failure))
                } catch (failure: Throwable) {
                    reportNativeDiagnosticWriteFailure(record, IOException("Go diagnostic appender unavailable", failure))
                }
            } else {
                storeNativeDiagnostic(
                    { File(diagnosticsDirectory(context), UI_DIAGNOSTIC_FILE) },
                    record,
                    ::reportNativeDiagnosticWriteFailure,
                )
            }
        }
    }

    internal fun storeNativeDiagnostic(
        destination: () -> File,
        record: String,
        onFailure: (String, Exception) -> Unit,
    ) {
        try {
            DiagnosticFiles.append(destination(), (record + "\n").toByteArray(Charsets.UTF_8))
        } catch (error: Exception) {
            onFailure(record, error)
        }
    }

    private fun reportNativeDiagnosticWriteFailure(record: String, error: Exception) {
        nativeDiagnosticWriteFailed = true
        Log.e("DobbyVPN", "Native diagnostic file write failed; full record and error follow as base64 chunks")
        emitNativeDiagnosticChunks("record", record) { Log.e("DobbyVPN", it) }
        emitNativeDiagnosticChunks("write-error", error.stackTraceToString()) { Log.e("DobbyVPN", it) }
    }

    internal fun emitNativeDiagnosticChunks(kind: String, content: String, emit: (String) -> Unit) {
        val bytes = content.toByteArray(Charsets.UTF_8)
        val parts = (bytes.size + LOGCAT_FALLBACK_CHUNK_BYTES - 1) / LOGCAT_FALLBACK_CHUNK_BYTES
        val id = System.nanoTime().toString(16)
        for (index in 0 until parts) {
            val offset = index * LOGCAT_FALLBACK_CHUNK_BYTES
            val size = minOf(LOGCAT_FALLBACK_CHUNK_BYTES, bytes.size - offset)
            val encoded = Base64.encodeToString(bytes, offset, size, Base64.NO_WRAP)
            emit("Native diagnostic fallback id=$id kind=$kind part=${index + 1}/$parts utf8_base64=$encoded")
        }
    }

    private fun stackTrace(failure: Throwable): String = failure.stackTraceToString()

    private fun isoTimestamp(): String =
        SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSXXX", Locale.US).apply {
            timeZone = java.util.TimeZone.getTimeZone("UTC")
        }.format(Date())
}
