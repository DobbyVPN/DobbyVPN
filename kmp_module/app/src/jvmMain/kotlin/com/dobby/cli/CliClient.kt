package com.dobby.cli

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.GrpcSessionController
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionIdentityStore
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import interop.GrpcVpnLibrary
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import java.io.File
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermissions
import java.time.Duration
import kotlin.time.Duration.Companion.milliseconds

private const val configFetchTimeoutSeconds = 20L
private const val externalIpTimeoutSeconds = 10L
private const val millisPerSecond = 1_000L
private val HTTP_SUCCESS_STATUS = 200..299

/**
 * JVM command-line shell for sessionapi/v1.
 *
 * This class deliberately never interprets a profile or runs a health check. It only acquires
 * configuration bytes, sends explicit session commands, and waits for the ordered terminal
 * events produced by Go. `check-config` is the one intentional explicit profile operation: it
 * asks Go to start each Go-provided profile index independently for compatibility with the
 * existing CLI command.
 */
class CliClient(
    private val sessionController: SessionController = GrpcSessionController(
        GrpcVpnLibrary.sessionGrpcLibrary,
        CliSessionIdentityStore(),
    ),
    private val logsRepository: LogsRepository = LogsRepository(
        additionalLogFilePaths = provideAdditionalLogFilePaths(),
    ),
    private val logger: Logger = Logger(logsRepository),
    private val externalIpLookup: () -> String? = ::fetchExternalIp,
) {
    private var observedSequence: ULong = 0u

    fun logs(options: List<String>): ExitCode = when {
        options.isEmpty() -> {
            logsRepository.readUILogs().forEach(::println)
            ExitCode.OK
        }
        options.size == 2 && options[0] == "-n" -> {
            val count = options[1].toIntOrNull()
            if (count == null || count <= 0) ExitCode.INVALID_ARGS else {
                logsRepository.readLogs(count).forEach(::println)
                ExitCode.OK
            }
        }
        options.size == 1 && options[0] == "clear" -> {
            logsRepository.clearLogs()
            ExitCode.OK
        }
        else -> ExitCode.INVALID_ARGS
    }

    fun connect(options: List<String>): ExitCode {
        if (options.isEmpty() || options.size > 2) return ExitCode.INVALID_ARGS
        val skipHealthCheck = options.size == 2 && options[1] == "--skip-healthcheck"
        if (options.size == 2 && !skipHealthCheck) return ExitCode.INVALID_ARGS

        val rawConfig = readConnectionArgument(options[0]) ?: return ExitCode.INVALID_ARGS
        if (!configure(rawConfig)) return ExitCode.CONFIG_FORMAT_ERROR

        // Go owns health policy. A CONNECTED event is the authoritative replacement for the
        // legacy health-check Boolean channel; the flag remains accepted for CLI compatibility.
        return if (runBlocking { startAndAwait(SessionStartTarget.AutoSelect) }) {
            ExitCode.OK
        } else {
            ExitCode.TUNNEL_START_ERROR
        }
    }

    /** Connects one exact Go-provided profile index without interpreting the configuration. */
    fun connectProfile(options: List<String>): ExitCode {
        return if (options.size != 2) {
            ExitCode.INVALID_ARGS
        } else {
            val profileIndex = options[1].toIntOrNull()?.takeIf { it >= 0 }
            val rawConfig = readConnectionArgument(options[0])
            when {
                profileIndex == null || rawConfig == null -> ExitCode.INVALID_ARGS
                !configure(rawConfig) -> ExitCode.CONFIG_FORMAT_ERROR
                runBlocking { startAndAwait(SessionStartTarget.ProfileIndex(profileIndex)) } -> ExitCode.OK
                else -> ExitCode.TUNNEL_START_ERROR
            }
        }
    }

    fun checkConfig(options: List<String>): ExitCode {
        if (options.size != 1) return ExitCode.INVALID_ARGS
        val rawConfig = readConnectionArgument(options[0]) ?: return ExitCode.INVALID_ARGS
        val configuration = configureResult(rawConfig) ?: return ExitCode.CONFIG_FORMAT_ERROR
        if (configuration.profiles.isEmpty()) return ExitCode.CONFIG_FORMAT_ERROR

        return runBlocking { checkProfiles(configuration.profiles) }
    }

    private suspend fun checkProfiles(profiles: List<SessionProfile>): ExitCode {
        var failures = 0
        for ((position, profile) in profiles.withIndex()) {
            val label = profile.label(position, profiles.size)
            println("Checking $label")
            logger.log("[CLI] Checking $label")

            val generation = when (val result = sessionController.start(SessionStartTarget.ProfileIndex(profile.index))) {
                is SessionControllerResult.Success -> result.value
                is SessionControllerResult.Failure -> {
                    failures += 1
                    println("FAILED $label: VPN tunnel did not start")
                    logger.log(
                        "[CLI] Session start rejected state=START_REJECTED generation=0 " +
                            "failureCode=${result.code.safeFailureCode()}"
                    )
                    continue
                }
            }

            var connected = false
            var cleanupSucceeded = false
            try {
                connected = awaitTerminal(generation)
            } finally {
                cleanupSucceeded = stopAndWait(generation)
            }
            if (connected && cleanupSucceeded) {
                println("OK $label")
                logger.log("[CLI] OK $label")
            } else {
                failures += 1
                val reason = if (!connected) "VPN tunnel did not reach Connected" else "VPN cleanup failed"
                println("FAILED $label: $reason")
                logger.log("[CLI] FAILED $label: $reason")
                printRecentLogs()
            }
        }

        println("Checked ${profiles.size} profile(s), failures=$failures")
        return if (failures == 0) ExitCode.OK else ExitCode.PROTOCOL_CHECK_FAILED
    }

    fun disconnect(options: List<String>): ExitCode {
        if (options.isNotEmpty()) return ExitCode.INVALID_ARGS
        val snapshot = runBlocking { sessionController.snapshot() }
        if (snapshot !is SessionControllerResult.Success) return ExitCode.PROGRAM_FAILED
        val generation = snapshot.value.generation
        if (generation == 0uL) return ExitCode.OK
        if (snapshot.value.cleanupComplete) {
            return if (snapshot.value.cleanupSucceeded()) ExitCode.OK else ExitCode.PROGRAM_FAILED
        }
        return if (runBlocking { stopAndWait(generation) }) ExitCode.OK else ExitCode.PROGRAM_FAILED
    }

    fun externalIp(options: List<String>): ExitCode {
        if (options.isNotEmpty()) return ExitCode.INVALID_ARGS
        val ip = externalIpLookup() ?: return ExitCode.PROGRAM_FAILED
        println(ip)
        return ExitCode.OK
    }

    fun verifySession(options: List<String>): ExitCode {
        if (options.size != 1) return ExitCode.INVALID_ARGS
        val rawConfig = readConnectionArgument(options[0]) ?: return ExitCode.INVALID_ARGS

        val baselineIp = externalIpLookup()
        if (baselineIp == null) {
            println("FAILED: could not determine baseline external IP")
            logger.log("[CLI] FAILED verify-session: baseline external IP unavailable")
            return ExitCode.SESSION_VERIFY_FAILED
        }
        println("Baseline IP: $baselineIp")
        logger.log("[CLI] verify-session baseline IP acquired")

        if (!configure(rawConfig)) return ExitCode.CONFIG_FORMAT_ERROR
        val generation = when (val result = runBlocking { sessionController.start(SessionStartTarget.AutoSelect) }) {
            is SessionControllerResult.Success -> result.value
            is SessionControllerResult.Failure -> return ExitCode.TUNNEL_START_ERROR
        }

        var outcome = ExitCode.OK
        try {
            if (!runBlocking { awaitTerminal(generation) }) {
                outcome = ExitCode.TUNNEL_START_ERROR
            } else {
                val tunnelIp = waitForExternalIpChange(baselineIp, TUNNEL_IP_VERIFY_TIMEOUT_SECONDS)
                if (tunnelIp == null) {
                    outcome = ExitCode.SESSION_VERIFY_FAILED
                } else {
                    println("Tunnel IP: $tunnelIp")
                    logger.log("[CLI] verify-session tunnel IP acquired")
                }
            }
            if (outcome == ExitCode.SESSION_VERIFY_FAILED) {
                println(
                    "FAILED: external IP did not change through tunnel " +
                        "(baseline=$baselineIp tunnel=unchanged after ${TUNNEL_IP_VERIFY_TIMEOUT_SECONDS}s)"
                )
                logger.log("[CLI] FAILED verify-session: IP unchanged")
            }
        } finally {
            if (!runBlocking { stopAndWait(generation) }) {
                println("FAILED: VPN cleanup did not complete successfully")
                logger.log("[CLI] FAILED verify-session: cleanup failed")
                outcome = ExitCode.SESSION_VERIFY_FAILED
            }
        }
        if (outcome != ExitCode.OK) return outcome

        val restoredIp = externalIpLookup()
        if (restoredIp == null) {
            println("FAILED: could not determine external IP after disconnect")
            return ExitCode.SESSION_VERIFY_FAILED
        }
        println("Restored IP: $restoredIp")
        if (restoredIp != baselineIp) {
            println("WARNING: restored IP differs from baseline (baseline=$baselineIp restored=$restoredIp)")
            logger.log("[CLI] verify-session warning: restored IP differs from baseline")
        }
        println("OK verify-session")
        return ExitCode.OK
    }

    fun status(options: List<String>): ExitCode {
        val snapshot = runBlocking { sessionController.snapshot() }
        val state = (snapshot as? SessionControllerResult.Success)?.value?.state ?: return ExitCode.PROGRAM_FAILED
        val display = state.displayState()
        return when {
            options.isEmpty() -> {
                println(display)
                ExitCode.OK
            }
            options.size == 1 && options[0] == "--json" -> {
                println("{ \"code\": ${state.statusCode()}, \"state\": \"$display\" }")
                ExitCode.OK
            }
            else -> ExitCode.INVALID_ARGS
        }
    }

    private fun configure(rawConfig: ByteArray): Boolean = configureResult(rawConfig) != null

    private fun configureResult(rawConfig: ByteArray): SessionConfiguration? = runBlocking {
        when (val result = sessionController.configure(rawConfig)) {
            is SessionControllerResult.Success -> {
                observedSequence = 0u
                logger.log("[CLI] Session configuration accepted: profiles=${result.value.profiles.size}")
                result.value
            }
            is SessionControllerResult.Failure -> {
                logger.log(
                    "[CLI] Session configuration rejected " +
                        "failureCode=${result.code.safeFailureCode()}"
                )
                null
            }
        }
    }

    private suspend fun startAndAwait(target: SessionStartTarget): Boolean = when (val result = sessionController.start(target)) {
        is SessionControllerResult.Success -> {
            val connected = awaitTerminal(result.value)
            if (!connected) stopAndWait(result.value)
            connected
        }
        is SessionControllerResult.Failure -> {
            logger.log(
                "[CLI] Session start rejected state=START_REJECTED generation=0 " +
                    "failureCode=${result.code.safeFailureCode()}"
            )
            false
        }
    }

    /** Waits only on ordered Go events, with Snapshot as a lossless terminal-state fallback. */
    private suspend fun awaitTerminal(generation: ULong): Boolean {
        repeat(MAX_EVENT_POLLS) {
            val terminal = pollEvents(generation) ?: pollSnapshot(generation)
            if (terminal != null) return terminal
            delay(EVENT_POLL_INTERVAL_MS.milliseconds)
        }
        logger.log("[CLI] Session start timed out waiting for generation=$generation")
        return false
    }

    private suspend fun pollEvents(generation: ULong): Boolean? =
        when (val observation = sessionController.observe(observedSequence)) {
            is SessionControllerResult.Success -> {
                observedSequence = observation.value.nextSequence
                observation.value.events
                    .asSequence()
                    .filter { it.generation == generation }
                    .mapNotNull { event ->
                        event.state.terminalResult()?.also { connected ->
                            if (!connected && event.state == SessionState.FAILED) {
                                logTerminalFailure(generation, event.failureCode)
                            }
                        }
                    }
                    .firstOrNull()
            }
            is SessionControllerResult.Failure -> {
                logger.log(
                    "[CLI] Session event poll failed " +
                        "failureCode=${observation.code.safeFailureCode()}"
                )
                false
            }
        }

    private suspend fun pollSnapshot(generation: ULong): Boolean? =
        when (val snapshot = sessionController.snapshot()) {
            is SessionControllerResult.Success -> snapshot.value
                .takeIf { it.generation == generation }
                ?.let {
                    it.state.terminalResult()?.also { connected ->
                        if (!connected && it.state == SessionState.FAILED) {
                            logTerminalFailure(generation, it.lastFailureCode)
                        }
                    }
                }
            is SessionControllerResult.Failure -> false
        }

    private suspend fun stopAndWait(generation: ULong): Boolean {
        when (val result = sessionController.stop(generation)) {
            is SessionControllerResult.Failure -> {
                logger.log(
                    "[CLI] Session stop rejected generation=$generation " +
                        "failureCode=${result.code.safeFailureCode()}"
                )
                return false
            }
            is SessionControllerResult.Success -> Unit
        }
        repeat(MAX_EVENT_POLLS) {
            when (val snapshot = sessionController.snapshot()) {
                is SessionControllerResult.Success -> {
                    if (snapshot.value.generation != generation) return true
                    if (snapshot.value.cleanupComplete) return snapshot.value.cleanupSucceeded()
                }
                is SessionControllerResult.Failure -> return false
            }
            delay(EVENT_POLL_INTERVAL_MS.milliseconds)
        }
        logger.log("[CLI] Session stop timed out waiting for cleanup generation=$generation")
        return false
    }

    private fun logTerminalFailure(generation: ULong, failureCode: SessionFailureCode?) {
        logger.log(
            "[CLI] Session terminal state=FAILED generation=$generation " +
                "failureCode=${failureCode.safeFailureCode()}"
        )
    }

    private fun SessionSnapshot.cleanupSucceeded(): Boolean =
        state != SessionState.FAILED && lastFailureCode != SessionFailureCode.CLEANUP_FAILED

    private fun printRecentLogs() {
        println("Recent logs:")
        logsRepository.readLogs(20).forEach(::println)
    }

    /** Acquires opaque bytes without decoding/reformatting a configuration. */
    private fun readConnectionArgument(value: String): ByteArray? {
        val uri = runCatching { URI(value) }.getOrNull()
        if (uri?.scheme == "http" || uri?.scheme == "https") return fetchConfig(uri)
        // A direct protocol URL (for example ss://) is configuration, not a local path.
        if (value.isValidUrl()) return value.encodeToByteArray()
        val path = File(value).toPath()
        if (Files.isRegularFile(path)) return runCatching { Files.readAllBytes(path) }.getOrNull()
        return null
    }

    private fun fetchConfig(uri: URI): ByteArray? = runCatching {
        val request = HttpRequest.newBuilder(uri).timeout(Duration.ofSeconds(configFetchTimeoutSeconds)).GET().build()
        val response = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(configFetchTimeoutSeconds)).build()
            .send(request, HttpResponse.BodyHandlers.ofByteArray())
        response.body().takeIf { response.statusCode() in HTTP_SUCCESS_STATUS }
    }.getOrNull()

    private fun SessionProfile.label(index: Int, total: Int): String {
        val descriptionPart = description.replace(Regex("\\s+"), " ").trim()
            .takeIf { it.isNotEmpty() }?.let { "description=\"$it\", " }.orEmpty()
        return "profile ${index + 1}/$total: ${descriptionPart}protocol=$protocol, sourceIndex=${this.index}"
    }

    private fun waitForExternalIpChange(baselineIp: String, timeoutSeconds: Int): String? {
        val deadlineMs = System.currentTimeMillis() + timeoutSeconds * millisPerSecond
        while (System.currentTimeMillis() < deadlineMs) {
            val ip = externalIpLookup()
            if (ip != null && ip != baselineIp) return ip
            Thread.sleep(TUNNEL_IP_POLL_INTERVAL_MS)
        }
        return externalIpLookup()?.takeIf { it != baselineIp }
    }

    private companion object {
        const val EVENT_POLL_INTERVAL_MS = 100L
        const val MAX_EVENT_POLLS = 900 // 90 seconds, matching the historical service-start timeout.
        const val TUNNEL_IP_VERIFY_TIMEOUT_SECONDS = 30
        const val TUNNEL_IP_POLL_INTERVAL_MS = 1_000L
    }
}

