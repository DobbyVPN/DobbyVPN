package com.dobby.feature.main.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.provideLogFilePath
import interop.logger.LoggerLibrary

internal class LoggerManagerImpl(
    private val logger: Logger,
    private val loggerLibrary: LoggerLibrary,
) : LoggerManager {

    override fun initLogger() {
        val logFilePath = provideLogFilePath()
        logger.log("Init tunnel logging to the path: $logFilePath")
        loggerLibrary.InitLogger(logFilePath.toString())
    }

    override fun initTelemetry(endpoint: String) {
        logger.log("Init tunnel telemetry to the endpoint: $endpoint")
        loggerLibrary.InitTelemetry(endpoint)
    }
}
