package com.dobby.nativebridge

import android.system.Os
import java.io.File
import java.io.FileInputStream
import java.io.FileNotFoundException
import java.io.FileOutputStream
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption

/** Each native stream has one process owner. Go owns its own independent files. */
internal object DiagnosticFiles {
    const val THRESHOLD = 150_000_000L
    private val initialized = mutableSetOf<String>()

    @Synchronized
    fun append(file: File, record: ByteArray) {
        if (!initialized.contains(file.path)) {
            migrate(file)
            initialized.add(file.path)
        }
        if (file.length() >= THRESHOLD) rotate(file)
        FileOutputStream(file, true).use { it.write(record) }
    }

    private fun previous(file: File) = File(file.path + ".previous")
    private fun rotate(file: File) {
        Files.move(file.toPath(), previous(file).toPath(), StandardCopyOption.REPLACE_EXISTING)
    }

    private fun migrate(file: File) {
        val paths = listOf(previous(file), file)
        if (paths.none { it.length() > THRESHOLD }) return
        val stage = File.createTempFile(".diagnostic-migration-", ".jsonl", file.parentFile)
        var output: FileOutputStream? = null
        var original: Throwable? = null
        try {
            output = FileOutputStream(stage)
            var size = 0L
            var atStart = true
            val buffer = ByteArray(65_536)
            for (path in paths) {
                if (!path.exists()) continue
                path.inputStream().use { input ->
                    var count = input.read(buffer)
                    while (count >= 0) {
                        var offset = 0
                        while (offset < count) {
                            if (atStart && size >= THRESHOLD) {
                                output!!.close()
                                output = null
                                rotate(stage)
                                output = FileOutputStream(stage)
                                size = 0
                            }
                            var end = offset
                            while (end < count && buffer[end] != 10.toByte()) end++
                            atStart = end < count
                            if (atStart) end++
                            output!!.write(buffer, offset, end - offset)
                            size += end - offset
                            offset = end
                        }
                        count = input.read(buffer)
                    }
                }
            }
            output!!.close()
            output = null
            Files.deleteIfExists(previous(file).toPath())
            if (previous(stage).exists()) Files.move(previous(stage).toPath(), previous(file).toPath())
            Files.move(stage.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING)
        } catch (failure: Throwable) {
            original = failure
            throw failure
        } finally {
            val cleanup = listOf<() -> Unit>(
                { output?.close() },
                { Files.deleteIfExists(stage.toPath()) },
                { Files.deleteIfExists(previous(stage).toPath()) },
            )
            var cleanupFailure: Throwable? = null
            for (operation in cleanup) {
                try { operation() } catch (failure: Exception) {
                    if (cleanupFailure == null) cleanupFailure = failure else cleanupFailure.addSuppressed(failure)
                }
            }
            if (cleanupFailure != null) {
                if (original != null) original.addSuppressed(cleanupFailure) else throw cleanupFailure
            }
        }
    }

    data class Input(val file: File, val stream: FileInputStream, val length: Long)

    // Open the current handle first, then the previous one, and verify that
    // current still names the same inode. A rename in between requires a new
    // snapshot, never copying across mismatched generations. Open handles keep
    // their captured bytes even if Go replaces both names during compression.
    @Synchronized
    fun capture(file: File): List<Input> {
        repeat(8) {
            val inputs = mutableListOf<Input>()
            try {
                val current = open(file)?.also { inputs.add(it) }
                open(previous(file))?.let { inputs.add(0, it) }
                val currentIdentity = current?.let { Os.fstat(it.stream.fd) }
                val namedIdentity = try { Os.stat(file.path) } catch (failure: android.system.ErrnoException) {
                    if (failure.errno != android.system.OsConstants.ENOENT) throw failure
                    null
                }
                if (currentIdentity?.st_dev == namedIdentity?.st_dev && currentIdentity?.st_ino == namedIdentity?.st_ino) {
                    return inputs
                }
            } catch (failure: Exception) {
                for (input in inputs) try { input.stream.close() } catch (cleanup: Exception) { failure.addSuppressed(cleanup) }
                throw failure
            }
            for (input in inputs) input.stream.close()
        }
        throw IOException("Diagnostic generations kept rotating while capturing ${file.path}")
    }

    private fun open(file: File): Input? {
        val stream = try { FileInputStream(file) } catch (failure: FileNotFoundException) {
            if (file.exists()) throw failure
            return null
        }
        try {
            val info = Os.fstat(stream.fd)
            if (!android.system.OsConstants.S_ISREG(info.st_mode)) throw IOException("Diagnostic input is not a regular file: ${file.path}")
            return Input(file, stream, info.st_size)
        } catch (failure: Exception) {
            try { stream.close() } catch (cleanup: Exception) { failure.addSuppressed(cleanup) }
            throw failure
        }
    }
}
