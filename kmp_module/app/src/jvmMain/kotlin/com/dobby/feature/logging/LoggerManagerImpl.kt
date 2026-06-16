package com.dobby.feature.logging

import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository
import interop.logger.LoggerLibrary

class LoggerManagerImpl(
    private val logger: Logger,
    private val loggerLibrary: LoggerLibrary,
    private val configsRepository: DobbyConfigsRepository,
) : LoggerManager {
    override fun initLogger() {
        val logFilePath = provideLogFilePath()
        val endpoint = configsRepository.getTelemetryEndpoint()
        val token = configsRepository.getTelemetryApiToken()
        val config = configsRepository.getTelemetryAttributes()

        logger.log("Init tunnel logging to the path: $logFilePath")
        loggerLibrary.InitLogger(logFilePath.toString())

        logger.log("Init tunnel telemetry to the endpoint=$endpoint, token.len=${token.length}")
        if (endpoint.isNotBlank()) {
            loggerLibrary.InitTelemetry(endpoint, token)
            logger.log("Initialized tunnel telemetry")
        } else {
            logger.log("No telemetry endpoint provided")
        }

        logger.log("Setup telemetry attributes")
        if (config.isNotBlank()) {
            loggerLibrary.SetupTelemetryAttributes(config)
            logger.log("Setup tunnel telemetry attributes")
        } else {
            logger.log("No telemetry attributes provided")
        }
    }
}
