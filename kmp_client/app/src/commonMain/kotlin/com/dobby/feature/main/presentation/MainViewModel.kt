package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.*
import com.dobby.feature.main.ui.MainUiState
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import net.peanuuutz.tomlkt.AbstractTomlWriter
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.TomlIndentation
import net.peanuuutz.tomlkt.TomlInteger
import net.peanuuutz.tomlkt.TomlWriter
import net.peanuuutz.tomlkt.decodeFromString
import net.peanuuutz.tomlkt.encodeToWriter

val httpClient = HttpClient()

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val logger: Logger,
) : ViewModel() {
    //region Cloak states
    private val _uiState = MutableStateFlow(MainUiState())

    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    init {
        // Cloak init
        viewModelScope.launch {
            _uiState.emit(
                MainUiState(
                    connectionURL = configsRepository.getConnectionURL(),
                )
            )
        }

        viewModelScope.launch {
            connectionStateRepository.flow.collect { isConnected ->
                val newState = _uiState.value.copy(isConnected = isConnected)
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
        connectionUrl: String,
    ) {
        logger.log("!!! The connection button was clicked with URL: $connectionUrl")

        if (!configsRepository.couldStart()) {
            logger.log("We couldn't do this operation, configsRepository.couldStart() returned FALSE")
            return
        }

        logger.log("Proceeding with setConfig for the provided URL...")
        try {
            setConfig(connectionUrl)
        } catch (e: Exception) {
            logger.log("!!! Error during setConfig: ${e.message}")
            return
        } finally {
            logger.log("Finish setConfig()")
        }


        viewModelScope.launch {
            val currentState = connectionStateRepository.flow.value
            logger.log("Current connection state: $currentState")

            when (currentState) {
                true -> {
                    logger.log("Stopping VPN service due to active connection")
                    stopVpnService()
                }
                false -> {
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

    private fun setConfig(connectionUrl: String) {
        logger.log("!!! Start setConfig() with connectionUrl: $connectionUrl")

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
        logger.log("!!! Start parseToml()")

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
            val outlineSuffix = if (ss.Outline == true) "/?outline=1" else ""
            configsRepository.setServerPortOutline("${ss.Server}:${ss.Port}$outlineSuffix")
            logger.log("Outline method, password, and server: ${ss.Method}@${ss.Server}:${ss.Port}")
        }

        if (root.Cloak != null) {
            logger.log("Detected Cloak config, enabling Cloak mode")
            configsRepository.setIsCloakEnabled(true)
            val cloakJson = Json { prettyPrint = true }.encodeToString(root.Cloak)
            configsRepository.setCloakConfig(cloakJson)
            logger.log("Cloak config saved successfully (length=${cloakJson.length})")
        }

        if (root.AWG != null) {
            logger.log("Detected AmneziaWG config, enabling awg mode")
            configsRepository.setVpnInterface(VpnInterface.AMNEZIA_WG)
            configsRepository.setIsAmneziaWGEnabled(true)
            configsRepository.setAwgConfig(root.AWG)
            logger.log("AmneziaWG config saved successfully (length=${root.AWG.length})")
        }

        logger.log("Finish parseToml()")
    }

    private fun getConfigByURL(connectionUrl: String): String {
        logger.log("!!! getConfigByURL() called with: $connectionUrl")

        return if (connectionUrl.startsWith("http://") || connectionUrl.startsWith("https://")) {
            try {
                logger.log("Fetching config from remote URL...")
                runBlocking {
                    httpClient.get(connectionUrl) {
                        headers {
                            append("User-Agent", "DobbyVPN")
                        }
                    }.bodyAsText()
                }.also {
                    logger.log("Successfully fetched remote config (${it.length} bytes)")
                }
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

    private fun startVpn(isPermissionGranted: Boolean) {
        if (isPermissionGranted) {
            logger.log("Permission granted — starting VPN service")
            startVpnService()
        } else {
            logger.log("Permission denied — skipping VPN start")
            // TODO: show Toast/snackbar
        }
    }

    private fun startVpnService() {
        logger.log("!!! Starting VPN service...")
        vpnManager.start()
    }

    private suspend fun stopVpnService() {
        logger.log("!!! Stopping VPN service...")
        vpnManager.stop()
        configsRepository.setIsOutlineEnabled(false)
        configsRepository.setIsCloakEnabled(false)
        connectionStateRepository.update(isConnected = false)
        logger.log("VPN service stopped successfully, state reset to disconnected")
    }
    //endregion
}

