package com.dobby.feature.main.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.clearOutlineAndCloakConfig
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.ui.MainUiState
import com.dobby.feature.main.domain.config.TomlConfigApplier
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import com.dobby.vpn.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

val httpClient = HttpClient()

class MainViewModel(
    val configsRepository: DobbyConfigsRepository,
    val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val awgManager: AwgManager,
    private val logger: Logger,
    healthCheck: HealthCheck,
) : ViewModel() {
    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    private val tomlConfigApplier = TomlConfigApplier(
        vpnRepo = configsRepository,
        outlineRepo = configsRepository,
        cloakRepo = configsRepository,
        mainRepo = configsRepository,
        logger = logger
    )

    //region AmneziaWG states
    val awgVersion: String = awgManager.getAwgVersion()

    var awgConfigState: MutableState<String> = mutableStateOf(configsRepository.getAwgConfig())
        private set

    var awgConnectionState: MutableState<AwgConnectionState> = mutableStateOf(
        if (configsRepository.getIsAmneziaWGEnabled()) AwgConnectionState.ON else AwgConnectionState.OFF
    )
        private set
    //endregion
    private val healthCheckManager: HealthCheckManager = HealthCheckManager(healthCheck, this, configsRepository, logger)
    private var serverAddress: String? = null
    private var serverPort: Int? = null
    private var connectionActionSequence = 0L

    init {
        viewModelScope.launch {
            _uiState.emit(
                MainUiState(
                    connectionURL = configsRepository.getConnectionURL(),
                )
            )
        }
        viewModelScope.launch {
            connectionStateRepository.statusFlow.collect { isConnected ->
                logger.log("Update connection state: $isConnected")
                val newState = _uiState.value.copy(isConnected = isConnected)
                _uiState.emit(newState)
            }
        }
        viewModelScope.launch {
            connectionStateRepository.vpnStartedFlow.collect { isStarted ->
                logger.log("Update vpn started state: $isStarted")
                val newState = _uiState.value.copy(isVpnStarted = isStarted)
                _uiState.emit(newState)
            }
        }
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

    //region Cloak functions
    fun onConnectionToggleRequested(
        connectionUrl: String,
        source: String,
    ) {
        connectionActionSequence += 1
        val actionId = connectionActionSequence
        val vpnStartedBefore = connectionStateRepository.vpnStartedFlow.value
        val statusBefore = connectionStateRepository.statusFlow.value
        logger.log(
            "[MainVM] connectionAction id=$actionId source=$source " +
                "vpnStartedBefore=$vpnStartedBefore statusBefore=$statusBefore urlLength=${connectionUrl.length}"
        )

        if (!configsRepository.couldStart()) {
            logger.log("[MainVM] connectionAction id=$actionId source=$source rejected: configsRepository.couldStart() returned FALSE")
            return
        }

        viewModelScope.launch(Dispatchers.Default) {
            logger.log("[MainVM] connectionAction id=$actionId source=$source proceeding on background dispatcher")
            if (!connectionStateRepository.vpnStartedFlow.value) {
                try {
                    logger.log("[MainVM] connectionAction id=$actionId source=$source loading config by ${maskStr(connectionUrl)}")
                    val ok = setConfig(connectionUrl)
                    if (!ok) {
                        logger.log(
                            "[MainVM] connectionAction id=$actionId source=$source " +
                                "config invalid or failed to apply → abort start (no HC/VPN)"
                        )
                        connectionStateRepository.updateVpnStarted(false)
                        connectionStateRepository.updateStatus(false)
                        return@launch
                    }
                } catch (e: Exception) {
                    logger.log("[MainVM] connectionAction id=$actionId source=$source error during setConfig: ${e.message}")
                    return@launch
                } finally {
                    logger.log("[MainVM] connectionAction id=$actionId source=$source finish setConfig()")
                }
            }

            val currentState = connectionStateRepository.vpnStartedFlow.value
            logger.log("[MainVM] connectionAction id=$actionId source=$source current vpnStarted state: $currentState")
            configsRepository.setIsUserInitStop(currentState)

            when (currentState) {
                true -> {
                    logger.log("[MainVM] connectionAction id=$actionId source=$source stopping VPN service due to active connection")
                    connectionStateRepository.updateVpnStarted(false)
                    connectionStateRepository.updateStatus(false)
                    healthCheckManager.stopHealthCheck()
                    stopVpnService(reason = "connectionAction source=$source id=$actionId active_connection_toggle")
                }
                false -> {
                    connectionStateRepository.updateVpnStarted(true)
                    logger.log(
                        "[MainVM] connectionAction id=$actionId source=$source " +
                            "update vpnStarted state: VpnState = ${connectionStateRepository.vpnStartedFlow.value}"
                    )
                    logger.log("[MainVM] connectionAction id=$actionId source=$source VPN is currently disconnected")
                    if (isPermissionCheckNeeded) {
                        logger.log(
                            "[MainVM] connectionAction id=$actionId source=$source " +
                                "permission check required, triggering permission dialog"
                        )
                        permissionEventsChannel.checkPermissions()
                    } else {
                        logger.log(
                            "[MainVM] connectionAction id=$actionId source=$source " +
                                "permission check is NOT required, starting VPN service directly"
                        )
                        startVpnService()
                    }
                }
            }
        }
    }

    private suspend fun setConfig(connectionUrl: String): Boolean {
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
                configsRepository.clearOutlineAndCloakConfig()
            }
            .getOrDefault(false)

        if (!applied) {
            logger.log("Config not applied (invalid/unsupported) → will not start VPN/HC")
            return false
        }

        updateServerTargetFromConfig()
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
            connectionStateRepository.tryUpdateVpnStarted(false)
            // TODO: show Toast/snackbar
        }
    }

    suspend fun startVpnService() {
        logger.log("Starting VPN service...")
        val address = serverAddress
        val port = serverPort
        if (address == null || port == null) {
            if (!updateServerTargetFromConfig()) {
                logger.log("Server address/port is not set → skipping health check start")
                vpnManager.start()
                return
            }
        }
        withContext(Dispatchers.Default) {
            healthCheckManager.startHealthCheck(serverAddress!!, serverPort!!)
        }
        vpnManager.start()
    }

    fun stopVpnService(stoppedByHealthCheck: Boolean = false, reason: String = "unknown") {
        logger.log("Stopping VPN service... stoppedByHealthCheck=$stoppedByHealthCheck reason=$reason")
        vpnManager.stop()
        if (!stoppedByHealthCheck) {
            configsRepository.clearOutlineAndCloakConfig()
            connectionStateRepository.tryUpdateStatus(false)
        }
        logger.log("VPN stop requested; local state updated. Await NEVPNStatusDidChange for actual disconnected status")
    }

    private fun updateServerTargetFromConfig(): Boolean {
        val serverPortOutline = configsRepository.getServerPortOutline()
        val parsed = parseHostPort(serverPortOutline)
        return if (parsed == null) {
            logger.log("Failed to parse server address/port from Outline config: ${maskStr(serverPortOutline)}")
            serverAddress = null
            serverPort = null
            false
        } else {
            serverAddress = parsed.first
            serverPort = parsed.second
            logger.log("Server target resolved: ${maskStr(serverAddress ?: "")}:$serverPort")
            true
        }
    }

    private fun parseHostPort(hostPortMaybeWithQuery: String): Pair<String, Int>? {
        val hostPort = hostPortMaybeWithQuery.substringBefore("?").trim()
        if (hostPort.isBlank()) return null

        return if (hostPort.startsWith("[")) {
            val host = hostPort.substringAfter("[").substringBefore("]")
            val portStr = hostPort.substringAfter("]:", "")
            val port = portStr.toIntOrNull() ?: return null
            host to port
        } else {
            val lastColon = hostPort.lastIndexOf(':')
            if (lastColon <= 0) return null
            val host = hostPort.substring(0, lastColon)
            val portStr = hostPort.substring(lastColon + 1)
            val port = portStr.toIntOrNull() ?: return null
            host to port
        }
    }
    //endregion
}
