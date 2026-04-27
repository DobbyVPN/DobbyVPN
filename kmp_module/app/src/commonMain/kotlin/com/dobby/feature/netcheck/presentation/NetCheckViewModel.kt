package com.dobby.feature.netcheck.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.NetCheckTomlConfigs
import com.dobby.feature.netcheck.domain.NetCheckRepository
import com.dobby.feature.netcheck.ui.NetCheckStatus
import com.dobby.feature.netcheck.ui.NetCheckUiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.decodeFromString
import net.peanuuutz.tomlkt.Toml

class NetCheckViewModel(
    private val logger: Logger,
    private val netCheckManager: NetCheckManager,
    private val netCheckRepository: NetCheckRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(NetCheckUiState())
    val uiState: StateFlow<NetCheckUiState> = _uiState.asStateFlow()

    init {
        _uiState.update {
            NetCheckUiState(
                tomlConfig = "",
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
            )
        }
    }

    fun update(tomlConfig: String) {
        _uiState.update {
            NetCheckUiState(
                tomlConfig = tomlConfig,
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
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
        logger.log("Got congih: length=${_uiState.value.tomlConfig.length}, parsing")
        val tomlConfig = try {
            Toml.decodeFromString<NetCheckTomlConfigs>(_uiState.value.tomlConfig)
        } catch (e: Exception) {
            _uiState.update {
                NetCheckUiState(
                    tomlConfig = it.tomlConfig,
                    netCheckStatus = NetCheckStatus.FAILED,
                    description = "Failed to parse TOML config: $e"
                )
            }

            return
        }

        netCheckRepository.setConfig(tomlConfig.ConfigValue)

        logger.log("Starting network check")
        val error = netCheckManager.start(netCheckRepository.getConfigPath())

        if (error == "") {
            _uiState.update {
                NetCheckUiState(
                    tomlConfig = it.tomlConfig,
                    netCheckStatus = NetCheckStatus.ON,
                    description = ""
                )
            }
        } else {
            _uiState.update {
                NetCheckUiState(
                    tomlConfig = it.tomlConfig,
                    netCheckStatus = NetCheckStatus.FAILED,
                    description = error
                )
            }
        }
    }

    private fun turnOff() {
        netCheckManager.cancel()

        _uiState.update {
            NetCheckUiState(
                tomlConfig = it.tomlConfig,
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
            )
        }
    }
}
