package com.dobby.feature.logging

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository

class LoggerManagerImpl(
    private val logger: Logger,
    private val configsRepository: DobbyConfigsRepository,
) : LoggerManager {
    override fun init() {
        val logFilePath = provideLogFilePath()
        val endpoint = configsRepository.getTelemetryEndpoint()
        val token = configsRepository.getTelemetryApiToken()
        val config = configsRepository.getTelemetryAttributes()

        logger.log("Init tunnel logging to the path: $logFilePath")
        GoBackendWrapper.initLogger(logFilePath.toString())

        logger.log("Init tunnel telemetry to the endpoint=$endpoint, token.len=${token.length}")
        if (endpoint.isNotBlank()) {
            GoBackendWrapper.initTelemetry(endpoint, token)
            logger.log("Initialized tunnel telemetry")
        } else {
            logger.log("No telemetry endpoint provided")
        }

        logger.log("Setup telemetry attributes")
        if (config.isNotBlank()) {
            GoBackendWrapper.setupTelemetryAttributes(config)
            logger.log("Setup tunnel telemetry attributes")
        } else {
            logger.log("No telemetry attributes provided")
        }
    }
}
