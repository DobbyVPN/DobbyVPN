import Foundation

public let appGroupIdentifier = "group.vpn.dobby.app"

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    private let lock = NSLock()
    public let path: URL

    public init(path: URL) {
        self.path = path
        do {
            try FileManager.default.createDirectory(
                at: path.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
        } catch {
            reportFileFailure("create diagnostic directory", error: error)
        }
        if !FileManager.default.fileExists(atPath: path.path) {
            if !FileManager.default.createFile(atPath: path.path, contents: nil) {
                reportFileFailure(
                    "create diagnostic file",
                    error: NSError(
                        domain: "DobbyLogStore",
                        code: 1,
                        userInfo: [NSLocalizedDescriptionKey: "FileManager.createFile returned false"]
                    )
                )
            }
        }
    }

    private func reportFileFailure(_ operation: String, error: Error) {
        let message = "DobbyLogStore \(operation) failed path=\(path.path): \(String(reflecting: error))\n"
        do {
            try FileHandle.standardError.write(contentsOf: Data(message.utf8))
        } catch {
            // There is no second diagnostic sink available if stderr itself is
            // unavailable. Keep the original write failure as the return
            // value of writeLog; this catch only prevents reporting from
            // masking the native operation that failed.
        }
    }

    @discardableResult
    public func writeLog(log: String) -> Bool {
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
        let encoded: Data
        do {
            encoded = try JSONSerialization.data(withJSONObject: record)
        } catch {
            reportFileFailure("encode diagnostic record", error: error)
            return false
        }
        var data = encoded
        data.append(contentsOf: [0x0A])
        do {
            let handle = try FileHandle(forWritingTo: path)
            var operationError: Error?
            do {
                try handle.seekToEnd()
                try handle.write(contentsOf: data)
            } catch {
                operationError = error
            }
            do {
                try handle.close()
            } catch {
                if operationError == nil {
                    operationError = error
                } else {
                    reportFileFailure("close diagnostic file", error: error)
                }
            }
            if let operationError {
                reportFileFailure("write diagnostic record", error: operationError)
                return false
            }
            return true
        } catch {
            reportFileFailure("open diagnostic file", error: error)
            return false
        }
    }

    public func lines() -> [String] {
        let text: String
        do {
            text = try String(contentsOf: path, encoding: .utf8)
        } catch {
            reportFileFailure("read diagnostic file", error: error)
            return []
        }
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
        return DobbyLogStore(path: current)
    }()

    public static let exportLogsInteractor = ExportLogsInteractorImpl()
    public static let vpnManager = VpnManagerImpl()
    public static let sessionShell = IOSSessionShell(manager: vpnManager)
}

public let configsRepository = DobbyConfigsRepositoryImpl.shared
