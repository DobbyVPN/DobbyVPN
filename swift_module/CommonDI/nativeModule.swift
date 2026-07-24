import app


public class NativeModuleHolder {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    private static let chan = LogEventsChannel()
    public static let logsRepository = LogsRepository
        .init(logFilePath: path, logEventsChannel: chan)
    private static let vpnManager = VpnManagerImpl(connectionRepository: connectionStateRepository)
    private static let sessionShell = IOSSessionShell(manager: vpnManager)
    
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
        configsRepository: { _ in
            return configsRepository
        },
        connectionStateRepository: { _ in
            return connectionStateRepository
        },
        authenticationManager: { _ in
            return AuthenticationManagerImpl()
        },
        loggerManager: { _ in 
            return LoggerManagerImpl()
        }
    )

    // Must run before StartDI constructs MainViewModel. The bridge is narrow:
    // it only persists opaque bytes, controls NetworkExtension, and reports
    // authoritative NE status; Go remains extension-process lifecycle owner.
    public static func installSessionBridge() {
        IosSessionBridgeRegistry.shared.install(bridge: sessionShell)
    }
    
    private init() {
    }
}


public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared

public var connectionStateRepository = ConnectionStateRepository()
