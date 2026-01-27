package com.dobby.feature.main.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.*
import com.dobby.feature.main.domain.config.TomlConfigApplier
import com.dobby.feature.main.ui.MainUiState
import com.dobby.vpn.BuildConfig
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

val httpClient = HttpClient()

class MainViewModel(
    val configsRepository: DobbyConfigsRepository,
    val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val logger: Logger,
    healthCheck: HealthCheck,
) : ViewModel() {
    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    private val tomlConfigApplier = TomlConfigApplier(
        outlineRepo = configsRepository,
        cloakRepo = configsRepository,
        logger = logger
    )

    private val healthCheckManager: HealthCheckManager = HealthCheckManager(healthCheck, this, configsRepository, logger)
    private var serverAddress: String? = null
    private var serverPort: Int? = null

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
            if (!connectionStateRepository.vpnStartedFlow.value) {
                try {
                    logger.log("We get config by ${maskStr(connectionUrl)}")
                    val ok = setConfig(connectionUrl)
                    if (!ok) {
                        logger.log("Config is invalid or failed to apply → abort start (no HC/VPN)")
                        connectionStateRepository.updateVpnStarted(false)
                        connectionStateRepository.updateStatus(false)
                        return@launch
                    }
                } catch (e: Exception) {
                    logger.log("Error during setConfig: ${e.message}")
                    return@launch
                } finally {
                    logger.log("Finish setConfig()")
                }
            }

            val currentState = connectionStateRepository.vpnStartedFlow.value
            logger.log("Current vpnStarted state: $currentState")
            configsRepository.setIsUserInitStop(currentState)

            when (currentState) {
                true -> {
                    logger.log("Stopping VPN service due to active connection")
                    connectionStateRepository.updateVpnStarted(false)
                    connectionStateRepository.updateStatus(false)
                    healthCheckManager.stopHealthCheck()
                    stopVpnService()
                }
                false -> {
                    connectionStateRepository.updateVpnStarted(true)
                    logger.log("Update vpnStarted state: VpnState = ${connectionStateRepository.vpnStartedFlow.value}")
                    logger.log("VPN is currently disconnected")
                    if (isPermissionCheckNeeded) {
                        logger.log("Permission check required, triggering permission dialog")
                        permissionEventsChannel.checkPermissions()
                    } else {
                        logger.log("Permission check is NOT required, starting VPN service directly")
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

    fun stopVpnService(stoppedByHealthCheck: Boolean = false) {
        logger.log("Stopping VPN service...")
        vpnManager.stop()
        if (!stoppedByHealthCheck) {
            configsRepository.clearOutlineAndCloakConfig()
            connectionStateRepository.tryUpdateStatus(false)
        }
        logger.log("VPN service stopped successfully, state reset to disconnected")
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
