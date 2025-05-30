package com.dobby.feature.logging.domain

import androidx.compose.runtime.snapshots.SnapshotStateList

interface LogsRepository {

    fun writeLog(log: String)

    fun readLogs(): SnapshotStateList<String>

    fun clearLogs()
}
