package com.dobby.feature.main.presentation

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
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.ui.MainUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString
import com.dobby.vpn.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.IO

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
    private val healthCheckManager: HealthCheckManager = HealthCheckManager(healthCheck, this, configsRepository, logger)
    private lateinit var serverAddress: String
    private var serverPort: Int = 0

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

    //region Cloak functions
    fun onConnectionButtonClicked(
        connectionUrl: String
    ) {
        logger.log("The connection button was clicked with URL: ${maskStr(connectionUrl)}")

        if (!configsRepository.couldStart()) {
            logger.log("We couldn't do this operation, configsRepository.couldStart() returned FALSE")
            return
        }

        viewModelScope.launch(Dispatchers.IO) {
            logger.log("Proceeding with setConfig for the provided URL...")
            if (!connectionStateRepository.vpnStartedFlow.value) {
                try {
                    logger.log("We get config by ${maskStr(connectionUrl)}")
                    setConfig(connectionUrl)
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

    private suspend fun setConfig(connectionUrl: String) {
        logger.log("Start setConfig() with connectionUrl: ${maskStr(connectionUrl)}")

        configsRepository.setConnectionURL(connectionUrl)
        logger.log("Connection URL saved to repository")

        val connectionConfig = getConfigByURL(connectionUrl)
        logger.log("Fetched connection config, size = ${connectionConfig.length}")

        configsRepository.setConnectionConfig(connectionConfig)
        logger.log("Connection config saved to repository")

        try {
            parseToml(connectionConfig)
        } catch (e: Exception) {
            val errorMsg = "Error during parsing TOML: ${e.message}"
            logger.log(errorMsg)
            throw RuntimeException(errorMsg)
        }
    }

    private fun parseToml(connectionConfig: String) {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)
        val ss = root.Shadowsocks?.Direct ?: root.Shadowsocks?.Local

        if (ss != null) {
            logger.log("Detected Shadowsocks config, applying Outline parameters")
            configsRepository.setIsOutlineEnabled(true)
            configsRepository.setMethodPasswordOutline("${ss.Method}:${ss.Password}")
            serverAddress = ss.Server
            serverPort = ss.Port
            val outlineSuffix = if (ss.Outline == true) "/?outline=1" else ""
            configsRepository.setServerPortOutline("${ss.Server}:${ss.Port}$outlineSuffix")
            logger.log("Outline method, password, and server: ${ss.Method}:${maskStr(ss.Password)}@${maskStr(ss.Server)}:${ss.Port}")
        } else {
            logger.log("Shadowsocks config didn't detected, turn off")
            configsRepository.setIsOutlineEnabled(false)
        }

        if (root.Cloak != null) {
            logger.log("Detected Cloak config, enabling Cloak mode")
            configsRepository.setIsCloakEnabled(true)
            val cloakJson = Json { prettyPrint = true }.encodeToString(root.Cloak)
            configsRepository.setCloakConfig(cloakJson)
            serverAddress = root.Cloak.RemoteHost
            serverPort = root.Cloak.RemotePort.toInt()
            root.Cloak.UID = maskStr(root.Cloak.UID)
            root.Cloak.RemoteHost = maskStr(root.Cloak.RemoteHost)
            root.Cloak.ServerName = maskStr(root.Cloak.ServerName)
            root.Cloak.CDNOriginHost = root.Cloak.CDNOriginHost?.let { maskStr(it) }
            root.Cloak.CDNWsUrlPath = root.Cloak.CDNWsUrlPath?.let { maskStr(it) }
            val cloakJsonForLog = Json { prettyPrint = true }.encodeToString(root.Cloak)
            logger.log("Cloak config saved successfully (config=${cloakJsonForLog})")
        } else {
            logger.log("Cloak config didn't detected, turn off")
            configsRepository.setIsCloakEnabled(false)
        }

        logger.log("Finish parseToml()")
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
            // TODO: show Toast/snackbar
        }
    }

    suspend fun startVpnService() {
        logger.log("Starting VPN service...")
        vpnManager.start()
        healthCheckManager.startHealthCheck(serverAddress, serverPort)
    }

    fun stopVpnService(stoppedByHealthCheck: Boolean = false) {
        logger.log("Stopping VPN service...")
        vpnManager.stop()
        if (!stoppedByHealthCheck) {
            configsRepository.setIsOutlineEnabled(false)
            configsRepository.setIsCloakEnabled(false)
            serverAddress = ""
            serverPort = 0
        }
        logger.log("VPN service stopped successfully, state reset to disconnected")
    }
    //endregion
}

