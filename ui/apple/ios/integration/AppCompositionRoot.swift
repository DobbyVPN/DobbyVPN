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

    /// Fixed files owned by the iOS app group and packet tunnel, plus the
    /// lifecycle timeline and retained hang captures beside them. The app and
    /// the extension share this directory, so both see the same artifacts.
    public static func diagnosticPaths() -> [URL] {
        diagnosticExtras() + [sharedLogPath("go_app_logs.jsonl"), sharedLogPath("go_tunnel_logs.jsonl")]
    }

    /// Timeline and up to four dumps in the shared directory; missing files are
    /// skipped so an archive never claims an artifact that was not written.
    public static func diagnosticExtras(dumpLimit: Int = 4) -> [URL] {
        let directory = sharedDirectory()
        var extras: [URL] = []
        let timeline = directory.appendingPathComponent("timeline.jsonl")
        if FileManager.default.fileExists(atPath: timeline.path) { extras.append(timeline) }
        let names = (try? FileManager.default.contentsOfDirectory(atPath: directory.path)) ?? []
        let dumps = names.filter { $0.hasPrefix("hang-dump-") && $0.hasSuffix(".txt") }
            .sorted().suffix(max(0, dumpLimit))
            .map { directory.appendingPathComponent($0) }
        return extras + dumps
    }

    public static let logsRepository: DobbyLogStore = {
        let current = appLogPath()
        return DobbyLogStore(path: current)
    }()

    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
