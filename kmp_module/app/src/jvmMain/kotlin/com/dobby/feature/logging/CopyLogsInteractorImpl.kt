package com.dobby.feature.logging

import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import com.dobby.feature.logging.domain.CopyLogsInteractor

class CopyLogsInteractorImpl : CopyLogsInteractor {

    override fun copy(logs: List<String>) {
        val joinedLogs = logs.joinToString("\n")
        val stringSelection = StringSelection(joinedLogs)
        val clipboard = Toolkit.getDefaultToolkit().systemClipboard
        clipboard.setContents(stringSelection, null)
    }
}
