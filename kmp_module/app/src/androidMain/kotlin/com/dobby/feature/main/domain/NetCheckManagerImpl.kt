package com.dobby.feature.main.domain

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.netcheck.domain.provideNetCheckConfigPath
import com.dobby.feature.netcheck.presentation.NetCheckManager

class NetCheckManagerImpl(
    private val logger: Logger,
    private val configsRepository: DobbyConfigsRepository,
): NetCheckManager {
    fun enableTunnelLogging() {
        val logFilePath = provideLogFilePath()
        logger.log("Init tunnel logging to the path: $logFilePath")
        GoBackendWrapper.initLogger(logFilePath.toString())
    }

    fun enableTunnelTelemetry() {
        val endpoint = configsRepository.getTelemetryEndpoint()
        logger.log("Init tunnel telemetry to the endpoint: $endpoint")
        if (endpoint.isNotBlank()) {
            GoBackendWrapper.initTelemetry(endpoint)
        } else {
            logger.log("No telemetry endpoint provided")
        }
    }

    override fun start(configPath: String): String {
        enableTunnelLogging()
        enableTunnelTelemetry()
        return GoBackendWrapper.netCheck(provideNetCheckConfigPath().toString()) ?: ""
    }

    override fun cancel() {
        GoBackendWrapper.cancelNetCheck()
    }
}
