package com.dobby.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.ClipDescription
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.Process
import android.os.SystemClock
import android.os.Trace
import android.util.Log
import android.view.View
import android.view.ViewTreeObserver
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.toArgb
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.dobby.nativebridge.LogUploader
import com.dobby.nativebridge.NativeGoSession
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.presentation.ConsentRecovery
import com.dobby.presentation.ConsentRecoveryOutcome
import com.dobby.presentation.LoadTrigger
import com.dobby.presentation.PendingConsent
import com.dobby.presentation.LogFollowState
import com.dobby.presentation.PermissionSnapshot
import com.dobby.presentation.PermissionTarget
import com.dobby.presentation.PermissionTargetFence
import com.dobby.presentation.SubscriptionLoadCoordinator
import com.dobby.presentation.CallbackLifetime
import com.dobby.presentation.SelectionAdmission
import com.dobby.presentation.SubscriptionRequest
import com.dobby.presentation.UiOperationDiagnostics
import com.dobby.vpn.BuildConfig
import com.dobby.vpn.R
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

private const val SOURCE_EDITOR_STATE = "com.dobby.ui.SOURCE_EDITOR_STATE"
private const val SOURCE_TEXT = "source"
private const val SOURCE_DIRTY = "dirty"
private const val SOURCE_LOAD_ERROR = "load_error"
private const val SOURCE_LOAD_PENDING = "load_pending"

internal data class FrozenLogViewState(
    val entries: List<LogEntry>,
    val scrollY: Int,
    val clearBoundary: Map<String, Long>,
)

internal class RetainedLogViewModel : ViewModel() {
    var frozenView: FrozenLogViewState? = null
    val exportWorker = Executors.newSingleThreadExecutor()
    var exportPreparing = false
    var exportFile: File? = null
    var exportLaunched = false
    var exportReturned = false
    var exportDeparted = false
    var exportOwnerCleared = false
    var exportContext: android.content.Context? = null
    override fun onCleared() {
        exportOwnerCleared = true
        val context = exportContext
        val archive = exportFile
        exportFile = null
        if (context != null && archive != null) {
            try { NativeVpnBridge.deleteLogExport(context, archive) }
            catch (failure: Exception) { NativeVpnBridge.recordDiagnostic(context, "logs.export_cleanup_failed", "Diagnostic archive owner cleanup failed", failure) }
        }
        exportWorker.shutdown()
    }
}

class MainActivity : ComponentActivity() {
    companion object {
        private const val LOG_VIEW_STATE = "com.dobby.ui.LOG_VIEW_STATE"
        private const val DISMISSED_ERRORS = "com.dobby.ui.DISMISSED_ERRORS"
        private const val PENDING_CONSENT = "com.dobby.ui.PENDING_CONSENT"
        private const val EXPORT_NAME = "com.dobby.ui.EXPORT_NAME"
        private const val EXPORT_LAUNCHED = "com.dobby.ui.EXPORT_LAUNCHED"
        private const val EXPORT_RETURNED = "com.dobby.ui.EXPORT_RETURNED"
        private const val EXPORT_DEPARTED = "com.dobby.ui.EXPORT_DEPARTED"
        internal const val CONNECTION_CONTROLS_DESCRIPTION = "Connection controls"

        @Volatile
        @JvmField
        var current: MainActivity? = null
    }

