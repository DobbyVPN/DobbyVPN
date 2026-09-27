package com.dobby

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.nativebridge.NativeVpnBridge
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class NativeDiagnosticRetentionTest {
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
