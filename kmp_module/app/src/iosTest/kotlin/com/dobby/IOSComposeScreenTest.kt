package com.dobby

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertValueEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.test.waitUntilAtLeastOneExists
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.LocalViewModelStoreOwner
import com.dobby.feature.logging.Logger as AppLogger
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionChangeEvents
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionSourceKind
import com.dobby.feature.main.domain.SessionStart
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionStop
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.main.ui.AutomationSemantics
import com.dobby.feature.main.ui.ConnectionScreen
import com.dobby.navigation.App
import com.dobby.vpn.BuildConfig
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import okio.FileSystem
import okio.Path

@OptIn(ExperimentalTestApi::class)
class IOSComposeScreenTest {
    private val fixtures = mutableListOf<UiFixture>()

    @AfterTest
    fun removeTestLogs() {
        fixtures.forEach { it.close() }
        fixtures.clear()
    }

    @Test
    fun connection_screen_accepts_input_starts_session_and_renders_state() {
        val fixture = fixture()

        runComposeUiTest {
            setContent {
                ConnectionScreen(fixture.mainViewModel, fixture.logsViewModel)
            }

            onNodeWithTag(AutomationSemantics.CONNECTION_SCREEN).assertExists()
            onNodeWithTag(AutomationSemantics.CONNECTION_STATUS)
                .assertState("disconnected")
            val input = onNodeWithTag(AutomationSemantics.SUBSCRIPTION_INPUT)
            input.performTextInput("https://example.test/profile")
            input.assertTextContains("https://example.test/profile")
            onNodeWithTag(AutomationSemantics.CONNECTION_ACTION).performClick()

            waitUntil(timeoutMillis = 5_000) {
                fixture.sessionController.lastConfigured == "https://example.test/profile"
            }
            onNodeWithTag(AutomationSemantics.CONNECTION_STATUS)
                .assertState("connecting")
            onNodeWithText("Stop").assertExists()
        }
    }

    @Test
    fun connection_error_is_visible_and_written_to_the_log_view() {
        val fixture = fixture(
            configureResult = SessionControllerResult.Failure(
                message = "synthetic config rejected",
                code = SessionFailureCode.MALFORMED_CONFIG,
            ),
        )

        runComposeUiTest {
            setContent {
                ConnectionScreen(fixture.mainViewModel, fixture.logsViewModel)
            }

            onNodeWithTag(AutomationSemantics.SUBSCRIPTION_INPUT)
                .performTextInput("https://example.test/bad-profile")
            onNodeWithTag(AutomationSemantics.CONNECTION_ACTION).performClick()

            waitUntilAtLeastOneExists(
                hasTestTag(AutomationSemantics.FAILURE_STATUS),
                timeoutMillis = 5_000,
            )
            onNodeWithText("Connection error: MALFORMED_CONFIG").assertExists()
            waitUntilAtLeastOneExists(
                hasText(
                    "Session configuration rejected: failureCode=MALFORMED_CONFIG",
                    substring = true,
                ),
                timeoutMillis = 5_000,
            )
            onNodeWithTag(AutomationSemantics.LOGS).assertExists()
        }
    }

    @Test
    fun app_navigates_to_settings_and_shows_build_information() {
        val fixture = fixture()
        val viewModelStoreOwner = TestViewModelStoreOwner()
        val lifecycleOwner = TestLifecycleOwner()

        try {
            runComposeUiTest {
                setContent {
                    CompositionLocalProvider(
                        LocalViewModelStoreOwner provides viewModelStoreOwner,
                        LocalLifecycleOwner provides lifecycleOwner,
                    ) {
                        App(fixture.appDependencies)
                    }
                }

                onNodeWithTag(AutomationSemantics.CONNECTION_SCREEN).assertExists()
                val subscriptionUrl = "https://example.test/persisted-profile"
                val subscriptionInput = onNodeWithTag(AutomationSemantics.SUBSCRIPTION_INPUT)
                subscriptionInput.performTextInput(subscriptionUrl)
                subscriptionInput.assertTextContains(subscriptionUrl)
                onNodeWithText("Settings").performClick()
                onNodeWithTag(AutomationSemantics.SETTINGS_SCREEN).assertExists()
                onNodeWithText(BuildConfig.VERSION_NAME).assertExists()
                onNodeWithText(BuildConfig.PROJECT_REPOSITORY_COMMIT).assertExists()
                onNodeWithTag(AutomationSemantics.CONNECTION_NAV).performClick()
                onNodeWithTag(AutomationSemantics.SUBSCRIPTION_INPUT)
                    .assertTextContains(subscriptionUrl)
            }
        } finally {
            viewModelStoreOwner.viewModelStore.clear()
        }
    }

