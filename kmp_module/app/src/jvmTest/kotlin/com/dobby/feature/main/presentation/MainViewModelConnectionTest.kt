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
import com.dobby.feature.main.domain.SessionFailure
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionProtocol
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionSourceKind
import com.dobby.feature.main.domain.SessionStart
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.domain.SessionStop
import com.dobby.feature.main.domain.SessionWarning
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope
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
import kotlin.test.assertFalse
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
    fun duplicateConnectedSnapshotDoesNotClearStopCommandFailure() = runBlocking {
        val failureText = "The current connection could not be stopped"
        val fixture = Fixture(
            initialState = SessionState.CONNECTED,
            generation = 17uL,
            configured = true,
            stopHandler = {
                SessionControllerResult.Failure(failureText, SessionFailureCode.RUNTIME_FAILED)
            },
        )
        try {
            fixture.awaitState(VpnConnectionState.CONNECTED)
            fixture.viewModel.onConnectionButtonClicked("")
            assertEquals(17uL, fixture.sessionController.stoppedGenerations.receiveSoon())
            withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.lastFailureMessage == failureText }
            }

            val marker = SessionWarning("RETURNED", "Connection remained active after rejected stop")
            fixture.sessionController.publish(
                testSnapshot(SessionState.CONNECTED, 17uL, configured = true).copy(
                    sequence = 1uL,
                    warnings = listOf(marker),
                ),
            )
            val returnedToConnected = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.warnings == listOf(marker) }
            }
            assertEquals(VpnConnectionState.CONNECTED, returnedToConnected.connectionState)
            assertEquals(failureText, returnedToConnected.lastFailureMessage)
            assertEquals(SessionFailureCode.RUNTIME_FAILED, returnedToConnected.lastFailureCode)

            val duplicateMarker = SessionWarning("DUPLICATE", "Duplicate connected snapshot")
            fixture.sessionController.publish(
                testSnapshot(SessionState.CONNECTED, 17uL, configured = true).copy(
                    sequence = 2uL,
                    warnings = listOf(duplicateMarker),
                ),
            )
            val duplicate = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.warnings == listOf(duplicateMarker) }
            }
            assertEquals(failureText, duplicate.lastFailureMessage)
            assertEquals(SessionFailureCode.RUNTIME_FAILED, duplicate.lastFailureCode)
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
    fun concurrentStartsIssueOnlyOneControllerCall() = runBlocking {
        val response = CompletableDeferred<SessionControllerResult<SessionStart>>()
        val fixture = Fixture(configured = true, startHandler = { response.await() })
        try {
            val first = async { fixture.viewModel.startVpnService() }
            assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())

            val second = async(start = CoroutineStart.UNDISPATCHED) { fixture.viewModel.startVpnService() }
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)

            response.complete(
                SessionControllerResult.Success(
                    SessionStart(sessionId = "test-session", generation = 37uL, sequence = 2uL),
                ),
            )
            assertTrue(first.await())
            assertFalse(second.await())
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun rejectedStartCanBeRetried() = runBlocking {
        val responses = Channel<SessionControllerResult<SessionStart>>(Channel.UNLIMITED)
        val fixture = Fixture(configured = true, startHandler = { responses.receive() })
        try {
            val first = async { fixture.viewModel.startVpnService() }
            assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())
            responses.send(
                SessionControllerResult.Failure(
                    message = "The selected profile could not start",
                    code = SessionFailureCode.RUNTIME_FAILED,
                ),
            )
            assertFalse(first.await())
            val failedUi = fixture.viewModel.uiState.value
            assertEquals(SessionFailureCode.RUNTIME_FAILED, failedUi.lastFailureCode)
            assertEquals("The selected profile could not start", failedUi.lastFailureMessage)

            val retry = async { fixture.viewModel.startVpnService() }
            assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())
            responses.send(
                SessionControllerResult.Success(
                    SessionStart(sessionId = "test-session", generation = 41uL, sequence = 3uL),
                ),
            )
            assertTrue(retry.await())
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun thrownStartDoesNotLeaveAStickyInFlightState() = runBlocking {
        supervisorScope {
            val firstStartEntered = CompletableDeferred<Unit>()
            val fixture = Fixture(
                configured = true,
                startHandler = { call ->
                    if (call == 1) {
                        firstStartEntered.complete(Unit)
                        throw IllegalStateException("synthetic controller failure")
                    }
                    SessionControllerResult.Success(
                        SessionStart(sessionId = "test-session", generation = 43uL, sequence = 3uL),
                    )
                },
            )
            try {
                val failed = async { fixture.viewModel.startVpnService() }
                assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())
                firstStartEntered.await()
                try {
                    failed.await()
                    error("expected the synthetic controller failure")
                } catch (expected: IllegalStateException) {
                    assertEquals("synthetic controller failure", expected.message)
                }

                assertTrue(fixture.viewModel.startVpnService())
                assertEquals(SessionStartTarget.AutoSelect, fixture.sessionController.startTargets.receiveSoon())
                assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
            } finally {
                fixture.close()
            }
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

    @Test
    fun recoveryIdleSnapshotRemainsReconnectingAndCanBeStopped() = runBlocking {
        val fixture = Fixture(configured = true)
        try {
            fixture.sessionController.publish(
                testSnapshot(
                    SessionState.IDLE,
                    generation = 73uL,
                    configured = true,
                    recovering = true,
                ).copy(sequence = 1uL),
            )
            fixture.awaitState(VpnConnectionState.RECONNECTING)

            fixture.viewModel.onConnectionButtonClicked("")

            assertEquals(73uL, fixture.sessionController.stoppedGenerations.receiveSoon())
            assertTrue(fixture.sessionController.startTargets.tryReceive().isFailure)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun terminalFailureExitsRecoveryPresentation() = runBlocking {
        val fixture = Fixture(configured = true)
        try {
            fixture.sessionController.publish(
                testSnapshot(SessionState.IDLE, 81uL, configured = true, recovering = true)
                    .copy(sequence = 1uL),
            )
            fixture.awaitState(VpnConnectionState.RECONNECTING)

            fixture.sessionController.publish(
                testSnapshot(SessionState.FAILED, 81uL, configured = true).copy(
                    sequence = 2uL,
                    lastFailure = SessionFailure(SessionFailureCode.RUNTIME_FAILED, "Retry limit reached"),
                ),
            )
            val failed = withTimeout(5_000) {
                fixture.viewModel.uiState.first {
                    it.connectionState == VpnConnectionState.DISCONNECTED &&
                        it.lastFailureCode == SessionFailureCode.RUNTIME_FAILED
                }
            }
            assertEquals("Retry limit reached", failed.lastFailureMessage)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun snapshotDetailsReachUiAndInactiveProtocolIsCleared() = runBlocking {
        val fixture = Fixture(configured = true)
        try {
            fixture.sessionController.publish(
                testSnapshot(SessionState.FAILED, 75uL, configured = true).copy(
                    sequence = 1uL,
                    lastFailure = SessionFailure(
                        SessionFailureCode.PROBE_FAILED,
                        "The primary profile did not respond",
                    ),
                ),
            )
            val failed = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.lastFailureCode == SessionFailureCode.PROBE_FAILED }
            }
            assertEquals("The primary profile did not respond", failed.lastFailureMessage)

            val profile = SessionProfile(1, SessionProtocol.XRAY, "backup")
            val warning = SessionWarning("DEGRADED", "Using backup profile")
            fixture.sessionController.publish(
                testSnapshot(SessionState.CONNECTED, 75uL, configured = true).copy(
                    sequence = 2uL,
                    activeProfile = profile,
                    warnings = listOf(warning),
                ),
            )
            val connected = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.activeProfile == profile }
            }
            assertEquals(listOf(warning), connected.warnings)
            assertEquals(null, connected.lastFailureMessage)
            assertEquals(null, connected.lastFailureCode)

            fixture.sessionController.publish(
                testSnapshot(SessionState.STOPPING, 75uL, configured = true).copy(sequence = 3uL),
            )
            val stopping = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.connectionState == VpnConnectionState.STOPPING }
            }
            assertEquals(null, stopping.activeProfile)
        } finally {
            fixture.close()
        }
    }

    @Test
    fun commandFailureSurvivesIdleSnapshotAndClearsWhenConnected() = runBlocking {
        val failureText = "The selected profile could not start"
        val fixture = Fixture(
            configured = true,
            startHandler = {
                SessionControllerResult.Failure(failureText, SessionFailureCode.RUNTIME_FAILED)
            },
        )
        try {
            assertTrue(fixture.viewModel.setConfig("https://example.test/profile"))
            assertFalse(fixture.viewModel.startVpnService())
            assertEquals(failureText, fixture.viewModel.uiState.value.lastFailureMessage)

            val marker = SessionWarning("READY", "Snapshot received")
            fixture.sessionController.publish(
                testSnapshot(SessionState.IDLE, 0uL, configured = true).copy(
                    sequence = 2uL,
                    warnings = listOf(marker),
                ),
            )
            val idle = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.warnings == listOf(marker) }
            }
            assertEquals(failureText, idle.lastFailureMessage)

            val profile = SessionProfile(0, SessionProtocol.OUTLINE, "primary")
            fixture.sessionController.publish(
                testSnapshot(SessionState.CONNECTED, 0uL, configured = true).copy(
                    sequence = 3uL,
                    activeProfile = profile,
                    warnings = listOf(marker),
                ),
            )
            val connected = withTimeout(5_000) {
                fixture.viewModel.uiState.first { it.connectionState == VpnConnectionState.CONNECTED }
            }
            assertEquals(null, connected.lastFailureCode)
            assertEquals(null, connected.lastFailureMessage)
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
        startHandler: suspend (Int) -> SessionControllerResult<SessionStart> = { call ->
            SessionControllerResult.Success(
                SessionStart("test-session", startGeneration, sequence = (call + 1).toULong()),
            )
        },
        stopHandler: suspend (ULong) -> SessionControllerResult<SessionStop> = { generation ->
            SessionControllerResult.Success(
                SessionStop(sessionId = "test-session", generation = generation, sequence = 3uL),
            )
        },
    ) {
        private val logPath: Path = FileSystem.SYSTEM_TEMPORARY_DIRECTORY /
            "dobby-view-model-${kotlin.random.Random.nextLong()}.jsonl"
        private val store = ViewModelStore()
        val sessionController = RecordingSessionController(
            initialSnapshot = testSnapshot(initialState, generation, configured),
            startHandler = startHandler,
            stopHandler = stopHandler,
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
        private val startHandler: suspend (Int) -> SessionControllerResult<SessionStart>,
        private val stopHandler: suspend (ULong) -> SessionControllerResult<SessionStop>,
    ) : SessionController {
        val configuredUrls = Channel<String>(Channel.UNLIMITED)
        val startTargets = Channel<SessionStartTarget>(Channel.UNLIMITED)
        val stoppedGenerations = Channel<ULong>(Channel.UNLIMITED)
        private val snapshots = MutableSharedFlow<SessionSnapshot>(replay = 1, extraBufferCapacity = 1)
        private var startCallCount = 0

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
            return startHandler(++startCallCount)
        }

        override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> {
            stoppedGenerations.send(generation)
            return stopHandler(generation)
        }

        override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
            SessionControllerResult.Success(testSnapshot(SessionState.IDLE, 0uL, false))

        override fun watch(): Flow<SessionSnapshot> = snapshots

        fun publish(snapshot: SessionSnapshot) {
            check(snapshots.tryEmit(snapshot))
        }

        override suspend fun reset(): SessionControllerResult<SessionSnapshot> = snapshot()
    }

    private companion object {
        fun testSnapshot(
            state: SessionState,
            generation: ULong,
            configured: Boolean,
            recovering: Boolean = false,
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
            recovering = recovering,
        )
    }
}
