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
        val logFilePath = try {
            goLogFilePath()
        } catch (_: Exception) {
            logger.log("[ERROR] service_logger_init result=failed failure_code=LOCAL_STORAGE_UNAVAILABLE")
            return false
        }

        logger.log("Starting Go tunnel logger using owner-only local storage")
        try {
            loggerLibrary.InitLogger(logFilePath.toString())
        } catch (_: Exception) {
            logger.log("[ERROR] service_logger_init result=failed failure_code=SERVICE_RPC_FAILED")
            return false
        }
        logger.log("service_logger_init result=success state=ready")
        return true
    }
}
