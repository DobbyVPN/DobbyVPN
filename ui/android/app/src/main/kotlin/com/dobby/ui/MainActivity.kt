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
import androidx.compose.ui.graphics.toArgb
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

class MainActivity : ComponentActivity() {
    companion object {
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
        controller = SessionController(this)
        if (intent?.action == Intent.ACTION_VIEW) controller.importLink(intent.dataString.orEmpty())
        setContent {
            MaterialTheme(colorScheme = if (isSystemInDarkTheme()) darkColorScheme() else lightColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    DobbyApp(controller)
                }
            }
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
    val logs: String = "",
    val logsError: String = "",
    val exportingLogs: Boolean = false,
)

private class SessionController(private val activity: MainActivity) {
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
    private var debounce: Runnable? = null
    private var restoredLoad = ""
    private val clipboard = activity.getSystemService(ClipboardManager::class.java)
    private val clipboardListener = ClipboardManager.OnPrimaryClipChangedListener { refreshClipboard() }

    init {
        clipboard.addPrimaryClipChangedListener(clipboardListener)
        worker.scheduleWithFixedDelay({ refreshSnapshot() }, 0, 500, TimeUnit.MILLISECONDS)
        NativeVpnBridge.recordDiagnostic(activity, "startup.diagnostic_store_ready", "Android diagnostic store resolved")
        logWorker.scheduleWithFixedDelay({ if (visible) readLogs() }, 0, 750, TimeUnit.MILLISECONDS)
    }

    fun sourceChanged(value: String, immediate: Boolean = false) {
        if (permissionTarget != null) state = state.copy(busy = false)
        permissionTarget = null
        state = state.copy(source = value, sourceDirty = true, error = "", loadError = "")
        loadRevision++
        pendingLoad = null
        debounce?.let(main::removeCallbacks)
        val source = value.trim()
        val uri = runCatching { java.net.URI(source) }.getOrNull()
        if (uri?.scheme?.lowercase() != "https" || uri.host.isNullOrEmpty()) return
        debounce = Runnable { pendingLoad = source; loadNext() }.also { main.postDelayed(it, if (immediate) 0 else 400) }
    }

    fun retryLoad() = sourceChanged(state.source, true)

    private fun loadNext() {
        if (loadInFlight || latest.sessionId.isEmpty()) return
        val source = pendingLoad ?: return
        pendingLoad = null
        loadInFlight = true
        state = state.copy(loading = true)
        val revision = loadRevision
        loadWorker.execute {
            val outcome = runCatching {
                val snapshotResponse = JSONObject(NativeGoSession.snapshot(""))
                requireOK(snapshotResponse)
                val current = snapshotResponse.getJSONObject("result")
                requireOK(JSONObject(NativeGoSession.configure(current.getString("session_id"), current.getLong("sequence"), source.toByteArray(Charsets.UTF_8))))
            }
            main.post {
                loadInFlight = false
                state = state.copy(loading = false)
                if (revision == loadRevision) {
                    state = if (outcome.isSuccess) state.copy(sourceDirty = false, loadError = "")
                    else state.copy(loadError = outcome.exceptionOrNull()?.message ?: "Subscription could not be loaded")
                }
                worker.execute { refreshSnapshot() }
                loadNext()
            }
        }
    }

    fun show(screen: String) { state = state.copy(screen = screen) }

    fun isStopTarget(index: Int?): Boolean {
        val s = state.session
        s.pendingTarget?.let { return it.digest == s.digest && if (index == null) it.mode == "AUTO_SELECT" else it.mode == "PROFILE_INDEX" && it.index == index }
        if (s.primaryAction != "STOP") return false
        if (index == null) return s.activeMode == "AUTO_SELECT"
        return s.activeDigest == s.digest && if (s.state == "CONNECTED") s.activeProfileIndex == index else s.activeMode == "PROFILE_INDEX" && s.activeIndex == index
    }

