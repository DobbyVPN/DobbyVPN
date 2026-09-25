package com.dobby.ui

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.dobby.nativebridge.NativeGoSession
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.vpn.BuildConfig
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

private const val VPN_PERMISSION_REQUEST = 4201

class MainActivity : ComponentActivity() {
    companion object {
        @Volatile
        @JvmField
        var current: MainActivity? = null
    }

    private lateinit var controller: SessionController

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        current = this
        NativeGoSession.attach(this)
        controller = SessionController(this)
        setContent {
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    DobbyApp(controller)
                }
            }
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == NativeVpnBridge.VPN_PERMISSION_REQUEST) {
            controller.permissionResult(resultCode == android.app.Activity.RESULT_OK)
        }
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
    val configured: Boolean = false,
    val sourceUrl: String = "",
    val activeProfile: String = "",
    val warnings: String = "",
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
    @Volatile private var latest = SessionData()
    @Volatile private var pendingPermission = false

    init {
        worker.scheduleWithFixedDelay({ refreshSnapshot() }, 0, 500, TimeUnit.MILLISECONDS)
    }

    fun sourceChanged(value: String) {
        state = state.copy(source = value, sourceDirty = true, error = "")
    }

    fun show(screen: String) {
        state = state.copy(screen = screen)
        if (screen == "logs") refreshLogs()
    }

    fun connectOrDisconnect() {
        if (state.busy) return
        if (state.session.state == "CONNECTED") {
            val current = latest
            if (current.generation <= 0) return
            state = state.copy(busy = true, error = "")
            worker.execute {
                runCatching { NativeGoSession.stop(current.sessionId, current.generation) }
                    .onSuccess { response -> consumeSnapshotCommand(response, "Disconnected") }
                    .onFailure { failure -> report(failure.message ?: "Stop failed") }
            }
        } else {
            state = state.copy(busy = true, error = "")
            val currentUI = state
            worker.execute { prepareAndStart(currentUI) }
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

    fun refreshLogs() {
        worker.execute {
            try {
                val paths = NativeVpnBridge.diagnosticPaths(activity)
                    .lineSequence()
                    .map(String::trim)
                    .filter(String::isNotEmpty)
                    .toList()
                val contents = paths.map { path ->
                    val file = File(path)
                    if (!file.exists()) "" else file.readText(Charsets.UTF_8)
                }.filter(String::isNotEmpty)
                val text = contents.joinToString("\n")
                main.post { state = state.copy(logs = text, logsError = "") }
            } catch (failure: Exception) {
                main.post { state = state.copy(logsError = failure.message ?: "Diagnostic files could not be read") }
            }
        }
    }

    fun exportLogs() {
        val content = state.logs
        worker.execute {
            if (content.isEmpty() || !NativeVpnBridge.exportLogs(activity, content.toByteArray(Charsets.UTF_8))) {
                main.post { state = state.copy(logsError = "Android could not open the log share sheet") }
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
        worker.shutdownNow()
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
            report(commandError(failure))
        }
    }

    private fun startCurrent(current: SessionData) {
        val response = JSONObject(
            NativeGoSession.start(current.sessionId, current.sequence, "AUTO_SELECT", 0),
        )
        requireOK(response)
        val result = response.getJSONObject("result")
        latest = current.copy(
            sequence = result.optLong("sequence", current.sequence),
            generation = result.optLong("generation", current.generation),
            state = "PROBING",
        )
        main.post { state = state.copy(session = latest, busy = false, error = "") }
    }

    private fun refreshSnapshot() {
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
                configured = snapshot.optBoolean("configured"),
                sourceUrl = snapshot.optString("source_url"),
                activeProfile = active?.let {
                    listOf(it.optString("protocol"), it.optString("description"))
                        .filter(String::isNotBlank)
                        .joinToString(" · ")
                }.orEmpty(),
                warnings = snapshot.optJSONArray("warnings")?.let { warnings ->
                    (0 until warnings.length()).mapNotNull { index ->
                        warnings.optJSONObject(index)?.optString("message")?.takeIf(String::isNotBlank)
                    }.joinToString("\n")
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
                        reattached -> ""
                        else -> state.error
                    },
                )
            }
        } catch (failure: Exception) {
            latest = SessionData()
            val message = commandError(failure)
            main.post { state = state.copy(session = SessionData(), busy = false, error = message) }
        }
    }

    private fun consumeSnapshotCommand(encoded: String, fallback: String) {
        try {
            val response = JSONObject(encoded)
            requireOK(response)
            val result = response.getJSONObject("result")
            latest = latest.copy(
                sequence = result.optLong("sequence", latest.sequence),
                state = result.optString("state", "IDLE"),
            )
            main.post { state = state.copy(session = latest, busy = false, error = "") }
        } catch (failure: Exception) {
            report(commandError(failure, fallback))
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

    private fun report(message: String) {
        main.post { state = state.copy(busy = false, error = message) }
    }
}

@Composable
private fun DobbyApp(controller: SessionController) {
    val state = controller.state
    when (state.screen) {
        "settings" -> SettingsScreen(controller)
        "logs" -> LogsScreen(controller)
        else -> ConnectionScreen(controller)
    }
}

@Composable
private fun ConnectionScreen(controller: SessionController) {
    val state = controller.state
    val session = state.session
    val status = when {
        session.recovering -> "Reconnecting"
        state.error.isNotEmpty() -> "Error"
        session.state == "CONNECTED" -> "Connected"
        session.state == "PROBING" || session.state == "PREPARING" || session.state == "STOPPING" -> "Connecting"
        session.failure.isNotEmpty() -> "Failed"
        else -> "Disconnected"
    }
    val action = if (session.state == "CONNECTED") "Disconnect" else "Connect"
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Dobby VPN", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
        Text(status, modifier = Modifier.semantics { contentDescription = status }, style = MaterialTheme.typography.titleLarge)
        if (session.activeProfile.isNotEmpty()) {
            Text(
                session.activeProfile,
                modifier = Modifier.semantics { contentDescription = "Active profile" },
            )
        }
        if (session.warnings.isNotEmpty()) Text(session.warnings)
        if (session.failure.isNotEmpty()) Text(session.failure)
        if (state.error.isNotEmpty()) Text(state.error)
        OutlinedTextField(
            value = state.source,
            onValueChange = controller::sourceChanged,
            modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Connection configuration" },
            label = { Text("Connection configuration") },
            placeholder = { Text("HTTPS connection URL or inline configuration") },
            minLines = 3,
            maxLines = 8,
        )
        Button(
            onClick = controller::connectOrDisconnect,
            enabled = !state.busy,
            modifier = Modifier.fillMaxWidth().semantics { contentDescription = "VPN connection action" },
        ) {
            Text(if (state.busy) "Working…" else action)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            OutlinedButton(onClick = { controller.show("logs") }) { Text("Logs") }
            OutlinedButton(
                onClick = { controller.show("settings") },
                modifier = Modifier.semantics { contentDescription = "Settings" },
            ) { Text("Settings") }
        }
    }
}