    private val startupDiagnostics = UiOperationDiagnostics(
        clockNs = { SystemClock.elapsedRealtimeNanos() },
        emit = ::recordStartupObservation,
        beginTrace = { operation -> Trace.beginSection("DobbyVPN.$operation") },
        endTrace = { Trace.endSection() },
        reportFailure = ::reportStartupDiagnosticFailure,
    )
    private lateinit var controller: SessionController
    private lateinit var retainedLogViewModel: RetainedLogViewModel
    private var startupDiagnosticsDisposed = false
    private var firstFrameView: View? = null
    private var firstPreDrawListener: ViewTreeObserver.OnPreDrawListener? = null
    private var firstOnDrawListener: ViewTreeObserver.OnDrawListener? = null
    private var firstPreDrawSeen = false
    private var firstOnDrawSeen = false
    private var activityResumed = false
    private val exportLauncher = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        retainedLogViewModel.exportReturned = true
        advanceLogExport()
    }
    private val vpnConsentLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        val granted = result.resultCode == android.app.Activity.RESULT_OK
        // Always release the bridge's consent slot; a recreated Activity's request was cancelled.
        NativeVpnBridge.consentResult(granted)
        if (::controller.isInitialized) controller.consentReturned(granted)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        val startedNs = SystemClock.elapsedRealtimeNanos()
        val pid = Process.myPid()
        val tid = Process.myTid()
        var traceStarted = false
        try {
            Trace.beginSection("DobbyVPN.onWindowFocusChanged hasFocus=$hasFocus")
            traceStarted = true
        } catch (diagnosticFailure: Exception) {
            reportFocusDiagnosticFailure("Trace.beginSection", diagnosticFailure)
        }

        var callbackFailure: Throwable? = null
        try {
            recordWindowFocusDiagnostic("entry", hasFocus, pid, tid, startedNs)
            super.onWindowFocusChanged(hasFocus)
            if (!hasFocus && ::retainedLogViewModel.isInitialized && retainedLogViewModel.exportLaunched)
                retainedLogViewModel.exportDeparted = true
            if (hasFocus && ::retainedLogViewModel.isInitialized) advanceLogExport()
        } catch (failure: Throwable) {
            callbackFailure = failure
            throw failure
        } finally {
            val finishedNs = SystemClock.elapsedRealtimeNanos()
            val failure = callbackFailure
            try {
                recordWindowFocusDiagnostic(
                    if (failure == null) "complete" else "error",
                    hasFocus,
                    pid,
                    tid,
                    startedNs,
                    finishedNs,
                    failure,
                )
            } finally {
                if (traceStarted) {
                    try {
                        Trace.endSection()
                    } catch (diagnosticFailure: Exception) {
                        reportFocusDiagnosticFailure("Trace.endSection", diagnosticFailure, failure)
                    }
                }
            }
        }
    }

    private fun recordWindowFocusDiagnostic(
        phase: String,
        hasFocus: Boolean,
        pid: Int,
        tid: Int,
        startedNs: Long,
        finishedNs: Long? = null,
        callbackFailure: Throwable? = null,
    ) {
        try {
            val message = buildString {
                append("callback=MainActivity.onWindowFocusChanged phase=$phase")
                append(" has_focus=$hasFocus pid=$pid tid=$tid")
                append(" started_elapsed_realtime_ns=$startedNs")
                if (finishedNs != null) {
                    append(" finished_elapsed_realtime_ns=$finishedNs")
                    append(" duration_ns=${finishedNs - startedNs}")
                }
            }
            NativeVpnBridge.recordDiagnostic(this, "ui.focus", message, callbackFailure, level = "DEBUG")
        } catch (diagnosticFailure: Exception) {
            reportFocusDiagnosticFailure("ui.focus $phase", diagnosticFailure, callbackFailure)
        }
    }

    private fun reportFocusDiagnosticFailure(
        operation: String,
        diagnosticFailure: Exception,
        callbackFailure: Throwable? = null,
    ) {
        if (callbackFailure != null && callbackFailure !== diagnosticFailure) {
            callbackFailure.addSuppressed(diagnosticFailure)
        }
        try {
            Log.e("DobbyVPN", "Android window-focus diagnostic failed during $operation", diagnosticFailure)
        } catch (loggingFailure: Exception) {
            if (callbackFailure != null && callbackFailure !== loggingFailure) {
                callbackFailure.addSuppressed(loggingFailure)
            }
            System.err.println(
                "Android window-focus diagnostic failed during $operation: " +
                    diagnosticFailure.stackTraceToString() + " (Logcat fallback failed: " +
                    loggingFailure.stackTraceToString() + ")",
            )
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        startupDiagnostics.observe("MainActivity.onCreate") {
            startupDiagnostics.observe("MainActivity.onCreate.super") {
                super.onCreate(savedInstanceState)
            }
            enableEdgeToEdge(
                statusBarStyle = SystemBarStyle.light(Color.TRANSPARENT, Color.TRANSPARENT),
                navigationBarStyle = SystemBarStyle.light(Color.TRANSPARENT, Color.TRANSPARENT),
            )
            current = this
            val backendFailure = try {
                startupDiagnostics.observe("MainActivity.onCreate.nativeGoSessionAttach") {
                    NativeGoSession.attach(this)
                }
                null
            } catch (failure: Throwable) {
                failure
            }
            if (backendFailure != null) {
                NativeVpnBridge.recordNativeFailure(this, "go.attach_failed", backendFailure)
            }
            retainedLogViewModel = ViewModelProvider(this)[RetainedLogViewModel::class.java]
            retainedLogViewModel.exportContext = applicationContext
            if (retainedLogViewModel.exportFile == null && !retainedLogViewModel.exportPreparing) {
                savedInstanceState?.getString(EXPORT_NAME)?.let { name ->
                    val restored = NativeVpnBridge.ownedLogExport(this, name)
                    if (restored != null) {
                        retainedLogViewModel.exportFile = restored
                        retainedLogViewModel.exportLaunched = savedInstanceState?.getBoolean(EXPORT_LAUNCHED) == true
                        retainedLogViewModel.exportReturned = savedInstanceState?.getBoolean(EXPORT_RETURNED) == true
                        retainedLogViewModel.exportDeparted = savedInstanceState?.getBoolean(EXPORT_DEPARTED) == true
                    }
                }
            }
            if (!retainedLogViewModel.exportPreparing) {
                try { NativeVpnBridge.cleanupLogExports(this, retainedLogViewModel.exportFile?.name) }
                catch (failure: Exception) { NativeVpnBridge.recordDiagnostic(this, "logs.export_cleanup_failed", "Diagnostic archive cleanup failed", failure) }
            }
            startupDiagnostics.observe("MainActivity.onCreate.sessionControllerInitialization") {
                controller = SessionController(
                    this,
                    savedInstanceState?.getBundle(LOG_VIEW_STATE),
                    savedInstanceState?.getBundle(SOURCE_EDITOR_STATE),
                    retainedLogViewModel,
                    savedInstanceState?.getStringArrayList(DISMISSED_ERRORS).orEmpty().toSet(),
                    restoredConsent = PendingConsent.decode(savedInstanceState?.getString(PENDING_CONSENT)),
                    backendAvailable = backendFailure == null,
                    backendUnavailableMessage = backendFailure?.message.orEmpty(),
                )
                controller.setExportingLogs(retainedLogViewModel.exportPreparing || retainedLogViewModel.exportFile != null)
                if (savedInstanceState == null && intent?.action == Intent.ACTION_VIEW) {
                    controller.importLink(intent.dataString.orEmpty())
                }
            }
            startupDiagnostics.observe("MainActivity.onCreate.setContent") {
                setContent { DobbyApp(controller) }
            }
            installFirstFrameDiagnostics()
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::retainedLogViewModel.isInitialized) {
            retainedLogViewModel.exportFile?.let { outState.putString(EXPORT_NAME, it.name) }
            outState.putBoolean(EXPORT_LAUNCHED, retainedLogViewModel.exportLaunched)
            outState.putBoolean(EXPORT_RETURNED, retainedLogViewModel.exportReturned)
            outState.putBoolean(EXPORT_DEPARTED, retainedLogViewModel.exportDeparted)
        }
        if (::controller.isInitialized) {
            controller.saveLogViewState()?.let { outState.putBundle(LOG_VIEW_STATE, it) }
            controller.saveSourceEditorState()?.let { outState.putBundle(SOURCE_EDITOR_STATE, it) }
                ?: outState.remove(SOURCE_EDITOR_STATE)
            outState.putStringArrayList(DISMISSED_ERRORS, ArrayList(controller.dismissedErrors))
            controller.pendingConsent()?.let { outState.putString(PENDING_CONSENT, it.encode()) }
                ?: outState.remove(PENDING_CONSENT)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.action == Intent.ACTION_VIEW) controller.importLink(intent.dataString.orEmpty())
    }

    override fun onStart() {
        startupDiagnostics.observe("MainActivity.onStart") {
            super.onStart()
            if (::controller.isInitialized) controller.setVisible(true)
        }
    }

    override fun onResume() {
        startupDiagnostics.observe("MainActivity.onResume") {
            super.onResume()
            activityResumed = true
            if (::retainedLogViewModel.isInitialized) advanceLogExport()
        }
    }

    override fun onStop() {
        if (::controller.isInitialized) controller.setVisible(false)
        super.onStop()
    }

    override fun onPause() {
        activityResumed = false
        if (::retainedLogViewModel.isInitialized && retainedLogViewModel.exportLaunched)
            retainedLogViewModel.exportDeparted = true
        super.onPause()
    }

    internal fun beginLogExport() {
        val owner = retainedLogViewModel
        if (owner.exportPreparing || owner.exportFile != null) return
        owner.exportPreparing = true
        controller.setExportingLogs(true)
        val app = applicationContext
        try { owner.exportWorker.execute {
            val result = runCatching { NativeVpnBridge.prepareLogExport(app) }
            Handler(Looper.getMainLooper()).post {
                owner.exportPreparing = false
                val archive = result.getOrNull()
                if (owner.exportOwnerCleared && archive != null) {
                    try { NativeVpnBridge.deleteLogExport(app, archive) }
                    catch (failure: Exception) { NativeVpnBridge.recordDiagnostic(app, "logs.export_cleanup_failed", "Late diagnostic archive cleanup failed", failure) }
                    return@post
                }
                owner.exportFile = archive
                val activity = current?.takeIf { it.retainedLogViewModel === owner }
                if (result.isFailure) {
                    NativeVpnBridge.recordDiagnostic(app, "logs.export_failed", "Diagnostic archive preparation failed", result.exceptionOrNull())
                    activity?.controller?.setExportingLogs(false, "Android could not export diagnostics. See logs for details.")
                } else activity?.advanceLogExport()
            }
        } } catch (failure: java.util.concurrent.RejectedExecutionException) {
            owner.exportPreparing = false
            NativeVpnBridge.recordDiagnostic(app, "logs.export_failed", "Diagnostic archive preparation could not start", failure)
            controller.setExportingLogs(false, "Android could not export diagnostics. See logs for details.")
        }
    }

    private fun advanceLogExport() {
        if (!activityResumed || !hasWindowFocus() || !::controller.isInitialized) return
        val owner = retainedLogViewModel
        val archive = owner.exportFile ?: return
        if (owner.exportReturned && owner.exportDeparted) {
            releaseLogExport(archive)
        } else if (!owner.exportLaunched) {
            try {
                val uri = FileProvider.getUriForFile(this, "$packageName.fileprovider", archive)
                val share = Intent(Intent.ACTION_SEND).setType("application/gzip")
                    .putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                share.clipData = ClipData.newUri(contentResolver, "DobbyVPN logs", uri)
                owner.exportLaunched = true
                exportLauncher.launch(Intent.createChooser(share, "Export logs"))
                NativeVpnBridge.recordDiagnostic(this, "logs.export_started", "Diagnostic export share chooser opened")
            } catch (failure: Exception) {
                NativeVpnBridge.recordDiagnostic(this, "logs.export_failed", "Diagnostic export chooser failed", failure)
                releaseLogExport(archive, "Android could not export diagnostics. See logs for details.")
            }
        }
    }

    private fun releaseLogExport(archive: File, message: String = "") {
        try { NativeVpnBridge.deleteLogExport(this, archive) }
        catch (failure: Exception) { NativeVpnBridge.recordDiagnostic(this, "logs.export_cleanup_failed", "Diagnostic archive cleanup failed", failure) }
        retainedLogViewModel.exportFile = null
        retainedLogViewModel.exportLaunched = false
        retainedLogViewModel.exportReturned = false
        retainedLogViewModel.exportDeparted = false
        controller.setExportingLogs(false, message)
    }

    internal fun launchVpnConsent(permission: Intent) {
        vpnConsentLauncher.launch(permission)
    }

    override fun onDestroy() {
        try {
            if (::controller.isInitialized) controller.close()
            if (current === this) current = null
            super.onDestroy()
        } finally {
            cleanupFirstFrameDiagnostics()
        }
    }

    private fun recordStartupObservation(observation: UiOperationDiagnostics.Observation) {
        val mainThread = Looper.myLooper() === Looper.getMainLooper()
        val observerBoundary = observation.operation.contains("Callback")
        val mainQueueReturn = observation.operation.endsWith("firstFrameMainQueueReturn")
        val finishedNs = observation.finishedNs
        val message = buildString {
            append("operation=${observation.operation} phase=${observation.phase}")
            append(" outcome=${when (observation.phase) {
                "entry" -> "running"
                "complete" -> "complete"
                "error" -> "error"
                else -> observation.phase
            }}")
            append(" pid=${Process.myPid()} tid=${Process.myTid()} is_main_thread=$mainThread")
            append(" started_elapsed_realtime_ns=${observation.startedNs.takeIf { it >= 0 } ?: "unavailable"}")
            if (finishedNs != null) {
                append(" finished_elapsed_realtime_ns=$finishedNs")
                if (observation.startedNs >= 0) {
                    append(" duration_ns=${finishedNs - observation.startedNs}")
                } else {
                    append(" duration_ns=unavailable")
                }
            } else if (observation.phase != "entry") {
                append(" finished_elapsed_realtime_ns=unavailable duration_ns=unavailable")
            }
            if (observerBoundary) append(" measurement=observer_callback_boundary gpu_wait_included=false")
            if (mainQueueReturn) append(" measurement=main_queue_return_boundary gpu_wait_included=false")
        }
        NativeVpnBridge.recordDiagnostic(this, "ui.startup", message, observation.failure, level = "DEBUG")
    }

    private fun reportStartupDiagnosticFailure(
        context: String,
        diagnosticFailure: Throwable,
        primaryFailure: Throwable?,
    ) {
        try {
            Log.e("DobbyVPN", "Android startup diagnostic failed during $context", diagnosticFailure)
        } catch (loggingFailure: Throwable) {
            if (diagnosticFailure !== loggingFailure) {
                try {
                    diagnosticFailure.addSuppressed(loggingFailure)
                } catch (_: Throwable) {
                }
            }
            if (primaryFailure != null && primaryFailure !== loggingFailure) {
                try {
                    primaryFailure.addSuppressed(loggingFailure)
                } catch (_: Throwable) {
                }
            }
            try {
                System.err.println(
                    "Android startup diagnostic failed during $context: " +
                        diagnosticFailure.stackTraceToString() + " (Log.e fallback failed: " +
                        loggingFailure.stackTraceToString() + ")",
                )
            } catch (_: Throwable) {
            }
        }
    }

    private fun safelyObserveStartupCallback(operation: String, callback: () -> Unit) {
        try {
            startupDiagnostics.observe(operation, callback)
        } catch (failure: Throwable) {
            reportStartupDiagnosticFailure("observer callback $operation", failure, null)
        }
    }

    private fun installFirstFrameDiagnostics() {
        if (startupDiagnosticsDisposed) return
        try {
            val decor = window.decorView
            val observer = decor.viewTreeObserver
            if (!observer.isAlive) {
                reportStartupDiagnosticFailure(
                    "installing first-frame callbacks",
                    IllegalStateException("decor ViewTreeObserver is not alive"),
                    null,
                )
                return
            }
            val preDrawListener = ViewTreeObserver.OnPreDrawListener {
                if (startupDiagnosticsDisposed || firstPreDrawSeen) return@OnPreDrawListener true
                firstPreDrawSeen = true
                safelyObserveStartupCallback("MainActivity.firstPreDrawCallback") { removeFirstPreDrawListener() }
                true
            }
            val onDrawListener = ViewTreeObserver.OnDrawListener {
                if (!startupDiagnosticsDisposed && !firstOnDrawSeen) {
                    firstOnDrawSeen = true
                    safelyObserveStartupCallback("MainActivity.firstOnDrawCallback") {
                        postFirstFrameMainQueueReturn(decor)
                    }
                }
            }
            firstFrameView = decor
            firstPreDrawListener = preDrawListener
            observer.addOnPreDrawListener(preDrawListener)
            firstOnDrawListener = onDrawListener
            observer.addOnDrawListener(onDrawListener)
        } catch (failure: Throwable) {
            cleanupFirstFrameDiagnostics()
            reportStartupDiagnosticFailure("installing first-frame callbacks", failure, null)
        }
    }

    private fun postFirstFrameMainQueueReturn(decor: View) {
        try {
            val posted = decor.post {
                if (!startupDiagnosticsDisposed) {
                    safelyObserveStartupCallback("MainActivity.firstFrameMainQueueReturn") { }
                }
                removeFirstOnDrawListener()
            }
            if (!posted) {
                reportStartupDiagnosticFailure(
                    "posting first main-queue-return callback",
                    IllegalStateException("decor.post returned false"),
                    null,
                )
            }
        } catch (failure: Throwable) {
            reportStartupDiagnosticFailure("posting first main-queue-return callback", failure, null)
        }
    }

    private fun removeFirstPreDrawListener() {
        val view = firstFrameView
        val listener = firstPreDrawListener
        if (view != null && listener != null) {
            try {
                val observer = view.viewTreeObserver
                if (observer.isAlive) {
                    observer.removeOnPreDrawListener(listener)
                    firstPreDrawListener = null
                }
            } catch (failure: Throwable) {
                reportStartupDiagnosticFailure("removing first pre-draw listener", failure, null)
            }
        }
    }

    private fun removeFirstOnDrawListener() {
        val view = firstFrameView
        val listener = firstOnDrawListener
        if (view != null && listener != null) {
            try {
                val observer = view.viewTreeObserver
                if (observer.isAlive) {
                    observer.removeOnDrawListener(listener)
                    firstOnDrawListener = null
                }
            } catch (failure: Throwable) {
                reportStartupDiagnosticFailure("removing first on-draw listener", failure, null)
            }
        }
    }

    private fun cleanupFirstFrameDiagnostics() {
        startupDiagnosticsDisposed = true
        removeFirstPreDrawListener()
        removeFirstOnDrawListener()
        firstFrameView = null
    }
}

