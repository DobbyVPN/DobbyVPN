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
    public static let logsRepository = LogsRepository.init(logFilePath: path, logEventsChannel: chan).setSentryLogger(_sentryLogger: SentryLogsRepositoryImpl())
    
    public static let shared: Koin_coreModule = MakeNativeModuleKt.makeNativeModule(
        copyLogsInteractor: { scope in
            return CopyLogsInteractorImpl()
        },
        logEventsChannel: { scope in
            return chan
        },
        logsRepository: { scope in
            return logsRepository
        },
        ipRepository: { scope in
            return IpRepositoryImpl()
        },
        configsRepository: { scope in
            return configsRepository
        },
        connectionStateRepository: { scope in
            return connectionStateRepository
        },
        vpnManager: { scope in
            return VpnManagerImpl(connectionRepository: connectionStateRepository)
        },
        awgManager: { scope in
            return AwgManagerImpl()
        },
        authenticationManager: { scope in
            return AuthenticationManagerImpl(logger: DobbyAuthLogger())
        },
        healthCheck: { scope in
            return HealthCheckImpl()
        }
    )
    
    private init() {
    }
}


public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
