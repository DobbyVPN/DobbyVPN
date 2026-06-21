package com.dobby.feature.main.ui

data class LogMessage(
    val level: LogMessageLevel,
    val time: String,
    val category: String,
    val message: String,
    val isBackend: Boolean,
) {
    companion object {
        private val backendLogMessageRegex = """^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})] \[(\w+)] "\[(\w+)] (.*)" \[from go]$""".toRegex()
        private val frontendLogMessageRegex = """^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})] (.*)$""".toRegex()

        fun parse(message: String): LogMessage {
            val match = backendLogMessageRegex.matchEntire(message)

            return if (match != null) {
                LogMessage(
                    level = when (match.groupValues[2]) {
                        "INFO" -> LogMessageLevel.INFO
                        "DEBUG" -> LogMessageLevel.DEBUG
                        "WARN" -> LogMessageLevel.WARN
                        "ERROR" -> LogMessageLevel.ERROR
                        else -> LogMessageLevel.DEBUG
                    },
                    time = match.groupValues[1],
                    category = match.groupValues[3],
                    message = match.groupValues[4],
                    isBackend = true
                )
            } else {
                val frontendMatch = frontendLogMessageRegex.matchEntire(message)

                if (frontendMatch != null) {
                    LogMessage(
                        level = LogMessageLevel.DEBUG,
                        time = frontendMatch.groupValues[1],
                        category = "UI",
                        message = frontendMatch.groupValues[2],
                        isBackend = false
                    )
                } else { // Never should be possible
                    LogMessage(
                        level = LogMessageLevel.DEBUG,
                        time = "yyyy-MM-dd HH:mm:ss",
                        category = "UI",
                        message = message,
                        isBackend = false
                    )
                }
            }
        }
    }
}

enum class LogMessageLevel {
    DEBUG,
    INFO,
    WARN,
    ERROR
}
