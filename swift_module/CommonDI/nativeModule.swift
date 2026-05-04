import app
import Sentry


class SentryLogsRepositoryImpl : SentryLogsRepository {
    func log(string: String) {
        SentrySDK.capture(message: string)
    }
}


public class NativeModuleHolder {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    private static let netCheckPath = NetCheckRepository_iosKt.provideNetCheckConfigPath()
    private static let chan = LogEventsChannel()
    public static let logsRepository = LogsRepository
        .init(logFilePath: path, logEventsChannel: chan)
        .setSentryLogger(_sentryLogger: SentryLogsRepositoryImpl())
    public static let netCheckRepository = NetCheckRepository
        .init(configPath: netCheckPath)
    
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
        authenticationManager: { _ in
            return AuthenticationManagerImpl()
        },
        healthCheck: { _ in
            return HealthCheckImpl()
        },
        netCheckManager { _ in
            return NetCheckManagerImpl()
        },
        netCheckRepository { _ in
            return netCheckRepository
        }
    )
    
    private init() {
    }
}


public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
