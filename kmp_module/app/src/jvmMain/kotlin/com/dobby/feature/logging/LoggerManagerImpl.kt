package com.dobby.feature.logging

import com.dobby.feature.logging.domain.provideLogFilePath
import interop.logger.LoggerLibrary

class LoggerManagerImpl(
    private val logger: Logger,
    private val loggerLibrary: LoggerLibrary,
) : LoggerManager {
    override fun initLogger() {
        val logFilePath = provideLogFilePath()

        logger.log("Init tunnel logging to the path: $logFilePath")
        loggerLibrary.InitLogger(logFilePath.toString())
        logger.log("Remote telemetry is disabled; tunnel logs remain local")
    }

    override fun stopTelemetry() {
        logger.log("Remote telemetry is disabled; no tunnel exporter to stop")
    }
}
