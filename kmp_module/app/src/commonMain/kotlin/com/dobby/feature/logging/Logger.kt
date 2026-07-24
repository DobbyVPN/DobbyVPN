package com.dobby.feature.logging

import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.redactLog

class Logger(
    private val logsRepository: LogsRepository
) {

    fun log(message: String) {
        logsRepository.writeLog(redactLog(message))
    }
}
