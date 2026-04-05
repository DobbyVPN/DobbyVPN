package com.dobby.feature.logging

import android.content.Context
import android.content.Intent
import androidx.core.content.FileProvider
import com.dobby.common.showToast
import com.dobby.feature.logging.domain.CopyLogsInteractor
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

class CopyLogsInteractorImpl(
    private val context: Context
) : CopyLogsInteractor {

    override fun copy(logs: List<String>) {
        try {
            val joinedLogs = logs.joinToString("\n")

            val timestamp = SimpleDateFormat(
                "yyyy-MM-dd_HH-mm-ss",
                Locale.getDefault()
            ).format(Date())
            val fileName = "DobbyVPN_logs_$timestamp.txt"

            val logFile = File(context.cacheDir, fileName)
            logFile.writeText(joinedLogs)

            val uri = FileProvider.getUriForFile(
                context,
                context.packageName + ".fileprovider",
                logFile
            )

            val shareIntent = Intent(Intent.ACTION_SEND).apply {
                type = "text/plain"
                putExtra(Intent.EXTRA_STREAM, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }

            context.startActivity(
                Intent.createChooser(shareIntent, "Send logs")
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            )

        } catch (e: Exception) {
            e.printStackTrace()
            context.showToast("Can't send logs")
        }
    }
}
