package com.dobby.ui

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
import androidx.compose.runtime.saveable.rememberSaveable
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
        setContent {
            MaterialTheme(colorScheme = if (isSystemInDarkTheme()) darkColorScheme() else lightColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    DobbyApp(controller)
                }
            }
        }
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

private data class SessionData(
    val sessionId: String = "",
    val sequence: Long = 0,
    val generation: Long = 0,
    val state: String = "IDLE",
    val primaryAction: String = "NONE",
    val configured: Boolean = false,
    val sourceUrl: String = "",
    val activeProfile: String = "",
    val recovering: Boolean = false,
    val failure: String = "",
    val sourceError: String = "",
)

private data class ScreenState(
    val session: SessionData = SessionData(),
    val source: String = "",
    val sourceDirty: Boolean = false,
    val busy: Boolean = false,
    val error: String = "",
    val screen: String = "connection",
    val logs: String = "",
    val logsError: String = "",
)

private class SessionController(private val activity: MainActivity) {
    var state by mutableStateOf(ScreenState())
        private set

    private val main = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadScheduledExecutor()
    private val logWorker = Executors.newSingleThreadScheduledExecutor()
    @Volatile private var visible = false
    @Volatile private var latest = SessionData()
    @Volatile private var pendingPermission = false

    init {
        worker.scheduleWithFixedDelay({ refreshSnapshot() }, 0, 500, TimeUnit.MILLISECONDS)
        NativeVpnBridge.recordDiagnostic(activity, "startup.diagnostic_store_ready", "Android diagnostic store resolved")
        logWorker.scheduleWithFixedDelay({ if (visible) readLogs() }, 0, 750, TimeUnit.MILLISECONDS)
    }

    fun sourceChanged(value: String) {
        state = state.copy(source = value, sourceDirty = true, error = "")
    }

    fun show(screen: String) {
        state = state.copy(screen = screen)

    }

    fun connectOrDisconnect() {
        if (state.busy) return
        val current = latest
        when (current.primaryAction) {
            "STOP" -> {
                if (current.generation <= 0) return
                state = state.copy(busy = true, error = "")
                worker.execute {
                    runCatching { NativeGoSession.stop(current.sessionId, current.generation) }
                        .onSuccess { response -> consumeSnapshotCommand(response, "Stop failed") }
                        .onFailure { failure -> report(failure.message ?: "Stop failed") }
                }
            }
            "START" -> {
                state = state.copy(busy = true, error = "")
                val currentUI = state
                worker.execute { prepareAndStart(currentUI) }
            }
            else -> return
        }
    }

    fun permissionResult(granted: Boolean) {
        if (!pendingPermission) return
        pendingPermission = false
        if (!granted) {
            report("VPN permission was not granted")
            return
        }
        state = state.copy(error = "VPN permission granted. Press Connect to continue")
    }

    fun setVisible(value: Boolean) { visible = value }

