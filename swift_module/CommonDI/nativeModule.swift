import app


public class NativeModuleHolder {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    public static let logsRepository = LogsRepository
        .init(
            logFilePath: path,
            additionalLogFilePaths: LogsRepository_iosKt.provideAdditionalLogFilePaths()
        )
    private static let vpnManager = VpnManagerImpl()
    private static let sessionShell = IOSSessionShell(manager: vpnManager)
    
    public static let shared: Koin_coreModule = MakeNativeModuleKt.makeNativeModule(
        copyLogsInteractor: { _ in
            return CopyLogsInteractorImpl()
        },
        logsRepository: { _ in
            return logsRepository
        },
        configsRepository: { _ in
            return configsRepository
        },
        connectionStateRepository: { _ in
            return connectionStateRepository
        },
        loggerManager: { _ in 
            return LoggerManagerImpl()
        }
    )

    // Must run before StartDI constructs MainViewModel. The bridge only stores
    // the one-shot mailbox and transports opaque authenticated commands.
    public static func installSessionBridge() {
        IosSessionBridgeRegistry.shared.install(bridge: sessionShell)
    }
    
    private init() {
    }
}


public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
