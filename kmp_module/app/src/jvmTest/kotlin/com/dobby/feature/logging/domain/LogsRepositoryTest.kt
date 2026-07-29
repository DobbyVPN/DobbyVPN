package com.dobby.feature.logging.domain

import java.nio.file.Files
import kotlin.concurrent.thread
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import okio.Path.Companion.toPath
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LogsRepositoryTest {
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
