package com.dobby.feature.logging

import com.dobby.feature.logging.domain.LogsRepository

class Logger(
    private val logsRepository: LogsRepository
) {

    fun log(message: String) {
        logsRepository.writeLog(message)
    }
}