internal data class ProfileData(val index: Int, val description: String, val protocol: String) {
    val name: String get() = description.ifBlank { "Profile ${index + 1}" }
}
internal data class SelectionData(val digest: String, val mode: String, val index: Int)

internal enum class ProfileStatus(val label: String) {
    CONNECTING("connecting"), CONNECTED("connected"), RECONNECTING("reconnecting"), STOPPING("stopping"), FAILED("failed")
}

private data class PendingPermissionAction(
    val session: SessionData,
    val selection: SelectionData,
    val fence: PermissionTarget,
    val retry: Boolean = false,
)
internal data class SessionData(
    val sessionId: String = "",
    val sequence: Long = 0,
    val generation: Long = 0,
    val networkGeneration: ULong = 0uL,
    val vpnActive: Boolean = false,
    val canRetry: Boolean = false,
    val state: String = "IDLE",
    val primaryAction: String = "NONE",
    val configured: Boolean = false,
    val logsUploadAvailable: Boolean = false,
    val sourceUrl: String = "",
    val digest: String = "",
    val profiles: List<ProfileData> = emptyList(),
    val activeDigest: String = "",
    val activeMode: String = "",
    val activeIndex: Int = 0,
    val activeProfileIndex: Int = -1,
    val pendingTarget: SelectionData? = null,
    val canSwitch: Boolean = false,
    val activeProfile: ProfileData? = null,
    val recovering: Boolean = false,
    val cleanupComplete: Boolean = true,
    val failure: String = "",
    val sourceError: String = "",
) {
    val protectedFailure: Boolean
        get() = state == "FAILED" && vpnActive && canRetry && primaryAction == "RETRY" && !cleanupComplete
}

private fun SessionData.permissionSnapshot(
    sourceRevision: Long,
    sourceDirty: Boolean,
) = PermissionSnapshot(
    sessionId = sessionId,
    digest = digest,
    generation = generation,
    sourceRevision = sourceRevision,
    sourceDirty = sourceDirty,
    hasPendingTarget = pendingTarget != null,
    primaryAction = primaryAction,
    canSwitch = canSwitch,
)

internal data class ScreenState(
    val session: SessionData = SessionData(),
    val source: String = "",
    val sourceDirty: Boolean = false,
    val loading: Boolean = false,
    val loadError: String = "",
    val canPaste: Boolean = false,
    val busy: Boolean = false,
    val error: String = "",
    val logs: List<LogEntry> = emptyList(),
    val clearRevision: Int = 0,
    val logsError: String = "",
    val uploadError: String = "",
    val exportingLogs: Boolean = false,
    val uploadingLogs: Boolean = false,
    val uploadPhase: String = "",
    val uploadedReference: String = "",
    val uploadedProvider: String = "",
    val uploadedTime: String = "",
    val showUploadSuccess: Boolean = false,
)

