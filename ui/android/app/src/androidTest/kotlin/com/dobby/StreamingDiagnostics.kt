package com.dobby

import com.dobby.nativebridge.DiagnosticFiles
import com.dobby.nativebridge.writeDiagnosticArchive
import java.io.DataInputStream
import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.io.RandomAccessFile
import java.util.zip.GZIPInputStream
import org.json.JSONObject

internal fun verifyStreamingDiagnostics(cache: File) {
    val directory = File(cache, "streaming-diagnostics-${System.nanoTime()}")
    check(directory.mkdir())
    try {
        val source = File(directory, "large.jsonl")
        val archive = File(directory, "export.gz")
        val block = ByteArray(65_536) { it.toByte() }
        source.outputStream().use { output -> repeat(1024) { output.write(block) } }
        val metadata = JSONObject().put("test", "exact bytes")
        archive.outputStream().use { writeDiagnosticArchive(it, listOf(source, directory), metadata) }
        DataInputStream(GZIPInputStream(archive.inputStream())).use { input ->
            val prefix = "$metadata\n${JSONObject().put("diagnostic_file", source.name)}\n".toByteArray()
            val actualPrefix = ByteArray(prefix.size)
            input.readFully(actualPrefix)
            check(actualPrefix.contentEquals(prefix)) { "Diagnostic metadata changed" }
            val actual = ByteArray(block.size)
            repeat(1024) {
                input.readFully(actual)
                check(actual.contentEquals(block)) { "Diagnostic bytes changed" }
            }
            val footer = input.bufferedReader().readText()
            check(JSONObject(footer.trim()).getString("collection_errors").contains(directory.path)) {
                "Diagnostic collection failure missing: $footer"
            }
        }
        val native = File(directory, "native.jsonl")
        RandomAccessFile(native, "rw").use {
            it.setLength(DiagnosticFiles.THRESHOLD - 1)
            it.seek(DiagnosticFiles.THRESHOLD - 1)
            it.write("\nlegacy-tail\n".toByteArray())
        }
        DiagnosticFiles.append(native, "first\n".toByteArray())
        val previous = File(native.path + ".previous")
        check(previous.length() == DiagnosticFiles.THRESHOLD)
        check(native.readText() == "legacy-tail\nfirst\n")
        val captured = DiagnosticFiles.capture(native)
        try {
            RandomAccessFile(native, "rw").use { it.setLength(DiagnosticFiles.THRESHOLD) }
            DiagnosticFiles.append(native, "second\n".toByteArray())
            RandomAccessFile(native, "rw").use { it.setLength(DiagnosticFiles.THRESHOLD) }
            DiagnosticFiles.append(native, "third\n".toByteArray())
            val old = captured.last()
            val retained = ByteArray(old.length.toInt())
            DataInputStream(old.stream).readFully(retained)
            check(String(retained) == "legacy-tail\nfirst\n") { "Rotation lost captured bytes" }
        } finally { captured.forEach { it.stream.close() } }
        previous.writeText("retained prior\n")
        archive.outputStream().use { writeDiagnosticArchive(it, listOf(native), metadata) }
        val both = GZIPInputStream(archive.inputStream()).bufferedReader().use { it.readText() }
        check(both.contains("retained prior") && both.contains("third"))
        val destinationFailure = IOException("synthetic destination failure")
        try {
            writeDiagnosticArchive(object : OutputStream() {
                override fun write(value: Int) { throw destinationFailure }
            }, listOf(source), metadata)
            error("Partial export reported success")
        } catch (failure: IOException) {
            check(failure === destinationFailure)
        }
        println("Native diagnostics: 64 MiB exact export, collection errors, and destination failure passed")
    } finally {
        check(directory.deleteRecursively()) { "Diagnostic test cleanup failed: $directory" }
    }
}
