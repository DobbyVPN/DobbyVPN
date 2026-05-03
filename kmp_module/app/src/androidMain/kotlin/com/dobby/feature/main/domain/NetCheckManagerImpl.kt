package com.dobby.feature.main.domain

import com.dobby.backend.LoggerBackendWrapper
import com.dobby.backend.NetCheckBackendWrapper
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.netcheck.domain.provideNetCheckConfigPath
import com.dobby.feature.netcheck.presentation.NetCheckManager

class NetCheckManagerImpl(
    private val configsRepository: DobbyConfigsRepository
): NetCheckManager {
    override fun start(): String {
        val path = provideLogFilePath().toString()
        LoggerBackendWrapper.initLogger(path)
        val endpoint = configsRepository.getTelemetryEndpoint()
        if (endpoint.isNotBlank()) {
            LoggerBackendWrapper.initTelemetry(endpoint)
        }
        val configPath = provideNetCheckConfigPath().toString()
        return NetCheckBackendWrapper.netCheck(configPath) ?: ""
    }

    override fun cancel() {
        NetCheckBackendWrapper.cancelNetCheck()
    }
}
