package com.dobby.ui

import android.system.Os
import android.util.AtomicFile
import org.json.JSONObject
import java.io.File
import java.time.Instant

data class LogEntry(val id: String, val timestamp: String, val level: String, val source: String, val message: String, val raw: String, val time: Instant?)

internal class StructuredLogs(private val paths: List<String>, private val boundary: File) {
    private fun names() = paths.flatMap { listOf(it + ".previous", it) }
    private fun identity(input: java.io.FileInputStream): String = Os.fstat(input.fd).let { "${it.st_dev}:${it.st_ino}" }

    fun clear() {
        val offsets = JSONObject()
        val failures = mutableListOf<Exception>()
        names().forEach { path ->
            try { File(path).inputStream().use { offsets.put(identity(it), it.channel.size()) } }
            catch (failure: java.io.FileNotFoundException) { if (File(path).exists()) failures.add(failure) }
            catch (failure: Exception) { failures.add(failure) }
        }
        if (failures.isNotEmpty()) throw java.io.IOException(failures.joinToString("\n") { it.stackTraceToString() })
        boundary.parentFile?.mkdirs()
        val atomic = AtomicFile(boundary)
        val output = atomic.startWrite()
        try { output.write(offsets.toString().toByteArray(Charsets.UTF_8)); atomic.finishWrite(output) }
        catch (failure: Exception) { atomic.failWrite(output); throw failure }
    }

    fun read(): Pair<List<LogEntry>, String> {
        val entries = mutableListOf<LogEntry>()
        val errors = mutableListOf<String>()
        val offsets = try { if (boundary.exists()) JSONObject(AtomicFile(boundary).openRead().bufferedReader().use { it.readText() }) else JSONObject() }
            catch (failure: Exception) { return emptyList<LogEntry>() to "Viewing boundary: ${failure.stackTraceToString()}" }
        names().forEach { path ->
            try {
                File(path).inputStream().use { input ->
                    val id = identity(input)
                    val size = input.channel.size()
                    val start = maxOf(minOf(offsets.optLong(id), size), size - 131_072L)
                    input.channel.position(if (start > 0) start - 1 else 0)
                    var bytes = ByteArray(minOf(size - input.channel.position(), 131_073L).toInt())
                    java.io.DataInputStream(input).readFully(bytes)
                    var position = start
                    if (start > 0 && bytes.isNotEmpty()) {
                        val boundaryLine = bytes[0] == 10.toByte()
                        bytes = bytes.copyOfRange(1, bytes.size)
                        if (!boundaryLine) {
                            val newline = bytes.indexOf(10.toByte())
                            if (newline < 0) return@use
                            position += newline + 1
                            bytes = bytes.copyOfRange(newline + 1, bytes.size)
                        }
                    }
                    val stream = friendlyStream(File(path).name)
                    val decoder = Charsets.UTF_8.newDecoder().onMalformedInput(java.nio.charset.CodingErrorAction.REPLACE)
                    val characters = java.nio.CharBuffer.allocate(bytes.size)
                    // A writer may be between bytes of the final UTF-8 character.
                    decoder.decode(java.nio.ByteBuffer.wrap(bytes), characters, false)
                    characters.flip()
                    val lines = characters.toString().split('\n')
                    lines.forEachIndexed { index, raw ->
                        val entryId = "$id:$position"
                        position += raw.toByteArray(Charsets.UTF_8).size + 1
                        if (raw.isNotEmpty() && !(index == lines.lastIndex && raw.startsWith("{"))) entries.add(parse(raw, entryId, stream))
                    }
                }
            } catch (failure: java.io.FileNotFoundException) { if (File(path).exists()) errors.add("$path: ${failure.stackTraceToString()}") }
            catch (failure: Exception) { errors.add("$path: ${failure.stackTraceToString()}") }
        }
        return entries.sortedWith(compareBy<LogEntry> { it.time == null }.thenBy { it.time }) to errors.joinToString("\n")
    }

    companion object {
        fun friendlyStream(name: String): String = when {
            name.contains("stderr") -> if (name.contains("tunnel")) "Tunnel stderr" else "Backend stderr"
            name.contains("ui") || name.contains("app") -> "App"
            name.contains("tunnel") -> "Tunnel"
            else -> "Backend"
        }
        fun parse(raw: String, id: String, stream: String): LogEntry {
            val value = runCatching { JSONObject(raw) }.getOrNull()
                ?: return LogEntry(id, "", "RAW", stream, raw, raw, null)
            val timestamp = value.optString("timestamp")
            val capture = value.optString("event") == "stderr.capture"
            return LogEntry(id, timestamp, if (capture) "INFO" else value.optString("level", "INFO").uppercase(),
                listOf(stream, value.optString("source")).filter(String::isNotEmpty).joinToString(" · "),
                if (capture) "Stderr capture initialized" else value.optString("message", raw), raw,
                runCatching { Instant.parse(timestamp) }.getOrNull())
        }
    }
}
