package com.dobby.cli

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionObservation
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionProtocol
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.domain.SessionWarning
import kotlinx.coroutines.runBlocking
import java.nio.file.Files
import okio.Path.Companion.toPath
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CliClientSessionControllerTest {
    @Test
    fun connectProfilePreservesRawBytesAndStartsRequestedProfile() = runBlocking {
        val config = byteArrayOf(0, 0xff.toByte(), 0x0a)
        val file = Files.createTempFile("dobby-cli-profile-config", ".toml")
        Files.write(file, config)
        val controller = RecordingSessionController()

        val result = CliClient(sessionController = controller).connectProfile(listOf(file.toString(), "9"))

        assertEquals(ExitCode.OK, result)
        assertContentEquals(config, controller.configuredBytes)
        assertEquals(listOf<SessionStartTarget>(SessionStartTarget.ProfileIndex(9)), controller.startTargets)
        assertEquals(emptyList(), controller.stoppedGenerations)
    }

    @Test
    fun connectProfileRejectsInvalidArgumentsWithoutConfiguring() {
        val controller = RecordingSessionController()
        val client = CliClient(sessionController = controller)

        listOf(
            emptyList(),
            listOf("missing-config"),
            listOf("missing-config", "-1"),
            listOf("missing-config", "not-an-index"),
            listOf("missing-config", "1", "extra"),
        ).forEach { options ->
            assertEquals(ExitCode.INVALID_ARGS, client.connectProfile(options))
        }
        assertContentEquals(byteArrayOf(), controller.configuredBytes)
        assertEquals(emptyList(), controller.startTargets)
    }

    @Test
    fun checkConfigUsesGoProfileSummariesAndStopsEachGeneration() = runBlocking {
        val config = byteArrayOf(0, 0xff.toByte(), 0x0a)
        val file = Files.createTempFile("dobby-cli-config", ".toml")
        Files.write(file, config)
        val controller = RecordingSessionController()

        val result = CliClient(sessionController = controller).checkConfig(listOf(file.toString()))

        assertEquals(ExitCode.OK, result)
        assertContentEquals(config, controller.configuredBytes)
        assertEquals(
            listOf<SessionStartTarget>(SessionStartTarget.ProfileIndex(4), SessionStartTarget.ProfileIndex(9)),
            controller.startTargets,
        )
        assertEquals(listOf(1uL, 2uL), controller.stoppedGenerations)
    }

    @Test
    fun terminalFailureLogsOnlyTypedCodeStateAndGeneration() {
        val config = Files.createTempFile("dobby-cli-failed-profile", ".toml")
        Files.write(config, byteArrayOf(1))
        val logPath = Files.createTempFile("dobby-cli-failure", ".log")
        val logs = LogsRepository(logPath.toString().toPath())
        val controller = RecordingSessionController(
            terminalState = SessionState.FAILED,
            terminalFailureCode = SessionFailureCode.PLATFORM_FAILED,
        )

        val result = CliClient(
            sessionController = controller,
            logsRepository = logs,
            logger = Logger(logs),
        ).connectProfile(listOf(config.toString(), "4"))

        assertEquals(ExitCode.TUNNEL_START_ERROR, result)
        val output = logs.readAllLogs().joinToString("\n")
        assertTrue(output.contains("state=FAILED"))
        assertTrue(output.contains("generation=1"))
        assertTrue(output.contains("failureCode=PLATFORM_FAILED"))
        assertFalse(output.contains("credential-value"))
    }

    @Test
    fun directStartFailureDoesNotLogRawMessage() {
        val config = Files.createTempFile("dobby-cli-rejected-profile", ".toml")
        Files.write(config, byteArrayOf(1))
        val logPath = Files.createTempFile("dobby-cli-rejected", ".log")
        val logs = LogsRepository(logPath.toString().toPath())
        val controller = RecordingSessionController(
            startFailure = SessionControllerResult.Failure(
                message = "credential-value",
                code = SessionFailureCode.RUNTIME_FAILED,
            ),
        )

        val result = CliClient(
            sessionController = controller,
            logsRepository = logs,
            logger = Logger(logs),
        ).connectProfile(listOf(config.toString(), "4"))

        assertEquals(ExitCode.TUNNEL_START_ERROR, result)
        val output = logs.readAllLogs().joinToString("\n")
        assertTrue(output.contains("state=START_REJECTED"))
        assertTrue(output.contains("failureCode=RUNTIME_FAILED"))
        assertFalse(output.contains("credential-value"))
    }

    @Test
    fun disconnectFailsWhenCleanupCompletedWithFailure() = runBlocking {
        val controller = RecordingSessionController(
            snapshotState = SessionState.FAILED,
            snapshotFailureCode = SessionFailureCode.CLEANUP_FAILED,
        )
        controller.start(SessionStartTarget.ProfileIndex(4))

        val result = CliClient(sessionController = controller).disconnect(emptyList())

        assertEquals(ExitCode.PROGRAM_FAILED, result)
    }

    @Test
    fun checkConfigFailsWhenAProfileCleanupFails() {
        val config = Files.createTempFile("dobby-cli-cleanup-config", ".toml")
        Files.write(config, byteArrayOf(1))
        val controller = RecordingSessionController(
            snapshotState = SessionState.FAILED,
            snapshotFailureCode = SessionFailureCode.CLEANUP_FAILED,
        )

        val result = CliClient(sessionController = controller).checkConfig(listOf(config.toString()))

        assertEquals(ExitCode.PROTOCOL_CHECK_FAILED, result)
    }

    @Test
    fun verifySessionFailsWhenCleanupFails() {
        val config = Files.createTempFile("dobby-cli-cleanup-verify", ".toml")
        Files.write(config, byteArrayOf(1))
        val controller = RecordingSessionController(
            snapshotState = SessionState.FAILED,
            snapshotFailureCode = SessionFailureCode.CLEANUP_FAILED,
        )
        var lookups = 0
        val client = CliClient(
            sessionController = controller,
            externalIpLookup = { if (lookups++ == 0) "192.0.2.1" else "198.51.100.2" },
        )

        assertEquals(ExitCode.SESSION_VERIFY_FAILED, client.verifySession(listOf(config.toString())))
    }
}

