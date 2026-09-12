package com.dobby.feature.logging

import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.domain.provideGoLogFilePath

class LoggerManagerImpl(
    private val logger: Logger,
) : LoggerManager {
    override fun initLogger(): Boolean {
        val logFilePath = provideGoLogFilePath()
        logger.log("Starting Go tunnel logger using local storage")
        if (!GoBackendWrapper.initLogger(logFilePath.toString())) {
            logger.log("[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            return false
        }
        logger.log("service_logger_init result=success state=ready")
        return true
    }
}
