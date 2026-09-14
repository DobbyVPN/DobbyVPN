package com.dobby.feature.vpn_service

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.AppDependenciesProvider
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionProtocol
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.firstOrNull
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.Inet4Address
import java.net.InetAddress
import java.net.URI
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import okhttp3.Dns
import okhttp3.OkHttpClient
import okhttp3.Request

/** One semantic operation supplied by an external canonical runner; this app owns no scenarios. */
internal data class AndroidHostedOperation(
    val id: String,
    val operation: String,
    val timeoutSeconds: Int,
    val controlFile: String? = null,
)

/** Owner-injected network settings used by the requested observations. */
internal data class AndroidHostedEndpoints(
    val identityUrl: String,
    val latencyUrl: String,
    val downloadUrl: String,
    val uploadUrl: String,
)

/** Owner-injected command envelope. Profile bytes are read separately. */
internal data class AndroidHostedCommand(
    val sourceSha: String?,
    val profileFile: String,
    val outputFile: String,
    val endpoints: AndroidHostedEndpoints,
    val operations: List<AndroidHostedOperation>,
    val preserveActive: Boolean,
    val profileIndex: Int?,
)

internal class AndroidHostedInputException(cause: Throwable? = null) :
    IllegalArgumentException("INPUT_INVALID", cause)

/**
 * The product seam is deliberately a small data contract. An external runner supplies the ordered
 * operations and owns scenario meaning/assertions; Dobby only executes observations.
 */
internal object AndroidHostedCommandContract {
    const val COMMAND_ARGUMENT = "dobby.hosted_command_file"
    const val REAL_PROFILE_ARGUMENT = "dobby.real_profile"

    private val SHA = Regex("[0-9a-f]{40}")
    internal val EXTERNAL_CONTROL_OPERATIONS = setOf("network_transition", "observe_routing_identity")
    private val OPERATIONS = setOf(
        "configure",
        "connect",
        "observe_tunnel",
        "observe_routing_identity",
        "measure_stability",
        "measure_throughput",
        "disconnect",
        "reconnect",
        "inspect_cleanup",
        *EXTERNAL_CONTROL_OPERATIONS.toTypedArray(),
    )

    fun parse(jsonText: String): AndroidHostedCommand {
        val json = try {
            JSONObject(jsonText)
        } catch (failure: Exception) {
            invalid(failure)
        }
        val requiredKeys = setOf("profile_file", "output_file", "endpoints", "operations")
        val commandKeys = json.keys().asSequence().toSet()
        if (!requiredKeys.all(commandKeys::contains)) invalid()

        val sourceSha = if (json.has("source_sha")) {
            requiredString(json, "source_sha").also { value ->
                if (!SHA.matches(value)) invalid()
            }
        } else {
            null
        }
        val preserveActive = if (json.has("preserve_active")) {
            json.opt("preserve_active") as? Boolean ?: invalid()
        } else {
            false
        }
        val profileIndex = if (json.has("profile_index")) {
            val value = exactInt(json.opt("profile_index"))
            if (value == null || value < 0) invalid()
            value
        } else {
            null
        }
        val profileFile = requiredString(json, "profile_file")
        val outputFile = requiredString(json, "output_file")

        val endpoints = parseEndpoints(json.optJSONObject("endpoints") ?: invalid())
        val rawOperations = try {
            json.getJSONArray("operations")
        } catch (failure: Exception) {
            invalid(failure)
        }
        if (rawOperations.length() < 1) invalid()
        val operations = buildList(rawOperations.length()) {
            for (index in 0 until rawOperations.length()) {
                val item = try {
                    rawOperations.getJSONObject(index)
                } catch (failure: Exception) {
                    invalid(failure)
                }
                val id = requiredString(item, "id")
                val operation = requiredString(item, "operation")
                if (operation !in OPERATIONS) invalid()
                val external = operation in EXTERNAL_CONTROL_OPERATIONS
                val timeoutSeconds = positiveInt(item, "timeout_seconds")
                val controlFile = if (external) {
                    requiredString(item, "control_file")
                } else {
                    null
                }
                add(AndroidHostedOperation(id, operation, timeoutSeconds, controlFile))
            }
        }
        return AndroidHostedCommand(
            sourceSha, profileFile, outputFile, endpoints, operations, preserveActive, profileIndex,
        )
    }

    fun privateFile(filesDir: File, fileName: String): File = filesDir.resolve(fileName)

    fun readCommand(file: File): String = readFile(file).toString(Charsets.UTF_8)

    fun readProfile(file: File): ByteArray = readFile(file)

    internal fun readControl(file: File): ByteArray = readFile(file)

    private fun parseEndpoints(json: JSONObject): AndroidHostedEndpoints {
        val values = listOf(
            requiredString(json, "identity_url"),
            requiredString(json, "latency_url"),
            requiredString(json, "download_url"),
            requiredString(json, "upload_url"),
        )
        values.forEach { value ->
            if (!value.startsWith("https://") || value.any(Char::isWhitespace)) invalid()
            val uri = try {
                URI(value)
            } catch (failure: Exception) {
                invalid(failure)
            }
            if (uri.scheme != "https" || uri.host.isNullOrBlank() || uri.userInfo != null) invalid()
        }
        return AndroidHostedEndpoints(values[0], values[1], values[2], values[3])
    }

    private fun readFile(file: File): ByteArray = try {
            file.readBytes()
        } catch (failure: Exception) {
            invalid(failure)
        }

    private fun requiredString(json: JSONObject, key: String): String {
        val value = json.opt(key)
        if (value !is String) invalid()
        return value
    }

    private fun exactInt(value: Any?): Int? = when (value) {
        is Int -> value
        is Long -> value.takeIf { it in Int.MIN_VALUE..Int.MAX_VALUE }?.toInt()
        else -> null
    }

