package com.dobby.feature.logging.domain

import korlibs.time.DateTime
import kotlin.random.Random
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import okio.FileSystem
import okio.ForwardingFileSystem
import okio.Path
import okio.Sink
import okio.buffer
import okio.use

class LogsRepositoryCommonTest {
    private val temporaryPaths = mutableListOf<Path>()

    @AfterTest
    fun removeTemporaryLogs() {
        temporaryPaths.forEach { path ->
            runCatching { fileSystem.delete(path, mustExist = false) }
        }
        temporaryPaths.clear()
    }

    @Test
    fun legacy_lines_remain_visible_while_exports_keep_raw_records() {
        val path = temporaryLogPath("legacy")
        val timestamp = DateTime.now().format("yyyy-MM-dd HH:mm:ss")
        val legacy = "[$timestamp] [INFO] retained diagnostic"
        write(path, legacy)
        val repository = LogsRepository(path)

        assertTrue(repository.readUILogs().contains(legacy))
        assertTrue(repository.readAllLogs().contains(legacy))
    }

    @Test
    fun merges_producers_by_timestamp_and_clear_hides_older_external_records() {
        val primary = temporaryLogPath("primary")
        val goProducer = temporaryLogPath("go")
        val baseMillis = DateTime.now().unixMillisLong
        val goLine = encodeLogEvent(
            timestamp = timestampAt(baseMillis, -2_000),
            level = LogLevel.INFO,
            source = "go",
            event = "state.transition",
            message = "session state changed IDLE -> CONFIGURED",
        )
        val appLine = encodeLogEvent(
            timestamp = timestampAt(baseMillis, -1_000),
            level = LogLevel.DEBUG,
            source = "app",
            event = "log.message",
            message = "configuration accepted",
        )
        write(primary, appLine)
        write(goProducer, goLine)
        val repository = LogsRepository(primary, additionalLogFilePaths = listOf(goProducer))

        val merged = repository.readAllLogs()
        assertEquals(goLine, merged.first())
        assertEquals(appLine, merged[1])
        assertTrue(repository.readLogs(10).first().contains("IDLE -> CONFIGURED"))

        assertTrue(repository.clearLogs())
        val afterClear = repository.readAllLogs()
        assertTrue(afterClear.any { it.contains("\"event\":\"logs.cleared\"") })
        assertFalse(afterClear.any { it.contains("IDLE -> CONFIGURED") })
        assertFalse(afterClear.any { it.contains("configuration accepted") })
    }

    @Test
    fun same_second_records_keep_subsecond_cross_producer_order() {
        val primary = temporaryLogPath("primary-order")
        val goProducer = temporaryLogPath("go-order")
        val baseMillis = DateTime.now().unixMillisLong
        val laterApp = encodeLogEvent(
            timestamp = timestampAt(baseMillis, -100),
            level = LogLevel.INFO,
            source = "app",
            event = "action.end",
            message = "action completed",
        )
        val earlierGo = encodeLogEvent(
            timestamp = timestampAt(baseMillis, -900),
            level = LogLevel.INFO,
            source = "go",
            event = "state.transition",
            message = "state changed",
        )
        write(primary, laterApp)
        write(goProducer, earlierGo)

        val repository = LogsRepository(primary, additionalLogFilePaths = listOf(goProducer))
        val merged = repository.readAllLogs()
        assertTrue(merged.indexOf(earlierGo) < merged.indexOf(laterApp), merged.toString())
    }

    @Test
    fun retention_keeps_clear_marker_so_old_external_records_stay_hidden() {
        val primary = temporaryLogPath("primary-clear")
        val goProducer = temporaryLogPath("go-clear")
        val clear = encodeLogEvent(
            timestamp = "2000-01-02T00:00:00.000Z",
            level = LogLevel.INFO,
            source = "app",
            event = "logs.cleared",
            message = "Earlier diagnostic events were cleared from this view",
        )
        val oldGo = encodeLogEvent(
            timestamp = "2000-01-01T00:00:00.000Z",
            level = LogLevel.INFO,
            source = "go",
            event = "state.transition",
            message = "old external diagnostic",
        )
        write(primary, clear)
        write(goProducer, oldGo)

        val repository = LogsRepository(primary, additionalLogFilePaths = listOf(goProducer))
        assertTrue(read(primary).contains("\"event\":\"logs.cleared\""))
        assertFalse(repository.readAllLogs().any { it.contains("old external diagnostic") })
    }

    @Test
    fun missing_storage_is_created_without_degrading_diagnostics() {
        val path = temporaryLogPath("missing")
        val repository = LogsRepository(path)

        assertEquals(LogStorageStatus.READY, repository.storageStatus.value)
        assertTrue(fileSystem.exists(path))
    }

    @Test
    fun corrupt_directory_entry_degrades_without_crashing_startup() {
        val path = temporaryLogPath("corrupt")
        fileSystem.createDirectory(path)

        val repository = LogsRepository(path)

        assertEquals(LogStorageStatus.UNAVAILABLE, repository.storageStatus.value)
    }

    @Test
    fun unwritable_storage_degrades_without_crashing_startup() {
        val path = temporaryLogPath("unwritable")
        val repository = LogsRepository.withFileSystemForTesting(
            path,
            FailingSinkFileSystem(fileSystem, "permission denied"),
        )

        assertEquals(LogStorageStatus.UNAVAILABLE, repository.storageStatus.value)
    }

    @Test
    fun full_storage_degrades_without_crashing_startup() {
        val path = temporaryLogPath("full")
        val repository = LogsRepository.withFileSystemForTesting(
            path,
            FailingSinkFileSystem(fileSystem, "no space left"),
        )

        assertEquals(LogStorageStatus.UNAVAILABLE, repository.storageStatus.value)
    }

    @Test
    fun unreadable_storage_queries_degrade_without_crashing_startup() {
        val path = temporaryLogPath("unreadable-query")
        val repository = LogsRepository.withFileSystemForTesting(
            path,
            FailingMetadataFileSystem(fileSystem),
        )

        assertEquals(LogStorageStatus.UNAVAILABLE, repository.storageStatus.value)
        assertEquals(emptyList(), repository.readAllLogs())
    }

    private fun temporaryLogPath(label: String): Path =
        (FileSystem.SYSTEM_TEMPORARY_DIRECTORY / "dobby-$label-${Random.nextLong()}.jsonl")
            .also(temporaryPaths::add)

    private fun timestampAt(baseMillis: Long, offsetMillis: Long): String =
        DateTime.fromUnixMillis(baseMillis + offsetMillis)
            .format("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'")

    private fun write(path: Path, line: String) {
        fileSystem.sink(path).buffer().use { sink ->
            sink.writeUtf8(line)
            sink.writeUtf8("\n")
        }
    }

    private fun read(path: Path): String =
        fileSystem.source(path).buffer().use { source -> source.readUtf8() }

    private class FailingSinkFileSystem(
        delegate: FileSystem,
        private val reason: String,
    ) : ForwardingFileSystem(delegate) {
        override fun sink(file: Path, mustCreate: Boolean): Sink = error(reason)
        override fun appendingSink(file: Path, mustExist: Boolean): Sink = error(reason)
    }

    private class FailingMetadataFileSystem(delegate: FileSystem) : ForwardingFileSystem(delegate) {
        override fun metadataOrNull(path: Path) = error("metadata unavailable")
    }
}
