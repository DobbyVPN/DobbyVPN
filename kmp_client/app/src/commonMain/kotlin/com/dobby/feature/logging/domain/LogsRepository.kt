// LogsRepository.kt
package com.dobby.feature.logging.domain

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import okio.FileSystem
import okio.Path
import okio.buffer
import okio.use

expect val fileSystem: FileSystem
expect fun provideLogFilePath(): Path

interface SentryLogsRepository{
    fun log(string: String) {
        println(string)
    }
}

class LogsRepository(
    private val logFilePath: Path = provideLogFilePath()
) {
    private val _logState = MutableStateFlow<List<String>>(emptyList())
    val logState: StateFlow<List<String>> = _logState.asStateFlow()
    private var sentryLogger: SentryLogsRepository? = null

    init {
        if (!fileSystem.exists(logFilePath)) {
            fileSystem.sink(logFilePath).buffer().use { }
        }
        _logState.value = readLogs()
    }

    fun setSentryLogger(_sentryLogger: SentryLogsRepository) : LogsRepository {
        sentryLogger = _sentryLogger
        return this
    }

    fun getSentryLogger(): SentryLogsRepository? {
        return sentryLogger
    }

    fun writeLog(log: String) {
        runCatching {
            fileSystem.appendingSink(logFilePath).buffer().use { sink ->
                sink.writeUtf8(log)
                sink.writeUtf8("\n")
            }
            _logState.update { it + log }
        }.onFailure { it.printStackTrace() }
    }

    fun clearLogs() {
        runCatching {
            if (fileSystem.exists(logFilePath)) {
                fileSystem.delete(logFilePath)
            }
            fileSystem.sink(logFilePath).buffer().use { }
            _logState.value = emptyList()
        }.onFailure { it.printStackTrace() }
    }

    fun readAllLogs(): List<String> = readLogs()

    private fun readLogs(): List<String> {
        return runCatching {
            if (!fileSystem.exists(logFilePath)) return emptyList()
            fileSystem.source(logFilePath).buffer().use { source ->
                source.readUtf8().lines().filter { it.isNotBlank() }
            }
        }.getOrElse {
            it.printStackTrace()
            emptyList()
        }
    }
}
