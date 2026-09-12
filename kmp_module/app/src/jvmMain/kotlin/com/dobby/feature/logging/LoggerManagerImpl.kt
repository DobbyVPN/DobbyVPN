package com.dobby.feature.logging

import com.dobby.feature.logging.domain.provideGoLogFilePath
import interop.logger.LoggerLibrary
import okio.Path

class LoggerManagerImpl(
    private val logger: Logger,
    private val loggerLibrary: LoggerLibrary,
    private val goLogFilePath: () -> Path = ::provideGoLogFilePath,
) : LoggerManager {
    override fun initLogger(): Boolean {
        val logFilePath = goLogFilePath()

        logger.log("Starting Go tunnel logger using local storage")
        loggerLibrary.InitLogger(logFilePath.toString())
        logger.log("service_logger_init result=success state=ready")
        return true
    }
}
