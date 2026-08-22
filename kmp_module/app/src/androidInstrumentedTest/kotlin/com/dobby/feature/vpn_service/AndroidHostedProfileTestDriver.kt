package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.VpnService
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import com.dobby.feature.main.domain.AndroidSessionController
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.json.JSONObject
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.security.MessageDigest
import java.util.Arrays
import java.util.concurrent.TimeUnit
import java.util.regex.Pattern

/** One semantic operation supplied by Torturer; this app does not own scenarios. */
internal data class AndroidHostedOperation(
    val id: String,
    val operation: String,
    val timeoutSeconds: Int,
)

/** Owner-injected network settings. They are input-only and never appear in observations. */
internal data class AndroidHostedEndpoints(
    val identityUrl: String,
    val latencyUrl: String,
    val downloadUrl: String,
    val uploadUrl: String,
)

/** Validated owner-injected command envelope. It contains names, never profile bytes. */
internal data class AndroidHostedCommand(
    val sourceSha: String,
    val profileFile: String,
    val outputFile: String,
    val endpoints: AndroidHostedEndpoints,
    val operations: List<AndroidHostedOperation>,
)

internal class AndroidHostedInputException : IllegalArgumentException("INPUT_INVALID")

/**
 * The public product seam is deliberately a small data contract. Torturer supplies the ordered
 * operations and owns scenario meaning/assertions; Dobby only executes observations.
 */
internal object AndroidHostedCommandContract {
    const val SCHEMA = 1
    const val COMMAND_KIND = "dobbyvpn.android.profile-command"
    const val OBSERVATION_KIND = "dobbyvpn.android.profile-observation"
    const val PLATFORM = "android"
    const val COMMAND_ARGUMENT = "dobby.hosted_command_file"
    const val REAL_PROFILE_ARGUMENT = "dobby.real_profile"

