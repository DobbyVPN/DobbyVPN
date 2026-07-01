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

private val configHttpClient = HttpClient()

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
    private var backendRuntimeInitialized = false
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
                configHttpClient.get(connectionUrl) {
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
                    !stopRequested
                ) {
                    logger.log("[ProtocolSelection] Health check reported failed state=$connectionState for active profile")
                    requestProtocolRescan("health check state=$connectionState")
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

        // Native healthcheck uses CONNECTING both while checks are warming up and when a later
        // check fails. Right after start/restart it can also briefly report DISCONNECTED while
        // the native healthcheck state is being reset. After CONNECTED was observed, any non-
        // connected state means the active profile lost HC.
        if (connectedSeen) return true

        // Give a freshly started profile enough time for the first native healthcheck rounds.
        return elapsedMs >= HEALTH_CHECK_START_GRACE_MS
    }

    suspend fun startVpnService(): Boolean {
        stopRequested = false
        failoverJob?.cancel()
        failoverJob = null
        return startVpnServiceLoop(initialReason = "start requested")
    }

    private suspend fun startVpnServiceLoop(initialReason: String): Boolean {
        var reason = initialReason
        while (!stopRequested) {
            val bestProfile = scanProfilesForBest(reason)
            if (bestProfile == null) {
                if (stopRequested) {
                    logger.log("[ProtocolSelection] Profile scan stopped because stopRequested=true")
                    return false
                }

                logger.log("[ProtocolSelection] No working profile found during scan reason=$reason")
                stopVpnRuntime(resetUiState = true)
                return false
            }

            logger.log(
                "[ProtocolSelection] Starting selected profile " +
                    "${bestProfile.profile.label(bestProfile.index, bestProfile.total)} " +
                    "averageLatencyMs=${bestProfile.averageLatencyMs} reason=$reason"
            )
            val connected = startSelectedProfile(bestProfile, reason)
            if (connected) {
                logger.log(
                    "[ProtocolSelection] Selected profile is active " +
                        "index=${bestProfile.index} averageLatencyMs=${bestProfile.averageLatencyMs}"
                )
                return true
            }

            logger.log("[ProtocolSelection] Selected profile failed after scan; repeating full scan")
            stopVpnRuntime(resetUiState = false)
            reason = "selected profile failed after scan"
            delay(1.seconds)
        }

        logger.log("[ProtocolSelection] Start loop stopped because stopRequested=true")
        return false
    }

    private suspend fun scanProfilesForBest(reason: String): ProfileProbeResult? {
        val profiles = profileManager.getProfiles()
        if (profiles.isEmpty()) {
            logger.log("[ProtocolSelection] No saved profiles to scan reason=$reason")
            return null
        }

        logger.log("[ProtocolSelection] Start profile scan reason=$reason profiles=${profiles.size}")

        val results = mutableListOf<ProfileProbeResult>()
        for ((index, profile) in profiles.withIndex()) {
            if (stopRequested) {
                logger.log("[ProtocolSelection] Stop requested during profile scan")
                return null
            }

            val result = probeProfile(
                index = index,
                total = profiles.size,
                profile = profile,
                reason = reason
            )
            if (result != null) {
                results += result
            }
        }

        if (results.isEmpty()) {
            logger.log("[ProtocolSelection] Profile scan finished without working profiles")
            return null
        }

        logger.log(
            "[ProtocolSelection] Profile scan results " +
                "working=${results.size}/${profiles.size} " +
                "latencies=${results.joinToString { "index=${it.index}:avgMs=${it.averageLatencyMs}" }}"
        )

        return results.minWithOrNull(
            compareBy<ProfileProbeResult> { it.averageLatencyMs }
                .thenBy { it.index }
        )
    }

    private suspend fun probeProfile(
        index: Int,
        total: Int,
        profile: ConnectionProfile,
        reason: String,
    ): ProfileProbeResult? {
        val label = profile.label(index, total)
        logger.log("[ProtocolSelection] Probe start $label reason=$reason")

        try {
            val applied = profileManager.applyProfile(index, "protocol selection probe")
            if (!applied) {
                logger.log("[ProtocolSelection] Probe failed $label: profile config could not be applied")
                return null
            }
            saveTelemetryAttributes()

            connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
            _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

            val started = startActiveVpnServiceOnce(startDetector = false)
            if (!started) {
                logger.log("[ProtocolSelection] Probe failed $label: VPN tunnel did not start")
                return null
            }

            val averageLatencyMs = measureHealthCheckAverageLatencyMillis(
                maxAttempts = PROFILE_TUNNEL_PROBE_ATTEMPTS,
                retryDelayMs = PROFILE_TUNNEL_PROBE_RETRY_DELAY_MS,
                preferLastSuccessfulAttempt = true,
            )
            if (averageLatencyMs == null) {
                logger.log("[ProtocolSelection] Probe failed $label: HC latency probe failed")
                return null
            }

            logger.log("[ProtocolSelection] Probe OK $label averageLatencyMs=$averageLatencyMs")
            return ProfileProbeResult(
                index = index,
                total = total,
                profile = profile,
                averageLatencyMs = averageLatencyMs
            )
        } finally {
            logger.log("[ProtocolSelection] Probe finished $label; keeping VPN interface available for protocol reuse")
        }
    }

    private suspend fun startSelectedProfile(profile: ProfileProbeResult, reason: String): Boolean {
        val applied = profileManager.applyProfile(
            index = profile.index,
            reason = "selected best latencyMs=${profile.averageLatencyMs} after $reason"
        )
        if (!applied) {
            logger.log("[ProtocolSelection] Selected profile could not be applied index=${profile.index}")
            return false
        }

        saveTelemetryAttributes()
        connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
        _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

        val started = startActiveVpnServiceOnce(startDetector = true)
        if (!started) return false

        return (measureHealthCheckAverageLatencyMillis(maxAttempts = SELECTED_TUNNEL_PROBE_ATTEMPTS) != null).also { healthy ->
            if (!healthy) {
                logger.log("[ProtocolSelection] Selected profile failed active latency confirmation index=${profile.index}")
            }
        }
    }

    private suspend fun startActiveVpnServiceOnce(startDetector: Boolean): Boolean {
        logger.log("Starting VPN service...")

        connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
        _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

        logger.log("Init health check")
        healthCheckManager.initHealthCheck()

        if (!backendRuntimeInitialized) {
            logger.log("Init logger")
            logsRepository.cleanupOldLogs()
            loggerManager.initLogger()
            backendRuntimeInitialized = true
        } else {
            logger.log("Backend logger already initialized; skipping runtime logger init")
        }

        logger.log("Start tunnel service")
        connectionStateRepository.serviceStartedFlow.prepare()
        vpnManager.start()

        logger.log("Await service started result")
        val connected = connectionStateRepository.serviceStartedFlow.awaitResult(SERVICE_START_TIMEOUT_MS)
        logger.log("Got service started result: $connected")

        if (connected) {
            if (startDetector) {
                logger.log("Start health check")
                healthCheckManager.startHealthCheck()
                logger.log("Start connection detector")
                startConnectionStateDetector()
            } else {
                logger.log("Health check is not started for protocol probe")
                logger.log("Connection detector is not started for protocol probe")
            }
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
        backendRuntimeInitialized = false
        if (resetUiState) {
            val disconnectedState = _uiState.value.copy(connectionState = VpnConnectionState.DISCONNECTED)
            _uiState.tryEmit(disconnectedState)
            connectionStateRepository.tryUpdateStatus(VpnConnectionState.DISCONNECTED)
        }
        logger.log("VPN service stop requested, resetUiState=$resetUiState")
    }

    private fun requestProtocolRescan(reason: String) {
        if (failoverJob?.isActive == true) {
            logger.log("[ProtocolSelection] Rescan request ignored, rescan already running reason=$reason")
            return
        }

        failoverJob = viewModelScope.launch(Dispatchers.Default) {
            stopConnectionStateDetector()
            healthCheckManager.stopHealthCheck()
            logger.log("[ProtocolSelection] Restarting protocol selection after $reason")

            connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
            _uiState.emit(_uiState.value.copy(connectionState = VpnConnectionState.CONNECTING))

            startVpnServiceLoop(initialReason = "rescan after $reason")
        }
    }

    private suspend fun measureHealthCheckAverageLatencyMillis(
        maxAttempts: Int = DEFAULT_TUNNEL_PROBE_ATTEMPTS,
        retryDelayMs: Long = DEFAULT_TUNNEL_PROBE_RETRY_DELAY_MS,
        preferLastSuccessfulAttempt: Boolean = false,
    ): Long? {
        if (stopRequested) {
            logger.log("[ProtocolSelection] Tunnel probe stopped because stopRequested=true")
            return null
        }

        var lastSuccessfulLatencyMs: Long? = null
        val attempts = maxAttempts.coerceAtLeast(1)
        repeat(attempts) { attempt ->
            val attemptNumber = attempt + 1
            val averageLatencyMs = healthCheckManager.measureTunnelProbeAverageLatencyMillis()
            if (averageLatencyMs >= 0) {
                logger.log("[ProtocolSelection] Tunnel probe OK attempt=$attemptNumber/$maxAttempts averageLatencyMs=$averageLatencyMs")
                lastSuccessfulLatencyMs = averageLatencyMs
                if (!preferLastSuccessfulAttempt || attemptNumber == attempts) {
                    return averageLatencyMs
                }
            }

            if (averageLatencyMs < 0) {
                logger.log("[ProtocolSelection] Tunnel probe failed attempt=$attemptNumber/$maxAttempts")
            }
            if (attemptNumber < attempts && !stopRequested) {
                logger.log("[ProtocolSelection] Tunnel probe warmup retry after ${retryDelayMs}ms")
                delay(retryDelayMs)
            }
        }

        return lastSuccessfulLatencyMs
    }

    private fun ConnectionProfile.label(index: Int, total: Int): String {
        val descriptionPart = description
            ?.replace(Regex("\\s+"), " ")
            ?.trim()
            ?.takeIf { it.isNotEmpty() }
            ?.let { "description=\"$it\", " }
            .orEmpty()
        return "profile ${index + 1}/$total: ${descriptionPart}protocol=$protocol sourceIndex=$sourceIndex"
    }

    private fun saveTelemetryAttributes() {
        val attributes = configsProcessor.buildConfigAttributesJson()
        configsRepository.setTelemetryAttributes(attributes)
        logger.log("Configs attributes saved to repository")
    }

    private companion object {
        const val HEALTH_CHECK_START_GRACE_MS = 15_000L
        const val SERVICE_START_TIMEOUT_MS = 90_000L
        const val DEFAULT_TUNNEL_PROBE_ATTEMPTS = 1
        const val DEFAULT_TUNNEL_PROBE_RETRY_DELAY_MS = 500L
        const val PROFILE_TUNNEL_PROBE_ATTEMPTS = 1
        const val PROFILE_TUNNEL_PROBE_RETRY_DELAY_MS = 500L
        const val SELECTED_TUNNEL_PROBE_ATTEMPTS = 1
    }

    private data class ProfileProbeResult(
        val index: Int,
        val total: Int,
        val profile: ConnectionProfile,
        val averageLatencyMs: Long,
    )
    //endregion
}
