package com.dobby.feature.main.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.ui.MainUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import io.ktor.client.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString
import com.dobby.vpn.BuildConfig

val httpClient = HttpClient()

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val awgManager: AwgManager,
    private val logger: Logger,
) : ViewModel() {
    //region Cloak states
    private val _uiState = MutableStateFlow(MainUiState())

    val uiState: StateFlow<MainUiState> = _uiState
    //endregion

    //region AmneziaWG states
    val awgVersion: String

    var awgConfigState: MutableState<String>
        private set

    var awgConnectionState: MutableState<AwgConnectionState>
        private set
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

        // AmneziaWG init
        awgVersion = awgManager.getAwgVersion()

        val awgConfigStoredValue = configsRepository.getAwgConfig()
        val awgConnectionStoredValue =
            if (configsRepository.getIsAmneziaWGEnabled()) AwgConnectionState.ON
            else AwgConnectionState.OFF
        awgConfigState = mutableStateOf(awgConfigStoredValue)
        awgConnectionState = mutableStateOf(awgConnectionStoredValue)
    }

    //region Cloak functions
    fun onConnectionButtonClicked(
        connectionUrl: String,
        isConnected: Boolean
    ) {
        logger.log("The connection button was clicked with URL: ${maskStr(connectionUrl)}")

        if (!configsRepository.couldStart()) {
            logger.log("We couldn't do this operation, configsRepository.couldStart() returned FALSE")
            return
        }

        logger.log("Proceeding with setConfig for the provided URL...")
        if (!isConnected) {
            try {
                logger.log("We get config by ${maskStr(connectionUrl)}")
                setConfig(connectionUrl)
            } catch (e: Exception) {
                logger.log("Error during setConfig: ${e.message}")
                return
            } finally {
                logger.log("Finish setConfig()")
            }
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
        logger.log("Start setConfig() with connectionUrl: ${maskStr(connectionUrl)}")

        configsRepository.setConnectionURL(connectionUrl)
        logger.log("Connection URL saved to repository")

        val connectionConfig = getConfigByURL(connectionUrl)
        logger.log("Fetched connection config, size = ${connectionConfig.length}")

        configsRepository.setConnectionConfig(connectionConfig)
        logger.log("Connection config saved to repository")

        runCatching { parseToml(connectionConfig) }
            .onFailure { e ->
                logger.log("Error during parsing TOML (ignored): ${e.message}")
                configsRepository.setIsOutlineEnabled(false)
                configsRepository.setIsCloakEnabled(false)
                configsRepository.setCloakConfig("")
                configsRepository.setPrefixOutline("")
                configsRepository.setIsWebsocketEnabled(false)
                configsRepository.setTcpPathOutline("")
                configsRepository.setUdpPathOutline("")
            }
    }

    private fun parseToml(connectionConfig: String) {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)

        val outline = root.Outline

        if (outline != null) {
            logger.log("Detected [Outline] config, applying Outline parameters")

            configsRepository.setIsOutlineEnabled(true)
            val method = outline.Method.trim()
            val password = outline.Password.trim()
            val cloakEnabled = outline.Cloak == true
            if (method.isEmpty() || password.isEmpty()) {
                logger.log("Invalid [Outline]: Method/Password are required. Disabling Outline/Cloak.")
                configsRepository.setIsOutlineEnabled(false)
                configsRepository.setIsCloakEnabled(false)
                configsRepository.setCloakConfig("")
                return
            }

            configsRepository.setMethodPasswordOutline("$method:$password")

            if (cloakEnabled) {
                // When Cloak is enabled, Outline must connect to local Cloak endpoint.
                val localPort = if (outline.LocalPort in 1..65535) outline.LocalPort else 1984
                if (outline.LocalPort !in 1..65535) {
                    logger.log("Invalid Outline.LocalPort=${outline.LocalPort}; using default 1984")
                }

                configsRepository.setCloakLocalPort(localPort)

                // Ignore Outline.Server/Port when Cloak is enabled.
                configsRepository.setServerPortOutline("127.0.0.1:$localPort")
                logger.log("Cloak enabled: Outline will connect to local endpoint 127.0.0.1:$localPort (ignoring Outline.Server/Port)")
            } else {
                val server = outline.Server?.trim().orEmpty()
                val port = outline.Port
                if (port == null) {
                    logger.log("Invalid [Outline]: Port is required Disabling Outline.")
                    configsRepository.setIsOutlineEnabled(false)
                    return
                }
                if (server.isEmpty()) {
                    logger.log("Invalid [Outline]: Server is required. Disabling Outline.")
                    configsRepository.setIsOutlineEnabled(false)
                    return
                }
                configsRepository.setServerPortOutline("${server}:${port}")
            }


            val websocketEnabled = outline.Websocket == true
            configsRepository.setIsWebsocketEnabled(websocketEnabled)
            configsRepository.setPrefixOutline(outline.Prefix ?: "") // Don't trim! Spaces may be intentional
            configsRepository.setTcpPathOutline(outline.TcpPath?.trim() ?: "")
            configsRepository.setUdpPathOutline(outline.UdpPath?.trim() ?: "")

            logger.log("Outline prefix: ${outline.Prefix ?: "(none)"}")
            logger.log("Outline websocket: $websocketEnabled, tcpPath: ${outline.TcpPath ?: "(none)"}, udpPath: ${outline.UdpPath ?: "(none)"}")

            logger.log("Outline method, password, and server: ${method}:${maskStr(password)}@${maskStr(configsRepository.getServerPortOutline())}")
            
            if (cloakEnabled) {
                logger.log("Cloak enabled inside [Outline], building Cloak config")

                val transport = outline.Transport?.trim().orEmpty()
                val encryptionMethod = outline.EncryptionMethod?.trim().orEmpty()
                val uid = outline.UID?.trim().orEmpty()
                val publicKey = outline.PublicKey?.trim().orEmpty()
                val remoteHost = outline.RemoteHost?.trim().orEmpty()
                val remotePort = outline.RemotePort?.trim().orEmpty()

                if (
                    transport.isEmpty() ||
                    encryptionMethod.isEmpty() ||
                    uid.isEmpty() ||
                    publicKey.isEmpty() ||
                    remoteHost.isEmpty() ||
                    remotePort.isEmpty()
                ) {
                    logger.log("Invalid [Outline] Cloak fields: Transport/EncryptionMethod/UID/PublicKey/RemoteHost/RemotePort are required. Disabling Cloak.")
                    configsRepository.setIsCloakEnabled(false)
                    configsRepository.setCloakConfig("")
                    return
                }

                val serverName = outline.ServerName?.trim().orEmpty().ifEmpty { remoteHost }
                val cdnOriginHost = outline.CDNOriginHost?.trim().orEmpty().ifEmpty { remoteHost }

                val cloakConfig = CloakClientConfig(
                    Transport = transport,
                    EncryptionMethod = encryptionMethod,
                    UID = uid,
                    PublicKey = publicKey,
                    ServerName = serverName,
                    NumConn = outline.NumConn,
                    BrowserSig = outline.BrowserSig,
                    StreamTimeout = outline.StreamTimeout,
                    RemoteHost = remoteHost,
                    RemotePort = remotePort,
                    CDNWsUrlPath = outline.CDNWsUrlPath?.trim(),
                    CDNOriginHost = cdnOriginHost
                )

                configsRepository.setIsCloakEnabled(true)
                val cloakJson = Json { prettyPrint = true }.encodeToString(cloakConfig)
                configsRepository.setCloakConfig(cloakJson)

                val cloakForLog = cloakConfig.copy(
                    UID = maskStr(cloakConfig.UID),
                    RemoteHost = maskStr(cloakConfig.RemoteHost),
                    ServerName = maskStr(cloakConfig.ServerName),
                    CDNOriginHost = maskStr(cloakConfig.CDNOriginHost),
                    CDNWsUrlPath = cloakConfig.CDNWsUrlPath?.let { maskStr(it) }
                )
                val cloakJsonForLog = Json { prettyPrint = true }.encodeToString(cloakForLog)
                logger.log("Cloak config saved successfully (config=${cloakJsonForLog})")
            } else {
                configsRepository.setIsCloakEnabled(false)
                configsRepository.setCloakConfig("")
            }
        } else {
            logger.log("Outline config not detected, turning off")
            configsRepository.setIsOutlineEnabled(false)
            configsRepository.setIsCloakEnabled(false)
            configsRepository.setCloakConfig("")
            configsRepository.setPrefixOutline("")
            configsRepository.setIsWebsocketEnabled(false)
            configsRepository.setTcpPathOutline("")
            configsRepository.setUdpPathOutline("")
        }

        logger.log("Finish parseToml()")
    }

    private fun getConfigByURL(connectionUrl: String): String {
        logger.log("getConfigByURL() called with: ${maskStr(connectionUrl)}")

        return if (connectionUrl.startsWith("http://") || connectionUrl.startsWith("https://")) {
            try {
                logger.log("Fetching config from remote URL...")
                runBlocking {
                    httpClient.get(connectionUrl) {
                        headers {
                            append("User-Agent", "DobbyVPN v${BuildConfig.VERSION_NAME}")
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
        logger.log("Starting VPN service...")
        vpnManager.start()
    }

    private suspend fun stopVpnService() {
        logger.log("Stopping VPN service...")
        vpnManager.stop()
        configsRepository.setIsOutlineEnabled(false)
        configsRepository.setIsCloakEnabled(false)
        connectionStateRepository.update(isConnected = false)
        logger.log("VPN service stopped successfully, state reset to disconnected")
    }
    //endregion

    //region AmneziaWG functions
    fun onAwgConfigEdit(newConfig: String) {
        var configDelegate by awgConfigState
        configsRepository.setAwgConfig(newConfig)
        configDelegate = newConfig
    }

    fun onAwgConnect() {
        viewModelScope.launch { permissionEventsChannel.checkPermissions() }

        var connectionStateDelegate by awgConnectionState
        connectionStateDelegate = AwgConnectionState.ON
        configsRepository.setIsAmneziaWGEnabled(true)
        configsRepository.setVpnInterface(VpnInterface.AMNEZIA_WG)
        awgManager.onAwgConnect()
    }

    fun onAwgDisconnect() {
        var connectionStateDelegate by awgConnectionState
        connectionStateDelegate = AwgConnectionState.OFF
        configsRepository.setIsAmneziaWGEnabled(false)
        configsRepository.setVpnInterface(VpnInterface.AMNEZIA_WG)
        awgManager.onAwgDisconnect()
    }
    //endregion
}