internal class SessionController(
    private val activity: MainActivity,
    private var restoredLogViewState: Bundle? = null,
    restoredSourceEditorState: Bundle? = null,
    private val retainedLogViewModel: RetainedLogViewModel,
    restoredDismissedErrors: Set<String> = emptySet(),
    backendAvailable: Boolean = true,
    backendUnavailableMessage: String = "",
    restoredConsent: PendingConsent? = null,
) {
    private val consentRecovery = ConsentRecovery(restoredConsent)
    private var consentAwaiting = false
    @Volatile private var backendAvailable = backendAvailable
    @Volatile private var backendUnavailableMessage = backendUnavailableMessage

    var state by mutableStateOf(ScreenState(
        source = restoredSourceEditorState?.getString(SOURCE_TEXT).orEmpty(),
        sourceDirty = restoredSourceEditorState?.getBoolean(SOURCE_DIRTY) == true,
        uploadedReference = activity.getSharedPreferences("log-upload", android.content.Context.MODE_PRIVATE).getString("reference", "").orEmpty(),
        uploadedProvider = activity.getSharedPreferences("log-upload", android.content.Context.MODE_PRIVATE).getString("provider", "").orEmpty(),
        uploadedTime = activity.getSharedPreferences("log-upload", android.content.Context.MODE_PRIVATE).getString("time", "").orEmpty(),
        loadError = restoredSourceEditorState?.getString(SOURCE_LOAD_ERROR).orEmpty(),
    ))
        private set

    val errors: List<String> get() =
        listOf(state.loadError, state.error, state.session.failure, state.logsError, state.uploadError).filter(String::isNotEmpty).distinct()

    /** Messages the user closed; they stay hidden until they clear or the user makes a new attempt. */
    var dismissedErrors by mutableStateOf(restoredDismissedErrors)
        private set

    private val main = Handler(Looper.getMainLooper())
    private val lifetime = CallbackLifetime { action -> main.post { action() } }
    private val closed: Boolean get() = lifetime.isClosed
    private fun post(action: () -> Unit) = lifetime.post(action)
    private var preparation: NativeVpnBridge.Preparation? = null
    private fun cancelPreparation() { preparation?.cancel(); preparation = null; consentAwaiting = false }
    private val worker = Executors.newSingleThreadScheduledExecutor()
    private val logWorker = Executors.newSingleThreadScheduledExecutor()
    private val uploadWorker = Executors.newSingleThreadExecutor()
    private var uploader: LogUploader? = null
    @Volatile private var visible = false
    @Volatile private var latest = SessionData()
    private var permissionTarget: PendingPermissionAction? = null
    private val selectionAdmission = SelectionAdmission<PendingPermissionAction>()
    private val loadWorker = Executors.newSingleThreadExecutor()
    private val sourceLoads = SubscriptionLoadCoordinator(
        schedule = { delayMillis, callback ->
            val task = Runnable(callback)
            main.postDelayed(task, delayMillis)
            ({ main.removeCallbacks(task) })
        },
        sessionReady = { latest.sessionId.isNotEmpty() },
        start = ::startSourceLoad,
    )
    private var restoredLoad = ""
    private var acceptedSequence = 0L
    private var restoredPendingSource = restoredSourceEditorState
        ?.takeIf { it.getBoolean(SOURCE_LOAD_PENDING) && it.getString(SOURCE_LOAD_ERROR).isNullOrEmpty() }
        ?.getString(SOURCE_TEXT)
    @Volatile private var retainedLogViewState = retainedLogViewModel.frozenView
    @Volatile private var retainedLogViewValid: Boolean? = null
    @Volatile private var latestClearBoundary: Map<String, Long>? = null
    private val clipboard = activity.getSystemService(ClipboardManager::class.java)
    private val clipboardListener = ClipboardManager.OnPrimaryClipChangedListener {
        refreshClipboard()
    }

    private val diagnosticView = StructuredLogs(
        NativeVpnBridge.diagnosticPaths(activity).lineSequence().filter(String::isNotBlank).toList(),
        File(activity.filesDir, "diagnostic-view.json"),
    )


    init {
        LogUploader.cleanupStartup(activity.applicationContext)
        clipboard.addPrimaryClipChangedListener(clipboardListener)
        if (backendAvailable) {
            worker.scheduleWithFixedDelay({
                refreshSnapshot()
                if (visible) refreshClipboard()
            }, 0, 500, TimeUnit.MILLISECONDS)
        } else {
            post {
                state = state.copy(error = backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            }
        }
        NativeVpnBridge.recordDiagnostic(activity, "startup.diagnostic_store_ready", "Android diagnostic store resolved")
        logWorker.scheduleWithFixedDelay({ if (visible) readLogs() }, 0, 750, TimeUnit.MILLISECONDS)
    }

    fun sourceChanged(value: String, trigger: LoadTrigger = LoadTrigger.TYPED) {
        selectionAdmission.invalidate()
        if (!backendAvailable) {
            cancelPreparation()
            permissionTarget = null
            state = state.copy(source = value, sourceDirty = true, loading = false, busy = false,
                loadError = backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        cancelPreparation()
        restoredPendingSource = null
        if (trigger != LoadTrigger.RESTORE && trigger != LoadTrigger.TYPED) dismissedErrors = emptySet()
        val source = value.trim()
        val uri = runCatching { java.net.URI(source) }.getOrNull()
        val valid = uri?.scheme?.lowercase() == "https" && !uri.host.isNullOrEmpty()
        val alreadyAccepted = trigger == LoadTrigger.TYPED && valid &&
            state.loadError.isEmpty() && !sourceLoads.inFlight &&
            sourceLoads.isAlreadyAccepted(source, latest.configured, latest.sourceUrl)
        selectionAdmission.sourceChanged { sourceLoads.change(source, trigger, valid, alreadyAccepted) }
        if (alreadyAccepted) {
            if (permissionTarget != null) state = state.copy(busy = false)
            permissionTarget = null
            state = state.copy(
                source = source,
                sourceDirty = false,
                loading = false,
                loadError = "",
                error = "",
            )
            return
        }
        if (permissionTarget != null) state = state.copy(busy = false)
        permissionTarget = null
        state = state.copy(source = value, sourceDirty = true, error = "", loadError = "")
    }

    private fun startSourceLoad(request: SubscriptionRequest) {
        if (!backendAvailable) {
            state = state.copy(loading = false, loadError = backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        NativeVpnBridge.recordDiagnostic(activity, "subscription.load_started",
            "Loading subscription revision=${request.revision} source=${request.source}")
        state = state.copy(loading = true)
        loadWorker.execute {
            val outcome = runCatching {
                val snapshotResponse = JSONObject(NativeGoSession.snapshot(""))
                requireOK(snapshotResponse)
                val current = snapshotResponse.getJSONObject("result")
                val configured = JSONObject(NativeGoSession.configure(
                    current.getString("session_id"),
                    current.getLong("sequence"),
                    request.source.toByteArray(Charsets.UTF_8),
                ))
                requireOK(configured)
                configured.getJSONObject("result").getLong("sequence")
            }
            val failure = outcome.exceptionOrNull()
            val currentAtCompletion = !closed && request.revision == sourceLoads.revision
            val message = "Subscription revision=${request.revision} source=${request.source} " +
                "current=$currentAtCompletion closed=$closed succeeded=${outcome.isSuccess}"
            if (!currentAtCompletion && failure != null) {
                NativeVpnBridge.recordDiagnostic(activity.applicationContext, "subscription.load_completed",
                    "$message superseded_failure=${failure.stackTraceToString()}")
            } else {
                NativeVpnBridge.recordDiagnostic(activity.applicationContext, "subscription.load_completed", message, failure)
            }
            post {
                sourceLoads.complete(request, succeeded = outcome.isSuccess) { current ->
                    state = state.copy(loading = false)
                    if (outcome.isSuccess) acceptedSequence = maxOf(acceptedSequence, outcome.getOrThrow())
                    if (current) {
                        state = if (outcome.isSuccess) state.copy(sourceDirty = false, loadError = "")
                        else state.copy(loadError = outcome.exceptionOrNull()?.message ?: "Subscription could not be loaded")
                    }
                    worker.execute { refreshSnapshot() }
                }
            }
        }
    }

    fun retryLoad() = sourceChanged(state.source, LoadTrigger.RETRY)

    fun createLogView(context: android.content.Context): LiveLogView = LiveLogView(context).also { view ->
        restoredLogViewState?.let(view::restoreState)
        restoredLogViewState = null
    }

    fun saveLogViewState(): Bundle? = restoredLogViewState ?: runCatching {
        val view = activity.findViewById<android.view.View>(android.R.id.content)
        view?.let { content ->
            fun find(candidate: android.view.View): LiveLogView? {
                if (candidate is LiveLogView) return candidate
                if (candidate is android.view.ViewGroup) {
                    repeat(candidate.childCount) { index -> find(candidate.getChildAt(index))?.let { return it } }
                }
                return null
            }
            find(content)?.let { logView ->
                if (logView.hasStableState()) {
                    val boundary = latestClearBoundary
                    retainedLogViewModel.frozenView = if (boundary == null) null else {
                        logView.frozenState(boundary.toMap())?.takeIf {
                            diagnosticView.entriesVisibleAfterClear(it.entries, boundary)
                        }
                    }
                    retainedLogViewState = retainedLogViewModel.frozenView
                }
                logView.saveState()
            }
        }
    }.getOrNull()

    fun saveSourceEditorState(): Bundle? {
        if (!state.sourceDirty) return null
        return Bundle().apply {
            putString(SOURCE_TEXT, state.source)
            putBoolean(SOURCE_DIRTY, true)
            putString(SOURCE_LOAD_ERROR, state.loadError)
            putBoolean(SOURCE_LOAD_PENDING,
                state.loadError.isEmpty() && (restoredPendingSource != null || sourceLoads.hasWork))
        }
    }

    fun restoreRetainedLogView(view: LiveLogView) {
        val retained = retainedLogViewState ?: return
        val valid = retainedLogViewValid ?: return
        if (retainedLogViewState !== retained) return
        if (valid) view.restoreFrozenState(retained) else view.resetToLatest()
        retainedLogViewState = null
        retainedLogViewValid = null
        retainedLogViewModel.frozenView = null
    }

    fun isStopTarget(index: Int?): Boolean {
        val s = state.session
        s.pendingTarget?.let { return it.digest == s.digest && if (index == null) it.mode == "AUTO_SELECT" else it.mode == "PROFILE_INDEX" && it.index == index }
        if (s.primaryAction != "STOP") return false
        if (index == null) return s.activeMode == "AUTO_SELECT" && s.activeDigest == s.digest
        return s.activeDigest == s.digest && if (s.state == "CONNECTED") s.activeProfileIndex == index else s.activeMode == "PROFILE_INDEX" && s.activeIndex == index
    }

    fun canAct(index: Int? = null): Boolean = !state.busy && permissionTarget == null &&
        (isStopTarget(index) || (!state.sourceDirty && !state.loading && state.loadError.isEmpty() && state.session.sequence >= acceptedSequence && state.session.configured &&
            (state.session.primaryAction == "START" || state.session.canSwitch)))

    val isProtectedFailure: Boolean get() = state.session.protectedFailure
    val retryActionEnabled: Boolean get() = isProtectedFailure && !state.busy && permissionTarget == null
    val stopActionEnabled: Boolean get() = !state.busy &&
        (state.session.primaryAction == "STOP" || state.session.vpnActive && state.session.primaryAction == "RETRY")

    fun actionTitle(index: Int? = null): String = if (isStopTarget(index)) {
        if (state.session.state == "CONNECTED" && state.session.pendingTarget == null) "Disconnect" else "Stop"
    } else if (index == null) "Auto connect" else "Connect"

    val status: String get() {
        val session = state.session
        return when {
            session.recovering -> "Reconnecting"
            state.error.isNotEmpty() -> "Error"
            session.state == "CONNECTED" -> "Connected"
            session.state == "PROBING" || session.state == "PREPARING" -> "Connecting"
            session.state == "STOPPING" -> "Stopping"
            session.state == "FAILED" -> "Failed"
            else -> "Disconnected"
        }
    }

    private val connected get() = state.session.primaryAction == "STOP"

    val mainActionTitle: String get() = when {
        isProtectedFailure -> if (state.busy) "Retrying…" else "Retry"
        !connected -> if (state.busy) "Connecting…" else "Auto connect"
        state.session.state == "CONNECTED" && state.session.pendingTarget == null -> "Disconnect"
        state.busy && state.session.state == "STOPPING" -> "Stopping…"
        else -> "Stop"
    }

    val mainActionEnabled: Boolean get() = when {
        isProtectedFailure -> retryActionEnabled
        connected -> !state.busy
        else -> canAct()
    }

    fun mainAction() = when {
        isProtectedFailure -> retry()
        connected -> stop()
        else -> connectOrDisconnect()
    }

    fun profileStatus(index: Int): ProfileStatus? {
        val session = state.session
        val active = session.activeDigest == session.digest && (session.activeProfileIndex == index ||
            session.activeProfileIndex < 0 && session.activeMode == "PROFILE_INDEX" && session.activeIndex == index)
        return if (active) sessionStatus() else null
    }

    /** The connection still running from a subscription that has since been replaced. */
    val orphanProfile: ProfileData? get() {
        val session = state.session
        return session.activeProfile?.takeIf { connected && session.activeDigest != session.digest }
    }

    fun orphanStatus(): ProfileStatus? = sessionStatus()

    private fun sessionStatus(): ProfileStatus? {
        val session = state.session
        return when {
            session.recovering -> ProfileStatus.RECONNECTING
            session.state == "CONNECTED" -> ProfileStatus.CONNECTED
            session.state == "PROBING" || session.state == "PREPARING" -> ProfileStatus.CONNECTING
            session.state == "STOPPING" -> ProfileStatus.STOPPING
            session.failure.isNotEmpty() -> ProfileStatus.FAILED
            else -> null
        }
    }

    fun dismissError(message: String) { dismissedErrors = dismissedErrors + message }

    fun forgetClearedErrors() {
        if (latest.sessionId.isNotEmpty()) dismissedErrors = dismissedErrors.intersect(errors.toSet())
    }

    fun copyToClipboard(text: String) {
        clipboard.setPrimaryClip(android.content.ClipData.newPlainText("DobbyVPN error", text))
    }

    fun connectOrDisconnect(index: Int? = null) {
        val allowed = canAct(index)
        val displayed = state.session
        val latestSnapshot = latest
        NativeVpnBridge.recordDiagnostic(
            activity,
            "ui.connect_or_disconnect",
            "requested_index=${index ?: "auto"} allowed=$allowed stop_target=${isStopTarget(index)} " +
                "displayed_session_id=${displayed.sessionId} displayed_generation=${displayed.generation} " +
                "displayed_active_profile_index=${displayed.activeProfileIndex} " +
                "displayed_sequence=${displayed.sequence} " +
                "latest_session_id=${latestSnapshot.sessionId} latest_generation=${latestSnapshot.generation} " +
                "latest_active_profile_index=${latestSnapshot.activeProfileIndex} " +
                "latest_sequence=${latestSnapshot.sequence} busy=${state.busy} " +
                "source_dirty=${state.sourceDirty} loading=${state.loading} " +
                "permission_target_present=${permissionTarget != null} " +
                "source_revision=${sourceLoads.revision}",
        )
        if (!backendAvailable) {
            report(backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        if (!allowed) return
        if (isStopTarget(index)) { stop(); return }
        dismissedErrors = emptySet()
        val current = latest
        state = state.copy(busy = true, error = "")
        permissionTarget = PendingPermissionAction(
            current,
            SelectionData(
                digest = current.digest,
                mode = if (index == null) "AUTO_SELECT" else "PROFILE_INDEX",
                index = index ?: 0,
            ),
            PermissionTarget(current.sessionId, current.digest, current.generation, sourceLoads.revision),
        )
        preparePermission()
    }

    fun retry() {
        if (!backendAvailable) {
            report(backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        if (!retryActionEnabled) return
        val current = latest
        if (!current.protectedFailure || current.sessionId != state.session.sessionId) {
            worker.execute { refreshSnapshot(clearBusy = true) }
            return
        }
        dismissedErrors = emptySet()
        state = state.copy(busy = true, error = "")
        permissionTarget = PendingPermissionAction(
            session = current,
            selection = SelectionData(current.activeDigest, current.activeMode, current.activeIndex),
            fence = PermissionTarget(current.sessionId, current.digest, current.generation, sourceLoads.revision),
            retry = true,
        )
        preparePermission()
    }

    fun stop() {
        if (!backendAvailable) {
            report(backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        selectionAdmission.invalidate()
        cancelPreparation()
        permissionTarget = null
        val current = latest
        if (state.busy || (current.primaryAction != "STOP" && !current.vpnActive)) return
        // Preserve closed errors and acknowledge only the displayed transport failure.
        val displayed = errors.firstOrNull { it !in dismissedErrors }
        if (!displayed.isNullOrEmpty() && displayed == state.session.failure) dismissError(displayed)
        state = state.copy(busy = true, error = "")
        worker.execute {
            runCatching { NativeGoSession.stop(current.sessionId, current.generation) }
                .onSuccess { consumeSnapshotCommand(it, "Stop failed") }
                .onFailure { report(it.message ?: "Stop failed", it) }
        }
    }

    private fun preparePermission() {
        cancelPreparation()
        val target = permissionTarget ?: return
        if (!selectionAdmission.register(target, target.fence.sourceRevision)) return
        val details = "session_id=${target.session.sessionId} generation=${target.session.generation} " +
            "source_revision=${target.fence.sourceRevision} selection=${target.selection} retry=${target.retry}"
        preparation = NativeVpnBridge.prepareForSelection(activity, details) { result ->
            if (closed || permissionTarget !== target) return@prepareForSelection
            when (result) {
                NativeVpnBridge.PreparationResult.Ready -> { preparation = null; continuePermission(true) }
                NativeVpnBridge.PreparationResult.PermissionRequired -> {
                    consentAwaiting = true
                    state = state.copy(busy = false, error = "Approve the Android VPN permission to connect")
                }
                NativeVpnBridge.PreparationResult.Cancelled -> { selectionAdmission.invalidate(target); preparation = null; permissionTarget = null; state = state.copy(busy = false) }
                NativeVpnBridge.PreparationResult.PermissionDenied -> {
                    preparation = null
                    continuePermission(false)
                }
                is NativeVpnBridge.PreparationResult.Failed -> {
                    selectionAdmission.invalidate(target)
                    preparation = null
                    permissionTarget = null
                    report("Android VPN service could not be prepared: ${result.error.message}", result.error)
                }
            }
        }
    }

    /** The connection the user asked for, while the system consent dialog is open. */
    fun pendingConsent(): PendingConsent? {
        val target = permissionTarget?.takeIf { consentAwaiting } ?: return consentRecovery.pending
        return PendingConsent(
            index = if (target.selection.mode == "PROFILE_INDEX") target.selection.index else null,
            retry = target.retry,
            sessionId = target.fence.sessionId,
            digest = target.fence.digest,
            generation = target.fence.generation,
        )
    }

    /** Continues the connection saved by a previous Activity instance, or reports a refusal. */
    fun consentReturned(granted: Boolean) {
        when (val outcome = consentRecovery.result(granted)) {
            ConsentRecoveryOutcome.NotRestored -> Unit
            ConsentRecoveryOutcome.Refused -> report("VPN permission was not granted")
            is ConsentRecoveryOutcome.Resume -> worker.execute {
                refreshSnapshot()
                post { resumeConnection(outcome.intent) }
            }
        }
    }

    private fun resumeConnection(intent: PendingConsent) {
        if (closed) return
        val current = latest
        if (!intent.stillCurrent(current.sessionId, current.digest, current.generation)) {
            report("VPN connection was not started. Try again")
            return
        }
        if (intent.retry) {
            if (!retryActionEnabled) { report("VPN connection was not started. Try again"); return }
            retry()
        } else {
            if (isStopTarget(intent.index)) return
            if (!canAct(intent.index)) { report("VPN connection was not started. Try again"); return }
            connectOrDisconnect(intent.index)
        }
    }

    private fun continuePermission(granted: Boolean) {
        val target = permissionTarget ?: return
        permissionTarget = null
        if (!granted) { selectionAdmission.invalidate(target); report("VPN permission was not granted"); return }
        val selected = target.session
        val current = latest
        if (!target.retry && !PermissionTargetFence.stillCurrent(
                target.fence,
                current.permissionSnapshot(sourceLoads.revision, state.sourceDirty),
            )) {
            selectionAdmission.invalidate(target)
            worker.execute { refreshSnapshot(clearBusy = true) }
            return
        }
        state = state.copy(busy = true, error = "")
        worker.execute {
            var admitted = false
            val command = runCatching {
                val response = JSONObject(NativeGoSession.snapshot(selected.sessionId))
                requireOK(response)
                val snapshot = response.getJSONObject("result")
                val sessionId = snapshot.getString("session_id")
                val sequence = snapshot.getLong("sequence")
                val generation = snapshot.getLong("generation")
                val digest = snapshot.getString("digest")
                val primaryAction = snapshot.optString("primary_action", "NONE")
                val canSwitch = snapshot.optBoolean("can_switch")
                val hasPendingTarget = snapshot.optJSONObject("pending_target") != null
                val retryStillCurrent = target.retry &&
                    sessionId == selected.sessionId &&
                    generation == selected.generation &&
                    snapshot.optString("state", "IDLE") == "FAILED" &&
                    snapshot.optBoolean("vpn_active") &&
                    snapshot.optBoolean("can_retry") &&
                    primaryAction == "RETRY" &&
                    !snapshot.optBoolean("cleanup_complete", true) &&
                    snapshot.optString("active_digest") == target.selection.digest &&
                    snapshot.optString("active_mode") == target.selection.mode &&
                    snapshot.optInt("active_index") == target.selection.index
                val freshRevision = sourceLoads.revision
                val freshSnapshot = PermissionSnapshot(
                    sessionId = sessionId,
                    digest = digest,
                    generation = generation,
                    sourceRevision = freshRevision,
                    sourceDirty = freshRevision != target.fence.sourceRevision,
                    hasPendingTarget = hasPendingTarget,
                    primaryAction = primaryAction,
                    canSwitch = canSwitch,
                )
                if (target.retry && !retryStillCurrent) {
                    NativeVpnBridge.recordDiagnostic(
                        activity,
                        "ui.permission_selection_discarded",
                        "reason=retry_target_stale selection=${target.selection} " +
                            "fence=${target.fence} snapshot=$freshSnapshot",
                    )
                    null
                } else if (!target.retry && !PermissionTargetFence.mayStartAfterConsent(target.fence, freshSnapshot)) {
                    NativeVpnBridge.recordDiagnostic(
                        activity,
                        "ui.permission_selection_discarded",
                        "reason=consent_fence_rejected selection=${target.selection} " +
                            "fence=${target.fence} snapshot=$freshSnapshot",
                    )
                    null
                } else if (!selectionAdmission.claim(target, target.fence.sourceRevision)) {
                    NativeVpnBridge.recordDiagnostic(activity.applicationContext, "ui.permission_selection_discarded",
                        "reason=admission_invalidated selection=${target.selection} fence=${target.fence}")
                    null
                } else {
                    admitted = true
                    NativeVpnBridge.recordDiagnostic(activity.applicationContext, "ui.selection_admitted",
                        "session_id=$sessionId generation=$generation source_revision=${target.fence.sourceRevision} selection=${target.selection} retry=${target.retry}")
                    NativeGoSession.startSelection(
                        sessionId,
                        sequence,
                        target.selection.mode,
                        target.selection.index,
                        target.selection.digest,
                        !target.retry,
                    )
                }
            }
            if (!admitted) selectionAdmission.invalidate(target)
            if (admitted) {
                NativeVpnBridge.recordDiagnostic(activity.applicationContext, "ui.selection_completed",
                    "session_id=${target.session.sessionId} generation=${target.session.generation} source_revision=${target.fence.sourceRevision} " +
                        "selection=${target.selection} closed=$closed succeeded=${command.isSuccess}", command.exceptionOrNull())
            }
            command.onSuccess { encoded ->
                if (encoded == null) refreshSnapshot(clearBusy = true)
                else consumeSnapshotCommand(encoded, "Connect failed")
            }
                .onFailure { report(it.message ?: "Connect failed", it) }
        }
    }

    fun setVisible(value: Boolean) { visible = value; if (value) refreshClipboard() }

    private fun refreshClipboard() {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            post { refreshClipboard() }
            return
        }
        val available = runCatching {
            if (!clipboard.hasPrimaryClip()) false
            else clipboard.primaryClipDescription?.let { description ->
                description.hasMimeType(ClipDescription.MIMETYPE_TEXT_PLAIN) ||
                    description.hasMimeType(ClipDescription.MIMETYPE_TEXT_HTML)
            } == true
        }.getOrDefault(false)
        if (state.canPaste != available) state = state.copy(canPaste = available)
    }

    fun paste() {
        val clip = try {
            if (clipboard.hasPrimaryClip()) clipboard.primaryClip else null
        } catch (failure: Exception) {
            report("Android could not read the clipboard", failure)
            return
        }
        if (clip == null || clip.itemCount == 0) {
            report("Clipboard is empty. Copy an HTTPS subscription URL first")
            return
        }
        val value = try {
            val item = clip.getItemAt(0)
            (item.text ?: item.coerceToText(activity))?.toString()?.trim().orEmpty()
        } catch (failure: Exception) {
            report("Clipboard item could not be read. Copy an HTTPS subscription URL first", failure)
            return
        }
        if (value.isEmpty()) {
            report("Clipboard item has no text. Copy an HTTPS subscription URL first")
            return
        }
        importSubscription(value, LoadTrigger.PASTE)
    }

    private fun importSubscription(value: String, trigger: LoadTrigger) {
        val uri = runCatching { java.net.URI(value) }.getOrNull()
        if (uri?.scheme?.lowercase() != "https" || uri.host.isNullOrEmpty()) { report("Paste an HTTPS subscription URL with a host"); return }
        val source = value.trim()
        if (source == state.source && sourceLoads.hasOutstanding(source)) return
        val noOutstandingLoad = !sourceLoads.hasWork
        if (noOutstandingLoad && state.loadError.isEmpty() &&
            sourceLoads.isAlreadyAccepted(source, latest.configured, latest.sourceUrl)
        ) {
            state = state.copy(source = source, sourceDirty = false, loadError = "", error = "")
            return
        }
        sourceChanged(value, trigger)
    }

    fun importLink(value: String) {
        if (value.equals("dobbyvpn://", ignoreCase = true)) return
        try {
            val uri = java.net.URI(value)
            require(uri.scheme.equals("dobbyvpn", true) && uri.host == "import" && uri.rawPath.isNullOrEmpty() && uri.rawFragment == null && uri.rawUserInfo == null && uri.port == -1)
            val query = uri.rawQuery.orEmpty().split('&')
            require(query.size == 1)
            val pair = query.single().split('=', limit = 2)
            require(pair.size == 2 && pair[0] == "url")
            importSubscription(
                java.net.URLDecoder.decode(pair[1].replace("+", "%2B"), "UTF-8"),
                LoadTrigger.IMPORT,
            )
        } catch (failure: Exception) { report("Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL", failure) }
    }


    private fun readLogs() {
        val retained = retainedLogViewState
        val result = diagnosticView.readWithRetainedBoundary(retained?.entries, retained?.clearBoundary)
        post {
            latestClearBoundary = result.clearBoundary
            if (retained != null && retainedLogViewState === retained) {
                retainedLogViewValid = result.retainedEntriesVisible == true
            }
            state = state.copy(logs = result.entries.filter { StructuredLogs.isShownOnScreen(it) }, logsError = result.error)
        }
    }

    fun clearLogs() {
        logWorker.execute {
            try {
                diagnosticView.clear()
                NativeVpnBridge.recordAppStarted(activity)
                post { state = state.copy(logs = emptyList(), clearRevision = state.clearRevision + 1) }
            } catch (failure: Exception) { post { state = state.copy(logsError = failure.stackTraceToString()) } }
        }
    }

    fun exportLogs() {
        activity.beginLogExport()
    }

    fun setExportingLogs(exporting: Boolean, error: String = "") {
        state = state.copy(exportingLogs = exporting, logsError = error)
    }

    fun uploadLogs() {
        if (!backendAvailable) {
            state = state.copy(uploadError = backendUnavailableMessage.ifBlank { "Go backend is unavailable" })
            return
        }
        if (state.uploadingLogs || !latest.logsUploadAvailable) return
        val selected = latest
        val attempt = LogUploader(activity.applicationContext, phase = { phase ->
            post { if (state.uploadingLogs) state = state.copy(uploadPhase = phase) }
        })
        uploader = attempt
        state = state.copy(uploadingLogs = true, uploadPhase = "Preparing logs…", uploadError = "")
        uploadWorker.execute {
            val outcome = runCatching { attempt.upload(selected.sessionId, selected.digest) }
            NativeVpnBridge.recordDiagnostic(activity.applicationContext, "logs.upload_completed",
                if (outcome.isSuccess) "Diagnostic upload completed" else "Diagnostic upload failed",
                outcome.exceptionOrNull())
            post {
                if (uploader === attempt) {
                    uploader = null
                    state = state.copy(uploadingLogs = false,
                        uploadedReference = outcome.getOrNull()?.reference ?: state.uploadedReference,
                        uploadedProvider = outcome.getOrNull()?.provider ?: state.uploadedProvider,
                        uploadedTime = outcome.getOrNull()?.time ?: state.uploadedTime,
                        showUploadSuccess = outcome.isSuccess,
                        uploadError = outcome.exceptionOrNull()?.message ?: outcome.getOrNull()?.persistenceError.orEmpty())
                }
            }
        }
    }

    fun cancelUpload() { uploader?.cancel() }
    fun dismissUploadSuccess() { state = state.copy(showUploadSuccess = false) }
    fun copyUploadReference() { clipboard.setPrimaryClip(ClipData.newPlainText("DobbyVPN logs", state.uploadedReference)) }
    fun shareUploadReference() {
        activity.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain")
            .putExtra(Intent.EXTRA_TEXT, "DobbyVPN logs: ${state.uploadedReference}"), "Share log reference"))
    }

    fun close() {
        selectionAdmission.close {
            lifetime.close()
            sourceLoads.close()
        }
        cancelPreparation()
        permissionTarget = null
        clipboard.removePrimaryClipChangedListener(clipboardListener)
        uploader?.cancel()
        uploadWorker.shutdownNow()
        worker.shutdownNow()
        logWorker.shutdownNow()
        loadWorker.shutdownNow()
    }

    private fun refreshSnapshot(clearBusy: Boolean = false) {
        if (closed) return
        if (!backendAvailable) {
            if (clearBusy) post { state = state.copy(busy = false) }
            return
        }
        try {
            val sessionId = latest.sessionId
            var reattached = false
            var response = JSONObject(NativeGoSession.snapshot(sessionId))
            val failureCode = response.optJSONObject("error")?.optString("code").orEmpty()
            if (!response.optBoolean("ok") && sessionId.isNotEmpty() && failureCode == "NOT_FOUND") {
                response = JSONObject(NativeGoSession.snapshot(""))
                reattached = true
            }
            requireOK(response)
            val snapshot = response.getJSONObject("result")
            val active = snapshot.optJSONObject("active_profile")
            val failure = snapshot.optJSONObject("last_failure")
            val current = SessionData(
                sessionId = snapshot.optString("session_id"),
                sequence = snapshot.optLong("sequence"),
                generation = snapshot.optLong("generation"),
                networkGeneration = snapshot.opt("network_generation")?.toString()?.toULongOrNull() ?: 0uL,
                vpnActive = snapshot.optBoolean("vpn_active"),
                canRetry = snapshot.optBoolean("can_retry"),
                state = snapshot.optString("state", "IDLE"),
                primaryAction = snapshot.optString("primary_action", "NONE"),
                configured = snapshot.optBoolean("configured"),
                logsUploadAvailable = snapshot.optBoolean("logs_upload_available"),
                sourceUrl = snapshot.optString("source_url"),
                digest = snapshot.optString("digest"),
                profiles = snapshot.optJSONArray("profiles")?.let { profiles ->
                    (0 until profiles.length()).map { profiles.getJSONObject(it) }.map {
                        ProfileData(it.getInt("index"), it.optString("description"), it.optString("protocol"))
                    }
                }.orEmpty(),
                activeDigest = snapshot.optString("active_digest"),
                activeMode = snapshot.optString("active_mode"),
                activeIndex = snapshot.optInt("active_index"),
                activeProfileIndex = active?.optInt("index", -1) ?: -1,
                pendingTarget = snapshot.optJSONObject("pending_target")?.let { SelectionData(it.getString("digest"), it.getString("mode"), it.getInt("index")) },
                canSwitch = snapshot.optBoolean("can_switch"),
                activeProfile = active?.let {
                    ProfileData(it.optInt("index"), it.optString("description"), it.optString("protocol"))
                },
                recovering = snapshot.optBoolean("recovering"),
                cleanupComplete = snapshot.optBoolean("cleanup_complete", true),
                failure = failure?.let {
                    val message = it.optString("message")
                    val code = it.optString("code")
                    if (message.isBlank()) code else "$message ($code)"
                }.orEmpty(),
                sourceError = snapshot.optString("source_error"),
            )
            // Command policy consumes only the backend snapshot. The UI projection
            // below preserves inventory while a source edit is still pending.
            post {
                latest = current
                if (current.sessionId == state.session.sessionId && current.sequence < maxOf(state.session.sequence, acceptedSequence)) return@post
                if (current.sessionId != state.session.sessionId) acceptedSequence = 0
                sourceLoads.snapshotObserved()
                val displayedSession = if (state.sourceDirty && current.sessionId == state.session.sessionId) {
                    current.copy(
                        configured = state.session.configured,
                        sourceUrl = state.session.sourceUrl,
                        digest = state.session.digest,
                        profiles = state.session.profiles,
                    )
                } else {
                    current
                }
                state = state.copy(
                    session = displayedSession,
                    source = if (state.sourceDirty || current.sourceUrl.isEmpty()) state.source else current.sourceUrl,
                    error = when {
                        current.sourceError.isNotEmpty() -> current.sourceError
                        reattached || (state.session.sessionId.isNotEmpty() && current.sessionId != state.session.sessionId) -> ""
                        else -> state.error
                    },
                    busy = if (clearBusy) false else state.busy,
                )
                restoredPendingSource?.takeIf { current.sessionId.isNotEmpty() }?.let { source ->
                    restoredPendingSource = null
                    importSubscription(source, LoadTrigger.RESTORE)
                }
                val restoreKey = current.sessionId + "|" + state.source
                if (!current.configured && !state.sourceDirty && state.source.isNotEmpty() && restoredLoad != restoreKey) {
                    restoredLoad = restoreKey
                    sourceChanged(state.source, LoadTrigger.RESTORE)
                }
                sourceLoads.sessionBecameReady()
            }
        } catch (failure: LinkageError) {
            handleNativeLinkageFailure(failure)
        } catch (failure: Exception) {
            val message = commandError(failure)
            NativeVpnBridge.recordDiagnostic(activity, "ui.snapshot_failed", message, failure)
            post { latest = SessionData(); state = state.copy(session = SessionData(), busy = false, error = message) }
        }
    }

    internal fun handleNativeLinkageFailure(failure: LinkageError) {
        val message = failure.message?.takeIf(String::isNotBlank) ?: "Go backend is unavailable"
        NativeVpnBridge.recordNativeFailure(activity.applicationContext, "go.snapshot_linkage_failed", failure)
        if (closed) return
        post {
            backendUnavailableMessage = message
            backendAvailable = false
            latest = SessionData()
            state = state.copy(session = SessionData(), busy = false, error = message)
        }
    }

    private fun consumeSnapshotCommand(encoded: String, fallback: String) {
        try {
            val response = JSONObject(encoded)
            requireOK(response)
            response.getJSONObject("result")
            refreshSnapshot(clearBusy = true)
        } catch (failure: Exception) {
            report(commandError(failure, fallback), failure)
        }
    }

    private fun requireOK(response: JSONObject) {
        if (response.optBoolean("ok")) return
        val failure = response.optJSONObject("error")
        val message = failure?.optString("message").orEmpty()
        val code = failure?.optString("code").orEmpty()
        throw IllegalStateException(if (code.isEmpty()) message.ifEmpty { "Go backend command failed" } else "$message ($code)")
    }

    private fun commandError(failure: Exception, fallback: String = "Go backend command failed"): String =
        failure.message?.takeIf(String::isNotBlank) ?: fallback

    private fun report(message: String, failure: Throwable? = null) {
        NativeVpnBridge.recordDiagnostic(activity, "ui.failure", message, failure)
        post {
            dismissedErrors = dismissedErrors - message
            state = state.copy(busy = false, error = message)
        }
    }
}

internal class LiveLogView(context: android.content.Context) : android.widget.ScrollView(context) {
    private val content = android.widget.TextView(context).apply {
        id = R.id.logs_content
        typeface = android.graphics.Typeface.MONOSPACE
        textSize = 11.5f
        setLineSpacing(0f, 1.25f)
        setTextIsSelectable(true)
        contentDescription = "Connection logs"
    }
    var onDoubleTap: (x: Int, y: Int) -> Unit = { _, _ -> }
    private val gestures = android.view.GestureDetector(context, object : android.view.GestureDetector.SimpleOnGestureListener() {
        override fun onDoubleTap(event: android.view.MotionEvent): Boolean {
            onDoubleTap(event.x.toInt(), event.y.toInt())
            return true
        }
    })
    private val followState = LogFollowState()
    private val following get() = followState.followsLatest
    private var updating = false
    private var userScrolling = false
    private var lastClear = 0
    private val timeFormat = java.time.format.DateTimeFormatter.ofPattern("HH:mm:ss")
    private var rendered = emptyList<LogEntry>()
    private var latest = emptyList<LogEntry>()
    private var hasRendered = false
    private var awaitingRestoredEntries = false
    private var restoredScrollY: Int? = null
    private var restoredVisibleBoundaries: Map<String, Long>? = null

    init {
        id = R.id.logs_viewport
        isFillViewport = true
        addView(content)
        content.setTextColor(Ink.toArgb())
        // New text is laid out after render() returns; keep the newest line in view.
        content.addOnLayoutChangeListener { _, _, _, _, bottom, _, _, _, oldBottom ->
            if (following && bottom != oldBottom) scrollToLatest()
        }
    }

    private var consumingDoubleTap = false
    private var scrollingToLatest = false

    private fun scrollToLatest() {
        scrollingToLatest = true
        scrollTo(0, content.bottom)
        scrollingToLatest = false
    }

    // A long press selects text; the second tap of a double tap opens the menu
    // and never reaches the text, so it does not select a word.
    override fun dispatchTouchEvent(event: android.view.MotionEvent): Boolean {
        if (gestures.onTouchEvent(event) && event.actionMasked == android.view.MotionEvent.ACTION_DOWN) {
            consumingDoubleTap = true
        }
        if (!consumingDoubleTap) return super.dispatchTouchEvent(event)
        if (event.actionMasked == android.view.MotionEvent.ACTION_UP ||
            event.actionMasked == android.view.MotionEvent.ACTION_CANCEL) consumingDoubleTap = false
        return true
    }

    fun restoreState(state: Bundle) {
        followState.restore(state.getBoolean("following", true))
        restoredScrollY = state.getInt("scroll_y", 0).coerceAtLeast(0)
        awaitingRestoredEntries = !following
        val identities = state.getStringArrayList("visible_stream_ids").orEmpty()
        val offsets = state.getLongArray("visible_stream_offsets")
        restoredVisibleBoundaries = if (offsets != null && offsets.size == identities.size) {
            identities.indices.associate { identities[it] to offsets[it] }
        } else null
    }

    fun saveState(): Bundle = Bundle().apply {
        putBoolean("following", following)
        putInt("scroll_y", scrollY)
        val visibleBoundaries = rendered.mapNotNull { entry ->
            val separator = entry.id.lastIndexOf(':')
            val offset = entry.id.substring(separator + 1).toLongOrNull()
            if (separator < 0 || offset == null) null else entry.id.substring(0, separator) to offset
        }.groupBy({ it.first }, { it.second }).mapValues { (_, offsets) -> offsets.maxOrNull() ?: 0L }
        putStringArrayList("visible_stream_ids", ArrayList(visibleBoundaries.keys))
        putLongArray("visible_stream_offsets", visibleBoundaries.values.toLongArray())
    }

    fun hasStableState(): Boolean = hasRendered && !awaitingRestoredEntries

    fun frozenState(clearBoundary: Map<String, Long>): FrozenLogViewState? = if (following) null else {
        FrozenLogViewState(rendered.toList(), scrollY, clearBoundary.toMap())
    }

    fun restoreFrozenState(state: FrozenLogViewState) {
        followState.pause()
        restoredScrollY = state.scrollY.coerceAtLeast(0)
        awaitingRestoredEntries = false
        restoredVisibleBoundaries = null
        hasRendered = true
        render(state.entries)
    }

    fun resetToLatest() {
        followState.resume()
        restoredScrollY = null
        awaitingRestoredEntries = false
        restoredVisibleBoundaries = null
        render(latest)
    }

    override fun onInterceptTouchEvent(event: android.view.MotionEvent): Boolean {
        if (event.actionMasked == android.view.MotionEvent.ACTION_DOWN) userScrolling = true
        return super.onInterceptTouchEvent(event)
    }

    // Selectable text asks to keep its cursor in view after every update; while
    // following, the newest line must stay in view instead.
    override fun requestChildRectangleOnScreen(child: android.view.View, rectangle: android.graphics.Rect, immediate: Boolean): Boolean =
        if (following) false else super.requestChildRectangleOnScreen(child, rectangle, immediate)

    override fun onScrollChanged(left: Int, top: Int, oldLeft: Int, oldTop: Int) {
        super.onScrollChanged(left, top, oldLeft, oldTop)
        if (updating || scrollingToLatest || !userScrolling || top == oldTop) return
        if (followState.scrolled(canScrollVertically(1)) && latest != rendered) post { render(latest) }
    }

    fun update(entries: List<LogEntry>, clear: Int) {
        if (clear != lastClear) {
            followState.clear()
            lastClear = clear
            awaitingRestoredEntries = false
            restoredVisibleBoundaries = null
        }
        if (awaitingRestoredEntries && entries.isEmpty()) return
        if (awaitingRestoredEntries) {
            val boundaries = restoredVisibleBoundaries
            val visible = if (boundaries == null) entries else entries.filter { entry ->
                val separator = entry.id.lastIndexOf(':')
                val offset = entry.id.substring(separator + 1).toLongOrNull()
                if (separator < 0 || offset == null) false
                else boundaries[entry.id.substring(0, separator)]?.let { offset <= it } == true
            }
            awaitingRestoredEntries = false
            restoredVisibleBoundaries = null
            latest = entries
            render(visible)
            return
        }
        awaitingRestoredEntries = false
        latest = entries
        if (!hasRendered || following && rendered != entries) render(entries)
    }

    private fun render(entries: List<LogEntry>) {
        updating = true
        rendered = entries
        val text = android.text.SpannableStringBuilder()
        val muted = Muted.toArgb()
        entries.forEach { entry ->
            val time = entry.time?.atZone(java.time.ZoneId.systemDefault())?.format(timeFormat) ?: entry.timestamp
            if (time.isNotEmpty()) {
                text.append(time, android.text.style.ForegroundColorSpan(muted), android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE).append("  ")
            }
            val start = text.length
            text.append(entry.message).append("\n")
            when (entry.level) {
                "ERROR", "FATAL", "PANIC", "WARN", "WARNING" -> android.text.style.StyleSpan(android.graphics.Typeface.BOLD)
                "DEBUG", "TRACE" -> android.text.style.ForegroundColorSpan(muted)
                else -> null
            }?.let { text.setSpan(it, start, text.length, android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE) }
        }
        val offset = restoredScrollY ?: scrollY
        restoredScrollY = null
        content.text = text
        post {
            if (following) scrollToLatest() else scrollTo(0, offset)
            hasRendered = true
            updating = false
        }
    }
}
