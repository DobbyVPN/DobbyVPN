package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.ui.MainUiState
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val vpnManager: VpnManager,
    private val awgManager: AwgManager,
) : ViewModel() {

    private val _uiState = MutableStateFlow(MainUiState())

    val uiState: StateFlow<MainUiState> = _uiState

    val checkVpnPermissionEvents = MutableSharedFlow<Unit>()

    init {
        _uiState.tryEmit(
            MainUiState(
                cloakJson = configsRepository.getCloakConfig(),
                outlineKey = configsRepository.getOutlineKey()
            )
        )

        ConnectionStateRepository.init(false)
        viewModelScope.launch {
            connectionStateRepository.observe().collect { isConnected ->
                val newState = _uiState.value.copy(isConnected = isConnected)
                _uiState.tryEmit(newState)
            }
        }
    }

    fun onConnectionButtonClicked(
        cloakJson: String?,
        outlineKey: String,
        isCloakEnabled: Boolean
    ) {
        saveData(isCloakEnabled, cloakJson, outlineKey)
        viewModelScope.launch {
            when (ConnectionStateRepository.isConnected()) {
                true -> stopVpnService()
                false -> checkVpnPermissionEvents.emit(Unit)
            }
        }
    }

    private fun saveData(isCloakEnabled: Boolean, cloakJson: String?, outlineKey: String) {
        configsRepository.setOutlineKey(outlineKey)

        cloakJson?.let(configsRepository::setCloakConfig)
        configsRepository.setIsCloakEnabled(isCloakEnabled)
    }

    fun checkPermissionAndStartVpn(isGranted: Boolean) {
        if (isGranted) {
            startVpnService()
        } else {
            Unit // TODO Implement Toast logic or compose snackbar
        }
    }

    private fun startVpnService() {
        configsRepository.setIsOutlineEnabled(true)
        vpnManager.start()
    }

    private fun stopVpnService() {
        configsRepository.setIsOutlineEnabled(false)
        vpnManager.stop()
    }

    fun getAwgVersion(): String = awgManager.getAwgVersion()

    fun onAwgConnect(config: String) {
        viewModelScope.launch {
            checkVpnPermissionEvents.emit(Unit)
        }

        configsRepository.setAwgConfig(config)
        configsRepository.setIsAmneziaWGEnabled(true)
        awgManager.onAwgConnect()
    }

    fun onAwgDisconnect() {
        viewModelScope.launch {
            checkVpnPermissionEvents.emit(Unit)
        }

        configsRepository.setIsAmneziaWGEnabled(false)
        awgManager.onAwgDisconnect()
    }
}
