package com.dobby.feature.netcheck.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepositoryNetCheck
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
    private val configsRepository: DobbyConfigsRepository,
    private val netCheckManager: NetCheckManager,
    private val netCheckRepository: NetCheckRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(NetCheckUiState())
    val uiState: StateFlow<NetCheckUiState> = _uiState.asStateFlow()

    init {
        _uiState.update {
            it.copy(
                tomlConfig = configsRepository.getNetCheckConfig(),
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
            )
        }
    }

    fun update(tomlConfig: String) {
        _uiState.update {
            it.copy(
                tomlConfig = tomlConfig,
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
            )
        }
        configsRepository.setNetCheckConfig(tomlConfig)
    }

    fun onButtonClicked() {
        when (uiState.value.netCheckStatus) {
            NetCheckStatus.ON -> turnOff()
            NetCheckStatus.OFF, NetCheckStatus.FAILED -> turnOn()
        }
    }

    private fun turnOn() {
        logger.log("Got congih: length=${configsRepository.getNetCheckConfig().length}, parsing")
        val tomlConfig = try {
            Toml.decodeFromString<NetCheckTomlConfigs>(configsRepository.getNetCheckConfig())
        } catch (e: Exception) {
            _uiState.update {
                it.copy(
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
                it.copy(
                    netCheckStatus = NetCheckStatus.ON,
                    description = ""
                )
            }
        } else {
            _uiState.update {
                it.copy(
                    netCheckStatus = NetCheckStatus.FAILED,
                    description = error
                )
            }
        }
    }

    private fun turnOff() {
        netCheckManager.cancel()

        _uiState.update {
            it.copy(
                netCheckStatus = NetCheckStatus.OFF,
                description = ""
            )
        }
    }
}