    private fun positiveInt(json: JSONObject, key: String): Int {
        val value = exactInt(json.opt(key))
        if (value == null || value <= 0) invalid()
        return value
    }

    private fun invalid(cause: Throwable? = null): Nothing =
        throw AndroidHostedInputException(cause)
}

internal data class AndroidHostedMetrics(
    val latencyMs: Double,
    val downloadMbps: Double,
    val uploadMbps: Double,
)

/** Platform observations are facts only; no canonical assertion is evaluated here. */
internal interface AndroidHostedPlatform {
    val stabilitySampleCount: Int
        get() = 5
    val stabilitySampleIntervalSeconds: Double
        get() = 1.0
    suspend fun requestConsent()
    suspend fun observeTunnel(): Boolean
    suspend fun observeRoutingProof(
        controlFile: String,
        retryVpnProbeAfterTransition: Boolean = false,
    ): Boolean
    suspend fun measureStability(): Boolean
    suspend fun measureThroughput(): AndroidHostedMetrics
    suspend fun awaitDisconnected(): Boolean
}

/** Exact JSON shape consumed by the canonical Android profile-observation contract. */
internal data class AndroidHostedObservation(
    val sourceSha: String?,
    var configured: Boolean = false,
    var connected: Boolean = false,
    var tunnelInterface: Boolean = false,
    var routingVerified: Boolean = false,
    var disconnectClean: Boolean = false,
    var restartVerified: Boolean = false,
    var reconnectCompleted: Boolean = false,
    var secondTunnelInterface: Boolean = false,
    var secondRoutingVerified: Boolean = false,
    var stabilityVerified: Boolean = false,
    var stabilitySampleCount: Int = 5,
    var stabilitySampleIntervalSeconds: Double = 1.0,
    var networkTransitionVerified: Boolean = false,
    var processLossVerified: Boolean = false,
    var latencyMs: Double = 0.0,
    var downloadMbps: Double = 0.0,
    var uploadMbps: Double = 0.0,
    var finalDisconnectClean: Boolean = false,
    var cleanupVerified: Boolean = false,
    var errorCode: String? = null,
) {
    var connections: List<SessionProfile> = emptyList()
    var selectedConnection: SessionProfile? = null

    fun toJson(): JSONObject = JSONObject()
        .put("connections", JSONArray().also { array ->
            connections.forEach { profile ->
                array.put(JSONObject().put("index", profile.index).put("protocol", profile.protocol.name))
            }
        })
        .also { output -> selectedConnection?.let { profile ->
            output.put("connection", JSONObject().put("index", profile.index).put("protocol", profile.protocol.name))
        } }
        .also { output -> sourceSha?.let { output.put("source_sha", it) } }
        .put("configured", configured)
        .put("connected", connected)
        .put("tunnel_interface", tunnelInterface)
        .put("routing_verified", routingVerified)
        .put("disconnect_clean", disconnectClean)
        .put("restart_verified", restartVerified)
        .put("reconnect_completed", reconnectCompleted)
        .put("second_tunnel_interface", secondTunnelInterface)
        .put("second_routing_verified", secondRoutingVerified)
        .put("stability_verified", stabilityVerified)
        .put("stability_sample_count", stabilitySampleCount)
        .put("stability_sample_interval_seconds", stabilitySampleIntervalSeconds)
        .put("network_transition_verified", networkTransitionVerified)
        .put("process_loss_verified", processLossVerified)
        .put("latency_ms", latencyMs)
        .put("download_mbps", downloadMbps)
        .put("upload_mbps", uploadMbps)
        .put("final_disconnect_clean", finalDisconnectClean)
        .put("cleanup_verified", cleanupVerified)
        .also { output -> errorCode?.let { output.put("error_code", it) } }

}

private class AndroidHostedOperationFailure(val code: String, cause: Throwable? = null) :
    Exception(code, cause)

internal fun measurementServiceFailureCode(responseCode: Int): String? =
    if (responseCode !in HttpURLConnection.HTTP_OK..299) {
        "MEASUREMENT_SERVICE_UNAVAILABLE"
    } else {
        null
    }

