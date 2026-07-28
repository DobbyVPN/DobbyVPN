package com.dobby.feature.logging

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.domain.provideLogFilePath

class LoggerManagerImpl(
    private val logger: Logger,
) : LoggerManager {
    override fun initLogger() {
        val logFilePath = provideLogFilePath()
        logger.log("Init tunnel logging to the path: $logFilePath")
        GoBackendWrapper.initLogger(logFilePath.toString())
        logger.log("Remote telemetry is disabled; tunnel logs remain local")
    }

    override fun stopTelemetry() {
        logger.log("Remote telemetry is disabled; no tunnel exporter to stop")
    }
}