    private fun fixture(
        configureResult: SessionControllerResult<SessionConfiguration> =
            SessionControllerResult.Success(
                SessionConfiguration(
                    sessionId = "test-session",
                    sequence = 1uL,
                    digest = "test-digest",
                    sourceKind = SessionSourceKind.URL,
                    profiles = emptyList(),
                    warnings = emptyList(),
                ),
            ),
    ): UiFixture = UiFixture(configureResult).also(fixtures::add)
}

private class UiFixture(
    configureResult: SessionControllerResult<SessionConfiguration>,
) {
    private val logPath: Path =
        FileSystem.SYSTEM_TEMPORARY_DIRECTORY / "dobby-ios-compose-ui-${kotlin.random.Random.nextLong()}.jsonl"
    private val logsRepository = LogsRepository(logPath)
    val sessionController = FakeSessionController(configureResult)
    private val configsRepository = FakeConfigsRepository()
    private val sessionChangeEvents = SessionChangeEvents()
    private val permissionEventsChannel = PermissionEventsChannel()
    private val appLogger = AppLogger(logsRepository)
    val appDependencies: AppDependencies by lazy {
        AppDependencies(
            sessionChangeEvents = sessionChangeEvents,
            permissionEventsChannel = permissionEventsChannel,
            sessionController = sessionController,
            logger = appLogger,
            configsRepository = configsRepository,
            logsRepository = logsRepository,
            copyLogsInteractor = NoOpCopyLogsInteractor,
        )
    }
    val mainViewModel: MainViewModel by lazy {
        appDependencies.createMainViewModel()
    }
    val logsViewModel: LogsViewModel by lazy {
        appDependencies.createLogsViewModel()
    }

    fun close() {
        FileSystem.SYSTEM.delete(logPath, mustExist = false)
    }
}

private class FakeConfigsRepository : DobbyConfigsRepository {
    private var connectionUrl = ""

    override fun getConnectionURL(): String = connectionUrl

    override fun setConnectionURL(connectionURL: String) {
        connectionUrl = connectionURL
    }
}

private class FakeSessionController(
    private val configureResult: SessionControllerResult<SessionConfiguration>,
) : SessionController {
    var lastConfigured: String? = null
        private set

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> {
        lastConfigured = rawConfig.decodeToString()
        return configureResult
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart> =
        SessionControllerResult.Success(SessionStart(sessionId = "test-session", generation = 1uL, sequence = 2uL))

    override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> =
        SessionControllerResult.Success(SessionStop(sessionId = "test-session", generation = generation, sequence = 3uL))

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
        SessionControllerResult.Success(
            SessionSnapshot(
                sessionId = "test-session",
                sequence = 1uL,
                generation = 0uL,
                state = SessionState.IDLE,
                configured = false,
                digest = "",
                sourceKind = SessionSourceKind.INLINE,
                profiles = emptyList(),
                warnings = emptyList(),
                activeProfile = null,
                lastFailure = null,
                cleanupComplete = true,
            ),
        )

    override fun watch(): Flow<SessionSnapshot> = flow {
        awaitCancellation()
    }

    override suspend fun reset(): SessionControllerResult<SessionSnapshot> = snapshot()
}

private object NoOpCopyLogsInteractor : CopyLogsInteractor {
    override fun copy(logs: List<String>) = Unit
}

private class TestViewModelStoreOwner : ViewModelStoreOwner {
    override val viewModelStore = ViewModelStore()
}

private class TestLifecycleOwner : LifecycleOwner {
    override val lifecycle = LifecycleRegistry(this).apply {
        currentState = Lifecycle.State.RESUMED
    }
}

private fun SemanticsNodeInteraction.assertState(state: String): SemanticsNodeInteraction =
    assertValueEquals(state)
