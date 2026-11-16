import Foundation

public let appGroupIdentifier = "group.vpn.dobby.app"

public enum IOSAppCompositionRoot {
    private static func sharedDirectory() -> URL {
#if targetEnvironment(simulator)
        // Provisioning-free Simulator bundles do not receive an App Group
        // container. Keep startup/lifecycle logs inside the app-owned
        // temporary directory; the physical iOS target uses the real shared
        // App Group below.
        return FileManager.default.temporaryDirectory
#else
        FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: appGroupIdentifier
        ) ?? FileManager.default.temporaryDirectory
#endif
    }

    public static func sharedLogPath(_ name: String) -> URL {
        sharedDirectory().appendingPathComponent(name)
    }

    public static func appLogPath() -> URL {
        goLogFilePath()
    }

    public static func goLogFilePath() -> URL {
        let isTunnel = Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true
        return sharedLogPath(isTunnel ? "go_tunnel_logs.jsonl" : "go_app_logs.jsonl")
    }

    /// Fixed files owned by the iOS app group and packet tunnel.
    public static func diagnosticPaths() -> [URL] {
        [sharedLogPath("go_app_logs.jsonl"), sharedLogPath("go_tunnel_logs.jsonl")]
    }

    public static let logsRepository: DobbyLogStore = {
        let current = appLogPath()
        return DobbyLogStore(path: current)
    }()

    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
