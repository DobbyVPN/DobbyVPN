package com.dobby.ui

import android.content.ClipboardManager
import android.content.ClipDescription
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.dobby.nativebridge.NativeGoSession
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.vpn.BuildConfig
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

private const val COMPOSE_LAYOUT_TRACE_EXTRA = "dobbyvpn.traceComposeLayout"

private object ComposeLayoutTrace {
    private val lastMeasurements = mutableMapOf<String, String>()

    @Synchronized
    fun record(name: String, measurement: String) {
        if (lastMeasurements.put(name, measurement) != measurement) {
            android.util.Log.i("DobbyComposeLayout", "$name $measurement")
        }
    }
}

private fun Modifier.traceComposeConstraints(name: String): Modifier = layout { measurable, constraints ->
    val placeable = measurable.measure(constraints)
    if (MainActivity.current?.intent?.getBooleanExtra(COMPOSE_LAYOUT_TRACE_EXTRA, false) == true) {
        ComposeLayoutTrace.record(
            name,
            "incoming=$constraints boundedHeight=${constraints.hasBoundedHeight} " +
                "measured=${placeable.width}x${placeable.height}",
        )
    }
    layout(placeable.width, placeable.height) { placeable.placeRelative(0, 0) }
}

class MainActivity : ComponentActivity() {
    companion object {
        private const val LOG_VIEW_STATE = "com.dobby.ui.LOG_VIEW_STATE"

        @Volatile
        @JvmField
        var current: MainActivity? = null
    }

    private lateinit var controller: SessionController
    private val vpnConsentLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        controller.permissionResult(result.resultCode == android.app.Activity.RESULT_OK)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        current = this
        NativeGoSession.attach(this)
        controller = SessionController(this, savedInstanceState?.getBundle(LOG_VIEW_STATE))
        if (intent?.action == Intent.ACTION_VIEW) controller.importLink(intent.dataString.orEmpty())
        setContent {
            MaterialTheme(colorScheme = if (isSystemInDarkTheme()) darkColorScheme() else lightColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    DobbyApp(controller)
                }
            }
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::controller.isInitialized) {
            controller.saveLogViewState()?.let { outState.putBundle(LOG_VIEW_STATE, it) }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.action == Intent.ACTION_VIEW) controller.importLink(intent.dataString.orEmpty())
    }

    override fun onStart() {
        super.onStart()
        if (::controller.isInitialized) controller.setVisible(true)
    }

    override fun onStop() {
        if (::controller.isInitialized) controller.setVisible(false)
        super.onStop()
    }

    internal fun launchVpnConsent(permission: Intent) {
        vpnConsentLauncher.launch(permission)
    }

    override fun onDestroy() {
        if (::controller.isInitialized) controller.close()
        if (current === this) current = null
        super.onDestroy()
    }
}

private data class ProfileData(val index: Int, val description: String, val protocol: String) {
    val name: String get() = description.ifBlank { "Profile ${index + 1}" }
}
private data class SelectionData(val digest: String, val mode: String, val index: Int)
private data class SessionData(
    val sessionId: String = "",
    val sequence: Long = 0,
    val generation: Long = 0,
    val state: String = "IDLE",
    val primaryAction: String = "NONE",
    val configured: Boolean = false,
    val sourceUrl: String = "",
    val digest: String = "",
    val profiles: List<ProfileData> = emptyList(),
    val activeDigest: String = "",
    val activeMode: String = "",
    val activeIndex: Int = 0,
    val activeProfileIndex: Int = -1,
    val pendingTarget: SelectionData? = null,
    val canSwitch: Boolean = false,
    val activeProfile: String = "",
    val recovering: Boolean = false,
    val failure: String = "",
    val sourceError: String = "",
)

private data class ScreenState(
    val session: SessionData = SessionData(),
    val source: String = "",
    val sourceDirty: Boolean = false,
    val loading: Boolean = false,
    val loadError: String = "",
    val canPaste: Boolean = false,
    val busy: Boolean = false,
    val error: String = "",
    val screen: String = "connection",
    val logs: List<LogEntry> = emptyList(),
    val clearRevision: Int = 0,
    val logsError: String = "",
    val exportingLogs: Boolean = false,
)

