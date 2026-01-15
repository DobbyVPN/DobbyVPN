// LogsRepository.kt
package com.dobby.feature.logging.domain

import korlibs.time.DateTime
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

fun maskStr(input: String): String {
    val prefixes = listOf("http://", "https://")
    val prefix = prefixes.firstOrNull { input.startsWith(it) } ?: ""

    val rest = input.removePrefix(prefix)

    if (rest.length <= 2) {
        return prefix + rest
    }

    return prefix + "${rest.first()}***${rest.last()}"
}

class LogsRepository(
    private val logFilePath: Path = provideLogFilePath()
) {
    companion object {
        private const val UI_TAIL_LINES: Int = 50

        private const val EXPORT_TAIL_LINES: Int = 50
    }

    private val _logState = MutableStateFlow<List<String>>(emptyList())
    val logState: StateFlow<List<String>> = _logState.asStateFlow()
    private var sentryLogger: SentryLogsRepository? = null

    init {
        if (!fileSystem.exists(logFilePath)) {
            fileSystem.sink(logFilePath).buffer().use { }
        }
        _logState.value = readLogs(UI_TAIL_LINES)
    }

    fun setSentryLogger(_sentryLogger: SentryLogsRepository) : LogsRepository {
        sentryLogger = _sentryLogger
        return this
    }

    fun getSentryLogger(): SentryLogsRepository? {
        return sentryLogger
    }

    fun writeLog(log: String) {
        val now = DateTime.now().format("yyyy-MM-dd HH:mm:ss")
        val logEntry = "[$now] $log"

        runCatching {
            fileSystem.appendingSink(logFilePath).buffer().use { sink ->
                sink.writeUtf8(logEntry)
                sink.writeUtf8("\n")
            }
            _logState.update { (it + logEntry).takeLast(UI_TAIL_LINES) }
        }.onFailure { it.printStackTrace() }
    }

    fun clearLogs() {
        runCatching {
            fileSystem.sink(logFilePath).buffer().use { }
            _logState.value = emptyList()
        }.onFailure { it.printStackTrace() }
    }

    fun readAllLogs(): List<String> = readLogs(EXPORT_TAIL_LINES)

    private fun readLogs(limit: Int): List<String> {
        if (!fileSystem.exists(logFilePath)) return emptyList()

        val deque = ArrayDeque<String>(limit)

        return runCatching {
            fileSystem.source(logFilePath).buffer().use { source ->
                while (true) {
                    val line = source.readUtf8Line() ?: break
                    if (line.isNotBlank()) {
                        if (deque.size == limit) {
                            deque.removeFirst()
                        }
                        deque.addLast(line)
                    }
                }
            }
            deque.toList()
        }.getOrElse {
            it.printStackTrace()
            emptyList()
        }
    }
}
