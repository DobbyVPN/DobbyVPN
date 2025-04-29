package com.dobby.feature.logging

import com.dobby.feature.logging.domain.LogsRepository
import java.io.BufferedReader
import java.io.File
import java.io.FileReader
import java.io.FileWriter

class LogsRepositoryImpl(
    private val logFileName: String = "app_logs.txt"
) : LogsRepository {

    private val logFile: File
        get() = File(logFileName)

    init {
        logFile.createNewFile()
    }

    override fun writeLog(log: String) {
        runCatching {
            FileWriter(logFile, true).use { writer ->
                writer.appendLine(log)
            }
        }.onFailure { it.printStackTrace() }
    }

    override fun readLogs(): List<String> {
        val logs = mutableListOf<String>()
        runCatching {
            BufferedReader(FileReader(logFile)).use { reader ->
                var line: String? = reader.readLine()
                while(line != null) {
                    logs.add(line)
                    line = reader.readLine()
                }
            }
        }.onFailure { it.printStackTrace() }
        return logs
    }

    override fun clearLogs() {
        if (logFile.exists()) {
            logFile.delete()
        }
        logFile.createNewFile()
    }
}