private class SessionController(
    private val activity: MainActivity,
    private var restoredLogViewState: Bundle? = null,
) {
    var state by mutableStateOf(ScreenState())
        private set

    private val main = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadScheduledExecutor()
    private val logWorker = Executors.newSingleThreadScheduledExecutor()
    @Volatile private var visible = false
    @Volatile private var latest = SessionData()
    private var permissionTarget: Pair<SessionData, Int?>? = null
    private val loadWorker = Executors.newSingleThreadExecutor()
    private var loadInFlight = false
    private var loadRevision = 0
    private var pendingLoad: String? = null
    private var scheduledSource: String? = null
    private var inFlightSource: String? = null
    private var debounce: Runnable? = null
    private var restoredLoad = ""
    private var acceptedSequence = 0L
    private val clipboard = activity.getSystemService(ClipboardManager::class.java)
    private val clipboardListener = ClipboardManager.OnPrimaryClipChangedListener {
        refreshClipboard()
    }

    private val diagnosticView = StructuredLogs(
        NativeVpnBridge.diagnosticPaths(activity).lineSequence().filter(String::isNotBlank).toList(),
        File(activity.filesDir, "diagnostic-view.json"),
    )


    init {
        clipboard.addPrimaryClipChangedListener(clipboardListener)
        worker.scheduleWithFixedDelay({
            refreshSnapshot()
            if (visible) refreshClipboard()
        }, 0, 500, TimeUnit.MILLISECONDS)
        NativeVpnBridge.recordDiagnostic(activity, "startup.diagnostic_store_ready", "Android diagnostic store resolved")
        logWorker.scheduleWithFixedDelay({ if (visible) readLogs() }, 0, 750, TimeUnit.MILLISECONDS)
    }

    fun sourceChanged(value: String, immediate: Boolean = false) {
        val source = value.trim()
        val uri = runCatching { java.net.URI(source) }.getOrNull()
        val alreadyAccepted = !immediate && uri?.scheme?.lowercase() == "https" &&
            !uri.host.isNullOrEmpty() && latest.configured && latest.sourceUrl == source &&
            !loadInFlight && inFlightSource == null
        if (alreadyAccepted) {
            permissionTarget = null
            state = state.copy(
                source = source,
                sourceDirty = false,
                loading = false,
                loadError = "",
                error = "",
            )
            loadRevision++
            pendingLoad = null
            scheduledSource = null
            debounce?.let(main::removeCallbacks)
            return
        }
        if (permissionTarget != null) state = state.copy(busy = false)
        permissionTarget = null
        state = state.copy(source = value, sourceDirty = true, error = "", loadError = "")
        loadRevision++
        pendingLoad = null
        scheduledSource = null
        debounce?.let(main::removeCallbacks)
        if (uri?.scheme?.lowercase() != "https" || uri.host.isNullOrEmpty()) return
        val revision = loadRevision
        scheduledSource = source
        debounce = Runnable {
            if (revision == loadRevision) {
                scheduledSource = null
                pendingLoad = source
                loadNext()
            }
        }.also { main.postDelayed(it, if (immediate) 0 else 400) }
    }

    fun retryLoad() = sourceChanged(state.source, true)

    private fun loadNext() {
        if (loadInFlight || latest.sessionId.isEmpty()) return
        val source = pendingLoad ?: return
        pendingLoad = null
        loadInFlight = true
        inFlightSource = source
        state = state.copy(loading = true)
        val revision = loadRevision
        loadWorker.execute {
            val outcome = runCatching {
                val snapshotResponse = JSONObject(NativeGoSession.snapshot(""))
                requireOK(snapshotResponse)
                val current = snapshotResponse.getJSONObject("result")
                val configured = JSONObject(NativeGoSession.configure(current.getString("session_id"), current.getLong("sequence"), source.toByteArray(Charsets.UTF_8)))
                requireOK(configured)
                configured.getJSONObject("result").getLong("sequence")
            }
            main.post {
                loadInFlight = false
                inFlightSource = null
                state = state.copy(loading = false)
                if (revision == loadRevision) {
                    if (outcome.isSuccess) acceptedSequence = outcome.getOrThrow()
                    state = if (outcome.isSuccess) state.copy(sourceDirty = false, loadError = "")
                    else state.copy(loadError = outcome.exceptionOrNull()?.message ?: "Subscription could not be loaded")
                }
                worker.execute { refreshSnapshot() }
                loadNext()
            }
        }
    }

    fun show(screen: String) { state = state.copy(screen = screen) }

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
            find(content)?.saveState()
        }
    }.getOrNull()

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

    fun actionTitle(index: Int? = null): String = if (isStopTarget(index)) {
        if (state.session.state == "CONNECTED" && state.session.pendingTarget == null) "Disconnect" else "Stop"
    } else if (index == null) "Auto connect" else "Connect"

    fun connectOrDisconnect(index: Int? = null) {
        if (!canAct(index)) return
        if (isStopTarget(index)) { stop(); return }
        val current = latest
        state = state.copy(busy = true, error = "")
        permissionTarget = current to index
        worker.execute {
            when (NativeVpnBridge.prepare(activity)) {
                1 -> main.post { continuePermission(true) }
                0 -> main.post { state = state.copy(busy = false, error = "Approve the Android VPN permission to connect") }
                else -> main.post { permissionTarget = null; report("Android VPN service could not be prepared") }
            }
        }
    }

    fun stop() {
        permissionTarget = null
        val current = latest
        if (state.busy || current.primaryAction != "STOP") return
        state = state.copy(busy = true, error = "")
        worker.execute {
            runCatching { NativeGoSession.stop(current.sessionId, current.generation) }
                .onSuccess { consumeSnapshotCommand(it, "Stop failed") }
                .onFailure { report(it.message ?: "Stop failed", it) }
        }
    }

    fun permissionResult(granted: Boolean) {
        if (permissionTarget == null) return
        if (!granted) { continuePermission(false); return }
        state = state.copy(busy = true, error = "")
        // Granting consent only authorizes VpnService. Prepare again off the UI
        // thread to start it and wait for attachment before Go acquires a TUN.
        worker.execute {
            val ready = NativeVpnBridge.prepare(activity)
            main.post {
                if (permissionTarget == null) return@post
                if (ready == 1) continuePermission(true)
                else {
                    permissionTarget = null
                    report("Android VPN service could not be prepared after permission approval")
                }
            }
        }
    }

    private fun continuePermission(granted: Boolean) {
        val target = permissionTarget ?: return
        permissionTarget = null
        if (!granted) { report("VPN permission was not granted"); return }
        val (selected, index) = target
        val current = latest
        if (state.sourceDirty || current.sessionId != selected.sessionId || current.digest != selected.digest) {
            state = state.copy(busy = false)
            return
        }
        state = state.copy(busy = true, error = "")
        worker.execute {
            runCatching { NativeGoSession.startSelection(current.sessionId, current.sequence, if (index == null) "AUTO_SELECT" else "PROFILE_INDEX", index ?: 0, selected.digest) }
                .onSuccess { consumeSnapshotCommand(it, "Connect failed") }
                .onFailure { report(it.message ?: "Connect failed", it) }
        }
    }

    fun setVisible(value: Boolean) { visible = value; if (value) refreshClipboard() }

    private fun refreshClipboard() {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            main.post { refreshClipboard() }
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
        importSubscription(value)
    }

    private fun importSubscription(value: String) {
        val uri = runCatching { java.net.URI(value) }.getOrNull()
        if (uri?.scheme?.lowercase() != "https" || uri.host.isNullOrEmpty()) { report("Paste an HTTPS subscription URL with a host"); return }
        val source = value.trim()
        if (source == state.source && (scheduledSource == source || pendingLoad == source || inFlightSource == source)) return
        val noOutstandingLoad = !loadInFlight && scheduledSource == null && pendingLoad == null
        if (noOutstandingLoad && state.loadError.isEmpty() && latest.configured && latest.sourceUrl == source) {
            state = state.copy(source = source, sourceDirty = false, loadError = "", error = "")
            return
        }
        sourceChanged(value, true)
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
            importSubscription(java.net.URLDecoder.decode(pair[1].replace("+", "%2B"), "UTF-8"))
        } catch (failure: Exception) { report("Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL", failure) }
    }


    private fun readLogs() {
        val (entries, error) = diagnosticView.read()
        main.post { state = state.copy(logs = entries, logsError = error) }
    }

    fun clearLogs() {
        logWorker.execute {
            try {
                diagnosticView.clear()
                main.post { state = state.copy(logs = emptyList(), clearRevision = state.clearRevision + 1) }
            } catch (failure: Exception) { main.post { state = state.copy(logsError = failure.stackTraceToString()) } }
        }
    }

    fun exportLogs() {
        if (state.exportingLogs) return
        state = state.copy(exportingLogs = true)
        logWorker.execute {
            val succeeded = NativeVpnBridge.exportLogs(activity)
            main.post {
                state = state.copy(
                    exportingLogs = false,
                    logsError = if (succeeded) "" else "Android could not export diagnostics. See logs for details.",
                )
            }
        }
    }

    fun openSourceCommit() {
        val link = BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK
        if (link.isBlank() || link.endsWith("/N/A")) return
        activity.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(link)))
    }

    fun close() {
        clipboard.removePrimaryClipChangedListener(clipboardListener)
        worker.shutdownNow()
        logWorker.shutdownNow()
        loadWorker.shutdownNow()
        debounce?.let(main::removeCallbacks)
        scheduledSource = null
    }

    private fun refreshSnapshot(clearBusy: Boolean = false) {
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
                state = snapshot.optString("state", "IDLE"),
                primaryAction = snapshot.optString("primary_action", "NONE"),
                configured = snapshot.optBoolean("configured"),
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
                    listOf(it.optString("protocol"), it.optString("description"))
                        .filter(String::isNotBlank)
                        .joinToString(" · ")
                }.orEmpty(),
                recovering = snapshot.optBoolean("recovering"),
                failure = failure?.let {
                    val message = it.optString("message")
                    val code = it.optString("code")
                    if (message.isBlank()) code else "$message ($code)"
                }.orEmpty(),
                sourceError = snapshot.optString("source_error"),
            )
            latest = current
            main.post {
                if (current.sessionId == state.session.sessionId && current.sequence < maxOf(state.session.sequence, acceptedSequence)) return@post
                if (current.sessionId != state.session.sessionId) acceptedSequence = 0
                state = state.copy(
                    session = current,
                    source = if (state.sourceDirty || current.sourceUrl.isEmpty()) state.source else current.sourceUrl,
                    error = when {
                        current.sourceError.isNotEmpty() -> current.sourceError
                        reattached || state.session.sessionId.isEmpty() -> ""
                        else -> state.error
                    },
                    busy = if (clearBusy) false else state.busy,
                )
                val restoreKey = current.sessionId + "|" + state.source
                if (!current.configured && !state.sourceDirty && state.source.isNotEmpty() && restoredLoad != restoreKey) {
                    restoredLoad = restoreKey
                    sourceChanged(state.source, true)
                }
                loadNext()
            }
        } catch (failure: Exception) {
            latest = SessionData()
            val message = commandError(failure)
            NativeVpnBridge.recordDiagnostic(activity, "ui.snapshot_failed", message, failure)
            main.post { state = state.copy(session = SessionData(), busy = false, error = message) }
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
        main.post { state = state.copy(busy = false, error = message) }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DobbyApp(controller: SessionController) {
    Scaffold(
        modifier = Modifier.fillMaxSize(),
        topBar = {
            TopAppBar(title = { Text("DobbyVPN") }, actions = {
                TextButton(onClick = { controller.show("about") }) { Text("About") }
            })
        },
    ) { padding ->
        if (controller.state.screen == "about") {
            AboutScreen(controller, Modifier.padding(padding))
        } else {
            ConnectionScreen(controller, Modifier.padding(padding).consumeWindowInsets(padding).imePadding())
        }
    }
}

@Composable
private fun ConnectionScreen(controller: SessionController, modifier: Modifier) {
    val state = controller.state
    val session = state.session
    val focus = LocalFocusManager.current
    val status = when {
        session.recovering -> "Reconnecting"
        state.error.isNotEmpty() -> "Error"
        session.state == "CONNECTED" -> "Connected"
        session.state == "PROBING" || session.state == "PREPARING" -> "Connecting"
        session.state == "STOPPING" -> "Stopping"
        session.failure.isNotEmpty() -> "Failed"
        else -> "Disconnected"
    }
    BoxWithConstraints(modifier.traceComposeConstraints("connection-box").fillMaxSize().padding(horizontal = 16.dp)) {
        val controlsHeight = maxHeight * 0.5f
        Column(Modifier.traceComposeConstraints("connection-column").fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Column(
                Modifier.heightIn(max = controlsHeight).clipToBounds().verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                OutlinedTextField(
                    value = state.source,
                    onValueChange = { controller.sourceChanged(it) },
                    modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Connection configuration" },
                    label = { Text("Subscription URL") },
                    placeholder = { Text("https://…") },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = KeyboardType.Uri,
                        imeAction = ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(onDone = { focus.clearFocus() }),
                )
                if (state.canPaste) TextButton(onClick = controller::paste) { Text("Paste") }
                if (state.loading) Text("Loading profiles…")
                if (state.loadError.isNotEmpty()) {
                    Text(state.loadError, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = controller::retryLoad) { Text("Retry") }
                }
                Text(status, modifier = Modifier.semantics { contentDescription = status; liveRegion = LiveRegionMode.Polite }, style = MaterialTheme.typography.titleMedium)
                if (session.activeProfile.isNotEmpty()) {
                    Text(session.activeProfile, modifier = Modifier.semantics { contentDescription = "Active profile" })
                }
                if (state.error.isNotEmpty() || session.failure.isNotEmpty()) {
                    Text(
                        state.error.ifEmpty { session.failure },
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                Button(
                    onClick = { focus.clearFocus(); controller.connectOrDisconnect() },
                    enabled = controller.canAct(),
                    modifier = Modifier.semantics {
                        contentDescription = "VPN connection action"
                    },
                ) {
                    if (state.busy || session.state in setOf("PROBING", "PREPARING", "STOPPING")) {
                        CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(8.dp))
                    }
                    Text(controller.actionTitle())
                }
                if (session.primaryAction == "STOP" && !controller.isStopTarget(null) && session.profiles.none { controller.isStopTarget(it.index) }) {
                    TextButton(onClick = controller::stop, enabled = !state.busy) { Text(if (session.state == "CONNECTED") "Disconnect" else "Stop") }
                }
                session.profiles.forEach { profile ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                        Column(Modifier.weight(1f)) { Text(profile.name); Text(profile.protocol, style = MaterialTheme.typography.bodySmall) }
                        Button(onClick = { controller.connectOrDisconnect(profile.index) }, enabled = controller.canAct(profile.index),
                            modifier = Modifier.semantics {
                                contentDescription = "Profile ${profile.index + 1} action"
                            }) { Text(controller.actionTitle(profile.index)) }
                    }
                }
            }
            LogsPane(controller)
        }
    }
}

@Composable
private fun AboutScreen(controller: SessionController, modifier: Modifier) {
    Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text("About DobbyVPN", style = MaterialTheme.typography.headlineMedium)
        SelectionContainer {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Version: ${BuildConfig.VERSION_NAME}")
                Text("Commit: ${BuildConfig.PROJECT_REPOSITORY_COMMIT.take(8)}")
                Text("Source commit: ${BuildConfig.PROJECT_REPOSITORY_COMMIT}", modifier = Modifier.semantics { contentDescription = "Source commit" })
            }
        }
        TextButton(
            onClick = controller::openSourceCommit,
            enabled = BuildConfig.PROJECT_REPOSITORY_COMMIT.isNotBlank() && BuildConfig.PROJECT_REPOSITORY_COMMIT != "N/A",
            modifier = Modifier.semantics {
                contentDescription = "Source code ${BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK}"
            },
        ) { Text("Source code") }
        Button(onClick = { controller.show("connection") }) { Text("Back") }
    }
}

