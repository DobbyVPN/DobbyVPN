package com.dobby.feature.logging

import com.dobby.feature.logging.domain.LogsRepository
import interop.logger.LoggerLibrary
import java.nio.file.Files
import okio.Path.Companion.toPath
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LoggerManagerImplTest {
    @Test
    fun initLoggerForwardsTheOwnerOnlyServiceLogPath() {
        val appLog = Files.createTempFile("dobby-app-log", ".jsonl").toString().toPath()
        val serviceLog = Files.createTempFile("dobby-service-log", ".jsonl").toString().toPath()
        val library = RecordingLoggerLibrary()

        val result = LoggerManagerImpl(
            logger = Logger(LogsRepository(appLog)),
            loggerLibrary = library,
            goLogFilePath = { serviceLog },
        ).initLogger()

        assertTrue(result)
        assertEquals(listOf(serviceLog.toString()), library.paths)
    }

    @Test
    fun initLoggerFailsClosedWhenLocalStorageIsUnavailable() {
        val appLog = Files.createTempFile("dobby-app-log", ".jsonl").toString().toPath()
        val library = RecordingLoggerLibrary()

        val result = LoggerManagerImpl(
            logger = Logger(LogsRepository(appLog)),
            loggerLibrary = library,
            goLogFilePath = { error("sensitive local storage detail") },
        ).initLogger()

        assertFalse(result)
        assertEquals(emptyList(), library.paths)
        val logs = LogsRepository(appLog).readAllLogs().joinToString("\n")
        assertTrue(logs.contains("failure_code=LOCAL_STORAGE_UNAVAILABLE"))
        assertFalse(logs.contains("sensitive local storage detail"))
    }

    @Test
    fun initLoggerFailsClosedWhenTheServiceRejectsThePath() {
        val appLog = Files.createTempFile("dobby-app-log", ".jsonl").toString().toPath()
        val serviceLog = Files.createTempFile("dobby-service-log", ".jsonl").toString().toPath()
        val library = RecordingLoggerLibrary(fail = true)

        val result = LoggerManagerImpl(
            logger = Logger(LogsRepository(appLog)),
            loggerLibrary = library,
            goLogFilePath = { serviceLog },
        ).initLogger()

        assertFalse(result)
        assertEquals(listOf(serviceLog.toString()), library.paths)
        val logs = LogsRepository(appLog).readAllLogs().joinToString("\n")
        assertTrue(logs.contains("failure_code=SERVICE_RPC_FAILED"))
        assertFalse(logs.contains("sensitive service detail"))
    }
}

private class RecordingLoggerLibrary(private val fail: Boolean = false) : LoggerLibrary {
    val paths = mutableListOf<String>()

    override fun InitLogger(path: String) {
        paths += path
        if (fail) error("sensitive service detail")
    }

}
