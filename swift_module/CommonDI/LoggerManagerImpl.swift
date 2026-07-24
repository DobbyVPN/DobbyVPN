import app
import MyLibrary

public class LoggerManagerImpl: LoggerManager {
    private var logs = NativeModuleHolder.logsRepository

    public func doInitLogger() {
        let logFilePath = LogsRepository_iosKt.provideLogFilePath().normalized().description()

        logs.writeLog(log: "Init tunnel logging to the path: \(logFilePath)")
        Cloak_outlineInitLogger(logFilePath)
        logs.writeLog(log: "Finish go logger init")

        logs.writeLog(log: "Remote telemetry is disabled; tunnel logs remain local")
    }

    public func stopTelemetry() {
        logs.writeLog(log: "Remote telemetry is disabled; no tunnel exporter to stop")
    }
}