@Composable
private fun ColumnScope.LogsPane(controller: SessionController) {
    val state = controller.state
    val colors = MaterialTheme.colorScheme
    val normalColor = colors.onSurface.toArgb()
    val mutedColor = colors.onSurfaceVariant.toArgb()
    val warningColor = (if (isSystemInDarkTheme()) androidx.compose.ui.graphics.Color(0xFFFFD084)
        else androidx.compose.ui.graphics.Color(0xFF8A5A00)).toArgb()
    val errorColor = colors.error.toArgb()
    Column(Modifier.fillMaxWidth()) {
        Text("Logs", style = MaterialTheme.typography.titleMedium)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            TextButton(onClick = controller::clearLogs) { Text("Clear") }
            TextButton(onClick = controller::exportLogs, enabled = !state.exportingLogs) { Text("Share logs") }
        }
    }
    if (state.logsError.isNotEmpty()) {
        Text("Some diagnostics could not be read or shared. Details are included in the logs.", color = MaterialTheme.colorScheme.error)
    }
    AndroidView(
        factory = { controller.createLogView(it) },
        modifier = Modifier.fillMaxWidth().weight(1f)
            .traceComposeConstraints("logs-android-view")
            .clipToBounds(),
        update = { it.update(state.logs, state.clearRevision, normalColor, mutedColor, warningColor, errorColor) },
    )
}