    private fun readDiagnostics(preview: Boolean = false): Pair<String, String> {
        val contents = mutableListOf<String>()
        val errors = mutableListOf<String>()
        NativeVpnBridge.diagnosticPaths(activity).lineSequence().filter(String::isNotBlank).forEach { path ->
            try {
                val file = File(path)
                if (file.exists()) {
                    val text = file.inputStream().use { stream ->
                        if (preview && stream.channel.size() > 262_144) stream.channel.position(stream.channel.size() - 262_144)
                        stream.readBytes().toString(Charsets.UTF_8)
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
        val (text, error) = readDiagnostics(preview = true)
        main.post { state = state.copy(logs = text, logsError = error) }
    }

    fun exportLogs() {
        logWorker.execute {
            val (text, error) = readDiagnostics()
            val metadata = JSONObject().put("app_version", BuildConfig.VERSION_NAME)
                .put("source_commit", BuildConfig.PROJECT_REPOSITORY_COMMIT)
                .put("platform", "Android ${android.os.Build.VERSION.RELEASE}")
                .put("captured_at", java.time.Instant.now().toString())
                .put("collection_errors", error)
            val content = "$metadata\n$text"
            if (!NativeVpnBridge.exportLogs(activity, content.toByteArray(Charsets.UTF_8))) {
                main.post { state = state.copy(logsError = "Android could not open the log share sheet") }
            } else { main.post { state = state.copy(logsError = error) } }
        }
    }

    fun openSourceCommit() {
        val commit = BuildConfig.PROJECT_REPOSITORY_COMMIT
        if (commit.isBlank() || commit == "N/A") return
        val uri = Uri.parse("https://github.com/DobbyVPN/DobbyVPN/tree/$commit")
        activity.startActivity(Intent(Intent.ACTION_VIEW, uri))
    }

    fun close() {
        worker.shutdownNow()
        logWorker.shutdownNow()
    }

    private fun prepareAndStart(currentUI: ScreenState) {
        try {
            if (latest.sessionId.isEmpty()) refreshSnapshot()
            var current = latest
            if (current.sessionId.isEmpty()) throw IllegalStateException("Go session is not ready")
            if (!current.configured || currentUI.sourceDirty) {
                val source = currentUI.source.trim()
                if (source.isEmpty()) {
                    report("Enter an HTTPS connection URL or inline configuration")
                    return
                }
                val response = JSONObject(
                    NativeGoSession.configure(current.sessionId, current.sequence, source.toByteArray(Charsets.UTF_8)),
                )
                requireOK(response)
                val configured = response.getJSONObject("result")
                current = current.copy(
                    sequence = configured.optLong("sequence", current.sequence),
                    configured = true,
                    sourceUrl = if (configured.optString("source_kind").equals("URL", true)) source else "",
                    state = "CONFIGURED",
                )
                latest = current
                main.post {
                    state = state.copy(
                        source = if (current.sourceUrl.isNotEmpty()) current.sourceUrl else source,
                        sourceDirty = false,
                        session = current,
                    )
                }
            }

            when (NativeVpnBridge.prepare(activity)) {
                1 -> startCurrent(current)
                0 -> {
                    pendingPermission = true
                    main.post { state = state.copy(busy = false, error = "Approve the Android VPN permission to connect") }
                }
                else -> report("Android VPN service could not be prepared")
            }
        } catch (failure: Exception) {
            report(commandError(failure), failure)
        }
    }

    private fun startCurrent(current: SessionData) {
        val response = JSONObject(
            NativeGoSession.start(
                current.sessionId,
                current.sequence,
                "AUTO_SELECT",
                0,
                null,
            ),
        )
        requireOK(response)
        val result = response.getJSONObject("result")
        latest = current.copy(
            sequence = result.optLong("sequence", current.sequence),
            generation = result.optLong("generation", current.generation),
        )
        refreshSnapshot(clearBusy = true)
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
    var menu by remember { mutableStateOf(false) }
    Scaffold(
        topBar = {
            TopAppBar(title = { Text("DobbyVPN") }, actions = {
                TextButton(onClick = { menu = true }, modifier = Modifier.semantics { contentDescription = "More options" }) {
                    Text("More")
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text("About") }, onClick = { menu = false; controller.show("about") })
                }
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
    var configurationText by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(state.source) {
        if (state.source.contains('\n') || state.source.trimStart().startsWith("[")) configurationText = true
    }
    val status = when {
        session.recovering -> "Reconnecting"
        state.error.isNotEmpty() -> "Error"
        session.state == "CONNECTED" -> "Connected"
        session.state == "PROBING" || session.state == "PREPARING" -> "Connecting"
        session.state == "STOPPING" -> "Stopping"
        session.failure.isNotEmpty() -> "Failed"
        else -> "Disconnected"
    }
    val action = when (session.primaryAction) {
        "START" -> "Connect"
        "STOP" -> if (session.state == "CONNECTED") "Disconnect" else "Cancel"
        else -> if (session.state == "STOPPING") "Stopping…" else "Waiting…"
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
                    onValueChange = controller::sourceChanged,
                    modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Connection configuration" },
                    label = { Text(if (configurationText) "Configuration text" else "Subscription URL") },
                    placeholder = { Text(if (configurationText) "Paste your configuration" else "https://…") },
                    singleLine = !configurationText,
                    minLines = if (configurationText) 3 else 1,
                    maxLines = if (configurationText) 5 else 1,
                    enabled = !state.busy,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = if (configurationText) KeyboardType.Text else KeyboardType.Uri,
                        imeAction = if (configurationText) ImeAction.Default else ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(onDone = { focus.clearFocus() }),
                )
                TextButton(onClick = { configurationText = !configurationText }) {
                    Text(if (configurationText) "Use subscription URL" else "Use configuration text…")
                }
                Text(status, modifier = Modifier.semantics { contentDescription = status; liveRegion = LiveRegionMode.Polite }, style = MaterialTheme.typography.titleMedium)
                if (session.activeProfile.isNotEmpty()) {
                    Text(session.activeProfile, modifier = Modifier.semantics { contentDescription = "Active profile" })
                }
                if (state.error.isNotEmpty() || session.failure.isNotEmpty()) {
                    Text(
                        when {
                            state.error.contains("permission", ignoreCase = true) -> state.error
                            session.sessionId.isEmpty() -> "VPN service is unavailable. See logs for details."
                            else -> "Check your subscription URL or configuration. See logs for details."
                        },
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                Button(
                    onClick = { focus.clearFocus(); controller.connectOrDisconnect() },
                    enabled = !state.busy && session.primaryAction in setOf("START", "STOP"),
                    modifier = Modifier.fillMaxWidth().semantics { contentDescription = "VPN connection action" },
                ) {
                    if (state.busy || session.state in setOf("PROBING", "PREPARING", "STOPPING")) {
                        CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(8.dp))
                    }
                    Text(action)
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
            TextButton(onClick = controller::exportLogs) { Text("Share logs") }
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
