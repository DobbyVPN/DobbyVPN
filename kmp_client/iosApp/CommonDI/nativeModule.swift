import app
import Sentry
import AuthenticationModule

class SentryLogsRepositoryImpl : SentryLogsRepository {
    func log(string: String) {
        SentrySDK.capture(message: string)
    }
}

class DobbyAuthLogger: AuthenticationLogger {
    func writeLog(_ log: String) {
        NativeModuleHolder.logsRepository.writeLog(log: log)
    }
}


public class NativeModuleHolder {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    private static let chan = LogEventsChannel()
    public static let logsRepository = LogsRepository
        .init(logFilePath: path, logEventsChannel: chan)
        .setSentryLogger(_sentryLogger: SentryLogsRepositoryImpl())

    public static let shared: Koin_coreModule = MakeNativeModuleKt.makeNativeModule(
        copyLogsInteractor: { _ in
            return CopyLogsInteractorImpl()
        },
        logEventsChannel: { _ in
            return chan
        },
        logsRepository: { _ in
            return logsRepository
        },
        ipRepository: { _ in
            return IpRepositoryImpl()
        },
        configsRepository: { _ in
            return configsRepository
        },
        connectionStateRepository: { _ in
            return connectionStateRepository
        },
        vpnManager: { _ in
            return VpnManagerImpl(connectionRepository: connectionStateRepository)
        },
        awgManager: { _ in
            return AwgManagerImpl()
        },
        authenticationManager: { scope in
            return AuthenticationManagerImpl(logger: DobbyAuthLogger())
        },
        healthCheck: { _ in
            return HealthCheckImpl()
        }
    )

    private init() {
    }
}

public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
