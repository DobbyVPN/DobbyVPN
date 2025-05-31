package com.dobby.feature.logging

import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.runtime.toMutableStateList
import com.dobby.feature.logging.domain.LogsRepository
import java.io.BufferedReader
import java.io.File
import java.io.FileReader
import java.io.FileWriter

class LogsRepositoryImpl(
    private val logFileName: String = "app_logs.txt"
) : LogsRepository {

    private var _logState: SnapshotStateList<String>? = null
    private val logFile: File
        get() = File(logFileName)

    private fun readLogs(): List<String> {
        val newState = mutableListOf<String>()
        runCatching {
            BufferedReader(FileReader(logFile)).use { reader ->
                var line: String? = reader.readLine()
                while(line != null) {
                    newState.add(line)
                    line = reader.readLine()
                }
            }
        }.onFailure { it.printStackTrace() }

        return newState
    }

    override val logState: SnapshotStateList<String>
        get() {
            val currentState = _logState

            if (currentState == null) {
                val newState = readLogs().toMutableStateList()
                _logState = newState
                return newState
            } else {
                return currentState
            }
        }

    init {
        logFile.createNewFile()

        readLogs()
    }

    override fun writeLog(log: String) {
        runCatching {
            FileWriter(logFile, true).use { writer ->
                writer.appendLine(log)
            }

            _logState?.add(log)
        }.onFailure { it.printStackTrace() }
    }

    override fun clearLogs() {
        if (logFile.exists()) {
            logFile.delete()
        }

        logFile.createNewFile()
        _logState?.clear()
    }
}
