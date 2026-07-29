package com.dobby.feature.logging

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.domain.provideGoLogFilePath

class LoggerManagerImpl(
    private val logger: Logger,
) : LoggerManager {
    override fun initLogger() {
        val logFilePath = provideGoLogFilePath()
        logger.log("Starting Go tunnel logger using owner-only local storage")
        GoBackendWrapper.initLogger(logFilePath.toString())
        logger.log("Go tunnel logger initialization returned")
        logger.log("Remote telemetry is disabled; tunnel logs remain local")
    }

    override fun stopTelemetry() {
        logger.log("Remote telemetry is disabled; no tunnel exporter to stop")
    }
}
