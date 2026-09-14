package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModelStore
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionSourceKind
import com.dobby.feature.main.domain.SessionStart
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.domain.SessionStop
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeout
import okio.FileSystem
import okio.Path
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class MainViewModelConnectionTest {
    @BeforeTest
    fun setMainDispatcher() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
    }

    @AfterTest
    fun resetMainDispatcher() {
        Dispatchers.resetMain()
    }

    @Test
    fun disconnectedButtonConfiguresAndStartsWithAutomaticSelection() = runBlocking {
        val fixture = Fixture()
        try {
            val url = "https://example.test/profile"
            fixture.viewModel.onConnectionButtonClicked(url)

            assertEquals(url, fixture.sessionController.configuredUrls.receiveSoon())
            assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())
            fixture.awaitState(VpnConnectionState.CONNECTING)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun connectedButtonStopsTheGenerationFromTheCurrentSnapshot() = runBlocking {
        val fixture = Fixture(initialState = SessionState.CONNECTED, generation = 17uL, configured = true)
        try {
            fixture.awaitState(VpnConnectionState.CONNECTED)
            fixture.viewModel.onConnectionButtonClicked("")

            assertEquals(17uL, fixture.sessionController.stoppedGenerations.receiveSoon())
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun acceptedStartCanBeStoppedBeforeItsWatchSnapshotArrives() = runBlocking {
        val fixture = Fixture(startGeneration = 29uL)
        try {
            fixture.viewModel.onConnectionButtonClicked("https://example.test/profile")
            fixture.sessionController.configuredUrls.receiveSoon()
            fixture.sessionController.startTargets.receiveSoon()
            fixture.awaitState(VpnConnectionState.CONNECTING)

            fixture.viewModel.onConnectionButtonClicked("")

            assertEquals(29uL, fixture.sessionController.stoppedGenerations.receiveSoon())
        } finally {
            fixture.close()
        }
    }

    @Test
    fun stoppingButtonDoesNotIssueAnotherCommand() = runBlocking {
        val fixture = Fixture(initialState = SessionState.STOPPING, generation = 31uL, configured = true)
        try {
            fixture.awaitState(VpnConnectionState.STOPPING)
            fixture.viewModel.onConnectionButtonClicked("")

            assertTrue(fixture.sessionController.configuredUrls.tryReceive().isFailure)
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
            assertTrue(fixture.sessionController.stoppedGenerations.tryReceive().isFailure)
        } finally {
            fixture.close()
        }
    }

    private suspend fun Fixture.awaitState(state: VpnConnectionState) {
        withTimeout(5_000) { viewModel.uiState.first { it.connectionState == state } }
    }

    private suspend fun <T> Channel<T>.receiveSoon(): T = withTimeout(5_000) { receive() }

    private class Fixture(
        initialState: SessionState = SessionState.IDLE,
        generation: ULong = 0uL,
        configured: Boolean = false,
        startGeneration: ULong = 11uL,
    ) {
        private val logPath: Path = FileSystem.SYSTEM_TEMPORARY_DIRECTORY /
            "dobby-view-model-${kotlin.random.Random.nextLong()}.jsonl"
        private val store = ViewModelStore()
        val sessionController = RecordingSessionController(
            initialSnapshot = testSnapshot(initialState, generation, configured),
            startGeneration = startGeneration,
        )
        val viewModel = MainViewModel(
            configsRepository = TestConfigsRepository(),
            permissionEventsChannel = PermissionEventsChannel(),
            sessionController = sessionController,
            logger = Logger(LogsRepository(logPath)),
        ).also { store.put("main", it) }

        fun close() {
            store.clear()
            FileSystem.SYSTEM.delete(logPath, mustExist = false)
        }
    }

    private class TestConfigsRepository : DobbyConfigsRepository {
        override fun getConnectionURL(): String = ""
        override fun setConnectionURL(connectionURL: String) = Unit
    }

    private class RecordingSessionController(
        initialSnapshot: SessionSnapshot,
        private val startGeneration: ULong,
    ) : SessionController {
        val configuredUrls = Channel<String>(Channel.UNLIMITED)
        val startTargets = Channel<SessionStartTarget>(Channel.UNLIMITED)
        val stoppedGenerations = Channel<ULong>(Channel.UNLIMITED)
        private val snapshots = MutableSharedFlow<SessionSnapshot>(replay = 1, extraBufferCapacity = 1)

        init {
            check(snapshots.tryEmit(initialSnapshot))
        }

        override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> {
            configuredUrls.send(rawConfig.decodeToString())
            return SessionControllerResult.Success(
                SessionConfiguration(
                    sessionId = "test-session",
                    sequence = 1uL,
                    digest = "test-digest",
                    sourceKind = SessionSourceKind.URL,
                    profiles = emptyList(),
                    warnings = emptyList(),
                ),
            )
        }

        override suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart> {
            startTargets.send(target)
            return SessionControllerResult.Success(
                SessionStart(sessionId = "test-session", generation = startGeneration, sequence = 2uL),
            )
        }

        override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> {
            stoppedGenerations.send(generation)
            return SessionControllerResult.Success(
                SessionStop(sessionId = "test-session", generation = generation, sequence = 3uL),
            )
        }

        override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
            SessionControllerResult.Success(testSnapshot(SessionState.IDLE, 0uL, false))

        override fun watch(): Flow<SessionSnapshot> = snapshots

        override suspend fun reset(): SessionControllerResult<SessionSnapshot> = snapshot()
    }

    private companion object {
        fun testSnapshot(
            state: SessionState,
            generation: ULong,
            configured: Boolean,
        ) = SessionSnapshot(
            sessionId = "test-session",
            sequence = 0uL,
            generation = generation,
            state = state,
            configured = configured,
            digest = "test-digest",
            sourceKind = SessionSourceKind.INLINE,
            profiles = emptyList(),
            warnings = emptyList(),
            activeProfile = null,
            lastFailure = null,
            cleanupComplete = true,
        )
    }
}
