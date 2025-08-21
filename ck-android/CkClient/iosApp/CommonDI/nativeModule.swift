import app

public class NativeModuleHolder {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    public static let logsRepository = LogsRepository.init(logFilePath: path)
    
    public static let shared: Koin_coreModule = MakeNativeModuleKt.makeNativeModule(
        copyLogsInteractor: { scope in
            return CopyLogsInteractorImpl()
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
        }
    )
    
    private init() {}
}

public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
