package com.dobby.feature.logging

import com.dobby.feature.logging.domain.LogLevel
import com.dobby.feature.logging.domain.LogsRepository

class Logger(
    private val logsRepository: LogsRepository,
) {
    fun trace(message: String) = write(LogLevel.TRACE, message)
    fun debug(message: String) = write(LogLevel.DEBUG, message)
    fun log(message: String) = write(LogLevel.fromLegacyMessage(message), message)
    fun info(message: String) = write(LogLevel.INFO, message)
    fun warn(message: String) = write(LogLevel.WARN, message)
    fun error(message: String) = write(LogLevel.ERROR, message)

    private fun write(level: LogLevel, message: String) {
        logsRepository.writeEvent(
            level = level,
            source = "kmp",
            event = "log.message",
            message = message,
        )
    }
}
