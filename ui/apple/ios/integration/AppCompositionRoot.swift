import Foundation

public let appGroupIdentifier = "group.vpn.dobby.app"

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    public let path: URL
    public init(path: URL) { self.path = path }

    @discardableResult
    public func writeLog(level: String = "INFO", log: String) -> Bool {
        do {
            let source = Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true ? "ios-tunnel" : "ios-app"
            try DiagnosticFiles.append(log, event: "native.lifecycle", level: level, source: source, to: path)
            return true
        } catch {
            let message = "Native diagnostic write failed path=\(path.path): \(String(reflecting: error))\nOriginal record: \(log)\n"
            do { try FileHandle.standardError.write(contentsOf: Data(message.utf8)) }
            catch { /* The caller still receives the original write failure. */ }
            return false
        }
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
        sharedLogPath(Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true ? "tunnel_native.jsonl" : "app_logs.txt")
    }

    public static func goLogFilePath() -> URL {
        let isTunnel = Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true
        return sharedLogPath(isTunnel ? "go_tunnel_logs.jsonl" : "go_app_logs.jsonl")
    }

    /// Fixed files owned by the iOS app group and packet tunnel.
    public static func diagnosticPaths() -> [URL] {
        [
            sharedLogPath("ui_diagnostics.jsonl"),
            sharedLogPath("app_logs.txt"),
            sharedLogPath("tunnel_native.jsonl"),
            sharedLogPath("go_app_logs.jsonl"),
            sharedLogPath("go_app_logs.jsonl.stderr"),
            sharedLogPath("go_tunnel_logs.jsonl"),
            sharedLogPath("go_tunnel_logs.jsonl.stderr"),
        ]
    }

    public static let logsRepository: DobbyLogStore = {
        let current = appLogPath()
        return DobbyLogStore(path: current)
    }()

    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
