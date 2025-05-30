package com.dobby.feature.logging

import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.snapshots.SnapshotStateList
import com.dobby.feature.logging.domain.LogsRepository
import java.io.BufferedReader
import java.io.File
import java.io.FileReader
import java.io.FileWriter

class LogsRepositoryImpl(
    private val logFileName: String = "app_logs.txt"
) : LogsRepository {

    private var logState: SnapshotStateList<String>? = null
    private val logFile: File
        get() = File(logFileName)

    init {
        logFile.createNewFile()

        readLogs()
    }

    override fun writeLog(log: String) {
        runCatching {
            FileWriter(logFile, true).use { writer ->
                writer.appendLine(log)
            }

            logState?.add(log)
        }.onFailure { it.printStackTrace() }
    }

    override fun readLogs(): SnapshotStateList<String> {
        val currentState = logState

        if (currentState == null) {
            val newState = mutableStateListOf<String>()

            runCatching {
                BufferedReader(FileReader(logFile)).use { reader ->
                    var line: String? = reader.readLine()
                    while(line != null) {
                        newState.add(line)
                        line = reader.readLine()
                    }
                }
            }.onFailure { it.printStackTrace() }

            logState = newState

            return newState
        } else {
            return currentState
        }
    }

    override fun clearLogs() {
        if (logFile.exists()) {
            logFile.delete()
        }

        logFile.createNewFile()
        logState?.clear()
    }
}
