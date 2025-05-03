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
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.ui.MainUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

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
        _uiState.tryEmit(
            MainUiState(
                cloakJson = configsRepository.getCloakConfig(),
                outlineKey = configsRepository.getOutlineKey(),
                isCloakEnabled = configsRepository.getIsCloakEnabled()
            )
        )

        viewModelScope.launch {
            connectionStateRepository.flow.collect { isConnected ->
                val newState = _uiState.value.copy(isConnected = isConnected)
                _uiState.tryEmit(newState)
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
        cloakJson: String?,
        outlineKey: String,
        isCloakEnabled: Boolean
    ) {
        saveData(isCloakEnabled, cloakJson, outlineKey)
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

    private fun saveData(isCloakEnabled: Boolean, cloakJson: String?, outlineKey: String) {
        configsRepository.setOutlineKey(outlineKey)

        cloakJson?.let(configsRepository::setCloakConfig)
        configsRepository.setIsCloakEnabled(isCloakEnabled)
    }

    private fun startVpn(isPermissionGranted: Boolean) {
        if (isPermissionGranted) {
            startVpnService()
        } else {
            Unit // TODO Implement Toast logic or compose snackbar
        }
    }

    private fun startVpnService() {
        configsRepository.setIsOutlineEnabled(true)
        configsRepository.setVpnInterface(VpnInterface.CLOAK_OUTLINE)
        vpnManager.start()
    }

    private suspend fun stopVpnService() {
        configsRepository.setIsOutlineEnabled(false)
        connectionStateRepository.update(isConnected = false)
        vpnManager.stop()
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
