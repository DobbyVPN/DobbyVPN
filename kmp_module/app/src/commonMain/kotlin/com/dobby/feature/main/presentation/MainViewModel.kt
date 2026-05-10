package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.diagnostic.domain.VpnConnectionState
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
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import net.peanuuutz.tomlkt.Toml
import kotlin.concurrent.Volatile
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

val httpClient = HttpClient()

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val logger: Logger,
    private val healthCheck: HealthCheck,
) : ViewModel() {
    @Volatile
    private var connectionDetectorAtomic: Boolean = true
    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    private val tomlConfigApplier = TomlConfigApplier(
        vpnRepo = configsRepository,
        outlineRepo = configsRepository,
        cloakRepo = configsRepository,
        mainRepo = configsRepository,
        awgRepo = configsRepository,
        xrayRepo = configsRepository,
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
            val connectionState = healthCheck.GetConnectionState()
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
        connectionDetectorAtomic = false
    }

    /**
     * Starts connection state detector thread,
     * that runs only when health check is being run,
     * and repeatedly loads VPN connection state
     */
    fun startConnectionStateDetector() {
        connectionDetectorAtomic = true
        viewModelScope.launch {
            logger.log("Connection state detector: start")
            while (connectionDetectorAtomic) {
                val connectionState = healthCheck.GetConnectionState()
                val newState = _uiState.value.copy(connectionState = connectionState)
                _uiState.emit(newState)
                connectionStateRepository.updateStatus(connectionState)
                delay(1.seconds)
            }
            logger.log("Connection state detector: awaiting disconnection")
            while (true) {
                val connectionState = healthCheck.GetConnectionState()
                if (connectionState != VpnConnectionState.DISCONNECTED) {
                    logger.log("Closing healthcheck...")
                } else {
                    val newState = _uiState.value.copy(connectionState = connectionState)
                    _uiState.emit(newState)
                    connectionStateRepository.updateStatus(connectionState)
                    break
                }
                delay(150.milliseconds)
            }
            logger.log("Connection state detector: finished")
        }
    }

    private fun prepareHealthCheckCheckingConfig(): String =
        when (configsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> {
                val jsonConfig = configsRepository.getCloakConfig()
                val cloakConfig = if (!configsRepository.getIsCloakEnabled() || jsonConfig.isBlank()) {
                    null
                } else {
                    try {
                        Toml.decodeFromString<CloakClientConfig>(jsonConfig)
                    } catch (_: Exception) {
                        null
                    }
                }
                Json.encodeToString(
                    mapOf(
                        "interface" to "Outline",
                        "WebSocket" to configsRepository.getIsWebsocketEnabled().toString(),
                        "DisguisePrefix" to configsRepository.getPrefixOutline(),
                        "TcpPathOutline" to configsRepository.getTcpPathOutline(),
                        "UdpPathOutline" to configsRepository.getUdpPathOutline(),
                        "Cloak" to configsRepository.getIsCloakEnabled().toString(),
                        "Transport" to (cloakConfig?.Transport ?: "null"),
                        "ProxyMethod" to (cloakConfig?.ProxyMethod ?: "null"),
                        "EncryptionMethod" to (cloakConfig?.EncryptionMethod ?: "null"),
                        "UID" to (cloakConfig?.UID ?: "null"),
                        "ServerName" to (cloakConfig?.ServerName ?: "null"),
                        "NumConn" to (cloakConfig?.NumConn?.toString() ?: "null"),
                        "BrowserSig" to (cloakConfig?.BrowserSig ?: "null"),
                        "StreamTimeout" to (cloakConfig?.StreamTimeout?.toString() ?: "null"),
                        "RemoteHost" to (cloakConfig?.RemoteHost ?: "null"),
                        "RemotePort" to (cloakConfig?.RemotePort ?: "null"),
                        "CDNWsUrlPath" to (cloakConfig?.CDNWsUrlPath ?: "null"),
                        "CDNOriginHost" to (cloakConfig?.CDNOriginHost ?: "null")
                    )
                )
            }

            VpnInterface.AMNEZIA_WG -> {
                val tomlConfigString = configsRepository.getAwgTomlConfig()
                val tomlConfig = if (tomlConfigString.isBlank()) {
                    null
                } else {
                    try {
                        Toml.decodeFromString<AmneziaWGConfig>(tomlConfigString)
                    } catch (_: Exception) {
                        null
                    }
                }
                Json.encodeToString(
                    mapOf(
                        "interface" to "AmneziaWG",
                        "Jc" to (tomlConfig?.Interface?.Jc?.toString() ?: "null"),
                        "Jmin" to (tomlConfig?.Interface?.Jmin?.toString() ?: "null"),
                        "Jmax" to (tomlConfig?.Interface?.Jmax?.toString() ?: "null"),
                        "S1" to (tomlConfig?.Interface?.S1?.toString() ?: "null"),
                        "S2" to (tomlConfig?.Interface?.S2?.toString() ?: "null"),
                        "S3" to (tomlConfig?.Interface?.S3?.toString() ?: "null"),
                        "S4" to (tomlConfig?.Interface?.S4?.toString() ?: "null"),
                        "H1" to (tomlConfig?.Interface?.H1 ?: "null"),
                        "H2" to (tomlConfig?.Interface?.H2 ?: "null"),
                        "H3" to (tomlConfig?.Interface?.H3 ?: "null"),
                        "H4" to (tomlConfig?.Interface?.H4 ?: "null"),
                        "I1" to (tomlConfig?.Interface?.I1 ?: "null"),
                        "I2" to (tomlConfig?.Interface?.I2 ?: "null"),
                        "I3" to (tomlConfig?.Interface?.I3 ?: "null"),
                        "I4" to (tomlConfig?.Interface?.I4 ?: "null"),
                        "I5" to (tomlConfig?.Interface?.I5 ?: "null"),
                    )
                )
            }

            VpnInterface.XRAY -> {
                val jsonConfig = configsRepository.getXrayConfig()
                val xrayConfig = if (jsonConfig.isBlank()) {
                    null
                } else {
                    try {
                        Json.decodeFromString<XrayClientConfig>(jsonConfig)
                    } catch (_: Exception) {
                        null
                    }
                }
                Json.encodeToString(
                    mapOf(
                        "interface" to "Xray",
                        "version" to (xrayConfig?.version?.toString() ?: "null"),
                        "log" to (xrayConfig?.log?.toString() ?: "null"),
                        "api" to (xrayConfig?.api?.toString() ?: "null"),
                        "dns" to (xrayConfig?.dns?.toString() ?: "null"),
                        "routing" to (xrayConfig?.routing?.toString() ?: "null"),
                        "policy" to (xrayConfig?.policy?.toString() ?: "null"),
                        "inbounds" to (xrayConfig?.inbounds?.toString() ?: "null"),
                        "outbounds" to (xrayConfig?.outbounds?.toString() ?: "null"),
                        "transport" to (xrayConfig?.transport?.toString() ?: "null"),
                        "stats" to (xrayConfig?.stats?.toString() ?: "null"),
                        "reverse" to (xrayConfig?.reverse?.toString() ?: "null"),
                        "fakedns" to (xrayConfig?.fakedns?.toString() ?: "null"),
                        "metrics" to (xrayConfig?.metrics?.toString() ?: "null"),
                        "observatory" to (xrayConfig?.observatory?.toString() ?: "null"),
                        "burstObservatory" to (xrayConfig?.burstObservatory?.toString() ?: "null"),
                    )
                )
            }

            VpnInterface.NONE -> {
                Json.encodeToString(
                    mapOf(
                        "interface" to "None",
                    )
                )
            }
        }

    suspend fun startVpnService(): Boolean {
        logger.log("Starting VPN service...")
        logger.log("Init health check")
        val config = prepareHealthCheckCheckingConfig()
        healthCheck.InitHealthCheck(config)
        logger.log("Start tunnel service")
        connectionStateRepository.serviceStartedFlow.prepare()
        vpnManager.start()
        logger.log("Await service started result")
        val connected = connectionStateRepository.serviceStartedFlow.awaitResult()
        logger.log("Got service started result: $connected")
        if (connected) {
            logger.log("Start health check")
            healthCheck.StartHealthCheck()
            logger.log("Start connection detector")
            startConnectionStateDetector()
            return true
        } else {
            stopVpnService()
            return false
        }
    }

    fun stopVpnService() {
        logger.log("Stopping VPN service...")
        logger.log("Stop tunnel service")
        vpnManager.stop()
        logger.log("Stop health check")
        healthCheck.StopHealthCheck()
        logger.log("Stop connection detector")
        stopConnectionStateDetector()
        logger.log("VPN service stopped successfully, state reset to disconnected")
    }
    //endregion
}