private class RecordingSessionController(
    private val terminalState: SessionState = SessionState.CONNECTED,
    private val terminalFailureCode: SessionFailureCode? = null,
    private val startFailure: SessionControllerResult.Failure? = null,
    private val snapshotState: SessionState = SessionState.IDLE,
    private val snapshotFailureCode: SessionFailureCode? = null,
) : SessionController {
    var configuredBytes = byteArrayOf()
    val startTargets = mutableListOf<SessionStartTarget>()
    val stoppedGenerations = mutableListOf<ULong>()
    private var generation = 0uL
    private var sequence = 0uL

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> {
        configuredBytes = rawConfig.copyOf()
        return SessionControllerResult.Success(
            SessionConfiguration(
                digest = "digest",
                profiles = listOf(
                    SessionProfile(4, SessionProtocol.OUTLINE, "first"),
                    SessionProfile(9, SessionProtocol.XRAY, "second"),
                ),
                warnings = listOf(SessionWarning("legacy", "ignored")),
            ),
        )
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> {
        startTargets += target
        startFailure?.let { return it }
        generation += 1uL
        return SessionControllerResult.Success(generation)
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> {
        stoppedGenerations += generation
        return SessionControllerResult.Success(generation)
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
        SessionControllerResult.Success(
            SessionSnapshot(
                generation,
                snapshotState,
                configured = true,
                cleanupComplete = true,
                lastFailureCode = snapshotFailureCode,
            ),
        )

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> {
        if (afterSequence >= sequence) {
            sequence += 1uL
            return SessionControllerResult.Success(
                SessionObservation(
                    events = listOf(SessionEvent(generation, sequence, terminalState, terminalFailureCode)),
                    nextSequence = sequence,
                ),
            )
        }
        return SessionControllerResult.Success(SessionObservation(emptyList(), sequence))
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = SessionControllerResult.Success(Unit)
}
