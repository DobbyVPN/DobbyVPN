package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManager
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.*
import com.dobby.feature.main.domain.config.ConnectionProfileManager
import com.dobby.feature.main.domain.config.TomlConfigApplier
import com.dobby.feature.main.ui.MainUiState
import com.dobby.vpn.BuildConfig
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.time.Duration.Companion.seconds
import kotlin.time.TimeSource

val httpClient = HttpClient()

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val loggerManager: LoggerManager,
    private val logger: Logger,
    private val logsRepository: LogsRepository,
    private val healthCheckManager: HealthCheckManager,
    private val configsProcessor: ConfigsProcessor,
) : ViewModel() {
    private var connectionDetectorJob: Job? = null
    private var stopRequested = false
    private var failoverJob: Job? = null
    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    private val tomlConfigApplier = TomlConfigApplier(
        mainRepo = configsRepository,
        logger = logger
    )
    private val profileManager = ConnectionProfileManager(
        repo = configsRepository,
        logger = logger
    )

    init {
        // Load initial UI state from configs repository
        viewModelScope.launch {
            _uiState.emit(
                MainUiState(
                    connectionURL = configsRepository.getConnectionURL(),
                )
            )
        }
        // Load initial UI state from go backend
        viewModelScope.launch {
            val connectionState = healthCheckManager.getConnectionState()
            logger.log("Init connection state: $connectionState")
            val newState = _uiState.value.copy(connectionState = connectionState)
            _uiState.emit(newState)
            connectionStateRepository.updateStatus(connectionState)
            if (connectionState != VpnConnectionState.DISCONNECTED) {
                startConnectionStateDetector()
            }
        }
        // Launch utility threads
        viewModelScope.launch {
            permissionEventsChannel
                .permissionsGrantedEvents
                .collect { isPermissionGranted -> startVpn(isPermissionGranted) }
        }
    }

    fun onConnectionUrlChanged(connectionUrl: String) {
        _uiState.value = _uiState.value.copy(connectionURL = connectionUrl)

        viewModelScope.launch(Dispatchers.Default) {
            configsRepository.setConnectionURL(connectionUrl)
        }
    }

    fun onConnectionButtonClicked(
        connectionUrl: String
    ) {
        logger.log("The connection button was clicked with URL: ${maskStr(connectionUrl)}")

        if (!configsRepository.couldStart()) {
            logger.log("We couldn't do this operation, configsRepository.couldStart() returned FALSE")
            return
        }

        viewModelScope.launch(Dispatchers.Default) {
            logger.log("Proceeding with setConfig for the provided URL...")
            when (connectionStateRepository.statusFlow.value) {
                VpnConnectionState.DISCONNECTED -> {
                    try {
                        logger.log("We get config by ${maskStr(connectionUrl)}")
                        val ok = setConfig(connectionUrl)
                        if (!ok) {
                            logger.log("Config is invalid or failed to apply → abort start (no HC/VPN)")
                            connectionStateRepository.updateStatus(VpnConnectionState.DISCONNECTED)
                            return@launch
                        }
                    } catch (e: Exception) {
                        logger.log("Error during setConfig: ${e.message}")
                        return@launch
                    } finally {
                        logger.log("Finish setConfig()")
                    }

                    logger.log("VPN is currently disconnected")
                    if (isPermissionCheckNeeded) {
                        logger.log("Permission check required, triggering permission dialog")
                        permissionEventsChannel.checkPermissions()
                    } else {
                        logger.log("Permission check is NOT required, starting VPN service directly")
                        startVpnService()
                    }
                }

                VpnConnectionState.CONNECTING, VpnConnectionState.CONNECTED -> {
                    logger.log("Stopping VPN service due to active connection")
                    stopVpnService()
                }
            }
        }
    }

    suspend fun setConfig(connectionUrl: String): Boolean {
        logger.log("Start setConfig() with connectionUrl: ${maskStr(connectionUrl)}")

        configsRepository.setConnectionURL(connectionUrl)
        logger.log("Connection URL saved to repository")

        val connectionConfig = getConfigByURL(connectionUrl)
        logger.log("Fetched connection config, size = ${connectionConfig.length}")

        configsRepository.setConnectionConfig(connectionConfig)
        logger.log("Connection config saved to repository")

        val applied = runCatching { tomlConfigApplier.apply(connectionConfig) }
            .onFailure { e ->
                logger.log("Error during parsing TOML: ${e.message}")
                configsRepository.clearVpnConfig()
            }
            .getOrDefault(false)

        if (!applied) {
            logger.log("Config not applied (invalid/unsupported) → will not start VPN/HC")
            return false
        }

        saveTelemetryAttributes()
        return true
    }

    private suspend fun getConfigByURL(connectionUrl: String): String {
        logger.log("getConfigByURL() called with: ${maskStr(connectionUrl)}")

        return if (connectionUrl.startsWith("http://") || connectionUrl.startsWith("https://")) {
            try {
                logger.log("Fetching config from remote URL...")
                httpClient.get(connectionUrl) {
                    headers {
                        append("User-Agent", "DobbyVPN v${BuildConfig.VERSION_NAME}")
                    }
                }.bodyAsText()
            } catch (e: Exception) {
                val errorMsg = "Can't get config by url. Error: ${e.message}"
                logger.log(errorMsg)
                throw RuntimeException(errorMsg)
            }
        } else {
            logger.log("Provided config is inline (not a URL), returning raw string")
            connectionUrl
        }
    }

    private suspend fun startVpn(isPermissionGranted: Boolean) {
        if (isPermissionGranted) {
            logger.log("Permission granted — starting VPN service")
            startVpnService()
        } else {
            logger.log("Permission denied — skipping VPN start")
            connectionStateRepository.tryUpdateStatus(VpnConnectionState.DISCONNECTED)
            // TODO: show Toast/snackbar
        }
    }

    /**
     * Stops connection state detector
     */
    fun stopConnectionStateDetector() {
        connectionDetectorJob?.cancel()
        connectionDetectorJob = null
    }

    /**
     * Starts connection state detector thread,
     * that runs only when health check is being run,
     * and repeatedly loads VPN connection state
     */
    fun startConnectionStateDetector() {
        if (connectionDetectorJob?.isActive == true) {
            logger.log("Connection state detector: already running")
            return
        }

        connectionDetectorJob = viewModelScope.launch {
            logger.log("Connection state detector: start")
            val detectorStartedAt = TimeSource.Monotonic.markNow()
            var connectedSeen = false
            while (isActive) {
                val connectionState = healthCheckManager.getConnectionState()
                val elapsedMs = detectorStartedAt.elapsedNow().inWholeMilliseconds
                if (
                    shouldFailoverFromHealthState(
                        connectionState = connectionState,
                        connectedSeen = connectedSeen,
                        elapsedMs = elapsedMs
                    ) &&
                    profileManager.hasMultipleProfiles() &&
                    !stopRequested
                ) {
                    logger.log("[Failover] Health check reported failed state=$connectionState for active profile")
                    requestFailover("health check state=$connectionState")
                    return@launch
                }
                if (
                    elapsedMs >= TEST_PROFILE_SWITCH_INTERVAL_MS &&
                    profileManager.hasMultipleProfiles() &&
                    !stopRequested
                ) {
                    logger.log("[Failover] Test profile switch interval reached")
                    requestFailover("test interval reached")
                    return@launch
                }
                if (connectionState == VpnConnectionState.CONNECTED) {
                    connectedSeen = true
                }
                val newState = _uiState.value.copy(connectionState = connectionState)
                _uiState.emit(newState)
                connectionStateRepository.updateStatus(connectionState)
                delay(1.seconds)
            }
        }
    }

    private fun shouldFailoverFromHealthState(
        connectionState: VpnConnectionState,
        connectedSeen: Boolean,
        elapsedMs: Long,
    ): Boolean {
        if (connectionState == VpnConnectionState.CONNECTED) return false
        if (connectionState == VpnConnectionState.DISCONNECTED) return true

        // Native healthcheck uses CONNECTING both while checks are warming up and when a later
        // check fails. After CONNECTED was observed, CONNECTING means the active profile lost HC.
        if (connectedSeen) return true

        // Give a freshly started profile enough time for the first native healthcheck rounds.
        return elapsedMs >= HEALTH_CHECK_START_GRACE_MS
    }

    suspend fun startVpnService(): Boolean {
        stopRequested = false
        return startVpnServiceLoop(initialReason = "start requested")
    }

    private suspend fun startVpnServiceLoop(initialReason: String): Boolean {
        var reason = initialReason
        while (!stopRequested) {
            logger.log("[Failover] Starting active profile, reason=$reason")
            val connected = startActiveVpnServiceOnce()
            if (connected) return true

            if (!profileManager.hasMultipleProfiles()) {
                logger.log("[Failover] Active profile failed and no fallback profile exists")
                stopVpnRuntime(resetUiState = true)
                return false
            }

            stopVpnRuntime(resetUiState = false)
            val switched = profileManager.switchToNext("start failed")
            if (!switched) {
                logger.log("[Failover] Could not switch after start failure")
                stopVpnRuntime(resetUiState = true)
                return false
            }
            saveTelemetryAttributes()
            reason = "retry after start failure"
            delay(1.seconds)
        }

        logger.log("[Failover] Start loop stopped because stopRequested=true")
        return false
    }

    private suspend fun startActiveVpnServiceOnce(): Boolean {
        logger.log("Starting VPN service...")

        connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
        _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

        logger.log("Init health check")
        healthCheckManager.initHealthCheck()

        logger.log("Init logger")
        logsRepository.cleanupOldLogs()
        loggerManager.initLogger()

        logger.log("Start tunnel service")
        connectionStateRepository.serviceStartedFlow.prepare()
        vpnManager.start()

        logger.log("Await service started result")
        val connected = connectionStateRepository.serviceStartedFlow.awaitResult(SERVICE_START_TIMEOUT_MS)
        logger.log("Got service started result: $connected")

        if (connected) {
            logger.log("Start health check")
            healthCheckManager.startHealthCheck()
            logger.log("Start connection detector")
            startConnectionStateDetector()
            return true
        } else {
            return false
        }
    }

    fun stopVpnService() {
        stopRequested = true
        failoverJob?.cancel()
        failoverJob = null
        stopVpnRuntime(resetUiState = true)
    }

    private fun stopVpnRuntime(resetUiState: Boolean) {
        logger.log("Stopping VPN service...")
        logger.log("Stop tunnel service")
        vpnManager.stop()
        logger.log("Stop health check")
        healthCheckManager.stopHealthCheck()
        logger.log("Stop connection detector")
        stopConnectionStateDetector()
        if (resetUiState) {
            val disconnectedState = _uiState.value.copy(connectionState = VpnConnectionState.DISCONNECTED)
            _uiState.tryEmit(disconnectedState)
            connectionStateRepository.tryUpdateStatus(VpnConnectionState.DISCONNECTED)
        }
        logger.log("VPN service stop requested, resetUiState=$resetUiState")
    }

    private fun requestFailover(reason: String) {
        if (failoverJob?.isActive == true) {
            logger.log("[Failover] Request ignored, failover already running reason=$reason")
            return
        }

        failoverJob = viewModelScope.launch(Dispatchers.Default) {
            stopConnectionStateDetector()
            healthCheckManager.stopHealthCheck()
            val switched = profileManager.switchToNext(reason)
            if (!switched) {
                logger.log("[Failover] Could not switch profile after $reason")
                stopVpnRuntime(resetUiState = true)
                return@launch
            }
            saveTelemetryAttributes()
            connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
            _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

            logger.log("[Failover] Restarting VPN after $reason")
            stopVpnRuntime(resetUiState = false)
            startVpnServiceLoop(initialReason = "failover after $reason")
        }
    }

    private fun saveTelemetryAttributes() {
        val attributes = configsProcessor.buildConfigAttributesJson()
        configsRepository.setTelemetryAttributes(attributes)
        logger.log("Configs attributes saved to repository")
    }

    private companion object {
        const val HEALTH_CHECK_START_GRACE_MS = 15_000L
        const val SERVICE_START_TIMEOUT_MS = 90_000L
        const val TEST_PROFILE_SWITCH_INTERVAL_MS = 120_000L
    }
    //endregion
}
