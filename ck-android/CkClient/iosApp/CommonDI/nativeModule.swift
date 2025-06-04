import app

public class NativeModuleHolder {
    
    public static let shared: Koin_coreModule = MakeNativeModuleKt.makeNativeModule(
        copyLogsInteractor: { scope in
            return CopyLogsInteractorImpl()
        },
        logsRepository: { scope in
            return logsRepo
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

private let path = LogsRepository_iosKt.provideLogFilePath()
public var logsRepo = LogsRepository.init(logFilePath: path)

public var connectionStateRepository = ConnectionStateRepository()
