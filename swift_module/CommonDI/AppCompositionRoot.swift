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
        // Keep the native producer in the same structured JSONL format as Go.
        // This gives the shared FileDiagnosticStore a reliable timestamp at
        // the clear boundary while retaining a readable message for users.
        let record: [String: Any] = [
            "schema": "dobby.log/v1",
            "timestamp": ISO8601DateFormatter().string(from: Date()),
            "level": "INFO",
            "source": "ios-native",
            "event": "log.message",
            "message": log,
        ]
        guard let encoded = try? JSONSerialization.data(withJSONObject: record),
              var data = String(data: encoded, encoding: .utf8)?.data(using: .utf8) else { return }
        data.append(contentsOf: [0x0A])
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

    /// Fixed files are resolved by the native app-group/container boundary.
    /// The Go UI receives these URLs through the C bridge and validates that
    /// they stay in this one directory before opening them.
    public static func diagnosticPaths() -> [URL] {
        [
            sharedLogPath("ui_diagnostics.jsonl"),
            appLogPath(),
            sharedLogPath("go_app_logs.jsonl"),
            sharedLogPath("go_tunnel_logs.jsonl"),
        ]
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

    public static let exportLogsInteractor = ExportLogsInteractorImpl()
    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
