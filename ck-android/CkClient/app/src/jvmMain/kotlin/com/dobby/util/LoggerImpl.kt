package com.dobby.util

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository

class LoggerImpl(
    private val logsRepository: LogsRepository
) : Logger {

    override fun log(message: String) {
        logsRepository.writeLog(message)
    }
}
