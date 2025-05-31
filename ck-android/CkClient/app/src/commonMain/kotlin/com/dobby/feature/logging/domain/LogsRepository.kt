package com.dobby.feature.logging.domain

import androidx.compose.runtime.snapshots.SnapshotStateList

interface LogsRepository {
    val logState: SnapshotStateList<String>

    fun writeLog(log: String)

    fun clearLogs()
}