    private const val MAX_COMMAND_BYTES = 256 * 1024
    private const val MAX_PROFILE_BYTES = 8 * 1024 * 1024
    private const val MAX_OPERATIONS = 64
    private const val MAX_OPERATION_TIMEOUT_SECONDS = 1_800
    private val SHA = Regex("[0-9a-f]{40}")
    private val FILE_NAME = Regex("[A-Za-z0-9][A-Za-z0-9._-]{0,79}")
    private val OPERATION_ID = Regex("[a-z][a-z0-9._-]{2,95}")
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
    )

    fun parse(commandFileName: String, jsonText: String): AndroidHostedCommand {
        requireFileName(commandFileName)
        val json = try {
            JSONObject(jsonText)
        } catch (_: Exception) {
            invalid()
        }
        requireKeys(
            json,
            setOf(
                "schema", "kind", "platform", "source_sha", "profile_file", "output_file",
                "endpoints", "operations",
            ),
        )
        if (exactInt(json.opt("schema")) != SCHEMA ||
            requiredString(json, "kind") != COMMAND_KIND ||
            requiredString(json, "platform") != PLATFORM
        ) invalid()

        val sourceSha = requiredString(json, "source_sha")
        if (!SHA.matches(sourceSha) || sourceSha.all { it == '0' }) invalid()
        val profileFile = requiredString(json, "profile_file")
        val outputFile = requiredString(json, "output_file")
        requireFileName(profileFile)
        requireFileName(outputFile)
        if (profileFile == outputFile || profileFile == commandFileName || outputFile == commandFileName) invalid()

        val endpoints = parseEndpoints(json.optJSONObject("endpoints") ?: invalid())
        val rawOperations = try {
            json.getJSONArray("operations")
        } catch (_: Exception) {
            invalid()
        }
        if (rawOperations.length() !in 1..MAX_OPERATIONS) invalid()
        val seenIds = HashSet<String>()
        val operations = buildList(rawOperations.length()) {
            for (index in 0 until rawOperations.length()) {
                val item = try {
                    rawOperations.getJSONObject(index)
                } catch (_: Exception) {
                    invalid()
                }
                requireKeys(item, setOf("id", "operation", "timeout_seconds"))
                val id = requiredString(item, "id")
                val operation = requiredString(item, "operation")
                if (!OPERATION_ID.matches(id) || !seenIds.add(id) || operation !in OPERATIONS) invalid()
                val timeoutSeconds = positiveInt(item, "timeout_seconds")
                if (timeoutSeconds > MAX_OPERATION_TIMEOUT_SECONDS) invalid()
                add(AndroidHostedOperation(id, operation, timeoutSeconds))
            }
        }
        return AndroidHostedCommand(sourceSha, profileFile, outputFile, endpoints, operations)
    }

    fun privateFile(filesDir: File, fileName: String): File {
        requireFileName(fileName)
        val root = filesDir.canonicalFile
        val candidate = File(root, fileName)
        if (candidate.absoluteFile.parentFile?.canonicalFile != root ||
            candidate.canonicalFile.parentFile?.canonicalFile != root ||
            java.nio.file.Files.isSymbolicLink(candidate.toPath())
        ) invalid()
        return candidate
    }

    fun readCommand(file: File): String {
        val bytes = readBounded(file, MAX_COMMAND_BYTES)
        return try {
            bytes.toString(Charsets.UTF_8)
        } finally {
            Arrays.fill(bytes, 0)
        }
    }

    fun readProfile(file: File): ByteArray = readBounded(file, MAX_PROFILE_BYTES)

    private fun parseEndpoints(json: JSONObject): AndroidHostedEndpoints {
        requireKeys(json, setOf("identity_url", "latency_url", "download_url", "upload_url"))
        val values = listOf(
            requiredString(json, "identity_url"),
            requiredString(json, "latency_url"),
            requiredString(json, "download_url"),
            requiredString(json, "upload_url"),
        )
        values.forEach { value ->
            if (value.length !in 12..512 || !value.startsWith("https://") || value.any(Char::isWhitespace)) invalid()
            val uri = try {
                URI(value)
            } catch (_: Exception) {
                invalid()
            }
            if (uri.scheme != "https" || uri.host.isNullOrBlank() || uri.userInfo != null) invalid()
        }
        return AndroidHostedEndpoints(values[0], values[1], values[2], values[3])
    }

    private fun readBounded(file: File, maximumBytes: Int): ByteArray {
        if (!file.isFile || java.nio.file.Files.isSymbolicLink(file.toPath())) invalid()
        FileInputStream(file).use { input ->
            val result = ByteArray(maximumBytes + 1)
            var offset = 0
            while (offset < result.size) {
                val count = input.read(result, offset, result.size - offset)
                if (count < 0) break
                offset += count
            }
            if (offset > maximumBytes) {
                Arrays.fill(result, 0)
                invalid()
            }
            return result.copyOf(offset).also { Arrays.fill(result, 0) }
        }
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

    private fun requireFileName(fileName: String) {
        if (!FILE_NAME.matches(fileName)) invalid()
    }

    private fun requireKeys(json: JSONObject, expected: Set<String>) {
        if (json.keys().asSequence().toSet() != expected) invalid()
    }

    private fun invalid(): Nothing = throw AndroidHostedInputException()
}

internal data class AndroidHostedMetrics(
    val latencyMs: Double,
    val downloadMbps: Double,
    val uploadMbps: Double,
)

/** Platform observations are facts only; no canonical assertion is evaluated here. */
internal interface AndroidHostedPlatform {
    suspend fun requestConsent()
    suspend fun captureBaseline()
    suspend fun observeTunnel(): Boolean
    suspend fun observeRoutingIdentity(): Boolean
    suspend fun measureStability(): Boolean
    suspend fun measureThroughput(): AndroidHostedMetrics
    suspend fun awaitDisconnected(): Boolean
}

