import app
import MyLibrary

public class LoggerManagerImpl: LoggerManager {
    private var logs = NativeModuleHolder.logsRepository

    public func doInitLogger() {
        let logFilePath = LogsRepository_iosKt.provideGoLogFilePath().normalized().description()

        logs.writeLog(log: "Starting Go tunnel logger using owner-only local storage")
        Cloak_outlineInitLogger(logFilePath)
        logs.writeLog(log: "Go tunnel logger initialization returned")

        logs.writeLog(log: "Remote telemetry is disabled; tunnel logs remain local")
    }

    public func stopTelemetry() {
        logs.writeLog(log: "Remote telemetry is disabled; no tunnel exporter to stop")
    }
}