private class LiveLogView(context: android.content.Context) : android.widget.ScrollView(context) {
    private val content = android.widget.TextView(context).apply {
        textSize = 12f
        setTextIsSelectable(true)
        movementMethod = android.text.method.LinkMovementMethod.getInstance()
        contentDescription = "Connection logs"
    }
    private var following = true
    private var updating = false
    private var userScrolling = false
    private var lastClear = 0
    private var normalColor = android.graphics.Color.BLACK
    private var mutedColor = android.graphics.Color.GRAY
    private var warningColor = android.graphics.Color.rgb(138, 90, 0)
    private var errorColor = android.graphics.Color.rgb(179, 38, 30)
    private var rendered = emptyList<LogEntry>()
    private var latest = emptyList<LogEntry>()
    private val expanded = mutableSetOf<String>()
    private var hasRendered = false
    private var awaitingRestoredEntries = false
    private var restoredScrollY: Int? = null
    private var restoredVisibleBoundaries: Map<String, Long>? = null

    init { isFillViewport = true; addView(content) }

    fun restoreState(state: Bundle) {
        following = state.getBoolean("following", true)
        restoredScrollY = state.getInt("scroll_y", 0).coerceAtLeast(0)
        expanded.addAll(state.getStringArrayList("expanded_ids").orEmpty())
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
        putStringArrayList("expanded_ids", ArrayList(expanded))
        val visibleBoundaries = rendered.mapNotNull { entry ->
            val separator = entry.id.lastIndexOf(':')
            val offset = entry.id.substring(separator + 1).toLongOrNull()
            if (separator < 0 || offset == null) null else entry.id.substring(0, separator) to offset
        }.groupBy({ it.first }, { it.second }).mapValues { (_, offsets) -> offsets.maxOrNull() ?: 0L }
        putStringArrayList("visible_stream_ids", ArrayList(visibleBoundaries.keys))
        putLongArray("visible_stream_offsets", visibleBoundaries.values.toLongArray())
    }