private fun SessionFailureCode?.safeFailureCode(): String =
    (this ?: SessionFailureCode.UNSPECIFIED).name

private fun SessionState.displayState(): String = when (this) {
    SessionState.PROBING, SessionState.PREPARING, SessionState.STOPPING -> "Connecting"
    SessionState.CONNECTED -> "Connected"
    else -> "Disconnected"
}

private fun SessionState.terminalResult(): Boolean? = when (this) {
    SessionState.CONNECTED -> true
    SessionState.FAILED, SessionState.IDLE, SessionState.DESTROYED -> false
    else -> null
}

private fun SessionState.statusCode(): Int = when (this) {
    SessionState.CONNECTED -> 2
    SessionState.PROBING, SessionState.PREPARING, SessionState.STOPPING -> 1
    else -> 0
}

private fun fetchExternalIp(): String? {
    val endpoints = listOf("https://api.ipify.org", "https://ifconfig.me/ip")
    for (endpoint in endpoints) {
        val response = runCatching {
            val request = HttpRequest.newBuilder(URI.create(endpoint))
                .timeout(Duration.ofSeconds(externalIpTimeoutSeconds))
                .header("Cache-Control", "no-store")
                .header("Pragma", "no-cache")
                .GET().build()
            HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1)
                .connectTimeout(Duration.ofSeconds(externalIpTimeoutSeconds)).build()
                .send(request, HttpResponse.BodyHandlers.ofString())
        }.getOrNull()
        val ip = response?.body()?.trim()
        if (response != null && response.statusCode() in HTTP_SUCCESS_STATUS && !ip.isNullOrBlank()) return ip
    }
    return null
}