/** Candidate-owned Android profile driver, compiled only into the instrumentation APK. */
internal class AndroidHostedProfileTestDriver(
    private val context: Context,
    private val controllerFactory: () -> SessionController = {
        (context.applicationContext as AppDependenciesProvider).appDependencies.sessionController
    },
    private val platformFactory: (AndroidHostedEndpoints) -> AndroidHostedPlatform = { endpoints ->
        RealAndroidHostedPlatform(context, endpoints)
    },
    private val diagnosticLog: (String) -> Unit = {},
) {
    suspend fun run(commandFileName: String): AndroidHostedObservation {
        val commandFile = AndroidHostedCommandContract.privateFile(context.filesDir, commandFileName)
        val command = try {
            AndroidHostedCommandContract.parse(AndroidHostedCommandContract.readCommand(commandFile))
        } catch (failure: Throwable) {
            try {
                deleteFile(commandFile, "delete command input")
            } catch (cleanupFailure: Throwable) {
                failure.addSuppressed(cleanupFailure)
            }
            throw failure
        }.also {
            deleteFile(commandFile, "delete command input")
        }
        val profileFile = AndroidHostedCommandContract.privateFile(context.filesDir, command.profileFile)
        val outputFile = AndroidHostedCommandContract.privateFile(context.filesDir, command.outputFile)
        val observation = AndroidHostedObservation(command.sourceSha)
        val controller = controllerFactory()
        val platform = platformFactory(command.endpoints)
        var generation: ULong? = null
        var operationsSucceeded = false
        var activeOperation: AndroidHostedOperation? = null
        var primaryFailure: Throwable? = null

        fun recordFailure(failure: Throwable, code: String, stage: String) {
            val primary = primaryFailure
            if (primary == null) {
                primaryFailure = failure
            } else if (primary !== failure) {
                primary.addSuppressed(failure)
            }
            try {
                diagnosticLog(
                    "[ERROR] Android hosted failure failureCode=$code stage=$stage " +
                        "failureTypes=${failureTypes(failure)}",
                )
            } catch (loggingFailure: Throwable) {
                requireNotNull(primaryFailure).addSuppressed(loggingFailure)
            }
        }

        suspend fun <T> cleanupAttempt(
            stage: String,
            fallback: T,
            action: suspend () -> T,
        ): T = try {
            action()
        } catch (failure: Throwable) {
            recordFailure(failure, "CLEANUP_FAILED", stage)
            fallback
        }

        suspend fun deleteInput(file: File, stage: String): Boolean = cleanupAttempt(
            stage,
            false,
        ) {
            deleteFile(file, stage)
            true
        }

        diagnosticLog(
            "[DEBUG] Android hosted command accepted " +
                "operationCount=${command.operations.size} profileIndex=${command.profileIndex ?: -1}",
        )
        try {
            for (operation in command.operations) {
                activeOperation = operation
                diagnosticLog(
                    "[DEBUG] Android hosted operation started " +
                        "operation=${operation.operation} operationId=${operation.id}",
                )
                try {
                    withTimeout(TimeUnit.SECONDS.toMillis(operation.timeoutSeconds.toLong())) {
                        execute(
                            operation, profileFile, controller, platform, observation,
                            command.profileIndex,
                            setGeneration = { generation = it },
                            getGeneration = { generation },
                        )
                    }
                } catch (failure: TimeoutCancellationException) {
                    throw AndroidHostedOperationFailure("OPERATION_TIMEOUT", failure)
                }
                diagnosticLog(
                    "Android hosted operation completed " +
                        "operation=${operation.operation} operationId=${operation.id}",
                )
            }
            operationsSucceeded = true
        } catch (failure: AndroidHostedOperationFailure) {
            observation.errorCode = failure.code
            recordFailure(
                failure,
                failure.code,
                activeOperation?.operation ?: "none",
            )
        } catch (failure: CancellationException) {
            recordFailure(
                failure,
                "OPERATION_CANCELLED",
                activeOperation?.operation ?: "none",
            )
        } catch (failure: Exception) {
            observation.errorCode = "DRIVER_ERROR"
            recordFailure(
                failure,
                "DRIVER_ERROR",
                activeOperation?.operation ?: "none",
            )
        } finally {
            val preserveActive = command.preserveActive && operationsSucceeded && generation != null
            if (preserveActive) {
                cleanupAttempt("log_preserved_session", Unit) {
                    diagnosticLog(
                        "Android hosted active session preserved profileIndex=${command.profileIndex ?: -1}",
                    )
                }
                cleanupAttempt("write_preserved_observation", Unit) {
                    writeJson(outputFile, observation.toJson())
                }
                deleteInput(profileFile, "delete_preserved_profile")
            } else {
                withContext(NonCancellable) {
                    cleanupAttempt("log_cleanup_start", Unit) {
                        diagnosticLog(
                            "[DEBUG] Android hosted cleanup started activeGeneration=${generation != null}",
                        )
                    }
                    // A configure-only command has no generation to stop.  The Go
                    // session is therefore legitimately left in CONFIGURED while its
                    // profile is still clean; once a generation has started, cleanup
                    // must return to IDLE so the tunnel lifecycle is proven closed.
                    val stoppedGeneration = generation
                    var cleanupSucceeded = true
                    generation?.let { active ->
                        cleanupSucceeded = cleanupAttempt("stop_session", false) {
                            stopSession(controller, active)
                        } && cleanupSucceeded
                        generation = null
                    }
                    val cleanupSnapshot = cleanupAttempt("await_clean_snapshot", false) {
                        awaitCleanSnapshot(controller, stoppedGeneration)
                    }
                    if (cleanupSnapshot) {
                        cleanupSucceeded = cleanupAttempt("reset_session", false) {
                            requireControllerSuccess("CLEANUP_FAILED", "reset", controller.reset())
                            true
                        } && cleanupSucceeded
                    } else {
                        cleanupSucceeded = false
                    }
                    cleanupAttempt("stop_vpn_service", false) {
                        context.stopService(DobbyVpnService.createStopIntent(context, 0, false))
                    }
                    val disconnected = cleanupAttempt("await_disconnected", false) {
                        platform.awaitDisconnected()
                    }
                    observation.cleanupVerified = cleanupSucceeded && cleanupSnapshot && disconnected
                    if (!observation.cleanupVerified && observation.errorCode == null) {
                        observation.errorCode = "CLEANUP_FAILED"
                    }
                    cleanupAttempt("log_cleanup_finish", Unit) {
                        diagnosticLog(
                            "Android hosted cleanup completed " +
                                "cleanupVerified=${observation.cleanupVerified} " +
                                "sessionCleanup=$cleanupSucceeded snapshotClean=$cleanupSnapshot " +
                                "networkDisconnected=$disconnected",
                        )
                    }
                    cleanupAttempt("write_final_observation", Unit) {
                        writeJson(outputFile, observation.toJson())
                    }
                    deleteInput(profileFile, "delete_profile")
                }
            }
        }
        primaryFailure?.let { throw it }
        return observation
    }

    private suspend fun execute(
        operation: AndroidHostedOperation,
        profileFile: File,
        controller: SessionController,
        platform: AndroidHostedPlatform,
        observation: AndroidHostedObservation,
        profileIndex: Int?,
        setGeneration: (ULong?) -> Unit,
        getGeneration: () -> ULong?,
    ) {
        when (operation.operation) {
            "configure" -> {
                val profile = try {
                    AndroidHostedCommandContract.readProfile(profileFile)
                } catch (failure: Exception) {
                    throw AndroidHostedOperationFailure("PROFILE_UNAVAILABLE", failure)
                }
                val configured = try {
                    controller.configure(profile)
                } finally {
                    deleteFile(profileFile, "delete configured profile")
                }
                val profiles = requireControllerSuccess(
                    "CONFIGURE_REJECTED",
                    "configure",
                    configured,
                ).profiles
                if (profiles.isEmpty() || profiles.map { it.index } != profiles.indices.toList() ||
                    profiles.any { it.protocol !in setOf(SessionProtocol.OUTLINE, SessionProtocol.XRAY, SessionProtocol.TRUST_TUNNEL) }
                ) throw AndroidHostedOperationFailure("CONFIGURE_REJECTED")
                observation.connections = profiles
                observation.selectedConnection = profileIndex?.let { selected ->
                    profiles.singleOrNull { it.index == selected }
                        ?: throw AndroidHostedOperationFailure("PROFILE_UNAVAILABLE")
                }
                observation.configured = true
                diagnosticLog(
                    "Android hosted configuration accepted " +
                        "connectionCount=${profiles.size} selectedProfileIndex=${profileIndex ?: -1} " +
                        "selectedProtocol=${observation.selectedConnection?.protocol?.name ?: "NONE"}",
                )
            }
            "connect" -> connect(controller, platform, observation, profileIndex, setGeneration)
            "observe_tunnel" -> {
                val observed = platform.observeTunnel()
                if (observation.restartVerified) {
                    observation.secondTunnelInterface = observed
                } else {
                    observation.tunnelInterface = observed
                }
                if (!observed) throw AndroidHostedOperationFailure("TUNNEL_NOT_OBSERVED")
            }
            "observe_routing_identity" -> {
                val verified = platform.observeRoutingProof(requireNotNull(operation.controlFile))
                if (observation.restartVerified) {
                    observation.secondRoutingVerified = verified
                } else {
                    observation.routingVerified = verified
                }
                if (!verified) throw AndroidHostedOperationFailure("ROUTING_PROOF_FAILED")
            }
            "measure_stability" -> {
                val stable = platform.measureStability()
                observation.stabilityVerified = stable
                observation.stabilitySampleCount = platform.stabilitySampleCount
                observation.stabilitySampleIntervalSeconds = platform.stabilitySampleIntervalSeconds
                if (!stable) throw AndroidHostedOperationFailure("STABILITY_UNVERIFIED")
            }
            "measure_throughput" -> platform.measureThroughput().let { metrics ->
                observation.latencyMs = metrics.latencyMs
                observation.downloadMbps = metrics.downloadMbps
                observation.uploadMbps = metrics.uploadMbps
            }
            "disconnect" -> {
                if (!stopCurrentSession(controller, getGeneration, setGeneration)) {
                    throw AndroidHostedOperationFailure("DISCONNECT_FAILED")
                }
                val disconnected = platform.awaitDisconnected()
                if (observation.restartVerified) {
                    observation.finalDisconnectClean = disconnected
                } else {
                    observation.disconnectClean = disconnected
                }
                if (!disconnected) throw AndroidHostedOperationFailure("DISCONNECT_FAILED")
            }
            "reconnect" -> {
                if (getGeneration() != null) throw AndroidHostedOperationFailure("RECONNECT_PRECONDITION")
                connect(controller, platform, observation, profileIndex, setGeneration)
                observation.restartVerified = observation.connected
                observation.reconnectCompleted = observation.connected
            }
            "inspect_cleanup" -> {
                // Stop is acknowledged before the asynchronous platform
                // teardown necessarily publishes IDLE/cleanupComplete.  Wait for
                // the ordered IDLE event before the final snapshot verification.
                if (!awaitCleanSnapshot(controller, stoppedGeneration = null, requireIdle = true)) {
                    throw AndroidHostedOperationFailure("CLEANUP_INSPECTION_FAILED")
                }
                val disconnected = platform.awaitDisconnected()
                observation.cleanupVerified = disconnected
                if (!observation.cleanupVerified) throw AndroidHostedOperationFailure("CLEANUP_INSPECTION_FAILED")
            }
            "network_transition" -> {
                awaitExternalControl(operation)
                val tunnel = platform.observeTunnel()
                val identity = platform.observeRoutingProof(
                    "${requireNotNull(operation.controlFile)}.routing",
                    retryVpnProbeAfterTransition = true,
                )
                observation.networkTransitionVerified = tunnel && identity
                if (!observation.networkTransitionVerified) {
                    throw AndroidHostedOperationFailure("NETWORK_TRANSITION_UNVERIFIED")
                }
            }
            else -> throw AndroidHostedOperationFailure("INPUT_INVALID")
        }
    }

    /**
     * Waits for one owner-controlled external emulator action. The action itself is outside this
     * APK: the Torturer adapter performs it and signals completion through this file rendezvous.
     */
    private suspend fun awaitExternalControl(operation: AndroidHostedOperation) {
        val controlName = operation.controlFile ?: throw AndroidHostedOperationFailure("CONTROL_UNAVAILABLE")
        val control = context.filesDir.resolve(controlName)
        val ready = context.filesDir.resolve("$controlName.ready")
        withControlFiles(control, ready) {
            writeJson(ready, JSONObject().put("phase", "ready"))
            while (!control.exists()) delay(100L)
            val json = try {
                JSONObject(readControlPayload(control).toString(Charsets.UTF_8))
            } catch (failure: Exception) {
                throw AndroidHostedOperationFailure("CONTROL_INPUT_INVALID", failure)
            }
            if (json.opt("operation") != operation.operation) {
                throw AndroidHostedOperationFailure("CONTROL_INPUT_INVALID")
            }
        }
    }

    private fun readControlPayload(file: File): ByteArray {
        return try {
            AndroidHostedCommandContract.readControl(file)
        } catch (failure: AndroidHostedInputException) {
            throw AndroidHostedOperationFailure("CONTROL_INPUT_INVALID", failure)
        }
    }

    private suspend fun connect(
        controller: SessionController,
        platform: AndroidHostedPlatform,
        observation: AndroidHostedObservation,
        profileIndex: Int?,
        setGeneration: (ULong?) -> Unit,
    ) {
        val selected = profileIndex ?: throw AndroidHostedOperationFailure("PROFILE_UNAVAILABLE")
        var stage = "await_disconnected"
        try {
            // Process loss can remove the app before Android retires its VPN network.
            // Observe its absence before creating the replacement session.
            diagnosticLog(
                "[DEBUG] Android hosted connect stage started stage=$stage profileIndex=$selected",
            )
            if (!platform.awaitDisconnected()) {
                throw AndroidHostedOperationFailure("DISCONNECT_FAILED")
            }
            diagnosticLog("Android hosted connect stage completed stage=$stage profileIndex=$selected")
            stage = "request_consent"
            diagnosticLog(
                "[DEBUG] Android hosted connect stage started stage=$stage profileIndex=$selected",
            )
            platform.requestConsent()
            diagnosticLog("Android hosted connect stage completed stage=$stage profileIndex=$selected")
            stage = "start_session"
            diagnosticLog(
                "[DEBUG] Android hosted connect stage started stage=$stage profileIndex=$selected",
            )
            val start = requireControllerSuccess(
                "CONNECT_REJECTED",
                "start",
                controller.start(SessionStartTarget.ProfileIndex(selected)),
            )
            val generation = start.generation
            setGeneration(generation)
            diagnosticLog("Android hosted connect stage completed stage=$stage profileIndex=$selected")
            stage = "await_connected"
            diagnosticLog(
                "[DEBUG] Android hosted connect stage started stage=$stage profileIndex=$selected",
            )
            val snapshot = awaitState(controller, generation, SessionState.CONNECTED)
            if (snapshot?.state != SessionState.CONNECTED) {
                diagnosticLog(
                    "[ERROR] Android hosted connect stage failed " +
                        "stage=$stage profileIndex=$selected code=CONNECT_FAILED " +
                        "lastState=${snapshot?.state?.name ?: "UNAVAILABLE"} " +
                        "lastFailureCode=${snapshot?.lastFailure?.code?.name ?: "NONE"}",
                )
                throw AndroidHostedOperationFailure("CONNECT_FAILED")
            }
            observation.connected = true
            diagnosticLog("Android hosted connect stage completed stage=$stage profileIndex=$selected")
        } catch (failure: AndroidHostedOperationFailure) {
            diagnosticLog(
                "[ERROR] Android hosted connect failed " +
                    "stage=$stage profileIndex=$selected code=${failure.code}",
            )
            throw failure
        } catch (failure: CancellationException) {
            throw failure
        } catch (failure: Exception) {
            diagnosticLog(
                "[ERROR] Android hosted connect exception " +
                    "stage=$stage profileIndex=$selected " +
                    "failureTypes=${failureTypes(failure)} failureCode=CONNECT_FAILED",
            )
            throw AndroidHostedOperationFailure("CONNECT_FAILED", failure)
        }
    }

    private suspend fun awaitState(
        controller: SessionController,
        generation: ULong,
        expected: SessionState,
    ): SessionSnapshot? {
        val snapshot = withTimeoutOrNull(SESSION_STATE_TIMEOUT_MILLIS) {
            controller.watch().firstOrNull { current ->
                current.generation == generation &&
                    (current.state == expected || current.state == SessionState.FAILED)
            }
        }
        if (snapshot == null) {
            diagnosticLog(
                "[ERROR] Android hosted session state wait expired " +
                    "expected=${expected.name} generation=$generation",
            )
        }
        return snapshot
    }

    private fun failureTypes(failure: Throwable): String =
        generateSequence(failure) { current -> current.cause }
            .joinToString("->") { current -> current::class.simpleName ?: "Throwable" }

    private suspend fun stopCurrentSession(
        controller: SessionController,
        getGeneration: () -> ULong?,
        setGeneration: (ULong?) -> Unit,
    ): Boolean {
        val generation = getGeneration() ?: return true
        return stopSession(controller, generation).also { if (it) setGeneration(null) }
    }

    private suspend fun stopSession(controller: SessionController, generation: ULong): Boolean {
        requireControllerSuccess(
            "DISCONNECT_FAILED",
            "stop",
            controller.stop(generation),
        )
        return true
    }

    private fun <T> requireControllerSuccess(
        code: String,
        operation: String,
        result: SessionControllerResult<T>,
    ): T = when (result) {
        is SessionControllerResult.Success -> result.value
        is SessionControllerResult.Failure -> throw controllerFailure(code, operation, result)
    }

    private fun controllerFailure(
        code: String,
        operation: String,
        result: SessionControllerResult.Failure,
    ): AndroidHostedOperationFailure = AndroidHostedOperationFailure(
        code,
        IllegalStateException(
            "session controller $operation failed: code=${result.code.name}; message=${result.message}",
        ),
    )

    private suspend fun awaitCleanSnapshot(
        controller: SessionController,
        stoppedGeneration: ULong?,
        requireIdle: Boolean = stoppedGeneration != null,
    ): Boolean {
        val expectedGeneration = if (requireIdle && stoppedGeneration == null) {
            val result = controller.snapshot()
            if (result is SessionControllerResult.Failure) {
                throw controllerFailure("CLEANUP_FAILED", "snapshot", result)
            }
            (result as SessionControllerResult.Success).value.generation
        } else {
            stoppedGeneration
        }
        if (requireIdle && withTimeoutOrNull(SESSION_STATE_TIMEOUT_MILLIS) {
                controller.watch().firstOrNull { current ->
                    current.state == SessionState.IDLE &&
                        (expectedGeneration == null || current.generation == expectedGeneration)
                }
            } == null
        ) {
            return false
        }
        val result = controller.snapshot()
        if (result is SessionControllerResult.Failure) {
            throw controllerFailure("CLEANUP_FAILED", "snapshot", result)
        }
        val snapshot = (result as SessionControllerResult.Success).value
        return (snapshot.state == SessionState.IDLE ||
            (!requireIdle && snapshot.state == SessionState.CONFIGURED)) &&
            snapshot.cleanupComplete
    }

    private companion object {
        const val SESSION_STATE_TIMEOUT_MILLIS = 20_000L
    }
}