    fun canAct(index: Int? = null): Boolean = !state.busy && permissionTarget == null &&
        (isStopTarget(index) || (!state.sourceDirty && !state.loading && state.loadError.isEmpty() && state.session.configured &&
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

    fun permissionResult(granted: Boolean) = continuePermission(granted)

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
        state = state.copy(canPaste = clipboard.hasPrimaryClip() && clipboard.primaryClipDescription?.hasMimeType(ClipDescription.MIMETYPE_TEXT_PLAIN) == true)
    }

    fun paste() {
        val value = clipboard.primaryClip?.getItemAt(0)?.text?.toString()?.trim().orEmpty()
        importSubscription(value)
    }

    private fun importSubscription(value: String) {
        val uri = runCatching { java.net.URI(value) }.getOrNull()
        if (uri?.scheme?.lowercase() != "https" || uri.host.isNullOrEmpty()) { report("Paste an HTTPS subscription URL with a host"); return }
        if (value == state.source && (state.loading || !state.sourceDirty && state.session.configured)) return
        sourceChanged(value, true)
    }

    fun importLink(value: String) {
        try {
            if (value.equals("dobbyvpn://", true)) return
            val uri = java.net.URI(value)
            require(uri.scheme.equals("dobbyvpn", true) && uri.host == "import" && uri.rawPath.isNullOrEmpty() && uri.rawFragment == null && uri.rawUserInfo == null && uri.port == -1)
            val query = uri.rawQuery.orEmpty().split('&')
            require(query.size == 1)
            val pair = query.single().split('=', limit = 2)
            require(pair.size == 2 && pair[0] == "url")
            importSubscription(java.net.URLDecoder.decode(pair[1].replace("+", "%2B"), "UTF-8"))
        } catch (failure: Exception) { report("Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL", failure) }
    }

    private fun readDiagnostics(): Pair<String, String> {
        val contents = mutableListOf<String>()
        val errors = mutableListOf<String>()
        NativeVpnBridge.diagnosticPaths(activity).lineSequence().filter(String::isNotBlank).flatMap { sequenceOf(it + ".previous", it) }.forEach { path ->
            try {
                val file = File(path)
                if (file.exists()) {
                    val text = file.inputStream().use { stream ->
                        val size = stream.channel.size()
                        val bytes = ByteArray(minOf(size, 131_072L).toInt())
                        stream.channel.position(size - bytes.size)
                        java.io.DataInputStream(stream).readFully(bytes)
                        bytes.toString(Charsets.UTF_8)
                    }
                    contents.add(text)
                }
            } catch (failure: Exception) {
                errors.add("$path: ${failure.stackTraceToString()}")
            }
        }
        if (NativeVpnBridge.nativeDiagnosticsUnavailable()) {
            errors.add("Some native diagnostics could not be saved. Check Android system logs.")
        }
        val text = contents.joinToString("\n")
        return text to errors.joinToString("\n")
    }

    private fun readLogs() {
        val (text, error) = readDiagnostics()
        main.post { state = state.copy(logs = text, logsError = error) }
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
        val commit = BuildConfig.PROJECT_REPOSITORY_COMMIT
        if (commit.isBlank() || commit == "N/A") return
        val uri = Uri.parse("https://github.com/DobbyVPN/DobbyVPN/tree/$commit")
        activity.startActivity(Intent(Intent.ACTION_VIEW, uri))
    }

    fun close() {
        clipboard.removePrimaryClipChangedListener(clipboardListener)
        worker.shutdownNow()
        logWorker.shutdownNow()
        loadWorker.shutdownNow()
        debounce?.let(main::removeCallbacks)
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
    BoxWithConstraints(modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        val controlsHeight = maxHeight * 0.65f
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Column(
                Modifier.heightIn(max = controlsHeight).verticalScroll(rememberScrollState()),
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
                    modifier = Modifier.fillMaxWidth().semantics { contentDescription = "VPN connection action" },
                ) {
                    if (state.busy || session.state in setOf("PROBING", "PREPARING", "STOPPING")) {
                        CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(8.dp))
                    }
                    Text(controller.actionTitle())
                }
                if (session.primaryAction == "STOP" && session.activeDigest != session.digest) {
                    TextButton(onClick = controller::stop, enabled = !state.busy) { Text(if (session.state == "CONNECTED") "Disconnect" else "Stop") }
                }
                session.profiles.forEach { profile ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                        Column(Modifier.weight(1f)) { Text(profile.name); Text(profile.protocol, style = MaterialTheme.typography.bodySmall) }
                        Button(onClick = { controller.connectOrDisconnect(profile.index) }, enabled = controller.canAct(profile.index),
                            modifier = Modifier.semantics { contentDescription = "Profile ${profile.index + 1} action" }) { Text(controller.actionTitle(profile.index)) }
                    }
                }
            }
            LogsPane(controller, Modifier.weight(1f))
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
        TextButton(onClick = controller::openSourceCommit) { Text("Source code") }
        Button(onClick = { controller.show("connection") }) { Text("Back") }
    }
}

