package com.dobby.feature.logging

import com.dobby.feature.logging.domain.CopyLogsInteractor
import java.awt.FileDialog
import java.awt.Frame
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

class CopyLogsInteractorImpl : CopyLogsInteractor {

    override fun copy(logs: List<String>) {
        val joinedLogs = logs.joinToString("\n")
        val timestamp = SimpleDateFormat(
            "yyyy-MM-dd_HH-mm-ss",
            Locale.getDefault()
        ).format(Date())
        val fileName = "DobbyVPN_logs_$timestamp.txt"

        val dialog = FileDialog(null as Frame?, "Save logs", FileDialog.SAVE).apply {
            file = fileName
            isVisible = true
        }

        val selectedDirectory = dialog.directory ?: return
        val selectedFile = dialog.file ?: return

        File(selectedDirectory, selectedFile).writeText(joinedLogs)
    }
}