fun String.isValidUrl(): Boolean = runCatching {
    val url = java.net.URL(this)
    url.toURI()
}.isSuccess

/**
 * Persists only the opaque Go session ID so separate CLI processes can address the same tunnel.
 * The ID is not logged and the containing directory/file are owner-only on POSIX file systems.
 */
internal class CliSessionIdentityStore(
    private val path: Path = Path.of(System.getProperty("user.home"), ".dobbyvpn", "cli-session-id"),
) : SessionIdentityStore {
    override fun load(): String? = runCatching {
        Files.readString(path).trim().takeIf { SESSION_ID.matches(it) }
    }.getOrNull()

    override fun save(sessionId: String) {
        if (!SESSION_ID.matches(sessionId)) return
        runCatching {
            Files.createDirectories(path.parent)
            setOwnerOnly(path.parent, DIRECTORY_PERMISSIONS)
            Files.writeString(
                path,
                sessionId,
                StandardOpenOption.CREATE,
                StandardOpenOption.TRUNCATE_EXISTING,
                StandardOpenOption.WRITE,
            )
            setOwnerOnly(path, FILE_PERMISSIONS)
        }
    }

    override fun clear() {
        runCatching { Files.deleteIfExists(path) }
    }

    private fun setOwnerOnly(target: Path, permissions: Set<PosixFilePermission>) {
        runCatching { Files.setPosixFilePermissions(target, permissions) }
    }

    private companion object {
        val SESSION_ID = Regex("[A-Za-z0-9_-]{1,256}")
        val DIRECTORY_PERMISSIONS: Set<PosixFilePermission> = PosixFilePermissions.fromString("rwx------")
        val FILE_PERMISSIONS: Set<PosixFilePermission> = PosixFilePermissions.fromString("rw-------")
    }
}
