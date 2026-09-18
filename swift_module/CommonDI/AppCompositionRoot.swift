import Foundation

public let appGroupIdentifier = "group.vpn.dobby.app"

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    private let lock = NSLock()
    public let path: URL
    public let additionalPaths: [URL]

    public init(path: URL, additionalPaths: [URL] = []) {
        self.path = path
        self.additionalPaths = additionalPaths
        try? FileManager.default.createDirectory(
            at: path.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )
        if !FileManager.default.fileExists(atPath: path.path) {
            FileManager.default.createFile(atPath: path.path, contents: nil)
        }
    }

    public func writeLog(log: String) {
        lock.lock()
        defer { lock.unlock() }
        let line = log.hasSuffix("\n") ? log : log + "\n"
        guard let data = line.data(using: .utf8) else { return }
        guard let handle = try? FileHandle(forWritingTo: path) else { return }
        defer { try? handle.close() }
        try? handle.seekToEnd()
        try? handle.write(contentsOf: data)
    }

    public func cleanupOldLogs() {
        // Keep cleanup bounded and local to the known shared files. Never
        // remove another process's active log; only rotate files older than a
        // week. The Go logger owns JSONL retention inside its own process.
        let cutoff = Date().addingTimeInterval(-7 * 24 * 60 * 60)
        for candidate in additionalPaths {
            guard candidate != path,
                  let attributes = try? FileManager.default.attributesOfItem(atPath: candidate.path),
                  let modified = attributes[.modificationDate] as? Date,
                  modified < cutoff else { continue }
            try? FileManager.default.removeItem(at: candidate)
        }
    }

    public func lines() -> [String] {
        guard let text = try? String(contentsOf: path, encoding: .utf8) else { return [] }
        return text.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
    }
}

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
        sharedLogPath("app_logs.txt")
    }

    public static func goLogFilePath() -> URL {
        let isTunnel = Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true
        return sharedLogPath(isTunnel ? "go_tunnel_logs.jsonl" : "go_app_logs.jsonl")
    }

    public static let logsRepository: DobbyLogStore = {
        let current = appLogPath()
        let all = [
            sharedLogPath("app_logs.txt"),
            sharedLogPath("tunnel_logs.jsonl"),
            sharedLogPath("go_app_logs.jsonl"),
            sharedLogPath("go_tunnel_logs.jsonl"),
        ]
        return DobbyLogStore(path: current, additionalPaths: all)
    }()

    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