private fun writeJson(file: File, value: JSONObject) {
    val temporary = File(file.parentFile, "${file.name}.tmp")
    deleteFile(temporary, "delete stale JSON output")
    FileOutputStream(temporary, false).use { output ->
        output.write(value.toString().toByteArray(Charsets.UTF_8))
        output.flush()
        output.fd.sync()
    }
    if (!temporary.renameTo(file)) {
        deleteFile(temporary, "delete failed JSON output")
        error("JSON output rename failed: $file")
    }
}

private fun deleteFile(file: File, stage: String) {
    if (!file.delete() && file.exists()) error("$stage failed for ${file.name}")
}

private suspend fun <T> withControlFiles(control: File, ready: File, block: suspend () -> T): T {
    var primary: Throwable? = null
    try {
        return block()
    } catch (failure: Throwable) {
        primary = failure
        throw failure
    } finally {
        var cleanup: Throwable? = null
        for (file in listOf(control, ready)) {
            try {
                deleteFile(file, "delete control input")
            } catch (failure: Throwable) {
                if (cleanup == null) cleanup = failure else cleanup.addSuppressed(failure)
            }
        }
        cleanup?.let { failure ->
            val original = primary
            if (original == null) throw failure else original.addSuppressed(failure)
        }
    }
}

/** Real Android network APIs used by the candidate seam; Torturer evaluates routing proof. */
internal class RealAndroidHostedPlatform(
    private val context: Context,
    private val endpoints: AndroidHostedEndpoints,
) : AndroidHostedPlatform {
    private val connectivity: ConnectivityManager
        get() = requireNotNull(context.getSystemService(ConnectivityManager::class.java))
    private var lastTunnelFingerprint: ByteArray? = null

    // Instrumentation's synchronous activity launcher must not run on Android's
    // application-main thread.  The canonical driver is already a suspendable
    // instrumentation worker, so keep the UI interaction off Main while the
    // system dialog itself is still driven through the real Android APIs.
    override suspend fun requestConsent() = withContext(Dispatchers.Default) {
        try {
            VpnConsentTestHelper.grant(context, CONSENT_TIMEOUT_MILLIS, POLL_INTERVAL_MILLIS)
        } catch (failure: VpnConsentTestHelper.Failure) {
            throw AndroidHostedOperationFailure(failure.code, failure)
        }
    }

    override suspend fun observeTunnel(): Boolean = withContext(Dispatchers.IO) {
        connectivity.awaitVpnNetworkState(
            present = true,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS.toLong(),
            pollIntervalMillis = POLL_INTERVAL_MILLIS,
        )?.linkProperties?.interfaceName?.isNullOrBlank() == false
    }

    override suspend fun observeRoutingProof(
        controlFile: String,
        retryVpnProbeAfterTransition: Boolean,
    ): Boolean = withContext(Dispatchers.IO) {
        val networks = connectivity.allNetworks.toList()
        fun isVpn(network: Network): Boolean = connectivity.getNetworkCapabilities(network)
            ?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
        val physical = networks.firstOrNull { network ->
            !isVpn(network) && connectivity.getNetworkCapabilities(network)
                ?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true &&
                connectivity.getLinkProperties(network)?.routes?.any { it.isDefaultRoute } == true
        } ?: error("Routing probe has no non-VPN Internet network")
        // Routing can still be proved when the current network cannot be toggled
        // by this harness.
        val physicalTransport = connectivity.getNetworkCapabilities(physical)
            ?.let { capabilities ->
                supportedPhysicalTransport(
                    wifi = capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI),
                    ethernet = capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET),
                )
            }
        val vpn = networks.firstOrNull(::isVpn)
            ?: error("Routing probe has no VPN network")
        val endpoint = URL(endpoints.identityUrl)
        val address = physical.getAllByName(endpoint.host).filterIsInstance<Inet4Address>().firstOrNull()
            ?: error("Routing probe endpoint has no IPv4 address: ${endpoint.host}")
        val control = context.filesDir.resolve(controlFile)
        val ready = context.filesDir.resolve("$controlFile.ready")

        // The app supplies network-bound HTTP facts. Torturer owns the root
        // firewall/counter operations and decides whether those facts pass.
        withControlFiles(control, ready) {
            val readyPayload = JSONObject()
                .put("phase", "ready")
                .put("physical_interface", requireNotNull(connectivity.getLinkProperties(physical)?.interfaceName))
                .put("vpn_interface", requireNotNull(connectivity.getLinkProperties(vpn)?.interfaceName))
                .put("ipv4", address.hostAddress)
                .put("port", if (endpoint.port == -1) 443 else endpoint.port)
            physicalTransport?.let { readyPayload.put("physical_transport", it) }
            writeJson(ready, readyPayload)
            awaitRoutingCommand(control, "blocked")
            val direct = probeIdentity(physical, address)
            var throughVpn = probeIdentity(vpn, address)
            if (retryVpnProbeAfterTransition && throughVpn.has("error_type")) {
                // Android can publish the restored Wi-Fi route before VPN-bound
                // sockets have resumed. Retry one fresh request; HTTP responses
                // (including non-2xx statuses) remain subject to the routing proof.
                delay(VPN_PROBE_RETRY_DELAY_MILLIS)
                throughVpn = probeIdentity(vpn, address)
            }
            writeJson(ready, JSONObject().put("phase", "blocked")
                .put("direct", direct).put("vpn", throughVpn))
            awaitRoutingCommand(control, "unblocked")
            writeJson(ready, JSONObject().put("phase", "unblocked")
                .put("direct", probeIdentity(physical, address)))
            val completed = awaitRoutingCommand(control, "finish")
            if (completed.getBoolean("passed")) {
                lastTunnelFingerprint = MessageDigest.getInstance("SHA-256")
                    .digest(throughVpn.getString("body").toByteArray(Charsets.UTF_8))
                true
            } else {
                false
            }
        }
    }

    private suspend fun awaitRoutingCommand(control: File, expectedPhase: String): JSONObject {
        while (!control.exists()) delay(100L)
        val command = JSONObject(AndroidHostedCommandContract.readControl(control).toString(Charsets.UTF_8))
        deleteFile(control, "consume routing probe command")
        if (command.getString("phase") == "finish" && !command.getBoolean("passed")) {
            error("Torturer routing proof failed:\n${command.getString("error")}")
        }
        check(command.getString("phase") == expectedPhase) {
            "Routing probe expected phase $expectedPhase, received $command"
        }
        return command
    }

    private fun probeIdentity(network: Network, address: InetAddress): JSONObject {
        // Retain the selected ID to diagnose a stale VPN network after process loss.
        val result = JSONObject().put("network_id", network.networkHandle)
        return try {
            val client = OkHttpClient.Builder()
                .dns(object : Dns {
                    override fun lookup(hostname: String): List<InetAddress> = listOf(address)
                })
                .socketFactory(network.socketFactory)
                .callTimeout(IDENTITY_PROBE_TIMEOUT_MILLIS.toLong(), TimeUnit.MILLISECONDS)
                .followRedirects(false)
                .build()
            val request = Request.Builder()
                .url(endpoints.identityUrl)
                .header("User-Agent", "DobbyVPN-Harness/1")
                .build()
            client.newCall(request).execute().use { response ->
                result.put("status", response.code)
                    .put("message", response.message)
                    .put("body", response.body?.string().orEmpty())
            }
        } catch (failure: Exception) {
            // A physical request is deliberately rejected by the routing test.
            // Preserve the full error; only Torturer classifies it as expected.
            result.put("error_type", failure.javaClass.name)
                .put("error", failure.message).put("stack", failure.stackTraceToString())
        }
    }

    override suspend fun measureStability(): Boolean = withContext(Dispatchers.IO) {
        val first = lastTunnelFingerprint ?: fetchFingerprint()
        repeat(STABILITY_SAMPLE_COUNT - 1) {
            delay(STABILITY_INTERVAL_MILLIS)
            val current = fetchFingerprint()
            if (!MessageDigest.isEqual(first, current)) return@withContext false
            current.fill(0)
        }
        true
    }

    override suspend fun measureThroughput(): AndroidHostedMetrics = withContext(Dispatchers.IO) {
        // The canonical latency endpoint is intentionally a reusable download
        // object.  Latency only needs the time to the first response byte; it
        // must not be treated as a bounded complete-body transfer (which would
        // reject a valid 1 MiB response as NETWORK_BODY_INVALID).
        val latency = measureLatency(endpoints.latencyUrl).elapsedMs
        val download = measureTransfer(endpoints.downloadUrl, upload = false, maximumBytes = THROUGHPUT_BYTES).rateMbps
        val payload = ByteArray(THROUGHPUT_BYTES)
        val upload = measureTransfer(endpoints.uploadUrl, upload = true, maximumBytes = payload.size, payload = payload).rateMbps
        AndroidHostedMetrics(latency, download, upload)
    }

    private fun measureLatency(rawUrl: String): TransferMeasurement = withNetworkConnection(rawUrl, upload = false) { connection ->
        val started = System.nanoTime()
        requireSuccess(connection)
        connection.inputStream.use { input ->
            if (input.read() < 0) throw AndroidHostedOperationFailure("NETWORK_BODY_INVALID")
        }
        val elapsedNanos = System.nanoTime() - started
        if (elapsedNanos <= 0L || elapsedNanos > TimeUnit.SECONDS.toNanos(THROUGHPUT_TIMEOUT_SECONDS)) {
            throw AndroidHostedOperationFailure("THROUGHPUT_TIMEOUT")
        }
        TransferMeasurement(rateMbps = 0.0, elapsedMs = elapsedNanos / NANOS_PER_MILLISECOND)
    }

    override suspend fun awaitDisconnected(): Boolean = withContext(Dispatchers.IO) {
        connectivity.awaitVpnNetworkState(
            present = false,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS.toLong(),
            pollIntervalMillis = POLL_INTERVAL_MILLIS,
        ) == null
    }

    private fun fetchFingerprint(): ByteArray = withNetworkConnection(endpoints.identityUrl, upload = false) { connection ->
        requireSuccess(connection)
        val bytes = connection.inputStream.use { it.readBytes() }
        if (bytes.isEmpty()) throw AndroidHostedOperationFailure("NETWORK_BODY_INVALID")
        MessageDigest.getInstance("SHA-256").digest(bytes)
    }

    private data class TransferMeasurement(val rateMbps: Double, val elapsedMs: Double)

    private fun measureTransfer(
        rawUrl: String,
        upload: Boolean,
        maximumBytes: Int,
        payload: ByteArray? = null,
    ): TransferMeasurement = withNetworkConnection(rawUrl, upload) { connection ->
        val started = System.nanoTime()
        val transferred = if (upload) {
            connection.requestMethod = "POST"
            connection.doOutput = true
            connection.setFixedLengthStreamingMode(maximumBytes)
            connection.outputStream.use { output -> output.write(payload ?: ByteArray(0)) }
            requireSuccess(connection)
            drainResponse(connection)
            maximumBytes
        } else {
            requireSuccess(connection)
            readUpTo(connection, maximumBytes)
        }
        val elapsedNanos = System.nanoTime() - started
        if (elapsedNanos <= 0L || elapsedNanos > TimeUnit.SECONDS.toNanos(THROUGHPUT_TIMEOUT_SECONDS) || transferred <= 0) {
            throw AndroidHostedOperationFailure("THROUGHPUT_TIMEOUT")
        }
        TransferMeasurement(
            rateMbps = transferred * 8_000.0 / elapsedNanos,
            elapsedMs = elapsedNanos / NANOS_PER_MILLISECOND,
        )
    }

    private fun <T> withNetworkConnection(
        rawUrl: String,
        upload: Boolean,
        network: Network? = null,
        block: (HttpURLConnection) -> T,
    ): T {
        val original = URL(rawUrl)
        val connection = ((network?.openConnection(original) ?: original.openConnection()) as? HttpURLConnection)
            ?: throw AndroidHostedOperationFailure("NETWORK_UNAVAILABLE")
        connection.connectTimeout = NETWORK_TIMEOUT_MILLIS
        connection.readTimeout = connection.connectTimeout
        connection.instanceFollowRedirects = false
        // Use the same request identity as the private and signed-release checks.
        connection.setRequestProperty("User-Agent", "DobbyVPN-Harness/1")
        if (upload) connection.setRequestProperty("Content-Type", "application/octet-stream")
        return try {
            block(connection)
        } finally {
            connection.disconnect()
        }
    }

    private fun requireSuccess(connection: HttpURLConnection) {
        measurementServiceFailureCode(connection.responseCode)?.let { code ->
            throw AndroidHostedOperationFailure(code)
        }
    }

    private fun drainResponse(connection: HttpURLConnection) {
        connection.inputStream.use { input ->
            val buffer = ByteArray(8 * 1024)
            while (input.read(buffer) >= 0) { }
        }
    }

    private fun readUpTo(connection: HttpURLConnection, maximumBytes: Int): Int {
        connection.inputStream.use { input ->
            val result = ByteArray(maximumBytes)
            var offset = 0
            while (offset < result.size) {
                val count = input.read(result, offset, result.size - offset)
                if (count < 0) break
                offset += count
            }
            if (offset < 1) throw AndroidHostedOperationFailure("NETWORK_BODY_INVALID")
            return offset
        }
    }

    private companion object {
        const val CONSENT_TIMEOUT_MILLIS = 10_000L
        const val NETWORK_TIMEOUT_MILLIS = 20_000
        const val IDENTITY_PROBE_TIMEOUT_MILLIS = 5_000
        const val VPN_PROBE_RETRY_DELAY_MILLIS = 250L
        const val THROUGHPUT_TIMEOUT_SECONDS = 30L
        const val POLL_INTERVAL_MILLIS = 100L
        const val STABILITY_SAMPLE_COUNT = 5
        const val STABILITY_INTERVAL_MILLIS = 1_000L
        const val THROUGHPUT_BYTES = 1024 * 1024
        const val NANOS_PER_MILLISECOND = 1_000_000.0
    }
}

