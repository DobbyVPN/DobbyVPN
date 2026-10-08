package com.dobby

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.nativebridge.DiagnosticFiles
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.ui.StructuredLogs
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class NativeDiagnosticRetentionTest {
    @Test
    fun clearBoundarySurvivesStartupWithFinalRecordBeyondRotationThreshold() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "diagnostic-overflow-${System.nanoTime()}")
        assertTrue(directory.mkdir())
        try {
            val native = File(directory, "native.jsonl")
            val boundary = File(directory, "view.json")
            val view = StructuredLogs(listOf(native.path), boundary)
            val finalRecord = "{\"message\":\"pre-clear overflow record\"}\n"
            RandomAccessFile(native, "rw").use { output ->
                output.setLength(DiagnosticFiles.THRESHOLD - 2)
                output.seek(DiagnosticFiles.THRESHOLD - 2)
                output.write(10)
                output.write(finalRecord.toByteArray(Charsets.UTF_8))
            }

            val beforeClear = view.readWithRetainedBoundary()
            assertEquals("", beforeClear.error)
            assertEquals(listOf("pre-clear overflow record"), beforeClear.entries.map { it.message })
            view.clear()
            val cleared = view.readWithRetainedBoundary()
            assertEquals("", cleared.error)
            assertTrue(cleared.entries.isEmpty())
            val clearBoundary = cleared.clearBoundary ?: error("Clear did not retain file boundaries")
            val oldFileID = beforeClear.entries.single().id.substringBeforeLast(':')
            assertEquals(native.length(), clearBoundary[oldFileID] ?: -1L)

            val firstAfterClear = "{\"message\":\"first after clear\"}\n"
            DiagnosticFiles.append(native, firstAfterClear.toByteArray(Charsets.UTF_8))
            val afterRestartAppend = view.readWithRetainedBoundary(beforeClear.entries, clearBoundary)
            assertEquals("", afterRestartAppend.error)
            assertEquals(listOf("first after clear"), afterRestartAppend.entries.map { it.message })
            assertFalse(afterRestartAppend.retainedEntriesVisible ?: true)
            RandomAccessFile(File(native.path + ".previous"), "r").use { input ->
                val bytes = ByteArray(finalRecord.toByteArray(Charsets.UTF_8).size)
                input.seek(input.length() - bytes.size)
                input.readFully(bytes)
                assertArrayEquals(finalRecord.toByteArray(Charsets.UTF_8), bytes)
            }
        } finally {
            assertTrue(directory.deleteRecursively())
        }
    }

    @Test
    fun structuredViewKeepsOrderingRawDetailsAndDurableClear() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "structured-${System.nanoTime()}")
        assertTrue(directory.mkdir())
        try {
            val backend = File(directory, "backend.jsonl")
            val native = File(directory, "ui.jsonl")
            val boundary = File(directory, "view.json")
            val later = """{"timestamp":"2026-01-01T00:00:02Z","message":"later"}""" + "\n"
            val earlierRaw = """{"timestamp":"2026-01-01T00:00:01Z","level":"WARN","message":"earlier λ","extra":42}"""
            backend.writeText(later + "trace line 1\ntrace line 2\n")
            native.writeText(earlierRaw + "\n" + """{"message":"incomplete""")
            val view = StructuredLogs(listOf(backend.path, native.path), boundary)
            val (entries, error) = view.read()
            assertEquals("", error)
            assertEquals(listOf("earlier λ", "trace line 1", "trace line 2", "later"), entries.map { it.message })
            assertEquals("WARN", entries[0].level)
            assertEquals("2026-01-01T00:00:01Z", entries[0].timestamp)
            assertNotNull(entries[0].time)
            assertEquals("App", entries[0].source)
            assertEquals(earlierRaw, entries[0].raw)

            native.appendText(" record\"}\n")
            val (completed, completionError) = view.read()
            assertEquals("", completionError)
            val completedRecord = completed.single { it.message == "incomplete record" }
            assertEquals("INFO", completedRecord.level)
            assertEquals("", completedRecord.timestamp)
            assertEquals(null, completedRecord.time)
            assertEquals("App", completedRecord.source)
            assertEquals("""{"message":"incomplete record"}""", completedRecord.raw)

            view.clear()
            assertTrue(backend.renameTo(File(backend.path + ".previous")))
            backend.writeText("new after rotation\n")
            val (after, failure) = view.read()
            assertEquals("", failure)
            assertEquals(listOf("new after rotation"), after.map { it.message })
            assertTrue(File(backend.path + ".previous").readText().startsWith(later))
            backend.writeBytes("partial ".toByteArray() + byteArrayOf(0xCE.toByte()))
            assertEquals(listOf("partial "), view.read().first.map { it.message })
            backend.appendBytes(byteArrayOf(0xBB.toByte(), 10))
            assertEquals(listOf("partial λ"), view.read().first.map { it.message })
            val capture = StructuredLogs.parse("""{"event":"stderr.capture","level":"ERROR"}""", "capture", "Tunnel stderr")
            assertEquals("INFO", capture.level)
            assertEquals("Stderr capture initialized", capture.message)
            assertEquals("Tunnel stderr", capture.source)
        } finally {
            assertTrue(directory.deleteRecursively())
        }
    }

    @Test
    fun structuredPreviewKeepsEqualTimestampOrderAndCapsBytes() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "structured-ties-${System.nanoTime()}")
        assertTrue(directory.mkdir())
        try {
            val first = File(directory, "backend.jsonl")
            val second = File(directory, "ui.jsonl")
            val boundary = File(directory, "view.json")
            val timestamp = "2026-03-04T05:06:07Z"
            first.writeText(
                """{"timestamp":"$timestamp","level":"INFO","source":"backend","message":"first tie"}""" + "\n",
            )
            second.writeText(
                """{"timestamp":"$timestamp","level":"INFO","source":"native","message":"second tie"}""" + "\n",
            )
            val tieView = StructuredLogs(listOf(first.path, second.path), boundary)
            val (tied, tieError) = tieView.read()
            assertEquals("", tieError)
            assertEquals(listOf("first tie", "second tie"), tied.map { it.message })
            assertEquals("Backend · backend", tied[0].source)
            assertEquals("App · native", tied[1].source)
            assertEquals(timestamp, tied[0].timestamp)
            assertEquals(timestamp, tied[1].timestamp)
            val (repeated, repeatedError) = tieView.read()
            assertEquals("", repeatedError)
            assertEquals(tied.map { it.id to it.message }, repeated.map { it.id to it.message })

            val manyLines = buildString {
                repeat(24_000) { index ->
                    append("{\"message\":\"preview-").append(index).append("\"}\n")
                }
            }
            first.writeText(manyLines)
            val (preview, previewError) = StructuredLogs(listOf(first.path), boundary).read()
            assertEquals("", previewError)
            assertTrue(preview.isNotEmpty())
            assertTrue(preview.last().message.startsWith("preview-"))
            assertTrue(preview.sumOf { it.raw.toByteArray(Charsets.UTF_8).size } <= 131_072)
            assertFalse(preview.any { it.raw.length > 131_072 })
        } finally {
            assertTrue(directory.deleteRecursively())
        }
    }

    @Test
    fun rawJsonLikeTraceKeepsItsTextAndDoesNotInventFields() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "structured-raw-${System.nanoTime()}")
        assertTrue(directory.mkdir())
        try {
            val backend = File(directory, "backend.jsonl")
            val boundary = File(directory, "view.json")
            val first = "{\"message\": broken"
            val second = "trace follows in order"
            backend.writeText("$first\n$second\n")
            val (entries, error) = StructuredLogs(listOf(backend.path), boundary).read()
            assertEquals("", error)
            assertEquals(listOf(first, second), entries.map { it.message })
            assertTrue(entries.all { it.level == "RAW" && it.timestamp.isEmpty() && it.time == null })
            assertEquals(listOf(first, second), entries.map { it.raw })
            assertEquals(listOf("Backend", "Backend"), entries.map { it.source })
        } finally {
            assertTrue(directory.deleteRecursively())
        }
    }

    @Test
    fun captureEventRetainsRawSourceAndStableDisplaySeverity() {
        val raw = """{"timestamp":"2026-04-05T06:07:08Z","level":"ERROR","event":"stderr.capture","source":"xray stderr","message":"raw initialization"}"""
        val capture = StructuredLogs.parse(raw, "capture", "Backend stderr")
        assertEquals("INFO", capture.level)
        assertEquals("Backend stderr · xray stderr", capture.source)
        assertEquals("Stderr capture initialized", capture.message)
        assertEquals(raw, capture.raw)
        assertNotNull(capture.time)
    }

    @Test
    fun failedAppendRetainsOriginalRecordAndChunkedFallbackBytes() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val blocked = File(context.cacheDir, "native-diagnostic-blocked-${System.nanoTime()}")
        assertTrue(blocked.mkdir())
        val record = "{\"message\":\"${"λ".repeat(4000)}\"}"
        var retainedRecord: String? = null
        var writeError: Exception? = null

        try {
            NativeVpnBridge.storeNativeDiagnostic({ blocked }, record) { value, error ->
                retainedRecord = value
                writeError = error
            }
        } finally {
            assertTrue(blocked.delete())
        }

        assertEquals(record, retainedRecord)
        assertTrue(writeError is IOException)

        val chunks = mutableListOf<String>()
        NativeVpnBridge.emitNativeDiagnosticChunks("record", record) { chunks.add(it) }
        assertTrue(chunks.size > 1)
        val recovered = ByteArrayOutputStream()
        for ((index, chunk) in chunks.withIndex()) {
            assertTrue(chunk.contains("kind=record part=${index + 1}/${chunks.size}"))
            recovered.write(Base64.decode(chunk.substringAfter("utf8_base64="), Base64.NO_WRAP))
        }
        assertArrayEquals(record.toByteArray(Charsets.UTF_8), recovered.toByteArray())
    }
}
