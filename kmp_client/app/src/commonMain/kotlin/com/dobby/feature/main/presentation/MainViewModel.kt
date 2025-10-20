package com.dobby.feature.main.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
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

val httpClient = HttpClient()

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val vpnManager: VpnManager,
    private val awgManager: AwgManager,
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
    ) {
        setConfig(connectionUrl)
        viewModelScope.launch {
            when (connectionStateRepository.flow.value) {
                true -> stopVpnService()
                false -> {
                    if (isPermissionCheckNeeded) {
                        permissionEventsChannel.checkPermissions()
                    } else {
                        startVpnService()
                    }
                }
            }
        }
    }

    private fun setConfig(connectionUrl: String) {
        configsRepository.setConnectionURL(connectionUrl)
        val connectionConfig = getConfigByURL(connectionUrl)
        configsRepository.setConnectionConfig(connectionConfig)
        try {
            parseToml(connectionConfig)
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    private fun parseToml(connectionConfig: String) {
        if (connectionConfig.isBlank()) {
            return
        }
        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)
        val ss = root.Shadowsocks?.Direct ?: root.Shadowsocks?.Local
        if (ss != null) {
            configsRepository.setIsOutlineEnabled(true)
            configsRepository.setMethodPasswordOutline("${ss.Method}:${ss.Password}")
            val outlineSuffix = if (ss.Outline == true) "/?outline=1" else ""
            configsRepository.setServerPortOutline("${ss.Server}:${ss.Port}$outlineSuffix")
        }
        if (root.Cloak != null) {
            configsRepository.setIsCloakEnabled(true)

            configsRepository.setCloakConfig(Json { prettyPrint = true }.encodeToString(root.Cloak))

        }
    }

    private fun getConfigByURL(connectionUrl: String): String {
        return if (connectionUrl.startsWith("http://") || connectionUrl.startsWith("https://")) {
            try {
                runBlocking {
                    httpClient.get(connectionUrl) {
                        headers {
                            append("User-Agent", "DobbyVPN")
                        }
                    }.bodyAsText()
                }
            } catch (e: Exception) {
                "Can't get config by url. Error" + e.message
            }
        } else {
            connectionUrl
        }
    }

    private fun startVpn(isPermissionGranted: Boolean) {
        if (isPermissionGranted) {
            startVpnService()
        } else {
            Unit // TODO Implement Toast logic or compose snackbar
        }
    }

    private fun startVpnService() {
        vpnManager.start()
    }

    private suspend fun stopVpnService() {
        vpnManager.stop()
        configsRepository.setIsOutlineEnabled(false)
        configsRepository.setIsCloakEnabled(false)
        connectionStateRepository.update(isConnected = false)
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

