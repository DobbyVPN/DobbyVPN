package com.dobby.feature.logging

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import com.dobby.common.showToast
import com.dobby.feature.logging.domain.CopyLogsInteractor

class CopyLogsInteractorImpl(
    private val context: Context
) : CopyLogsInteractor {

    override fun copy(logs: List<String>) {
        val joinedLogs = logs.joinToString("\n")
        val clipboardManager = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val clipData = ClipData.newPlainText("Log Messages", joinedLogs)
        clipboardManager.setPrimaryClip(clipData)
        context.showToast("Logs copied!")
    }
}