@Composable
private fun SettingsScreen(controller: SessionController) {
    Column(
        modifier = Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Settings", style = MaterialTheme.typography.headlineMedium)
        Text("Version: ${BuildConfig.VERSION_NAME}")
        Text(
            "Source commit: ${BuildConfig.PROJECT_REPOSITORY_COMMIT}",
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.semantics { contentDescription = "Source commit" },
        )
        OutlinedButton(onClick = controller::openSourceCommit) { Text("Open source") }
        Spacer(Modifier.height(8.dp))
        Button(onClick = { controller.show("connection") }) { Text("Back") }
    }
}

@Composable
private fun LogsScreen(controller: SessionController) {
    val state = controller.state
    Column(
        modifier = Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Logs", style = MaterialTheme.typography.headlineMedium)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            OutlinedButton(onClick = controller::refreshLogs) { Text("Refresh") }
            Button(onClick = controller::exportLogs, enabled = state.logs.isNotEmpty()) { Text("Export logs") }
            OutlinedButton(onClick = { controller.show("connection") }) { Text("Back") }
        }
        if (state.logsError.isNotEmpty()) Text(state.logsError)
        Text(
            state.logs.ifEmpty { "No logs are available yet" },
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .semantics { contentDescription = "Connection logs" },
            style = MaterialTheme.typography.bodySmall,
        )
    }
}
