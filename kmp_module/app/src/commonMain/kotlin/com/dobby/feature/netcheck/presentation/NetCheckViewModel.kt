package com.dobby.feature.netcheck.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.netcheck.domain.NetCheckRepository
import com.dobby.feature.netcheck.ui.NetCheckStatus
import com.dobby.feature.netcheck.ui.NetCheckUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

class NetCheckViewModel(
    private val netCheckRepository: NetCheckRepository,
    private val netCheckManager: NetCheckManager,
) : ViewModel() {

    private val _uiState = MutableStateFlow(NetCheckUiState())
    val uiState: StateFlow<NetCheckUiState> = _uiState.asStateFlow()

    init {
        _uiState.update {
            NetCheckUiState(
                netCheckConfig = netCheckRepository.getConfig(),
                netCheckConfigPath = netCheckRepository.getConfigPath(),
                netCheckStatus = NetCheckStatus.OFF,
            )
        }
    }

    fun updateConfig(config: String) {
        netCheckRepository.setConfig(config)

        _uiState.update {
            NetCheckUiState(
                netCheckConfig = config,
                netCheckConfigPath = it.netCheckConfigPath,
                netCheckStatus = NetCheckStatus.OFF,
            )
        }
    }

    fun onButtonClicked() {
        when (uiState.value.netCheckStatus) {
            NetCheckStatus.ON -> turnOff()
            NetCheckStatus.OFF, NetCheckStatus.FAILED -> turnOn()
        }
    }

    private fun turnOn() {
        val error = netCheckManager.start(_uiState.value.netCheckConfigPath)

        if (error == "") {
            _uiState.update {
                NetCheckUiState(
                    netCheckConfig = it.netCheckConfig,
                    netCheckConfigPath = it.netCheckConfigPath,
                    netCheckStatus = NetCheckStatus.ON,
                )
            }
        } else {
            _uiState.update {
                NetCheckUiState(
                    netCheckConfig = it.netCheckConfig,
                    netCheckConfigPath = it.netCheckConfigPath,
                    netCheckStatus = NetCheckStatus.FAILED,
                )
            }
        }
    }

    private fun turnOff() {
        netCheckManager.cancel()

        _uiState.update {
            NetCheckUiState(
                netCheckConfig = netCheckRepository.getConfig(),
                netCheckConfigPath = netCheckRepository.getConfigPath(),
                netCheckStatus = NetCheckStatus.OFF,
            )
        }
    }
}
