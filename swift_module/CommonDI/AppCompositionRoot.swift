import app


public enum IOSAppCompositionRoot {
    private static let path = LogsRepository_iosKt.provideLogFilePath()
    public static let logsRepository = LogsRepository
        .init(
            logFilePath: path,
            additionalLogFilePaths: LogsRepository_iosKt.provideAdditionalLogFilePaths()
        )
    private static let vpnManager = VpnManagerImpl()
    private static let sessionShell = IOSSessionShell(manager: vpnManager)
    public static let appDependencies = IosAppDependenciesKt.createIosAppDependencies(
        bridge: sessionShell,
        logsRepository: logsRepository,
        exportLogsInteractor: ExportLogsInteractorImpl(),
        configsRepository: configsRepository
    )
}


public let appGroupIdentifier = "group.vpn.dobby.app"

public var configsRepository = DobbyConfigsRepositoryImpl.shared
