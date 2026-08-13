package com.dobby.feature.logging.domain

import com.dobby.vpn.BuildConfig
import korlibs.time.DateTime
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import okio.Buffer
import okio.FileSystem
import okio.Path
import okio.buffer
import okio.use

expect val fileSystem: FileSystem
expect fun provideLogFilePath(): Path
expect fun provideGoLogFilePath(): Path
expect fun provideAdditionalLogFilePaths(): List<Path>
expect fun platformLogInfo(): String
expect fun platformLogStorageInitializationAvailable(): Boolean
expect fun <T> withLogWriteLock(block: () -> T): T

fun maskStr(input: String): String = if (input.isEmpty()) "" else "[REDACTED]"

class LogsRepository private constructor(
    private val logFilePath: Path = provideLogFilePath(),
    additionalLogFilePaths: List<Path> = emptyList(),
    private val storageFileSystem: FileSystem = fileSystem,
) {
    constructor(
        logFilePath: Path = provideLogFilePath(),
        additionalLogFilePaths: List<Path> = emptyList(),
    ) : this(logFilePath, additionalLogFilePaths, fileSystem)

    companion object {
        const val UI_TAIL_LINES: Int = 50
        const val EXPORT_TAIL_LINES: Int = -1
        private const val LOG_RETENTION_HOURS: Int = 48

        internal fun withFileSystemForTesting(
            logFilePath: Path,
            storageFileSystem: FileSystem,
        ): LogsRepository = LogsRepository(logFilePath, emptyList(), storageFileSystem)
    }

    private val producerLogPaths = (listOf(logFilePath) + additionalLogFilePaths).distinct()
    private val _storageStatus = MutableStateFlow(LogStorageStatus.READY)
    val storageStatus: StateFlow<LogStorageStatus> = _storageStatus.asStateFlow()

    init {
        val initialized = platformLogStorageInitializationAvailable() && runCatching {
            withLogWriteLock {
                if (!storageFileSystem.exists(logFilePath)) {
                    storageFileSystem.sink(logFilePath).buffer().use { }
                }
            }
        }.onFailure { degradeStorage("initialize", it) }.isSuccess
        if (!initialized && _storageStatus.value == LogStorageStatus.READY) {
            degradeStorage("initialize", IllegalStateException("platform log storage unavailable"))
        }
        if (initialized) {
            cleanupOldLogs(writeDiagnostic = false)
            writeEvent(
                level = LogLevel.INFO,
                source = "app",
                event = "logger.ready",
                message = "Owner-only local diagnostic storage is ready",
                fields = mapOf("producer_count" to producerLogPaths.size.toString()),
            )
            writeLog("[Platform] ${platformLogInfo()}")
            writeLog(
                "[Build] version=${BuildConfig.VERSION_NAME} " +
                    "versionCode=${BuildConfig.VERSION_CODE} " +
                    "commit=${BuildConfig.PROJECT_REPOSITORY_COMMIT}",
            )
        }
    }

    fun writeLog(log: String) {
        writeEvent(
            level = LogLevel.fromLegacyMessage(log),
            source = "app",
            event = "log.message",
            message = log,
        )
    }

    fun writeEvent(
        level: LogLevel,
        source: String,
        event: String,
        message: String,
        fields: Map<String, String> = emptyMap(),
    ) {
        val logEntry = encodeLogEvent(
            timestamp = DateTime.now().format("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'"),
            level = level,
            source = source,
            event = event,
            message = message,
            fields = fields,
        )
        runCatching {
            withLogWriteLock {
                val record = Buffer().writeUtf8(logEntry).writeUtf8("\n")
                storageFileSystem.appendingSink(logFilePath).use { sink ->
                    sink.write(record, record.size)
                    sink.flush()
                }
            }
        }.onFailure { degradeStorage("write", it) }
    }

    fun clearLogs(): Boolean = runCatching {
        withLogWriteLock {
            storageFileSystem.sink(logFilePath).buffer().use { }
        }
        writeEvent(
            level = LogLevel.INFO,
            source = "app",
            event = "logs.cleared",
            message = "Earlier diagnostic events were cleared from this view",
        )
        check(_storageStatus.value == LogStorageStatus.READY)
    }.fold(
        onSuccess = { true },
        onFailure = {
            degradeStorage("clear", it)
            false
        },
    )

    fun cleanupOldLogs() {
        cleanupOldLogs(writeDiagnostic = true)
    }

    private fun cleanupOldLogs(writeDiagnostic: Boolean) {
        runCatching {
            if (!storageFileSystem.exists(logFilePath)) return
            val cutoff = DateTime
                .fromUnixMillis(DateTime.now().unixMillisLong - LOG_RETENTION_HOURS.toLong() * 60L * 60L * 1000L)
                .format("yyyy-MM-dd HH:mm:ss")
            val records = readRecords(logFilePath, producerIndex = 0)
            val latestClearIndex = records.indexOfLast { it.lines.firstOrNull()?.let(::logEventName) == "logs.cleared" }
            val retainedRecords = records.filterIndexed { index, record ->
                index == latestClearIndex || record.timestamp?.let { it >= cutoff } == true
            }
            val linesBefore = records.sumOf { it.lines.size }
            val retained = retainedRecords.flatMap { it.lines }
            if (retained.size != linesBefore) {
                withLogWriteLock {
                    storageFileSystem.sink(logFilePath).buffer().use { sink ->
                        retained.forEach { line ->
                            sink.writeUtf8(line)
                            sink.writeUtf8("\n")
                        }
                    }
                }
            }
            if (writeDiagnostic) {
                writeEvent(
                    level = LogLevel.DEBUG,
                    source = "app",
                    event = "logs.cleanup",
                    message = "Log retention cleanup completed",
                    fields = mapOf(
                        "retention_hours" to LOG_RETENTION_HOURS.toString(),
                        "lines_before" to linesBefore.toString(),
                        "lines_after" to retained.size.toString(),
                        "removed" to (linesBefore - retained.size).toString(),
                    ),
                )
            }
        }.onFailure { degradeStorage("cleanup", it) }
    }

    /** Raw JSONL/legacy records for support export. */
    fun readAllLogs(): List<String> = readMergedRaw(EXPORT_TAIL_LINES)

    /** Human rendering for the app and CLI; raw records remain unchanged on disk. */
    fun readLogs(limit: Int): List<String> = readMergedRaw(limit).map(::renderLogLine)

    fun readUILogs(): List<String> = readLogs(UI_TAIL_LINES)

    private fun readMergedRaw(limit: Int): List<String> {
        val records = producerLogPaths.flatMapIndexed { producerIndex, path ->
            readRecords(path, producerIndex)
        }
        val clearTimestamp = records
            .asSequence()
            .filter { it.producerIndex == 0 && it.lines.firstOrNull()?.let(::logEventName) == "logs.cleared" }
            .mapNotNull { it.timestamp }
            .maxOrNull()
        val merged = records
            .asSequence()
            .filter { record -> clearTimestamp == null || record.timestamp?.let { it >= clearTimestamp } == true }
            .sortedWith(
                compareBy<StoredRecord> { it.timestamp.orEmpty() }
                    .thenBy { it.producerIndex }
                    .thenBy { it.recordIndex },
            )
            .flatMap { it.lines.asSequence() }
            .toList()
        return if (limit <= 0) merged else merged.takeLast(limit)
    }

    private fun readRecords(path: Path, producerIndex: Int): List<StoredRecord> {
        return runCatching {
            if (!storageFileSystem.exists(path)) return@runCatching emptyList()
            withLogWriteLock {
                storageFileSystem.source(path).buffer().use { source ->
                    val records = mutableListOf<StoredRecord>()
                    var current: StoredRecord? = null
                    while (true) {
                        val line = source.readUtf8Line() ?: break
                        if (line.isNotBlank()) {
                            current = appendRecord(records, current, line, producerIndex)
                        }
                    }
                    records
                }
            }
        }.getOrElse {
            degradeStorage("read", it)
            emptyList()
        }
    }

    private fun appendRecord(
        records: MutableList<StoredRecord>,
        current: StoredRecord?,
        line: String,
        producerIndex: Int,
    ): StoredRecord {
        val timestamp = comparableLogTimestamp(line)
        if (timestamp == null && current != null && !line.startsWith("{")) {
            current.lines += line
            return current
        }
        return StoredRecord(
            timestamp = timestamp,
            producerIndex = producerIndex,
            recordIndex = records.size,
            lines = mutableListOf(line),
        ).also(records::add)
    }

    private data class StoredRecord(
        val timestamp: String?,
        val producerIndex: Int,
        val recordIndex: Int,
        val lines: MutableList<String>,
    )

    private fun degradeStorage(operation: String, failure: Throwable) {
        _storageStatus.value = LogStorageStatus.UNAVAILABLE
        reportLogFailure(operation, failure)
    }
}

enum class LogStorageStatus {
    READY,
    UNAVAILABLE,
}

private fun reportLogFailure(operation: String, failure: Throwable) {
    println(
        "DobbyVPN local log $operation failed " +
            "failureType=${failure::class.simpleName ?: "Throwable"}",
    )
}
