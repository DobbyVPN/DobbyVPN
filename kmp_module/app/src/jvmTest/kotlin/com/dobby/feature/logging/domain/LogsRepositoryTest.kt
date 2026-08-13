package com.dobby.feature.logging.domain

import java.nio.file.Files
import kotlin.concurrent.thread
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import okio.Path.Companion.toPath
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class LogsRepositoryTest {
    @Test
    fun discoversAnExistingLogFromItsDirectoryWithoutRecreatingIt() {
        val directory = Files.createTempDirectory("dobby-existing-log")
        val log = directory.resolve("app_logs.txt")
        Files.writeString(log, "retained")

        try {
            ensureLogFileEntry(directory, log)

            assertEquals("retained", Files.readString(log))
        } finally {
            Files.deleteIfExists(log)
            Files.deleteIfExists(directory)
        }
    }

    @Test
    fun createsAMissingLogAndRejectsANonFileEntry() {
        val directory = Files.createTempDirectory("dobby-new-log")
        val log = directory.resolve("app_logs.txt")
        val target = directory.resolve("target.txt")

        try {
            ensureLogFileEntry(directory, log)
            assertTrue(Files.isRegularFile(log))

            Files.delete(log)
            Files.createDirectory(log)
            assertFailsWith<IllegalStateException> { ensureLogFileEntry(directory, log) }
            Files.delete(log)

            Files.writeString(target, "target")
            Files.createSymbolicLink(log, target.fileName)
            assertFailsWith<IllegalStateException> { ensureLogFileEntry(directory, log) }
        } finally {
            Files.deleteIfExists(log)
            Files.deleteIfExists(target)
            Files.deleteIfExists(directory)
        }
    }

    @Test
    fun parsesOnlyOneBoundedEffectiveWindowsUserSid() {
        assertEquals(
            "S-1-5-21-1000-2000-3000-4000",
            parseWindowsUserSid("\"WORKSTATION\\user\",\"S-1-5-21-1000-2000-3000-4000\"\r\n"),
        )
        listOf(
            "S-1-5-21-1000",
            "\"user\",\"S-1-5-21-1000\" trailing",
            "\"user\",\"S-1-5-21-1000\"\n\"other\",\"S-1-5-18\"",
            "\"user\",\"S-1-5-21-1000 & injected\"",
        ).forEach { value ->
            assertFailsWith<IllegalStateException> { parseWindowsUserSid(value) }
        }
    }

    @Test
    fun parsesLocalizedSystemAccountWithoutAcceptingControlOutput() {
        assertEquals("NT-AUTORITÄT\\SYSTEM", parseWindowsAccountName("NT-AUTORITÄT\\SYSTEM"))
        listOf(
            "SYSTEM",
            "NT AUTHORITY\\SYSTEM\nextra",
            "",
            "D\\${"x".repeat(256)}",
        ).forEach { value ->
            assertFailsWith<IllegalStateException> { parseWindowsAccountName(value) }
        }
    }

    @Test
    fun concurrent_writes_remain_complete_and_ui_is_human_readable() {
        val path = Files.createTempFile("dobby-jsonl", ".log")
        val repository = LogsRepository(path.toString().toPath())
        val workers = (0 until 80).map { index ->
            thread(start = true) {
                repository.writeEvent(
                    level = if (index % 2 == 0) LogLevel.TRACE else LogLevel.INFO,
                    source = "kmp",
                    event = "test.concurrent",
                    message = "status worker=$index",
                    fields = mapOf("worker" to index.toString()),
                )
            }
        }
        workers.forEach { it.join() }

        val raw = repository.readAllLogs()
        assertEquals(83, raw.size)
        raw.forEach { line ->
            val event = Json.parseToJsonElement(line).jsonObject
            assertEquals("dobby.log/v1", event["schema"]?.toString()?.trim('"'))
        }
        val ui = repository.readUILogs()
        assertEquals(LogsRepository.UI_TAIL_LINES, ui.size)
        assertTrue(ui.all { it.startsWith("[") })
        assertFalse(ui.any { it.startsWith("{") })
    }
}
