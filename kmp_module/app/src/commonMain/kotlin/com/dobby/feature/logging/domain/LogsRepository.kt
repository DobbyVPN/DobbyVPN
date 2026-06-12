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
expect fun platformLogInfo(): String

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

        private const val LOG_RETENTION_MINUTES: Int = 2

        private const val LOG_TIMESTAMP_LENGTH: Int = 19
    }

    private var sentryLogger: SentryLogsRepository? = null

    init {
        if (!fileSystem.exists(logFilePath)) {
            fileSystem.sink(logFilePath).buffer().use { }
        }
        writeLog("[Platform] ${platformLogInfo()} logPath=$logFilePath")
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

    fun cleanupOldLogs() {
        if (!fileSystem.exists(logFilePath)) return

        runCatching {
            val cutoff = DateTime
                .fromUnixMillis(
                    DateTime.now().unixMillisLong -
                        LOG_RETENTION_MINUTES.toLong() * 60L * 1000L
                )
                .format("yyyy-MM-dd HH:mm:ss")
            val lines = readAllLogs()
            var keepContinuation = false
            val retained = lines.filter { line ->
                val timestamp = extractLogTimestamp(line)
                if (timestamp == null) {
                    keepContinuation
                } else {
                    keepContinuation = timestamp >= cutoff
                    keepContinuation
                }
            }

            if (retained.size != lines.size) {
                fileSystem.sink(logFilePath).buffer().use { sink ->
                    for (line in retained) {
                        sink.writeUtf8(line)
                        sink.writeUtf8("\n")
                    }
                }

                logEventsChannel.clear()
                CoroutineScope(Dispatchers.Default).launch {
                    logEventsChannel.emitLogs(retained.takeLast(UI_TAIL_LINES))
                }
            }

            writeLog(
                "[Logs] cleanup retentionMinutes=$LOG_RETENTION_MINUTES " +
                    "linesBefore=${lines.size} linesAfter=${retained.size} " +
                    "removed=${lines.size - retained.size}"
            )
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

    private fun extractLogTimestamp(line: String): String? {
        if (line.length < LOG_TIMESTAMP_LENGTH + 2) return null
        if (line.first() != '[' || line[LOG_TIMESTAMP_LENGTH + 1] != ']') return null

        val timestamp = line.substring(1, LOG_TIMESTAMP_LENGTH + 1)
        val timestampLooksValid =
            timestamp.length == LOG_TIMESTAMP_LENGTH &&
                timestamp[4] == '-' &&
                timestamp[7] == '-' &&
                timestamp[10] == ' ' &&
                timestamp[13] == ':' &&
                timestamp[16] == ':'

        return if (timestampLooksValid) timestamp else null
    }

}
