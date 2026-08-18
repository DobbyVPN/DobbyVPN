import app
import DobbyVPNRuntime

public class LoggerManagerImpl: LoggerManager {
    private var logs = NativeModuleHolder.logsRepository

    public func doInitLogger() -> Bool {
        let logFilePath = LogsRepository_iosKt.provideGoLogFilePath().normalized().description()

        logs.writeLog(log: "Starting Go tunnel logger using owner-only local storage")
        guard DobbyvpnInitLogger(logFilePath) else {
            logs.writeLog(log: "[ERROR] service_logger_init result=failed failure_code=LOCAL_LOGGER_REJECTED")
            return false
        }
        logs.writeLog(log: "service_logger_init result=success state=ready")

        return true
    }
}