    override fun onInterceptTouchEvent(event: android.view.MotionEvent): Boolean {
        if (event.actionMasked == android.view.MotionEvent.ACTION_DOWN) userScrolling = true
        return super.onInterceptTouchEvent(event)
    }

    override fun onScrollChanged(left: Int, top: Int, oldLeft: Int, oldTop: Int) {
        super.onScrollChanged(left, top, oldLeft, oldTop)
        if (updating || !userScrolling || top == oldTop) return
        following = !canScrollVertically(1)
        if (following && latest != rendered) post { render(latest) }
    }

    fun update(entries: List<LogEntry>, clear: Int, normal: Int, muted: Int, warning: Int, error: Int) {
        if (clear != lastClear) {
            following = true
            lastClear = clear
            expanded.clear()
            awaitingRestoredEntries = false
            restoredVisibleBoundaries = null
        }
        if (awaitingRestoredEntries && entries.isEmpty()) return
        val themeChanged = normalColor != normal || mutedColor != muted || warningColor != warning || errorColor != error
        normalColor = normal
        mutedColor = muted
        warningColor = warning
        errorColor = error
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
        if (!hasRendered || following && (rendered != entries || themeChanged)) render(entries)
        else if (themeChanged) render(rendered)
    }

    private fun render(entries: List<LogEntry>) {
        updating = true
        rendered = entries
        val text = android.text.SpannableStringBuilder()
        entries.forEach { entry ->
            val start = text.length
            text.append(listOf(entry.timestamp, entry.level, entry.source).filter(String::isNotEmpty).joinToString(" · "))
                .append("\n").append(entry.message).append("\n")
            val color = when (entry.level) {
                "ERROR", "FATAL", "PANIC" -> errorColor
                "WARN", "WARNING" -> warningColor
                "DEBUG", "TRACE" -> mutedColor
                else -> normalColor
            }
            text.setSpan(android.text.style.ForegroundColorSpan(color), start, text.length, android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            if (entry.level != "RAW") {
                val linkStart = text.length
                text.append(if (expanded.contains(entry.id)) "Hide details\n" else "Details\n")
                text.setSpan(object : android.text.style.ClickableSpan() {
                    override fun onClick(widget: android.view.View) {
                        if (!expanded.add(entry.id)) expanded.remove(entry.id)
                        render(rendered)
                    }
                }, linkStart, text.length, android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                if (expanded.contains(entry.id)) text.append(entry.raw).append("\n")
            }
        }
        val offset = restoredScrollY ?: scrollY
        restoredScrollY = null
        content.setTextColor(normalColor)
        content.text = text
        post {
            if (following) scrollTo(0, content.bottom) else scrollTo(0, offset)
            hasRendered = true
            updating = false
        }
    }
}