/** Exact safe JSON shape consumed by Torturer's AndroidProfileObservation contract. */
internal data class AndroidHostedObservation(
    val sourceSha: String,
    var configured: Boolean = false,
    var connected: Boolean = false,
    var tunnelInterface: Boolean = false,
    var routingIdentityChanged: Boolean = false,
    var disconnectClean: Boolean = false,
    var restartVerified: Boolean = false,
    var reconnectBounded: Boolean = false,
    var secondTunnelInterface: Boolean = false,
    var secondRoutingIdentityChanged: Boolean = false,
    var stabilityVerified: Boolean = false,
    var latencyMs: Double = 0.0,
    var downloadMbps: Double = 0.0,
    var uploadMbps: Double = 0.0,
    var finalDisconnectClean: Boolean = false,
    var cleanupVerified: Boolean = false,
    var errorCode: String? = null,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("schema", AndroidHostedCommandContract.SCHEMA)
        .put("kind", AndroidHostedCommandContract.OBSERVATION_KIND)
        .put("platform", AndroidHostedCommandContract.PLATFORM)
        .put("source_sha", sourceSha)
        .put("configured", configured)
        .put("connected", connected)
        .put("tunnel_interface", tunnelInterface)
        .put("routing_identity_changed", routingIdentityChanged)
        .put("disconnect_clean", disconnectClean)
        .put("restart_verified", restartVerified)
        .put("reconnect_bounded", reconnectBounded)
        .put("second_tunnel_interface", secondTunnelInterface)
        .put("second_routing_identity_changed", secondRoutingIdentityChanged)
        .put("stability_verified", stabilityVerified)
        .put("latency_ms", safeMetric(latencyMs))
        .put("download_mbps", safeMetric(downloadMbps))
        .put("upload_mbps", safeMetric(uploadMbps))
        .put("final_disconnect_clean", finalDisconnectClean)
        .put("cleanup_verified", cleanupVerified)
        .also { output -> errorCode?.let { output.put("error_code", it) } }

    private fun safeMetric(value: Double): Double = if (value.isFinite() && value >= 0.0) value else 0.0
}

private class AndroidHostedOperationFailure(val code: String) : Exception()

