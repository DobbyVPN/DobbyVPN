package com.dobby.cli

import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionObservation
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionProtocol
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.domain.SessionWarning
import kotlinx.coroutines.runBlocking
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals

class CliClientSessionControllerTest {
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
}

private class RecordingSessionController : SessionController {
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
        generation += 1uL
        return SessionControllerResult.Success(generation)
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> {
        stoppedGenerations += generation
        return SessionControllerResult.Success(generation)
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
        SessionControllerResult.Success(SessionSnapshot(generation, SessionState.IDLE, configured = true, cleanupComplete = true))

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> {
        if (afterSequence >= sequence) {
            sequence += 1uL
            return SessionControllerResult.Success(
                SessionObservation(
                    events = listOf(SessionEvent(generation, sequence, SessionState.CONNECTED)),
                    nextSequence = sequence,
                ),
            )
        }
        return SessionControllerResult.Success(SessionObservation(emptyList(), sequence))
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = SessionControllerResult.Success(Unit)
}
