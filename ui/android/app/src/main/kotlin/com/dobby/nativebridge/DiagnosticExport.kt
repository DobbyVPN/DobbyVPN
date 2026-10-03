package com.dobby.nativebridge

import java.io.File
import java.io.FileNotFoundException
import java.io.IOException
import java.io.OutputStream
import java.util.zip.Deflater
import java.util.zip.GZIPOutputStream
import org.json.JSONObject

/** Export exact file bytes with bounded memory, preserving collection errors. */
internal fun writeDiagnosticArchive(destination: OutputStream, paths: List<File>, metadata: JSONObject) {
    object : GZIPOutputStream(destination) {
        init { def.setLevel(Deflater.BEST_COMPRESSION) }
    }.use { output ->
        output.write("$metadata\n".toByteArray(Charsets.UTF_8))
        val buffer = ByteArray(65_536)
        val errors = mutableListOf<String>()
        for (file in paths) {
            val input = try {
                if (file.exists() && !file.isFile) throw IOException("Diagnostic input is not a regular file: ${file.path}")
                file.inputStream()
            } catch (failure: FileNotFoundException) {
                if (file.exists()) errors.add("${file.path}: ${failure.stackTraceToString()}")
                continue
            } catch (failure: IOException) {
                errors.add("${file.path}: ${failure.stackTraceToString()}")
                continue
            }
            input.use {
                var remaining = try {
                    input.channel.size()
                } catch (failure: IOException) {
                    errors.add("${file.path}: ${failure.stackTraceToString()}")
                    0L
                }
                while (remaining > 0) {
                    val count = try {
                        input.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt()).also {
                            if (it < 0) throw IOException("Log shortened during export")
                        }
                    } catch (failure: IOException) {
                        errors.add("${file.path}: ${failure.stackTraceToString()}")
                        break
                    }
                    // A destination failure propagates; never report a partial archive as successful.
                    output.write(buffer, 0, count)
                    remaining -= count
                }
            }
            output.write('\n'.code)
        }
        val issues = JSONObject().put("collection_errors", errors.joinToString("\n"))
        output.write("$issues\n".toByteArray(Charsets.UTF_8))
    }
}
