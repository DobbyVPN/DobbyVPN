package com.dobby.feature.logging

import android.content.Context
import android.content.Intent
import androidx.core.content.FileProvider
import com.dobby.common.showToast
import com.dobby.feature.logging.domain.ExportLogsInteractor
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.zip.Deflater
import java.util.zip.GZIPOutputStream

class ExportLogsInteractorImpl(
    private val context: Context,
    private val logger: Logger,
) : ExportLogsInteractor {

    override fun export(logs: List<String>) {
        try {
            val joinedLogs = logs.joinToString("\n")

            val timestamp = SimpleDateFormat(
                "yyyy-MM-dd_HH-mm-ss",
                Locale.getDefault()
            ).format(Date())
            val fileName = "DobbyVPN_logs_$timestamp.jsonl.gz"

            val logFile = File(context.cacheDir, fileName)
            bestCompressionGzip(logFile).bufferedWriter(Charsets.UTF_8).use { writer ->
                writer.write(joinedLogs)
            }

            val uri = FileProvider.getUriForFile(
                context,
                context.packageName + ".fileprovider",
                logFile
            )

            val shareIntent = Intent(Intent.ACTION_SEND).apply {
                type = "application/gzip"
                putExtra(Intent.EXTRA_STREAM, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }

            context.startActivity(
                Intent.createChooser(shareIntent, "Export logs")
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            )

        } catch (e: Exception) {
            logger.error("Log export failed\n${e.stackTraceToString()}")
            context.showToast("Can't export logs")
        }
    }

    private fun bestCompressionGzip(file: File): GZIPOutputStream {
        return object : GZIPOutputStream(file.outputStream()) {
            init {
                def.setLevel(Deflater.BEST_COMPRESSION)
            }
        }
    }
}