@Composable
private fun LogsPane(controller: SessionController, modifier: Modifier) {
    val state = controller.state
    var jump by remember { mutableIntStateOf(0) }
    val color = MaterialTheme.colorScheme.onSurface.toArgb()
    Column(modifier, verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text("Logs", modifier = Modifier.align(androidx.compose.ui.Alignment.CenterVertically), style = MaterialTheme.typography.titleMedium)
            TextButton(onClick = { jump++ }) { Text("Jump to latest") }
            TextButton(onClick = controller::exportLogs, enabled = !state.exportingLogs) { Text("Share logs") }
        }
        if (state.logsError.isNotEmpty()) {
            Text("Some diagnostics could not be read or shared. Details are included in the logs.", color = MaterialTheme.colorScheme.error)
        }
        Text("Recent logs. Shared diagnostics include the complete files.", style = MaterialTheme.typography.labelSmall)
        AndroidView(
            factory = { LiveLogView(it) },
            modifier = Modifier.fillMaxWidth().weight(1f),
            update = { it.update(state.logs, jump, color) },
        )
    }
}

private class LiveLogView(context: android.content.Context) : android.widget.ScrollView(context) {
    private val content = android.widget.TextView(context).apply {
        textSize = 12f
        typeface = android.graphics.Typeface.MONOSPACE
        setTextIsSelectable(true)
        contentDescription = "Connection logs"
    }
    private var following = true
    private var lastJump = 0
    private var rendered = ""

    init {
        isFillViewport = true
        outlineProvider = android.view.ViewOutlineProvider.BOUNDS
        clipToOutline = true
        addView(content)
    }

    override fun onInterceptTouchEvent(event: android.view.MotionEvent): Boolean {
        if (event.actionMasked == android.view.MotionEvent.ACTION_DOWN) following = false
        return super.onInterceptTouchEvent(event)
    }

    override fun onTouchEvent(event: android.view.MotionEvent): Boolean {
        val handled = super.onTouchEvent(event)
        if (event.actionMasked == android.view.MotionEvent.ACTION_UP) {
            post { following = !canScrollVertically(1) }
        }
        return handled
    }

    fun update(text: String, jump: Int, color: Int) {
        content.setTextColor(color)
        if (jump != lastJump) { following = true; lastJump = jump }
        if (!following) return
        if (rendered != text) {
            val offset = scrollY
            if (text.startsWith(rendered)) content.append(text.substring(rendered.length)) else content.text = text
            rendered = text
            // fullScroll also moves keyboard focus; log refresh must not take
            // focus from the configuration field or dismiss its keyboard.
            post { if (following) scrollTo(0, content.bottom) else scrollTo(0, offset) }
        } else if (following) { post { scrollTo(0, content.bottom) } }
    }
}