/** Candidate-owned Android profile driver, compiled only into the instrumentation APK. */
internal class AndroidHostedProfileTestDriver(
    private val context: Context,
    private val controllerFactory: () -> SessionController = { AndroidSessionController(context) },
    private val platformFactory: (AndroidHostedEndpoints) -> AndroidHostedPlatform = { endpoints ->
        RealAndroidHostedPlatform(context, endpoints)
    },
) {
    suspend fun run(commandFileName: String): AndroidHostedObservation {
        val commandFile = AndroidHostedCommandContract.privateFile(context.filesDir, commandFileName)
        val command = try {
            AndroidHostedCommandContract.parse(commandFileName, AndroidHostedCommandContract.readCommand(commandFile))
        } finally {
            commandFile.delete()
        }
        val profileFile = AndroidHostedCommandContract.privateFile(context.filesDir, command.profileFile)
        val outputFile = AndroidHostedCommandContract.privateFile(context.filesDir, command.outputFile)
        val observation = AndroidHostedObservation(command.sourceSha)
        val controller = controllerFactory()
        val platform = platformFactory(command.endpoints)
        var generation: ULong? = null
        try {
            for (operation in command.operations) {
                try {
                    withTimeout(TimeUnit.SECONDS.toMillis(operation.timeoutSeconds.toLong())) {
                        execute(
                            operation, profileFile, controller, platform, observation,
                            setGeneration = { generation = it },
                            getGeneration = { generation },
                        )
                    }
                } catch (_: TimeoutCancellationException) {
                    throw AndroidHostedOperationFailure("OPERATION_TIMEOUT")
                }
            }
        } catch (failure: AndroidHostedOperationFailure) {
            observation.errorCode = failure.code
        } catch (_: Exception) {
            observation.errorCode = "DRIVER_ERROR"
        } finally {
            var cleanupSucceeded = true
            generation?.let { active ->
                cleanupSucceeded = stopSession(controller, active) && cleanupSucceeded
                generation = null
            }
            val cleanupSnapshot = runCatching { controller.snapshot() }.getOrNull()?.let { result ->
                result is SessionControllerResult.Success &&
                    result.value.state == SessionState.IDLE &&
                    result.value.cleanupComplete
            } == true
            cleanupSucceeded = runCatching { controller.destroy() is SessionControllerResult.Success }
                .getOrDefault(false) && cleanupSucceeded
            runCatching { context.stopService(DobbyVpnService.createStopIntent(context, 0, false)) }
            val disconnected = runCatching { platform.awaitDisconnected() }.getOrDefault(false)
            observation.cleanupVerified = cleanupSucceeded && cleanupSnapshot && disconnected
            if (!observation.cleanupVerified && observation.errorCode == null) observation.errorCode = "CLEANUP_FAILED"
            writeObservation(outputFile, observation)
            runCatching { profileFile.delete() }
        }
        return observation
    }

    private suspend fun execute(
        operation: AndroidHostedOperation,
        profileFile: File,
        controller: SessionController,
        platform: AndroidHostedPlatform,
        observation: AndroidHostedObservation,
        setGeneration: (ULong?) -> Unit,
        getGeneration: () -> ULong?,
    ) {
        when (operation.operation) {
            "configure" -> {
                val profile = try {
                    AndroidHostedCommandContract.readProfile(profileFile)
                } catch (_: Exception) {
                    throw AndroidHostedOperationFailure("PROFILE_UNAVAILABLE")
                }
                val configured = try {
                    controller.configure(profile)
                } finally {
                    Arrays.fill(profile, 0)
                    profileFile.delete()
                }
                if (configured !is SessionControllerResult.Success) throw AndroidHostedOperationFailure("CONFIGURE_REJECTED")
                observation.configured = true
            }
            "connect" -> connect(controller, platform, observation, setGeneration)
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
                val changed = platform.observeRoutingIdentity()
                if (observation.restartVerified) {
                    observation.secondRoutingIdentityChanged = changed
                } else {
                    observation.routingIdentityChanged = changed
                }
                if (!changed) throw AndroidHostedOperationFailure("ROUTING_IDENTITY_NOT_OBSERVED")
            }
            "measure_stability" -> {
                val stable = platform.measureStability()
                observation.stabilityVerified = stable
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
                connect(controller, platform, observation, setGeneration)
                observation.restartVerified = observation.connected
                observation.reconnectBounded = observation.connected
            }
            "inspect_cleanup" -> {
                val snapshot = controller.snapshot()
                val value = (snapshot as? SessionControllerResult.Success)?.value
                    ?: throw AndroidHostedOperationFailure("CLEANUP_INSPECTION_FAILED")
                val disconnected = platform.awaitDisconnected()
                observation.cleanupVerified = value.state == SessionState.IDLE && value.cleanupComplete && disconnected
                if (!observation.cleanupVerified) throw AndroidHostedOperationFailure("CLEANUP_INSPECTION_FAILED")
            }
            else -> throw AndroidHostedOperationFailure("INPUT_INVALID")
        }
    }

    private suspend fun connect(
        controller: SessionController,
        platform: AndroidHostedPlatform,
        observation: AndroidHostedObservation,
        setGeneration: (ULong?) -> Unit,
    ) {
        try {
            platform.requestConsent()
            platform.captureBaseline()
            val started = controller.start(SessionStartTarget.AutoSelect)
            val value = (started as? SessionControllerResult.Success)?.value
                ?: throw AndroidHostedOperationFailure("CONNECT_REJECTED")
            setGeneration(value)
            if (awaitState(controller, SessionState.CONNECTED)?.state != SessionState.CONNECTED) {
                throw AndroidHostedOperationFailure("CONNECT_FAILED")
            }
            observation.connected = true
        } catch (failure: AndroidHostedOperationFailure) {
            throw failure
        } catch (_: Exception) {
            throw AndroidHostedOperationFailure("CONNECT_FAILED")
        }
    }

    private suspend fun awaitState(controller: SessionController, expected: SessionState): SessionSnapshot? {
        var latest: SessionSnapshot? = null
        repeat(80) {
            val snapshot = controller.snapshot()
            if (snapshot is SessionControllerResult.Success) {
                latest = snapshot.value
                if (snapshot.value.state == expected || snapshot.value.state == SessionState.FAILED) return latest
            }
            delay(250L)
        }
        return latest
    }

    private suspend fun stopCurrentSession(
        controller: SessionController,
        getGeneration: () -> ULong?,
        setGeneration: (ULong?) -> Unit,
    ): Boolean {
        val generation = getGeneration() ?: return true
        return stopSession(controller, generation).also { if (it) setGeneration(null) }
    }

    private suspend fun stopSession(controller: SessionController, generation: ULong): Boolean =
        runCatching { controller.stop(generation) is SessionControllerResult.Success }.getOrDefault(false)

    private fun writeObservation(file: File, observation: AndroidHostedObservation) {
        val temporary = File(file.parentFile, "${file.name}.tmp")
        runCatching { temporary.delete() }
        FileOutputStream(temporary, false).use { output ->
            output.write(observation.toJson().toString().toByteArray(Charsets.UTF_8))
            output.flush()
            output.fd.sync()
        }
        if (!temporary.renameTo(file)) {
            temporary.delete()
            throw IllegalStateException("OUTPUT_WRITE_FAILED")
        }
    }
}

