package com.dobby.feature.logging

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.domain.provideGoLogFilePath

class LoggerManagerImpl(
    private val logger: Logger,
) : LoggerManager {
    override fun initLogger(): Boolean {
        return try {
            val logFilePath = provideGoLogFilePath()
            logger.log("Starting Go tunnel logger using owner-only local storage")
            if (!GoBackendWrapper.initLogger(logFilePath.toString())) {
                logger.log("[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
                return false
            }
            logger.log("service_logger_init result=success state=ready")
            true
        } catch (_: Exception) {
            logger.log("[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_UNAVAILABLE")
            false
        }
    }
}
