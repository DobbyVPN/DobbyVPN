package com.dobby.nativebridge

import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.util.zip.GZIPOutputStream
import org.json.JSONObject

/** Export exact retained bytes with bounded memory and explicit collection errors. */
internal fun writeDiagnosticArchive(destination: OutputStream, paths: List<File>, metadata: JSONObject) {
    val inputs = mutableListOf<DiagnosticFiles.Input>()
    val errors = mutableListOf<String>()
    var original: Throwable? = null
    try {
        for (file in paths) {
            try { inputs.addAll(DiagnosticFiles.capture(file)) }
            catch (failure: Exception) { errors.add("${file.path}: ${failure.stackTraceToString()}") }
        }
        GZIPOutputStream(destination, 65_536).use { output ->
            output.write("$metadata\n".toByteArray(Charsets.UTF_8))
            val buffer = ByteArray(65_536)
            for ((file, input, length) in inputs) {
                output.write("${JSONObject().put("diagnostic_file", file.name)}\n".toByteArray(Charsets.UTF_8))
                var remaining = length
                while (remaining > 0) {
                    val count = try {
                        input.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt()).also {
                            if (it < 0) throw IOException("Log shortened during export: ${file.path}")
                        }
                    } catch (failure: IOException) {
                        errors.add("${file.path}: ${failure.stackTraceToString()}")
                        break
                    }
                    output.write(buffer, 0, count)
                    remaining -= count
                }
                output.write('\n'.code)
            }
            for ((file, input) in inputs) {
                try { input.close() } catch (failure: Exception) { errors.add("${file.path}: ${failure.stackTraceToString()}") }
            }
            inputs.clear()
            val issues = JSONObject().put("collection_errors", errors.joinToString("\n"))
            output.write("$issues\n".toByteArray(Charsets.UTF_8))
        }
    } catch (failure: Throwable) {
        original = failure
        throw failure
    } finally {
        var cleanupFailure: Throwable? = null
        for ((_, input) in inputs) {
            try { input.close() } catch (cleanup: Exception) {
                if (cleanupFailure == null) cleanupFailure = cleanup else cleanupFailure.addSuppressed(cleanup)
            }
        }
        if (cleanupFailure != null) {
            if (original != null) original.addSuppressed(cleanupFailure) else throw cleanupFailure
        }
    }
}