/** Real Android APIs used by the candidate seam; identities are compared as digests only. */
internal class RealAndroidHostedPlatform(
    private val context: Context,
    private val endpoints: AndroidHostedEndpoints,
) : AndroidHostedPlatform {
    private val connectivity: ConnectivityManager
        get() = requireNotNull(context.getSystemService(ConnectivityManager::class.java))
    private var baselineFingerprint: ByteArray? = null
    private var lastTunnelFingerprint: ByteArray? = null

    override suspend fun requestConsent() = withContext(Dispatchers.Main) {
        if (VpnService.prepare(context) == null) return@withContext
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        instrumentation.startActivitySync(
            Intent(context, VpnConsentTestActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        val approval = UiDevice.getInstance(instrumentation).wait(
            Until.findObject(By.res(Pattern.compile(".+:id/button1"))),
            CONSENT_TIMEOUT_MILLIS,
        ) ?: throw AndroidHostedOperationFailure("CONSENT_UNAVAILABLE")
        approval.click()
        val deadline = android.os.SystemClock.elapsedRealtime() + CONSENT_TIMEOUT_MILLIS
        while (android.os.SystemClock.elapsedRealtime() < deadline) {
            if (VpnService.prepare(context) == null) return@withContext
            delay(POLL_INTERVAL_MILLIS)
        }
        throw AndroidHostedOperationFailure("CONSENT_REJECTED")
    }

    override suspend fun captureBaseline() = withContext(Dispatchers.IO) {
        baselineFingerprint?.fill(0)
        baselineFingerprint = fetchFingerprint()
    }

    override suspend fun observeTunnel(): Boolean = withContext(Dispatchers.IO) {
        connectivity.awaitVpnNetworkState(
            present = true,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS.toLong(),
            pollIntervalMillis = POLL_INTERVAL_MILLIS,
        )?.linkProperties?.interfaceName?.isNullOrBlank() == false
    }

    override suspend fun observeRoutingIdentity(): Boolean = withContext(Dispatchers.IO) {
        val baseline = baselineFingerprint ?: throw AndroidHostedOperationFailure("IDENTITY_BASELINE_MISSING")
        val current = fetchFingerprint()
        lastTunnelFingerprint?.fill(0)
        lastTunnelFingerprint = current
        !MessageDigest.isEqual(baseline, current)
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
        val latency = measureTransfer(endpoints.latencyUrl, upload = false, maximumBytes = 64 * 1024).elapsedMs
        val download = measureTransfer(endpoints.downloadUrl, upload = false, maximumBytes = THROUGHPUT_BYTES).rateMbps
        val payload = ByteArray(THROUGHPUT_BYTES)
        try {
            val upload = measureTransfer(endpoints.uploadUrl, upload = true, maximumBytes = payload.size, payload = payload).rateMbps
            AndroidHostedMetrics(latency, download, upload)
        } finally {
            Arrays.fill(payload, 0)
        }
    }

    override suspend fun awaitDisconnected(): Boolean = withContext(Dispatchers.IO) {
        connectivity.awaitVpnNetworkState(
            present = false,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS.toLong(),
            pollIntervalMillis = POLL_INTERVAL_MILLIS,
        ) == null
    }

    private fun fetchFingerprint(): ByteArray = withNetworkConnection(endpoints.identityUrl, upload = false) { connection ->
        val bytes = readAtMost(connection, MAX_IDENTITY_BYTES)
        MessageDigest.getInstance("SHA-256").digest(bytes).also { Arrays.fill(bytes, 0) }
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
            readAtMost(connection, maximumBytes).size
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

    private fun <T> withNetworkConnection(rawUrl: String, upload: Boolean, block: (HttpURLConnection) -> T): T {
        val connection = (URL(rawUrl).openConnection() as? HttpURLConnection)
            ?: throw AndroidHostedOperationFailure("NETWORK_UNAVAILABLE")
        connection.connectTimeout = NETWORK_TIMEOUT_MILLIS
        connection.readTimeout = NETWORK_TIMEOUT_MILLIS
        connection.instanceFollowRedirects = false
        if (upload) connection.setRequestProperty("Content-Type", "application/octet-stream")
        return try {
            block(connection)
        } finally {
            connection.disconnect()
        }
    }

    private fun requireSuccess(connection: HttpURLConnection) {
        if (connection.responseCode !in HttpURLConnection.HTTP_OK..299) throw AndroidHostedOperationFailure("NETWORK_STATUS")
    }

    private fun drainResponse(connection: HttpURLConnection) {
        connection.inputStream.use { input ->
            val buffer = ByteArray(8 * 1024)
            var total = 0
            while (total < MAX_UPLOAD_RESPONSE_BYTES) {
                val count = input.read(buffer, 0, minOf(buffer.size, MAX_UPLOAD_RESPONSE_BYTES - total))
                if (count < 0) break
                total += count
            }
            Arrays.fill(buffer, 0)
        }
    }

    private fun readAtMost(connection: HttpURLConnection, maximumBytes: Int): ByteArray {
        connection.inputStream.use { input ->
            val result = ByteArray(maximumBytes + 1)
            var offset = 0
            while (offset < result.size) {
                val count = input.read(result, offset, result.size - offset)
                if (count < 0) break
                offset += count
            }
            if (offset !in 1..maximumBytes) {
                Arrays.fill(result, 0)
                throw AndroidHostedOperationFailure("NETWORK_BODY_INVALID")
            }
            return result.copyOf(offset).also { Arrays.fill(result, 0) }
        }
    }

    private companion object {
        const val CONSENT_TIMEOUT_MILLIS = 10_000L
        const val NETWORK_TIMEOUT_MILLIS = 20_000
        const val THROUGHPUT_TIMEOUT_SECONDS = 30L
        const val POLL_INTERVAL_MILLIS = 100L
        const val STABILITY_SAMPLE_COUNT = 3
        const val STABILITY_INTERVAL_MILLIS = 1_000L
        const val MAX_IDENTITY_BYTES = 128
        const val MAX_UPLOAD_RESPONSE_BYTES = 64 * 1024
        const val THROUGHPUT_BYTES = 1024 * 1024
        const val NANOS_PER_MILLISECOND = 1_000_000.0
    }
}

/** Instrumentation entrypoint used by the Torturer runner. */
@org.junit.runner.RunWith(androidx.test.ext.junit.runners.AndroidJUnit4::class)
class AndroidHostedProfileInstrumentationTest {
    @org.junit.Test
    fun run_owner_supplied_ordered_profile_command() = kotlinx.coroutines.runBlocking {
        val commandFile = InstrumentationRegistry.getArguments()
            .getString(AndroidHostedCommandContract.COMMAND_ARGUMENT)
        org.junit.Assume.assumeTrue(commandFile != null)
        requireNotNull(commandFile)
        val observation = AndroidHostedProfileTestDriver(InstrumentationRegistry.getInstrumentation().targetContext).run(requireNotNull(commandFile))
        org.junit.Assert.assertNull("hosted Android driver reported an error", observation.errorCode)
    }
}
