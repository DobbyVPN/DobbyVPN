import app
import MyLibrary

public class LoggerManagerImpl: LoggerManager {
    private var logs = NativeModuleHolder.logsRepository
    private var configsRepository: DobbyConfigsRepository

    init(configsRepository: DobbyConfigsRepository) {
        self.configsRepository = configsRepository
    }

    public func initLogger() {
        let logFilePath = LogsRepository_iosKt.provideLogFilePath().normalized().description()
        let endpoint = configsRepository.getTelemetryEndpoint()
        let token = configsRepository.getTelemetryApiToken()
        let config = configsRepository.getTelemetryAttributes()

        logs.writeLog(log: "Init tunnel logging to the path: \(logFilePath)")
        Cloak_outlineInitLogger(logFilePath)
        logs.writeLog(log: "Finish go logger init")

        logs.log("Init tunnel telemetry to the endpoint=\(endpoint)")
        if !endpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            Cloak_outlineInitTelemetry(endpoint, token)
            logs.log("Initialized tunnel telemetry")
        } else {
            logs.log("No telemetry endpoint provided")
        }

        logs.log("Setup telemetry attributes")
        if !config.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            Cloak_outlineSetupTelemetryAttributes(config)
            logs.log("Setup tunnel telemetry attributes")
        } else {
            logs.log("No telemetry attributes provided")
        }
    }
}
