// LogsRepository.kt
package com.dobby.feature.logging.domain

import korlibs.time.DateTime
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import okio.FileSystem
import okio.Path
import okio.buffer
import okio.use
import org.koin.compose.koinInject

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
    private val logFilePath: Path = provideLogFilePath(),
    private val logEventsChannel: LogEventsChannel
) {
    companion object {
        const val UI_TAIL_LINES: Int = 50

        const val EXPORT_TAIL_LINES: Int = -1
    }

    private var sentryLogger: SentryLogsRepository? = null

    init {
        if (!fileSystem.exists(logFilePath)) {
            fileSystem.sink(logFilePath).buffer().use { }
        }
        CoroutineScope(Dispatchers.Default).launch {
            logEventsChannel.emitLogs(readLogs(UI_TAIL_LINES))
        }
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
            CoroutineScope(Dispatchers.Default).launch {
                logEventsChannel.emitLog(logEntry)
            }
        }.onFailure { it.printStackTrace() }
    }

    fun clearLogs() {
        runCatching {
            fileSystem.sink(logFilePath).buffer().use { }
            logEventsChannel.clear()
        }.onFailure { it.printStackTrace() }
    }

    fun readAllLogs(): List<String> = readLogs(EXPORT_TAIL_LINES)

    fun readUILogs(): List<String> = readLogs(UI_TAIL_LINES)

    private fun readLogs(limit: Int): List<String> {
        if (!fileSystem.exists(logFilePath)) return emptyList()

        return runCatching {
            fileSystem.source(logFilePath).buffer().use { source ->

                if (limit <= 0) {
                    val result = mutableListOf<String>()
                    while (true) {
                        val line = source.readUtf8Line() ?: break
                        if (line.isNotBlank()) {
                            result.add(line)
                        }
                    }
                    return@use result
                }

                val deque = ArrayDeque<String>(limit)

                while (true) {
                    val line = source.readUtf8Line() ?: break
                    if (line.isNotBlank()) {
                        if (deque.size == limit) {
                            deque.removeFirst()
                        }
                        deque.addLast(line)
                    }
                }

                deque.toList()
            }
        }.getOrElse {
            it.printStackTrace()
            emptyList()
        }
    }

}
