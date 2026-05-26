package com.dobby.feature.netcheck

import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.netcheck.domain.provideNetCheckConfigPath
import com.dobby.feature.netcheck.presentation.NetCheckManager
import interop.logger.LoggerLibrary
import interop.netcheck.NetCheckLibrary

class NetCheckManagerImpl(
    private val loggerLibrary: LoggerLibrary,
    private val netCheckLibrary: NetCheckLibrary,
    private val configsRepository: DobbyConfigsRepository,
) : NetCheckManager {
    override fun startNetCheck(): String {
        val path = provideLogFilePath().toString()
        loggerLibrary.InitLogger(path)
        val endpoint = configsRepository.getTelemetryEndpoint()
        if (endpoint.isNotBlank()) {
            loggerLibrary.InitTelemetry(endpoint)
        }
        val configPath = provideNetCheckConfigPath().toString()
        return netCheckLibrary.NetCheck(configPath)
    }

    override fun cancelNetCheck() {
        netCheckLibrary.CancelNetCheck()
    }
}
