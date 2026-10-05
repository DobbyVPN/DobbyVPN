package com.dobby

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.ui.StructuredLogs
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
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
    fun structuredViewKeepsOrderingRawDetailsAndDurableClear() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "structured-${System.nanoTime()}")
        assertTrue(directory.mkdir())
        try {
            val backend = File(directory, "backend.jsonl")
            val native = File(directory, "ui.jsonl")
            val boundary = File(directory, "view.json")
            val later = """{"timestamp":"2026-01-01T00:00:02Z","message":"later"}""" + "\n"
            backend.writeText(later + "trace line 1\ntrace line 2\n")
            native.writeText("""{"timestamp":"2026-01-01T00:00:01Z","level":"WARN","message":"earlier λ","extra":42}""" + "\n" + """{"message":"incomplete""")
            var view = StructuredLogs(listOf(backend.path, native.path), boundary)
            val (entries, error) = view.read()
            assertEquals("", error)
            assertEquals(listOf("trace line 1", "trace line 2", "earlier λ", "later"), entries.map { it.message })
            assertEquals("WARN", entries[2].level)
            assertTrue(entries[2].raw.contains("extra"))
            view.clear()
            assertTrue(backend.renameTo(File(backend.path + ".previous")))
            backend.writeText("new after rotation\n")
            native.appendText(" record\"}\n")
            view = StructuredLogs(listOf(backend.path, native.path), boundary)
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