internal fun supportedPhysicalTransport(wifi: Boolean, ethernet: Boolean): String? {
    return when {
        wifi && !ethernet -> "wifi"
        ethernet && !wifi -> "ethernet"
        else -> null
    }
}

/** Instrumentation entrypoint used by the canonical external runner. */
@org.junit.runner.RunWith(androidx.test.ext.junit.runners.AndroidJUnit4::class)
class AndroidHostedProfileInstrumentationTest {
    @org.junit.Test
    fun run_owner_supplied_ordered_profile_command() = kotlinx.coroutines.runBlocking {
        val commandFile = InstrumentationRegistry.getArguments()
            .getString(AndroidHostedCommandContract.COMMAND_ARGUMENT)
        org.junit.Assume.assumeTrue(commandFile != null)
        requireNotNull(commandFile)
        val logger = (InstrumentationRegistry.getInstrumentation().targetContext.applicationContext
            as AppDependenciesProvider).appDependencies.logger
        logger.log("Android hosted instrumentation started")
        val observation = try {
            AndroidHostedProfileTestDriver(
                InstrumentationRegistry.getInstrumentation().targetContext,
                diagnosticLog = logger::log,
            ).run(requireNotNull(commandFile))
        } catch (failure: Throwable) {
            try {
                logger.log(
                    "[ERROR] Android hosted instrumentation failed\n" +
                        failure.stackTraceToString(),
                )
            } catch (loggingFailure: Throwable) {
                failure.addSuppressed(loggingFailure)
            }
            failure.printStackTrace()
            throw failure
        }
        logger.log(
            "Android hosted instrumentation completed errorCode=${observation.errorCode ?: "NONE"}",
        )
        org.junit.Assert.assertNull("hosted Android driver reported an error", observation.errorCode)
    }
}
